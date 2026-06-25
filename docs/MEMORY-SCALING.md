# Crawler memory scaling + parallel crawling — investigation & implementation plan

Status: **open / not started** — investigation complete and re-verified against `main`; implementation deferred (pick up cold from this doc).
Code baseline: all `file:line` references are as of commit **`f1b1d45`**; re-confirm with `grep` on pickup (the crawler moves fast).
Referenced from [DESIGN.md §9.2](DESIGN.md) ("Persistent-frontier worker pool (scale)"). Owner: TBD.

This note is meant to be **self-contained**: the measured evidence, root causes (in current code), the
target architecture (including the future *parallel multi-site crawling* requirement), the locked
product decisions, the test-first build order, and the per-phase plan. You should be able to resume
without re-deriving anything.

---

## 0. Locked decisions (from product owner, 2026-06-25)

These were decided up front so the plan doesn't drift. They materially simplify the design.

1. **Global download cap is a number the user sets.** There IS a single process-wide ceiling on total
   simultaneous fetches across all running crawls, but it is an explicit config value (no CPU auto-sizing
   required). Per-crawl `speed.max_threads` stays a real per-crawl ceiling; the global cap bounds the sum.
   → new knob `speed.max_global_threads` (default `0` = unlimited, i.e. single-crawl behaviour unchanged;
   set it when running crawls in parallel).
2. **No shared cross-crawl per-host politeness.** Two crawls landing on the same host/CDN (e.g. main domain
   + a competitor) is rare and explicitly **not** worth optimising. **Drop** the shared per-host token-bucket
   machinery. Per-crawl rate limiting (`speed.max_urls_per_sec`, robots `Crawl-delay`) stays exactly as it is
   today. This is the single biggest simplification vs. the original "governor" design.
3. **Clean break on storage — no legacy code.** New schema columns mean crawls stored before this work
   cannot be re-analysed for new features. That is fine: **re-crawl, or delete the old crawl (with user
   permission)**. Do **not** write migration/backfill code, compatibility shims, or "this crawl is old"
   warnings. Bump the crawl-DB schema version; an incompatible DB is a re-crawl-or-delete, nothing more.
4. **Dedup stays exact; Bloom is sized sensibly (impl. choice).** Use a Bloom filter as a RAM-cheap fast
   path, DB as the authority (see §7). Auto-growing or a fixed conservative size are both acceptable — pick
   the sensible implementation. **We never start revisiting URLs** (see §7.1 — this was the owner's explicit
   concern).

---

## 1. TL;DR

- The crawler's resident memory grows **roughly linearly with crawled pages** and **explosively with the
  discovered frontier**. Measured peak **~1.7–1.9 GB for a 4,000-page crawl** of `bfab.com` — at *thousands*
  of pages, not millions. Re-measured on current `main` (§3): same curve, same slopes.
- This is **RAM only**. Everything is already persisted to SQLite continuously; the RAM is a *redundant*
  in-memory copy plus goroutine stacks. On-disk size is a separate concern (DESIGN.md §9.2).
- There are **~9 distinct in-memory sinks** in two groups (during-crawl vs end-of-crawl). The single largest
  is **goroutine-per-URL**: the engine spawns one goroutine per *admitted* URL, all parked on a semaphore.
- **Re-verified against `main` (commit f1b1d45):** every sink still present. The recently-merged
  `internal/queue` + `internal/runner` is a **whole-crawl job scheduler** (one crawl at a time, persisted
  across surfaces) — it does **not** touch per-URL memory. `internal/finalize` consolidated the post-crawl
  path but kept the whole-map model (and can now hold *two* full maps at once).
- The fix is **one architectural shift in two independent halves**, plus a **thin global limiter** for the
  future parallel mode:
  - **(A) bounded worker pool + persistent (SQLite-backed) frontier + Bloom dedup** → caps goroutines at N
    and moves the visited-set/queue to disk. Kills the frontier burst.
  - **(B) stream page records to SQLite and make finalize/issues/analysis read from SQLite** (compact
    integer-ID structures) → stops retaining the full result set in RAM.
  - **(C) thin global limiter** → one user-set cap on total concurrent fetches + a finalize-concurrency cap,
    so M parallel crawls don't multiply RAM/IO without bound. (No shared per-host politeness — decision §0.2.)
- **Performance impact expected negligible** (network-bound pipeline; SQLite already in the hot path),
  *provided* we read-once-into-compact-RAM and never put a disk round-trip inside a hot loop (Bloom handles the
  hot dedup path).

---

## 2. Recommended architecture: per-crawl isolation + a thin global limiter

The design was chosen from a 3-way panel (per-crawl isolation / global shared pool / hybrid governor).
**Decision §0.2 removed the only reason to prefer the heavy hybrid governor** (shared per-host politeness), so
the recommendation is the simplest option that still bounds memory per-crawl AND across M parallel crawls:

> **Each crawl is fully isolated** — its own `crawls/<id>.db` (already `SetMaxOpenConns(1)`,
> [store.go:243](../internal/store/store.go)), its own worker pool, its own SQLite-backed frontier, its own
> Bloom filter. **One thin process-wide limiter** owns only what *must* be global: a user-set cap on total
> concurrent fetches and a cap on concurrent finalize passes.

**Per-crawl RAM bound:** `O(threads + ready_buffer + bloom + small_counters)` — **independent of frontier and
crawled-page count.** Target: tens of MB per crawl (vs ~1.7–1.9 GB today at 4k pages).

**Across-M-crawls bound:** `M·(small per-crawl footprint)`, with `SUM(in-flight fetches) ≤ max_global_threads`
and `concurrent finalizes ≤ F`. Each crawl has a separate DB file, so there is **no cross-crawl SQLite
lock contention**; the only shared state is the limiter's two semaphores.

Why not the alternatives: a **single global worker pool** (multiplexed scheduler across crawls) gives a
marginally tighter goroutine bound but rewrites the crawler's concurrency core into shared, fairness-managed,
lock-on-the-hot-path machinery — far larger blast radius for ~no extra benefit once per-host sharing is off the
table. **Pure per-crawl with no global cap** was rejected by decision §0.1 (user wants a total ceiling).

---

## 3. Measured evidence

### 3.1 Original run (pre-merge code, 2026-06-13) — bfab.com, 4k pages, 10 threads

Crawl summary: `Crawled 3985 URLs (4085 internal) in 10m45s`; `indexable: 3700`. **Peak RSS 1,943 MB.
Frontier peak 239,601.** The RSS curve has three regimes mapping 1:1 onto the sink groups in §4:

| Phase | Window | RSS change | Pages crawled | What it is |
|---|---|---|---|---|
| **1. Frontier burst** | 0→30 s | 127 → **978 MB** (+851) | only 523 | ~239k URLs admitted → goroutines + `seen` + `inlinks` (**~3.7 KB / discovered URL**) |
| **2. Record retention** | 30→626 s | 978 → **1,701 MB** (+723) | 523 → 3,992 | `c.pages` holding each `PageRecord` w/ full `Facts` (**~208 KB / crawled page**) |
| **3. Analysis spike** | end | 1,704 → **1,943 MB** (+239) | (drained) | `recomputeDepths` + `issues.Evaluate` + `analyze.Run` over the whole in-memory map |

**The damning detail:** `--max-urls 4000` caps *crawling*, but discovery had already admitted all 239,601 URLs
in the first 30 s — so **~235,000 URLs were discovered, each spawned a goroutine and entered `seen`/`inlinks`,
and were then never crawled**; their ~851 MB sat resident for the entire 11-minute run doing nothing.

<details><summary>Raw sampled curve — <code>t_s,pages,frontier,rss_mb</code></summary>

```
0,0,0,0
10,266,438,127
20,404,75885,341
30,523,239601,978
40,680,239489,1097
50,833,239348,1106
61,936,239266,1130
71,1033,239191,1139
81,1129,239113,1141
91,1215,239031,1141
101,1303,238952,1141
131,1585,238677,1213
151,1772,238495,1282
202,2107,238162,1337
252,2380,237894,1370
303,2607,237667,1425
353,2864,237410,1472
404,3108,237166,1540
454,3328,236946,1579
505,3534,236740,1620
545,3693,236581,1654
606,3913,236361,1700
626,3992,236282,1701
636,4027,206499,1701
646,4085,0,1704
656,4085,0,1943
```
</details>

### 3.2 Re-measurement (current `main`, f1b1d45, 2026-06-25) — CONFIRMS the numbers

Same site/caps. `Found 4086 URLs — 4000 crawled in 11m3s`. **Peak RSS 1,732 MB. Frontier peak 239,358.**

| Metric | Pre-merge | Current main |
|---|---|---|
| Peak RSS | 1,943 MB | **1,732 MB** |
| Per-discovered-URL (frontier burst) | ~3.7 KB | **~3.65 KB** |
| Per-crawled-page (record retention) | ~208 KB | **~206 KB** |

Identical two-regime curve and slopes. The ~210 MB lower peak is **GC/RSS-return variance** (Go releases
memory to the OS lazily), not a real change; per-page retention did **not** drift up despite `PageRecord`
gaining fields. The `finalize` double-`LoadPages` did not spike hard here (crawl 1,705 → finalize 1,732 MB)
only because analysis was benign (`0 near-duplicate pages, 1 chain`); the structural double-map risk remains
for near-dup-enabled / larger / **parallel** crawls.

### 3.3 Reproduce

```sh
make build
STORE=/tmp/bfab4k; rm -rf "$STORE"; mkdir -p "$STORE"
./bin/bluesnake crawl https://bfab.com --store-dir "$STORE" --max-urls 4000 --threads 10 &
BPID=$!
while kill -0 $BPID 2>/dev/null; do
  DB=$(ls "$STORE"/crawls/*.db 2>/dev/null | head -1)
  P=$([ -n "$DB" ] && sqlite3 "$DB" "SELECT COUNT(*) FROM pages" || echo 0)
  F=$([ -n "$DB" ] && sqlite3 "$DB" "SELECT COUNT(*) FROM frontier" || echo 0)
  echo "$(ps -o rss= -p $BPID | tr -d ' '),$P,$F"; sleep 10
done
```
Note: `bfab.com`'s robots.txt has **no `Crawl-delay`** — the ~2–6 URLs/s rate is server latency + per-page
parse/issues CPU, not throttling. Per-crawl threads scale sub-linearly (latency-bound on a single host).

### 3.4 Extrapolation → OOM on both axes

- **Discovered axis** (~3.7 KB/URL, materialises in the first minutes regardless of `--max-urls`):
  2M discovered → **~7.4 GB** just for goroutines + seen-set.
- **Crawled axis** (~206 KB/crawled page): 50k pages → **~10.3 GB** of retained records.
- A 16 GB machine OOMs around a **~250k-URL frontier** or **~60k crawled pages** — well within mid-size
  e-commerce, and **M parallel crawls multiply this** with no ceiling today.

---

## 4. Root causes (the ~9 sinks, current `main` locations)

Unifying root cause: **everything is already written to SQLite continuously** (`store`, batched tx), **but**
(a) the crawler spawns a goroutine per pending URL and retains every record in `c.pages`, and (b) post-crawl
`finalize` re-loads the whole table into a map for `issues`/`analyze`. The engine pays for the data twice
(RAM + disk), plus a third time in goroutine stacks.

### Group A — grow *during* the crawl

| # | Sink | Location (f1b1d45) | Notes |
|---|---|---|---|
| **1** | **Goroutine-per-URL** — `go c.process(...)` for *every admitted URL*; each parks on `c.sem` (cap = `max_threads`) | spawn [crawler.go:293](../internal/crawler/crawler.go), discovery [crawler.go:515](../internal/crawler/crawler.go), sem [crawler.go:227](../internal/crawler/crawler.go)/[:505](../internal/crawler/crawler.go), join [crawler.go:335](../internal/crawler/crawler.go) | ~239k live goroutines on bfab. The dominant cost. "worker pool" appears only in comments. |
| **2** | **Frontier `seen map[string]bool`** — every discovered URL string | field [frontier.go:27](../internal/frontier/frontier.go), check/set in `Admit` [frontier.go:46](../internal/frontier/frontier.go), `MarkSeen` [:97](../internal/frontier/frontier.go) | Already mirrored to SQLite `frontier`/`pages`. SQLite table is a write-through resume mirror, never the dedup authority today. |
| **3** | **`c.pages map[string]*PageRecord`** retaining full `Facts` per page | field [crawler.go:171](../internal/crawler/crawler.go), set in `record()` [crawler.go:1093](../internal/crawler/crawler.go), returned as `Result.Pages` [:344](../internal/crawler/crawler.go) | `parse.Facts` holds `ContentText` (full page text) **and** `Links []Link` → ~206 KB/page on bfab. |
| **4** | **`c.inlinks map[string]inlinkInfo`** — one entry per link *target* | field [crawler.go:172](../internal/crawler/crawler.go), `noteInlink` [:1104](../internal/crawler/crawler.go), drained [:352](../internal/crawler/crawler.go) | ~one entry per discovered URL (frontier-sized). |
| **9** | **`seenContent map[string]string`** — raw-body content-hash → first URL (R8 SPA-shell short-circuit) | field [crawler.go:175](../internal/crawler/crawler.go), `firstWithContent` [:1079](../internal/crawler/crawler.go) | **Newly identified.** Unbounded on SPA sites; gated by `advanced.skip_identical_content_links`. Must be bounded too. |

Plus a secondary one: **`perSub map[host]int`** ([frontier.go](../internal/frontier/frontier.go)) grows with
distinct host count under `scope.crawl_all_subdomains` (hosts ≪ URLs, so secondary — LRU + DB overflow if ever needed).

### Group B — spike *at end of crawl* (consume the whole in-memory `pages` map)

`internal/finalize/finalize.go` (new) re-loads the full table: `st.LoadPages()` at [finalize.go:88](../internal/finalize/finalize.go)
(resume recompute) and [:116](../internal/finalize/finalize.go) (Analyze), and still consumes the crawler's live
`res.Pages` at [:57](../internal/finalize/finalize.go) — so **two full maps can coexist at the peak**.

| # | Sink | Location | Notes |
|---|---|---|---|
| **5** | `issues.Evaluate(pages, cfg)` + dup maps `byHash/byTitle/byDesc/byH1/byH2` + `inlinkAggregates` | [issues.go:286](../internal/issues/issues.go), dup [:1114](../internal/issues/issues.go), inlink agg [:1186](../internal/issues/issues.go) | Whole-map scan; aggregate dup detection in Go maps. **Zero SQL in the package.** |
| **6** | `analyze.Run(pages, …)` PageRank **`edges map[string]map[string]bool`** + `rank/next/nodes` | [analyze.go:80](../internal/analyze/analyze.go), edges [:149](../internal/analyze/analyze.go), vectors [:180](../internal/analyze/analyze.go) | Nested string-keyed maps over every edge — heaviest analysis structure. |
| **7** | Near-dup minhash `cands` (128-int sig/page) + `exact` + LSH buckets | [analyze.go:334](../internal/analyze/analyze.go), [:336](../internal/analyze/analyze.go) | Also the *reason* `Facts.ContentText` is retained. Gated off by default (`near_duplicates.enabled=false`). |
| **8** | `recomputeDepths` adjacency over all pages | [crawler.go:383](../internal/crawler/crawler.go), follow-gate `followsForDepth` [:483](../internal/crawler/crawler.go) | Shortest-followed-path depth (SF parity). Runs after the crawl loop. |

**Already-persisted data the rework reads from instead:** `pages`, `links` (`idx_links_src`/`idx_links_dst`),
`frontier`, `hreflang`. Store helpers exist: `FrontierAdd`/`PendingFrontier`/`ProcessedURLs`
([store.go:644](../internal/store/store.go)/[:658](../internal/store/store.go)/[:676](../internal/store/store.go)),
`LoadPages` ([store.go:785](../internal/store/store.go)), `UpdateInlinks`.

---

## 5. Target design (details)

### 5.1 Frontier API — persistent SQLite + Bloom, SQLite is the authority

Keep the public surface (`Admit`/`MarkSeen`/`Seen`/`Admitted`) identical so the two call sites
([crawler.go:290](../internal/crawler/crawler.go), [:512](../internal/crawler/crawler.go)) are untouched. Internally:

- **`seen map[string]bool` → Bloom (fast negative) + SQLite-UNIQUE(url) authority.** A URL is "seen" iff it is
  an un-done `frontier` row OR a `pages` row — derivable from existing tables, no third bookkeeping structure.
- **`Admit(it)` stays one atomic critical section per URL** (preserve count-at-admit). Order:
  1. Cheap lock-free pre-gates unchanged: MaxURLLength, MaxDepth, MaxQueryStrings, MaxFolderDepth.
  2. Dedup: query Bloom. **MISS ⇒ definitely novel ⇒ proceed.** **HIT ⇒ maybe-seen ⇒ confirm via
     `INSERT OR IGNORE INTO frontier … RETURNING` (rowsAffected ⇒ first-vs-dup).** This is the false-positive
     backstop — a Bloom false positive resolves to a real DB check, **never a silent drop**.
  3. On confirmed-first: bump `perDepth/perSub/perPath` and set the Bloom bit **in the same locked section**.
- **`MarkSeen` stays limit-free** (resume preseed of processed URLs; must NOT consume limit budget).
- **Rehydrate `perDepth/perSub/perPath` on resume** from the stored graph (`SELECT depth, COUNT(*) … GROUP BY`),
  else a live worker pool enforcing limits across a resume would over-admit past caps a straight crawl rejected.
- **Schema:** add `seq INTEGER` (monotonic) + `claimed INTEGER DEFAULT 0` to the `frontier` table; index
  `(claimed, depth, seq)`. `seq` gives a **total deterministic pull order** (`ORDER BY depth, seq`) so multi-worker
  pickup and first-wins `DiscoveredFrom` are run-to-run stable. `claimed` marks in-flight rows; on resume reset
  `claimed=1 → 0`. `source`/`redirect_hops` must keep round-tripping (carry cross-interrupt `DiscoveredFrom`).

### 5.2 Bounded worker pool (replaces goroutine-per-URL)

Replace `go c.process(...)` at [crawler.go:293](../internal/crawler/crawler.go) and [:515](../internal/crawler/crawler.go)
with **N = `cfg.Speed.MaxThreads` persistent workers** per crawl draining an in-RAM **ready-buffer**.

- **Feeder** (one goroutine): refills the buffer via `SELECT … FROM frontier WHERE claimed=0 ORDER BY depth, seq
  LIMIT batch`, marking rows `claimed=1`. Overflow stays as durable rows (spill-to-disk is free — `FrontierAdd`
  already wrote every admitted item).
- **Worker loop:** pull Item → `limiter.AcquireFetch(ctx)` (global slot, §5.6) → existing per-crawl rate wait
  → `crawlOne(ctx, it)` → release → for each discovered link: `frontier.Admit` then push to buffer (or leave
  durable if full) → `FrontierDone(url)` **only if `done==true`** (preserves pause-leaves-pending).
- **`crawlOne` internals untouched:** MaxURLs reserve-then-check stays at the fetch boundary
  ([crawler.go:580](../internal/crawler/crawler.go), `c.fetched`); per-crawl `rateWait` stays.
- **Termination — replace `wg.Wait()` ([crawler.go:335](../internal/crawler/crawler.go)) with an atomic in-flight
  counter** = `(rows in ready-buffer) + (claimed-but-not-done)`. Exit when it hits 0 AND no unclaimed frontier
  rows remain. **Critical happens-before:** a worker INCREMENTS its discoveries' count (Admit + write) **before**
  it decrements its own item (FrontierDone) — getting this backwards causes early shutdown with work pending
  (the #1 trap in the swap).
- `c.sem` ([crawler.go:227](../internal/crawler/crawler.go)) becomes redundant (pool size IS the per-crawl cap);
  keep it only if it cleanly doubles as the per-crawl sub-cap under the global limiter.

### 5.3 Ready-buffer sizing

`cap = clamp(factor·N, min, MAX)`, `factor ≈ 4`. At ~200 B/Item this is tens of KB per crawl. It is a *window*,
not the frontier — everything else lives durably in SQLite. This is what makes per-crawl RAM independent of
frontier size, and gives natural backpressure with no in-RAM overflow queue (so no backpressure deadlock).

### 5.4 Stream-PageRecords-and-release

In `record()` ([crawler.go:1091](../internal/crawler/crawler.go)): **drop `c.pages[rec.URL]=rec`
([:1093](../internal/crawler/crawler.go)); keep only `c.sink.Page(rec)`.** The record GCs once the worker returns.
`Result.Pages` ([:344](../internal/crawler/crawler.go)) can no longer be the live map — return a slim aggregate
(or nil) and make `finalize`'s `UpdateInlinks` ([finalize.go:57](../internal/finalize/finalize.go)) read fresh
depth/inlinks from the store the same way resume already does. **Bound `seenContent` (#9)** identically: a
Bloom/cuckoo of `hash→first-URL` + a SQLite `content_hash(hash PRIMARY KEY, canonical_url)` authority, preserving
first-writer-wins-under-lock and the `claim=willExpand` gate ([crawler.go:713](../internal/crawler/crawler.go),
the R8 sweetgreen rule).

### 5.5 Streaming/SQL finalize (kills both `LoadPages` sites)

Converge fresh and resume onto the **same** store-backed path (today fresh computes in-RAM, resume via
`LoadPages`) so `fresh == resume`, guarded by `TestResumeEquivalence`.

- **Depth:** DB-backed BFS over `links` loaded once into a compact **int32-ID CSR** (needs a `url→id` map/table;
  `links` is TEXT today). **Re-apply the exact `followsForDepth` gate** ([crawler.go:483](../internal/crawler/crawler.go):
  typeFlags by type+scope + per-scope nofollow, redirect-as-hop, sitemap-only=NoDepth(-1)) when building the CSR —
  `links` is a *superset* (no follow-gate at write), so reading it raw over-counts edges. **Sharp parity trap.**
- **Inlinks / DiscoveredFrom:** SQL `COUNT(*) FILTER(type='hyperlink' AND NOT nofollow AND dst!=src) GROUP BY dst`
  for the count; `MIN(seq)` for first-wins `DiscoveredFrom`. **Requires an explicit monotonic discovery-`seq`
  column on `links`** — rowid is unsafe because re-crawl does delete+reinsert ([store.go:625](../internal/store/store.go)),
  reordering rowids and silently changing which source "wins".
- **Duplicates ([issues.go:1114](../internal/issues/issues.go)):** GROUP BY hash/title/desc; H1/H2 "either of first-2"
  needs a projected `(url,key)` table or `json_each`. `inlinkAggregates` → SQL join `links.src → pages.indexable`.
- **PageRank ([analyze.go](../internal/analyze/analyze.go)):** read `links` once into the int32 CSR (hyperlink ∧
  !nofollow ∧ both-internal in SQL), 40 iters in RAM, d=0.85, init 1/N, self-loops excluded from rank but counted in
  unique metrics, scale `v/max·100`, **reproduce within ~1e-9**.
- **Near-dup:** persist the 128-int minhash signature to a `pages` column **at crawl time** so `Facts.ContentText`
  is never reloaded; stream LSH band-by-band.
- `SaveIssues`/`SaveAnalysis` become batched/streaming writers preserving DELETE-then-insert replace semantics.

### 5.6 Thin global limiter (the parallel-mode piece — simplified per decision §0.2)

One small object constructed once per surface (desktop/MCP/CLI) and injected into `crawler.New` via a new Option
(mirroring `WithSink`/`WithResume`, [crawler.go:118](../internal/crawler/crawler.go)). It holds **only**:

1. **Global fetch semaphore (`speed.max_global_threads`, user-set, default 0 = unlimited):** every worker acquires
   a global slot around its fetch; `SUM(in-flight fetches) ≤ G` regardless of M or per-crawl threads. Caps total
   goroutines, FDs, connections. Effective per-crawl concurrency = `min(per-crawl threads, global slots free)`.
2. **Finalize-concurrency cap (`F`, default 1):** analyse/CSR passes are CPU+RAM-bursty; bound how many run at once
   so M crawls finishing together don't each materialise a CSR simultaneously.

**Explicitly NOT included** (decision §0.2): shared per-host token buckets, shared robots cache. Per-host politeness
stays per-crawl, unchanged. (If the rare same-host-across-crawls case ever matters, it can be added later behind this
same limiter without touching the per-crawl hot path.)

**Control-surface change (parallel prerequisite):** `Pause/Stop/Cancel/Snapshot` are implicitly "the one running
crawl" ([dispatcher.go:84](../internal/queue/dispatcher.go), runner `e.cur`). Make them **crawl-id-addressed**, and
run W dispatcher loops (`current *Job` → `map[jobID]*inFlight`). `ClaimNext` is already atomic FIFO, so N claimers
are safe; verify `MemStore` is concurrency-safe.

---

## 6. Performance — why this should be ~free

1. **The crawl is network-bound** (~2.6 s latency/page, ~6 pages/s on bfab). SQLite ops are microseconds — orders
   of magnitude of slack.
2. **SQLite is already in the hot path** (continuous commit; frontier already mirrored). We move *reads* onto it and
   remove the redundant RAM copy; we do not add I/O.
3. **A hot WAL DB lives in the OS page cache** — below working-set-exceeds-RAM a read is RAM-speed; above it is exactly
   where we were OOMing anyway.
4. **Post-crawl phases are one-time batch passes** — a full `pages` scan is seconds against an hours-long crawl.

**The one rule that makes it hold:** read from disk *once* into bounded compact in-memory working sets, then compute in
RAM. Never a disk round-trip in a hot loop — that is exactly why the Bloom filter exists (it absorbs the high-frequency
"already seen" re-discoveries so only genuinely-novel URLs touch the DB).

Likely **net wins:** 235k parked goroutines aren't free (GC scans stacks, scheduler tracks them); `GROUP BY` in SQLite
is often lighter than building Go group-maps. **Edge cases:** tiny crawls marginally slower (ms, not the problem);
very high-rate single-host crawls need batched frontier pulls/pushes to avoid WAL-lock churn.

---

## 7. How dedup stays exact (the owner's explicit concern: "are we revisiting URLs now?")

**No — not today, and not after this change.**

- **Today:** an *exact* in-memory set (`seen map[string]bool`) — 100% accurate, zero re-visits, zero misses, but it
  holds every URL string in RAM forever (sink #2).
- **After:** a **Bloom filter** (RAM-cheap) as the fast path, with the **SQLite UNIQUE(url) constraint as the
  authority**. Correctness is identical because:
  - Bloom says **"never seen"** ⇒ *guaranteed* novel (Bloom filters have **no false negatives**) ⇒ crawl it.
  - Bloom says **"maybe seen"** ⇒ confirm against the DB (`INSERT OR IGNORE … RETURNING`) ⇒ skip if truly seen, crawl
    if it was a rare false positive.
  - Net: **never re-crawl a seen URL, never drop a novel one.** A false positive costs one extra DB lookup, never a
    wrong decision.

This invariant is non-negotiable and is pinned by a **property/fuzz test against the exact map oracle** (§8, test #2).

---

## 8. Test-first plan (THE backbone — write tests first, RED → GREEN)

The owner's directive: **write all necessary tests first, let them fail, then turn them green as we implement; and
keep a guard layer that fails the moment the current crawler's behaviour breaks.** Build order below honours that.

### 8.1 Guard layer — write/confirm FIRST, must stay GREEN through every phase

These pin *current* behaviour so the rework can't silently break the product. Golden outputs must remain
**byte-identical** (regenerate only for an intended change, never to paper over a diff):

- **`test/resume_equivalence_test.go`** ([:229](../test/resume_equivalence_test.go)): resumed == straight on depth,
  raw Inlinks, UniqueInlinks/Outlinks, DiscoveredFrom, State, Scope, Indexable, LinkScore (±0.01), issue set+counts,
  registry counts. (The `/hub` inlink=4 case only resolves over the merged two-session graph.)
- `internal/analyze/coverage_test.go` — catalogue-coverage meta-test (every issue ID still triggers; healthy fixture
  triggers zero).
- `test/` golden files — stored DB contents + exports unchanged.
- Acceptance features: `spider_crawl`, `limits`, `speed`, `pause_resume`, `crawls_management`, `links`,
  `analysis_linkscore`, `chains`, `near_duplicates`, `hreflang`.
- Coverage gate (`make cover`, currently 90%) must not regress.

### 8.2 RED tests — write FIRST, must fail on today's code

1. **Memory/goroutine regression test** (`internal/crawler`): a fixture site (via `test/fixture`, served by
   `httptest`, no network) with a **large fan-out frontier** (a few hundred "category" pages each linking to thousands
   of synthetic URLs, discovered ≫ crawled, mimicking bfab's facets). Assert **`runtime.NumGoroutine()` sampled during
   the crawl stays ≤ `max_threads + C`**, independent of frontier size — *fails today* (goroutines ≈ admitted URLs).
   Optionally assert bounded `HeapAlloc` against a generous ceiling that scales with `max_threads`, not URLs (keep loose
   to avoid flakiness; goroutine count is the primary signal). Run **two fixture sizes** and assert RAM does not scale
   with page count. → **This is the gate for "done" (Phase 6).**

### 8.3 Per-phase parity tests — write before each phase, prove behaviour unchanged

2. **Bloom-vs-oracle property/fuzz test** (Phase 4/5): randomised URL streams through Bloom+SQLite vs the exact
   `map[string]bool` oracle, **under concurrent workers** — never re-crawl a seen URL, never drop a novel one (§7).
3. **Frontier dedup/order/limits** table tests (`internal/frontier`): every limit
   (depth/folder/query/url-len/per-path/per-subdomain/total), count-at-admit atomicity, deterministic `seq` order.
4. **Over-limit resume test** (Phase 4): pause+resume a crawl that hits `MaxURLsPerDepth`/`MaxPerSubdomain`; the resumed
   crawl must admit *exactly* the same set (counters rehydrated, no over-admit).
5. **Depth parity** (Phase 5): DB-backed CSR BFS == current in-memory `recomputeDepths` on fixtures (incl. sitemap-only
   `NoDepth` and redirect-as-hop).
6. **Inlinks / DiscoveredFrom parity** (Phase 5): SQL-derived == current `c.inlinks`, including first-wins + seed-lock.
7. **PageRank parity** (Phase 5): int32-CSR == current string-map PageRank within ~1e-9.
8. **Dup-detection parity** (Phase 5): SQL `GROUP BY` == current `byHash/byTitle/byDesc/byH1/byH2`, including the H1/H2
   either-of-first-2 rule, eligibility gates, and lexical `MIN(src)` tie-breaks.
9. **Parallel per-host sanity test** (Phase 7): M crawls under a global cap `G` keep `SUM(in-flight fetches) ≤ G`; a
   small crawl isn't starved (FIFO global slots). (No cross-crawl per-host assertion — decision §0.2.)
10. **Stream-and-drop memory probe** (Phase 1): assert the Phase-2 record-retention slope is gone.

---

## 9. Phased plan (each phase independently shippable; tests green at each)

Order = lowest-risk-first. **Phases 1–6 are per-crawl memory; Phases 7–8 are the parallel prerequisites.**
**Phase 0 also writes the guard layer (§8.1) and the RED tests (§8.2) before any production change.**

| Phase | Scope | Risk | Parallel prereq |
|---|---|---|---|
| **0** | Write guard + RED tests (§8.1/§8.2). Then the safe quick win: nil `Facts.ContentText` in `record()` after persisting it (and persist the 128-int minhash signature to a new `pages` column at crawl time so near-dup reads the column, not `ContentText`). | Low | — |
| **1** | Stop the redundant `c.pages` copy (stream-and-drop, [crawler.go:1093](../internal/crawler/crawler.go)); route fresh depth/inlinks through the store path resume already uses, so `Result.Pages` is no longer load-bearing. | Med | — |
| **2** | SQL/CSR `finalize` for issues + analyze (kill both `LoadPages`, [finalize.go:88](../internal/finalize/finalize.go)/[:116](../internal/finalize/finalize.go)); add `url→id` table + `links.seq`. | **High** (parity surface) | — |
| **3** | Bounded worker pool: N persistent workers + feeder + atomic in-flight counter replacing `wg.Wait()`; `claimed`+`seq` on the frontier table. Frontier stays in-memory for now. | **High** | — |
| **4** | Persistent frontier dedup derived from `frontier ∪ pages` (`INSERT OR IGNORE … RETURNING`); remove `c.inlinks`; rehydrate limit counters on resume. | Med-High | — |
| **5** | Bloom fast-negative in front of the SQLite authority; bound `seenContent` (#9) the same way. | Med | — |
| **6** | Wire the bounded-RAM/goroutine regression test (§8.2) into CI as the "done" gate. Per-crawl RAM now constant. | Low | — |
| **7** | Thin global limiter: `speed.max_global_threads` semaphore + finalize cap `F`; `WithLimiter` Option threaded into `crawler.New`. **No shared per-host buckets.** | Med | ✓ |
| **8** | Crawl-id-addressed control (`Pause/Stop/Snapshot`) + N dispatcher loops. | Med | ✓ |

Parallel crawling requires Phases 1–8: 1–6 prevent per-crawl OOM (the prerequisite for M>1 to be feasible at all);
7–8 add the global ceiling + the control surface.

---

## 10. Invariants to preserve (product correctness contract)

- **Issue occurrences + analysis outputs byte-identical** (golden files are the contract).
- **Resume semantics** ([test/resume_equivalence_test.go](../test/resume_equivalence_test.go)): config frozen at start;
  frontier + visited-set round-trip; resume refuses a changed config unless `--force`; MaxURLs budget cumulative across
  resume ([crawler.go:286](../internal/crawler/crawler.go)); pause leaves the in-flight item PENDING (re-fetch on resume,
  [crawler.go:518](../internal/crawler/crawler.go)).
- **Depth = shortest *followed*-link BFS** from a seed; redirect = a hop; sitemap-only/unreached = NoDepth(-1);
  follow-gate = exact `followsForDepth` ([crawler.go:483](../internal/crawler/crawler.go)), not a static subset.
- **Inlinks** = hyperlink-only, self-excluded, order-independent; redirect/iframe/meta-refresh set DiscoveredFrom but
  don't inflate the count.
- **DiscoveredFrom first-wins** with seed-lock, now pinned by the `seq` column (never rowid).
- **Dup keys exactly:** content=`Facts.Hash`, title=`Titles[0]`, desc=`Descriptions[0]`, h1/h2=either-of-first-2; fires
  at ≥2 members; full eligibility gate (StateCrawled ∧ internal ∧ Facts≠nil ∧ isHTMLPage ∧ not skipForIndexability ∧
  IgnorePaginatedForDuplicates).
- **Tie-breaks = lexical `MIN(src)` / `url < cur`** ([issues.go:1216](../internal/issues/issues.go),
  [analyze.go:436](../internal/analyze/analyze.go)), NOT discovery order.
- **PageRank:** d=0.85, 40 iters, init 1/N, internal∧StateCrawled nodes, self-loops excluded from rank but counted in
  unique metrics, scale `v/max·100`, within ~1e-9.
- **Near-dup:** 128-int minhash over ContentText, LSH banding, exact-hash pairs excluded, symmetric.
- **Identical-content short-circuit** determinism (`firstWithContent`, first-owner-wins, `claim=willExpand`).
- **WAL crash-safety**; graceful pause on Ctrl-C.
- **Determinism** for `compare`-stability (no run-to-run ordering changes in outputs) — protected by `seq` total order.

---

## 11. Risks & open items

### Risks (with mitigations)
- **Dedup false positive silently loses a novel URL** (highest correctness risk). → Bloom answers only the
  negative-skip side; every HIT confirms against SQLite before rejecting. Property test (§8 #2) is mandatory.
- **Atomicity TOCTOU at Admit** (split dedup/counting lets two workers pass a per-bucket cap). → keep limit-check +
  dedup-confirm + counter-bump + Bloom-set in **one serialized critical section** per URL.
- **Early termination on `wg.Wait()`→counter swap.** → increment children before decrementing parent; pin with a
  high-fan-out termination test.
- **Determinism drift** from multi-worker pickup. → `ORDER BY depth, seq` with explicit monotonic `seq` (never rowid).
- **`links` rowid instability** under re-crawl delete+reinsert breaks `MIN`-based first-wins. → explicit `seq` column.
- **Global limiter mis-scoping** can serialize independent crawls. → per-crawl-spill (durable frontier) + a soft global
  cap, never a hard shared mutex on the admit path; FIFO global slots so a small crawl isn't starved.
- **Forgetting `seenContent` / renderer.** → Phase 5 bounds `seenContent`; bound concurrent Chrome instances behind the
  same limiter when JS-mode parallel crawling lands ([render.go](../internal/render/render.go)).

### Open items (lower-stakes, decide during implementation)
- Exact `links.seq` mechanism (dedicated column vs an insert-order table) and the `url→id` mapping (table vs in-memory
  per finalize pass) — both are internal, pick the simplest that keeps the parity tests green.
- Finalize cap `F` default (proposed `1`) and whether `speed.max_global_threads` lives under `speed:` or a new
  `parallel:` config block — cosmetic, decide when Phase 7 lands.
- Storage version bump + the re-crawl-or-delete UX for incompatible old crawls (decision §0.3 — clean break, no migration
  code; a `crawls prune`/delete-with-permission path is acceptable but optional).

---

## 12. Pointers

- Storage schema & write strategy: DESIGN.md §5.3. Pipeline: §5.2. Analysis: §5.6.
- On-disk store measurements (separate concern): DESIGN.md §9.2 "Measured storage & memory".
- Backlog row this elaborates: DESIGN.md §9.2 "Persistent-frontier worker pool (scale)".
- Queue/runner scope (job scheduler, NOT a per-URL pool): `internal/queue/queue.go:9-12`,
  [dispatcher.go](../internal/queue/dispatcher.go), [runner.go](../internal/runner/runner.go).
