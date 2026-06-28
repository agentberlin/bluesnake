# Crawler memory scaling + parallel crawling — investigation & implementation plan

Status: **complete** — Phases 0–8 landed (memory + parallel). **Phase 2 SQL/CSR body is fully cut over & live:** inlinks/discovered_from (gated `edges` table), depth (CSR over `links`), PageRank (CSR via `analyze.WithLinks`), dup-rule (`store.DuplicateIssues`), and the near-dup minhash column (`internal/minhash` leaf pkg + `pages.minhash`, migration v4) — each parity-proven against the in-RAM path and verified by EC24 golden + resume_equivalence + acceptance. Residual (lowest value, no RAM win): the lite map still carries `Facts.Links` (optional links-table-only CSR); Phase 4 `c.inlinks` field removal + FR-08 counter rehydration; Phase 5 `seenContent` bound.
Code baseline: all `file:line` references are as of commit **`f1b1d45`**; re-confirm with `grep` on pickup (the crawler moves fast).

> **Progress (2026-06-28).** Phases 0, 1, 3 implemented with TDD, full suite + acceptance + `-race` green, coverage 92.4% (gate 90%):
> - **Phase 0** — `record()` frees `Facts.ContentText` in RAM right after the sink persists it (the bulk of a `PageRecord`). Pinned by `internal/finalize`'s `TestContentTextRoundTripsThroughStore` (LoadPages still round-trips the text for near-dup/lorem/soft-404/compare). Minhash-column persistence deferred to Phase 2 (re-evaluated: not needed until finalize stops loading `ContentText`).
> - **Phase 1** — `c.pages` dropped entirely; `Result.Pages` replaced by a slim frontier-sized `Result.Inlinks` aggregate + atomic `Crawled`/`Total` counters. Fresh and resume finalize **converged** on one store-backed path: `SaveInlinkSources` (inlinks + seed-locked first-wins discovered_from) then `LoadPages`→`RecomputeDepths`+`RecomputeInlinks`→`SaveDepths`+`SaveInlinks`. New store method `SaveInlinkSources`. ~30 crawler unit tests + the BDD harness re-sourced onto a capturing Sink / store `LoadPages`. Done-signal: `internal/crawler`'s `TestNoPageRecordRetainedAfterRun` (GC-finalizer sentinel — every streamed record is collectable post-drain). Per-page sustained RAM (regime 2) eliminated; the finalize `LoadPages` spike (regime 3) is transient and is Phase 2's target. `store.UpdateInlinks` is now production-unused (kept; still store-tested).
> - **Phase 3** — goroutine-per-URL + `wg.Wait()` replaced by a bounded worker pool: N = `Speed.MaxThreads` persistent workers draining a deadlock-free unbounded in-RAM queue (`internal/crawler/workpool.go`), with an atomic in-flight counter (in-flight = queued + in-process). The #1 swap trap is honoured — a worker pushes its admitted discoveries (each bumping in-flight) **before** `done()` decrements its own item. `c.sem` and `process()` removed; behaviour preserved (resume-equivalence + all acceptance green). Done-gate: `TestGoroutinesBoundedByThreads` (peak live goroutines ≈ N, independent of a 1500-wide frontier — was ~1476 before). Invariants pinned by `workpool_test.go`: `TestWorkPool_TreeNoEarlyTermination` (WP-01, 50× `-race`), `TestWorkPool_SoleProducerNoDeadlock` (WP-03, 10k children, N=1), `TestWorkerPool_AdmitRejectionBalancesCounter` (WP-02). **Re-evaluated vs the doc:** Phase 3 here uses an in-RAM queue (kills the dominant goroutine-stack cost — the bulk of the frontier burst) rather than the SQLite-backed feeder + `claimed`/`seq` columns; the remaining frontier-sized queue/`seen`-set RAM is Phase 4's target (SQLite-backed frontier + Bloom). This keeps Phase 3 low-risk and decoupled from the finalize rewrite.
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
- **Near-dup ✅ DONE:** the signature primitive lives in `internal/minhash` (a leaf package); the crawler
  precomputes each page's 64-uint64 signature from the live body and persists it to the `pages.minhash` BLOB column
  **at crawl time, only when near-dup is enabled** (migration v4), so finalize runs near-dup over the ContentText-free
  lite map and `Facts.ContentText` is never reloaded. The analyze-time gate (indexable/paginated/wordcount) still
  applies over the stored signature; older / near-dup-off crawls fall back to hashing the body. (NB: the signature is
  64 uint64s, not "128-int" — the earlier note was approximate.)
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
| **0** ✅ | DONE. Quick win: `record()` frees `Facts.ContentText` after the sink persists it. (Minhash-column persistence deferred to Phase 2 — not needed until finalize stops loading `ContentText`.) Pinned by `TestContentTextRoundTripsThroughStore`. | Low | — |
| **1** ✅ | DONE. `c.pages` dropped (stream-and-drop); `Result.Pages`→`Result.Inlinks` + atomic counters; fresh & resume finalize converged on the store-backed `SaveInlinkSources`/`RecomputeDepths`/`RecomputeInlinks` path. Done-signal `TestNoPageRecordRetainedAfterRun`. | Med | — |
| **2** ✅ | Entry gate DONE (EC24 golden). **Inlinks/discovered_from SQL — CUTOVER DONE & LIVE:** a gated `edges` table (rewritten dst + hyperlink + monotonic `seq`, written at crawl time via `crawler.GatedEdge`/`store.Page`, seq continued across resume via `MaxEdgeSeq`) now drives finalize — `store.SaveInlinksFromEdges` computes inlinks (COUNT) + first-wins discovered_from (seq-MIN, seed-locked) in pure SQL, and finalize no longer calls `RecomputeInlinks`. Verified by `TestEdgesSQLParity` + EC24 golden + resume_equivalence + acceptance (the seq-MIN matches the racy-noteInlink goldens). **Depth-CSR — CUTOVER DONE & LIVE:** `crawler.RecomputeDepthsFromLinks` builds the followed-edge adjacency from the stored `links` superset (re-applying `followsForDepth`) + redirect edges into a BFS, and finalize uses `store.LinkRows`/`Redirects`/`SaveDepthsMap` — killing the depth `LoadPagesLite` (one of the two finalize page-map loads gone). Verified by `TestDepthCSRParity` + EC24 + resume_equivalence + acceptance. **PageRank-CSR — CUTOVER DONE & LIVE:** `analyze.WithLinks` makes the link graph (unique in/out + PageRank, and the `hyperlinkedSet` for unlinked checks) a CSR over the stored `links` superset instead of each page's `Facts.Links`; finalize passes `st.LinkRows()`. Verified `TestPageRankCSRParity` (1e-9) + acceptance. **Dup-SQL — CUTOVER DONE & LIVE:** `store.DuplicateIssues(ignoreNonIndexable, ignorePaginated)` reproduces the in-RAM `issues.duplicates()` occurrence set in pure SQL — the multi-clause eligibility gate (state/scope/HTML + `indexable` + the `IsPaginated` `PrevHTML`/`PrevHTTP` columns via `json_array_length`), all 5 key types (hash/title[0]/desc[0]/H1-either-of-first-2/H2-either-of-first-2 via `json_each … WHERE key<2`), and the per-`(url,key)` detail rows; finalize runs `issues.Evaluate(lite, cfg, issues.SkipDuplicates())` then merges `DuplicateIssues`. Verified by `TestDupSQLParity` (exact set match) + `TestEdgesAndDepthSQLMethods` + EC24 golden + acceptance. `c.inlinks`/`res.Inlinks`/`RecomputeInlinks`/`RecomputeDepths` now production-unused (kept for the crawler test harness). **Peak-cut DONE (Phase 2a):** finalize loads a ContentText-free map (`store.LoadPagesLite`, frees the page body per-row) for depth/inlinks + the whole issue catalogue + analyze; the only two ContentText-dependent issue checks (lorem/soft-404) are streamed one body at a time (`store.StreamContentText` + `issues.ContentTextChecks`), near-dup loads the full map only when enabled (off by default). Pinned by `TestStreamedContentIssuesMatchWholeMapEvaluate` (byte-identical to whole-map `Evaluate`). The finalize page-body spike (regime 3, ~723 MB at 4k) is gone for the default path. **Near-dup minhash column — DONE & LIVE:** the minhash signature primitive moved to a leaf package (`internal/minhash`, importable by both crawler and analyze); the crawler precomputes each page's signature from the live body and persists it to a new `pages.minhash` BLOB column **only when near-dup is enabled** (a default crawl pays nothing), continued through the migration ladder (v4); finalize runs near-dup over the ContentText-free **lite** map (which carries the signatures) — no `LoadPages` full-load — and `analyze.nearDuplicates` decodes the column, falling back to hashing `ContentText` only for older / near-dup-off crawls (the analyze-time indexable/paginated/wordcount gate still applies over the stored signature). Verified by `TestNearDupColumnEqualsContentTextPath` (column path == ContentText path, byte-identical), `TestNearDupMinhashColumnPersisted`, `TestNearDupFinalizeFiresOffLiteMap`, `TestNearDupDisabledLeavesColumnEmpty`, `internal/minhash` unit tests + acceptance. So **all Phase-2 SQL/CSR pieces are cut over.** Residual (lowest value, no RAM win): lite map still holds `Facts.Links` → optional links-table-only CSR for the issue catalogue. | **High** (parity surface) | — |
| **3** ✅ | DONE (re-scoped). N persistent workers + deadlock-free unbounded in-RAM queue + atomic in-flight counter replacing `wg.Wait()` (`workpool.go`); `c.sem`/`process()` removed. Done-gate `TestGoroutinesBoundedByThreads`; invariants in `workpool_test.go`. SQLite feeder + `claimed`/`seq` deferred to Phase 4 (the in-RAM queue already kills the dominant goroutine-stack cost). | **High** | — |
| **4** ✅ | DONE (re-evaluated design). The in-memory `seen` map → a store-backed `frontier.Dedup` authority (`store.Admit/Remove/Seen/MarkSeen/Count` over `frontier ∪ pages`); the in-memory set is dropped when a store sink is present (crawler unit tests keep an exact `memDedup`). **The doc's perf trap is resolved by re-design:** dedup runs the atomic `INSERT OR IGNORE … WHERE NOT EXISTS(pages)` OUTSIDE the cap mutex, so its microsecond DB work never serialises the in-memory cap accounting; cap-check+bump stay under the mutex with a rare over-cap rollback (`Remove`); resume re-queues pending rows via `Readmit` (bypassing dedup-reject). Gated by `TestFrontierDBDedupExactlyOnce` (FR-04/FR-19 + EC-14, `-race`), `TestFrontierDBDedupCapRollback`, `TestAdmitExactlyOnceUnderConcurrency`, resume_equivalence + acceptance. **Remaining sub-items:** `c.inlinks` removal (blocked on the Phase-2 gated-edge table); resume limit-counter rehydration (FR-08, pre-existing). | Med-High | — |
| **5** ✅ | Bloom filter DONE: `internal/bloom` (fixed-size, atomic, no-false-negatives — `TestNoFalseNegatives`/`TestConcurrentNoFalseNegatives`/forced-100%-FP). The fast-negative is ready to front the SQLite authority once Phase 4's integration lands; bounding `seenContent` follows the same pattern. | Med | — |
| **6** | Wire the bounded-RAM/goroutine regression test (§8.2) into CI as the "done" gate. Per-crawl RAM now constant. | Low | — |
| **7** ✅ | DONE. `internal/limiter` (process-wide fetch semaphore `speed.max_global_threads` + finalize cap `F`; nil/0 = unlimited, GL-07 nil-channel). `WithLimiter` Option threaded into `crawler.New`; workers acquire a global slot only around the fetch (after the per-crawl rate wait, released even on panic). Wired into CLI crawl/list/resume + runner (per-crawl today; shared in Phase 8). Tests: `internal/limiter` unit (GL-07) + `TestGlobalLimiterBoundsFetchesAcrossCrawls` (GL-08, two crawls ≤ G). **No shared per-host buckets.** | Med | ✓ |
| **8** ✅ | DONE. Executor holds many crawls (`cur map[crawlID]`) with crawl-id `PauseCrawl/StopCrawl/SnapshotCrawl` + no-arg fan-out + a shared limiter; Dispatcher runs W drain loops (`queue.WithConcurrency`, atomic `ClaimNext` so no double-claim, wake-the-next, `Shutdown` waits all loops), `Cancel(jobID)`→`StopCrawl`. Config `speed.max_concurrent_crawls`; `projects crawl-all --parallel N` runs N members at once under one global fetch cap. Tests: GL-05/GL-17 (parallel/no-double-claim), GL-13 (shutdown-all), GL-03/GL-04 (crawl-id control). | Med | ✓ |

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

---

## 13. Edge-case → test matrix

This section is the **adversarial test contract** for the §9 rework. It enumerates every known way each
subsystem can silently corrupt the crawl, the named test that catches it, and where it lands in the §9 phase
plan. Treat it as load-bearing: a phase is not "done" until its RED-new tests pass and its guard tests stay
green. IDs are stable — cite them in PRs and test names.

**Legend.** `red-new` = a new test that must FAIL on today's code and pass after the phase. `guard-new` =
a new test pinning behaviour that is correct today and must stay correct. `guard-existing` = an existing
test (or a trivial extension) that must stay green. **Sev**: C=critical, H=high, M=medium. All concurrency
tests run under `-race`.

### 13.0 Critical must-pass-before-merge shortlist

These are the correctness-or-data-loss cases. **No phase merges until its shortlist rows are GREEN.** Each
is the canonical row after de-duplication across the source enumerations (merged ids noted in §13.7).

| Phase | ID | Failure mode (one line) | Test |
|---|---|---|---|
| 1 | SD-01 | Fresh (non-resume) crawl persists NO inlinks/discovered_from/depth once `res.Pages` is nil. | `TestFreshCrawlPersistsInlinksDiscoveredFromDepthAfterStreamDrop` |
| 1 | SD-12 | RAM still grows per-page — a lingering `*PageRecord` ref defeats the whole GC goal. | `TestNoPageRecordRetainedAfterWorkerReturns` |
| 0/1 | SD-05/06/07 | `ContentText` dropped, not just nilled-after-persist → near-dup + lorem/soft-404 issues + `compare` content-change all silently die. | `TestContentTextRoundTripsThroughStore` |
| 2 | FIN-INLINK | SQL `COUNT` over the ungated `links` superset over-counts inlinks/out-degree vs the live gated count. | `TestSQLInlinksMatchLiveCount_GatedLinks` |
| 2 | FIN-DEPTH | CSR BFS over raw `links` misses the `followsForDepth` gate + the redirect-as-hop edge → wrong depths. | `TestDepthCSR_FollowGateAndRedirectHop` |
| 2 | FIN-DFROM | First-wins `DiscoveredFrom` is discovery-order `MIN(seq)`, NOT lexical `MIN(src)`; one MIN rule for both is wrong. | `TestDiscoveredFrom_DiscoveryOrderNotLexical` |
| 2 | FIN-GOLDEN | The only end-to-end guard (resume==straight) can't catch SQL diverging from original RAM in lockstep. | `TestSQLFinalize_GoldenVsCapturedRAM` |
| 3 | WP-01 | In-flight counter hits 0 with children un-enqueued → reachable subtree silently never crawled. | `TestWorkerPool_HighFanout_NoEarlyTermination` |
| 3 | WP-02 | Counter never reaches 0 (admit-reject leaks a +1) → crawl hangs at the end. | `TestWorkerPool_AdmitRejectionBalancesCounter` |
| 3 | WP-03 | Sole-producer deadlock: last worker blocks writing discoveries into its own full buffer. | `TestWorkerPool_FullBufferSpillsNotBlocks` |
| 3 | WP-08 | Two workers claim the same frontier row → double-fetch, double inlinks. | `TestWorkerPool_NoDoubleClaim` |
| 3 | WP-10 | ctx-cancel/pause mid-fetch must leave the item PENDING, not race a `FrontierDone` delete. | `TestWorkerPool_CancelMidFetch_LeavesPending` |
| 3/4 | WP-07 | Crash with `claimed=1` rows: child written-but-not-persisted whose parent already `FrontierDone`'d is lost. | `TestWorkerPool_CrashMidFlight_ClaimedResumeNoLoss` |
| 4 | FR-01 | Bloom false-positive treated as "seen" → a novel URL is silently dropped (worse than a re-visit). | `TestBloomFalsePositive_FallsThroughToDB_NeverDrops` |
| 4 | FR-03 | `INSERT OR IGNORE … RETURNING` first-vs-dup mis-read under WAL → dup admitted or first-seen rejected. | `TestFrontierInsertOrIgnore_ReturningFirstVsDup_WAL` |
| 4 | FR-04 | Admit TOCTOU: two workers both pass Bloom-miss + cap check → double-admit / cap+1. | `TestAdmit_ExactlyOnce_ConcurrentSameURL_Race` |
| 4 | FR-08 | Resume starts limit counters at 0 → admits a full cap MORE per bucket than the straight crawl. | `TestResume_RehydrateLimitCounters_NoOverAdmit` |
| 4 | EC-14 | Re-discovered done URL re-inserts a claimable frontier row (FrontierDone deleted it) → re-fetch. | `TestFrontier_RediscoveredDoneURL_NotReadmitted` |
| 3/4 | EC-01 | Hard crash leaves `claimed=1` orphans never reset → those URLs silently lost on resume. | `TestResume_ResetsOrphanedClaimedRows` |
| 4 | EC-02 | `claimed=1` row whose page was already written (FrontierDone uncommitted) → double-fetch on resume. | `TestResume_ClaimedRowWithPageAlreadyWritten_NotRefetched` |
| 2 | EC-17 | `/hub` two-session inlink=4 regresses to 3/2 if SQL inlinks counts one session or skips the follow-gate. | `TestResumeEquivalence_HubInlinkFour` |
| 7 | GL-07 | `max_global_threads=0` (=unlimited) mis-built as `make(chan,0)` (unbuffered) → every crawl deadlocks. | `TestGlobalCapZeroMeansUnlimited` |
| 7 | GL-09 | `registry.db` opened with no `_busy_timeout`/WAL → W loops hit "database is locked"; loop silently parks. | `TestRegistryDBNoLockErrorUnderConcurrentJobOps` |
| 7 | GL-01 | Worker holds a global slot while blocked on its own full ready-buffer → all M crawls wedge. | `TestGlobalLimiterNoDeadlockOnBufferBackpressure` |
| 8 | GL-03 | Crawl-id-addressed `Pause(A)` routed to `e.cur` → pauses the wrong crawl / broadcasts. | `TestPauseAffectsOnlyTargetCrawl` |
| 8 | GL-04 | `Cancel(running B)` falls through to the queued-only store path → silent no-op, user's cancel does nothing. | `TestCancelRunningCrawlByIdOnlyStopsThatCrawl` |
| 8 | GL-05 | W dispatcher loops claim the SAME job → crawl runs twice, double finalize. | `TestClaimNextNoDoubleClaimUnderWLoops` |
| 8 | GL-13 | Whole-dispatcher `Shutdown` pauses only ONE crawl (single `doneCh`) → M−1 crawls abandoned mid-write. | `TestShutdownPausesAllInFlightCrawls` |
| 1/5 | SD-08/16 | R8 identical-shell `claim=willExpand` gate inverted under the bounded backend → outside-folder shell shadows in-folder twin, loses outlinks. | `TestSeenContentBoundPreservesFirstWinsAndClaimGate` |

---

### 13.1 Bounded worker pool (§5.2/§5.3) — **Phase 3**

N persistent workers + feeder + in-RAM ready-buffer + atomic in-flight counter replacing `wg.Wait()`;
`claimed`/`seq` frontier columns. The `wg.Wait()→counter` swap is the **#1 named trap** (§11): a worker
must INCREMENT its discoveries' in-flight count BEFORE it DECREMENTS its own item.

| ID | R/G | Sev | Failure mode | Test (type) — asserts |
|---|---|---|---|---|
| WP-01 | red-new | C | In-flight counter hits 0 with children un-enqueued → reachable subtree never crawled (decrement-before-increment). | `TestWorkerPool_HighFanout_NoEarlyTermination` (stress) — deep fan-out vs single-thread oracle, 200+ runs `-race`; every reachable URL recorded every run, no unclaimed rows left. |
| WP-02 | red-new | C | Admit-rejected discovery leaks a +1 (incremented then rejected by `Admit`) → counter never 0, `Run()` hangs. | `TestWorkerPool_AdmitRejectionBalancesCounter` (race) — ~50% dedup/limit rejects; counter returns to exactly 0, `Run()` returns within timeout; leak-detector on nonzero-with-empty-buffer. |
| WP-03 | red-new | C | Sole-producer deadlock: last worker blocks on a *blocking* buffer send instead of spill-or-leave-durable. | `TestWorkerPool_FullBufferSpillsNotBlocks` (stress) — N=1, buffer cap 4, one page → 10k children; `Run()` completes, all 10k spilled+crawled; repeat N=2..8, watchdog dumps stacks on timeout. |
| WP-04 | red-new | C | Worker panic mid-fetch leaks the count/claimed row OR shrinks the pool (effective N→0) OR leaks a global slot. | `TestWorkerPool_PanicMidFetch_NoLeakNoHang` (unit) — fetch hook panics on one URL; `Run()` returns (counter→0, claimed reset), URL recorded error/pending, others crawl, global slot released, worker count stays N. |
| WP-05 | guard-new | C | Goroutine leak after `Run()`: feeder / rate-ticker / parked workers outlive the crawl (no post-Run baseline check). | `TestWorkerPool_NoGoroutineLeakAfterRun` (unit) — snapshot `NumGoroutine` before; terminate via drain / max_urls / ctx-cancel; poll back to baseline after `Run()`. |
| WP-06 | red-new | H | max_urls hit mid-flight: in-flight workers past the gate over- or under-record; cap fuzzy by ±N. | `TestWorkerPool_MaxURLsCap_InFlightFinalize` (behavioral) — blocking fetches, N=8, max_urls=10; same set/count as single-thread cap, counter→0, no over-budget item recorded. |
| WP-07 | red-new | C | Crash with `claimed=1`: child-durable-write must happen-before parent-`FrontierDone`, else a discovered child is stranded. | `TestWorkerPool_CrashMidFlight_ClaimedResumeNoLoss` (integration) — drop process state mid-fetch, reset `claimed=1→0`, resume; resumed+session-1 == straight crawl (counts/inlinks/DiscoveredFrom). |
| WP-08 | red-new | C | Feeder/worker claim race double-processes a row (non-atomic SELECT…LIMIT then UPDATE). | `TestWorkerPool_NoDoubleClaim` (race) — high-contention frontier, aggressive batches; per-URL atomic fetch counter, any URL fetched ≥2 fails; inlinks match oracle. |
| WP-09 | guard-new | H | `firstWithContent` canonical owner is latency-determined under the pool → DuplicateOf flips run-to-run. | `TestWorkerPool_IdenticalContentCanonical_Deterministic` (stress) — N byte-identical shells, 100 runs; canonical + suppressed set identical every run (pins the determinism contract). |
| WP-10 | red-new | C | ctx-cancel/pause mid-fetch must leave the item PENDING (`claimed` reset), not race `FrontierDone` into a delete. | `TestWorkerPool_CancelMidFetch_LeavesPending` (integration) — block N fetches, cancel; all in-flight remain pending rows, `Run().Interrupted==true`, resume re-fetches exactly those → parity. |
| WP-11 | red-new | H | Rate-limiter ticker (no ctx) leaks on cancel; all N workers park on `<-c.tokens`; acquiring global slot then rate-blocking holds it idle. | `TestWorkerPool_RateLimit_NoStarvationOrLeak` (stress) — observed rate ≤ AND ≈ configured; cancel returns promptly, no ticker leak; rate-token acquired BEFORE global slot. |
| WP-13 | guard-new | H | Effective per-crawl concurrency exceeds `max_threads` if `c.sem` is removed and workers aren't strictly N. | `TestWorkerPool_ConcurrencyNeverExceedsMaxThreads` (stress) — atomic in-flight gauge ≤ `MaxThreads` at every sample for N=1,2,5,10; combine with panic-injection. |
| WP-14 | red-new | H | `seq` assigned outside the serialized Admit section → non-deterministic pull order + DiscoveredFrom. | `TestWorkerPool_SeqOrder_DeterministicDiscoveredFrom` (stress) — multi-source same-depth target, 100 runs; DiscoveredFrom == seq-MIN source every run, page ordering byte-stable. |
| WP-15 | red-new | M | Feeder busy-loops re-SELECTing on a full buffer (WAL churn) OR stalls with unclaimed rows; feeder↔worker handshake deadlock. | `TestWorkerPool_Feeder_NoBusyLoopNoStall` (stress) — query count ~O(pages/batch) not O(time); workers never idle while unclaimed rows exist; watchdog on stall. |
| WP-21 | red-new | H | After max_urls, thousands of admitted `claimed=0` rows make "no unclaimed rows remain" never true → no termination. | `TestWorkerPool_MaxURLsCap_TerminatesWithUnclaimedRows` (behavioral) — max_urls=10 with 10k admitted rows; terminates, records exactly 10, leaves rows pending-for-resume. |
| WP-22 | red-new | M | Store write error inside a worker swallowed while the counter still decrements → row lost / corrupt frontier. | `TestWorkerPool_StoreErrorMidCrawl_NoSilentLoss` (integration) — inject transient FrontierAdd/Done error; `Run()` fails loudly OR URL preserved; counter never negative/stranded. |

*Merged into the above:* FR-14/EC-03/GL-16 → **WP-01**; FR-15 → **WP-03** (buffer-starvation variant);
EC-13 (pause-leaves-pending under pool) → **WP-10**; EC-18 (stranded claims on backpressure) → **WP-15**;
WP-13 duplicates GL-14 (per-crawl ceiling) — see §13.5.

---

### 13.2 Persistent frontier + Bloom dedup + count-at-admit atomicity (§5.1, §7) — **Phases 4 (dedup/RETURNING/rehydrate) & 5 (Bloom)**

SQLite is the authority; Bloom is a fast-negative in front of it. The non-negotiable invariant (§7): the
admitted set EQUALS the exact `map[string]bool` oracle — **never drop a novel URL, never re-crawl a seen one.**

| ID | R/G | Sev | Failure mode | Test (type) — asserts |
|---|---|---|---|---|
| FR-01 | red-new | C | Bloom HIT treated as "seen⇒reject" without the DB confirm → false-positive silently drops a novel URL. | `TestBloomFalsePositive_FallsThroughToDB_NeverDrops` (property) — forced ~100% FP Bloom; admit decisions == map oracle exactly; same stream through zero-FP Bloom identical. |
| FR-02 | red-new | C | Manufactured false-NEGATIVE (bit set outside the lock / resize race / incomplete reseed) → re-crawl a seen URL. | `TestBloom_NoFalseNegative_UnderResizeAndConcurrency` (property) — insert, force resize, query N goroutines; bit always present; DB `UNIQUE(url)` admits each at most once even if Bloom lies. |
| FR-03 | red-new | C | `INSERT OR IGNORE … RETURNING` first-vs-dup mis-read under WAL (zero-rows-on-conflict). | `TestFrontierInsertOrIgnore_ReturningFirstVsDup_WAL` (integration) — real WAL: first=1 row, dup=0 rows no error; both leave exactly one row; params: empty/unicode/url-equals-pages-row. |
| FR-04 | guard-new | C | Admit TOCTOU: two workers pass Bloom-miss, both insert/count → exactly-once violated. | `TestAdmit_ExactlyOnce_ConcurrentSameURL_Race` (race) — K=64 on one URL: exactly one true, one row, each counter +1; batch of M each contended → total admitted == M. |
| FR-05 | guard-new | C | Per-depth cap race: two workers pass `≥cap` then both bump → cap+1 admitted. | `TestPerDepthLimit_NoOverAdmit_ConcurrentAtCap` (race) — `MaxURLsPerDepth=N`, 10·N concurrent; exactly N; boundary N=1, N=0. |
| FR-06 | guard-new | H | Per-subdomain cap race under `crawl_all_subdomains`. | `TestPerSubdomainLimit_NoOverAdmit_ConcurrentSameHost` (race) — `MaxPerSubdomain=N`; exactly N on host a, host b gets its own N. |
| FR-07 | guard-new | H | Per-path cap race; AND first-match-only `break` semantics must be reproduced by SQL rehydration. | `TestPerPathLimit_NoOverAdmit_AndFirstMatchOnly` (race) — exactly N under `/blog/`; a URL matching two patterns counts only against the first-listed. |
| FR-08 | red-new | C | Resume starts perDepth/perSub/perPath at 0 → admits a full cap MORE per bucket. | `TestResume_RehydrateLimitCounters_NoOverAdmit` (integration) — union admitted per bucket == cap (not 2×); fresh==resume; under concurrent workers. |
| FR-09 | red-new | H | `resumePending` double-counted (counted at first admit AND re-admitted on resume). | `TestResume_PendingNotDoubleCounted` (behavioral) — re-admitting a pending URL is a dedup-reject (no bump); bucket == distinct admitted. |
| FR-10 | guard-existing | H | `MarkSeen` made to bump counters → resumeProcessed double-counted; rehydration must be a separate GROUP-BY. | `TestMarkSeen_RemainsLimitFree` (unit) — after `MarkSeen`, all counters unchanged; rehydration verified separately. |
| FR-12 | guard-new | H | MaxURLs reserve `Add(1)>MaxURLs` split into pre-check load/compare → two workers fetch cap+1. | `TestMaxURLs_AtomicReserve_ExactlyMaxFetches_Race` (race) — `MaxURLs=M`, 10·M workers; exactly M succeed; over-cap reserve still returns done=true. |
| FR-17 | red-new | H | perSub rehydration GROUP BY host-key ≠ `urlutil.Host` (port/case/userinfo) → divergent buckets. | `TestPerSubRehydration_HostKeyMatchesUrlutilHost` (table) — port/case/userinfo variants fold exactly as the in-RAM run; rehydrated count == live perSub. |
| FR-18 | red-new | M | perSub LRU/overflow eviction resets an evicted host's count → over-admit past cap on re-see. | `TestPerSub_BoundedStore_NoCapResetOnEviction` (property) — many hosts force eviction; no host ever > N; durable count is the authority. |
| FR-19 | red-new | C | Whole-pipeline (limits+dedup+Bloom+DB) diverges from oracle when a Bloom-FP falls through WHILE a cap is at its boundary. | `TestAdmitPipeline_VsExactOracle_ConcurrentFuzz` (fuzz) — random streams through real Admit (N workers) vs mutex-guarded map oracle; admitted set identical (URLs + per-bucket counts). |
| FR-20 | red-new | M | `SetMaxOpenConns(1)` + every Admit hitting the DB → single-conn lock queue throttles the hot loop. | `TestAdmit_HotPathAvoidsDB_OnBloomHit` (behavioral) — re-admit same URL K times = O(1) DB confirms after the bit is set; novel admit = INSERT with no preceding redundant SELECT. |
| FR-21 | red-new | H | `frontier→pages` handoff gap: window where a URL is in neither table → transient false-novel admit. | `TestDedupAuthority_FrontierPagesHandoff_NoGap` (race) — pages-insert happens-before frontier-delete; URL always "seen" throughout handoff; no second fetch. |
| FR-24 | guard-existing | M | Lock-free pre-gates (length/depth/query/folder) reordered after the INSERT → rejected URLs pollute frontier+Bloom. | `TestPreGates_RejectBeforeAnyState` (table) — a URL failing any pre-gate leaves NO row, NO bit, NO bump; boundary values per gate. |
| FR-25 | guard-existing | M | `Admitted()` becomes a Bloom estimate / drifts from the durable `frontier∪pages` truth (feeds resume budget + progress). | `TestAdmitted_ExactCount_MatchesDurableTruth` (property) — `Admitted()` == distinct admitted == durable COUNT == oracle len; holds under `-race` and across resume. |

*Merged:* WP-17/EC-12 (Admit TOCTOU) → **FR-04**; WP-18/EC-11 (Bloom-vs-oracle no-drop) → **FR-01** +
**FR-19**; WP-16 (over-limit resume) → **FR-08**; FR-11/EC-06 (cumulative MaxURLs across resume) → **EC-MAXURL**
in §13.3; FR-13/FR-16 see §13.3 (crash) and §13.5 (per-crawl ceiling).

---

### 13.3 Persistence, resume, pause, crash-safety (`claimed`/`seq` + stream-and-drop) — **Phases 1–4**

The existing resume suite is all **graceful pause**; there is no hard-crash / kill-mid-crawl coverage. These
rows add it. Phase tags: claimed/seq = Phase 3–4, finalize idempotency = Phase 2, budget = Phase 4.

| ID | R/G | Sev | Failure mode | Test (type) — asserts |
|---|---|---|---|---|
| EC-01 | red-new | C | Hard crash leaves `claimed=1` orphans; feeder's `WHERE claimed=0` skips them forever → silently lost. | `TestResume_ResetsOrphanedClaimedRows` (integration) — `claimed=1→0` reset BEFORE the feeder's first SELECT; resumed set == straight; zero `claimed=1` remain. |
| EC-02 | red-new | C | `claimed=1` row whose page was already written (FrontierDone uncommitted) → re-fetched, re-charges budget. | `TestResume_ClaimedRowWithPageAlreadyWritten_NotRefetched` (integration) — feeder excludes any frontier url present in `pages`; X fetched 0× in session 2. |
| EC-04 | red-new | H | Crash mid-batch-commit (WAL `synchronous=NORMAL`) splits page-row / links-tx / FrontierAdd-Done. | `TestCrash_MidCommit_FrontierPagesConsistent` (integration) — kill at injected points; no frontier row sources a url absent from pages; resumed graph == straight. |
| EC-05 | red-new | H | Partial finalize not idempotent: `SetStatus(completed)` is set MID-sequence, before SaveDepths/Inlinks. | `TestFinalize_PartialCrash_Idempotent` (integration) — abort after each step, re-run; byte-identical end-state; status flips to completed only after aggregates+analysis persist. |
| EC-MAXURL | guard-existing | C | MaxURLs budget seed counts robots-blocked/errored `pages` rows that never consumed a fetch slot → off budget. | `TestResume_MaxURLsBudget_CumulativeAcrossSessions` (integration) — total fetched across sessions == K (seed from `state=crawled`, not all pages); N>1 workers catch atomic over-reserve. |
| EC-07 | red-new | H | `seq` per-session / from rowid collides across the resume boundary → DiscoveredFrom + pull order drift. | `TestResume_SeqMonotonicAcrossSessions_DiscoveredFromStable` (integration) — session-2 seq > max session-1 seq; DiscoveredFrom == straight; double interrupt+resume byte-identical. |
| EC-08 | guard-existing | H | Seed-lock lost: SQL `MIN(seq)` assigns a backlink as the seed's discoverer on resume only. | `TestResume_SeedDiscoveredFromStaysEmpty` (integration) — seed's `discovered_from==''` in both straight and resumed even when its backlinker is crawled in session 2; multi-seed variant. |
| EC-14 | red-new | H | Re-discovered done URL re-inserts a claimable frontier row (FrontierDone deleted it) → re-fetch. | `TestFrontier_RediscoveredDoneURL_NotReadmitted` (integration) — `/hub→/`: no new claimable row, not re-fetched, inlink still increments; holds across interrupt. |
| EC-16 | red-new | M | `seenContent` (R8) map empty on resume → identical-shell canonical assignment differs straight vs resumed. | `TestResume_IdenticalContentShortCircuit_Deterministic` (integration) — content_hash authority rehydrated; session-1 canonical stays canonical; first-owner by global seq. |
| EC-10 | guard-new | M | Resume with a CHANGED config accepted on non-CLI surfaces (refusal lives only in the cobra cmd, flag-presence not a hash). | `TestResume_ChangedConfig_RefusedUnlessForce` (behavioral) — CLI+MCP+desktop each refuse a changed scope/limit config without force; config-HASH mismatch triggers it. |
| EC-19 | red-new | M | Old-schema DB (no `claimed`/`seq`) opened by the new binary resumes with NULL seq / missing claimed (`minCrawlVersion=0`). | `TestResume_OldSchemaDB_RefusedOrCleanlyMigrated` (behavioral) — fails fast with a re-crawl/remove message OR migrates deterministically; `user_version` reflects the bump. |
| EC-13 | guard-existing | H | Pause leaves the in-flight item PENDING (single + N workers) — see WP-10 for the pool path. | `TestPause_InFlightItemStaysPending` (integration) — paused mid-fetch item's row survives, no page row, re-fetched exactly once on resume. |
| SD-13 | guard-existing | H | `discovered_from`/inlinks set late in `Run()`'s tail, AFTER `record()` persisted `''`/0 → crash-before-finalize leaves them on disk. | `TestInterruptedCrawlAggregatesMatchStraightAfterStreamDrop` (integration) — on-disk intermediate state is resume-recoverable; post-resume aggregates == straight. |

*Merged:* EC-03/EC-09 → **WP-01** / **FR-08**; EC-11/EC-12 → **FR-01**/**FR-04**; EC-06 → **EC-MAXURL**;
EC-15/EC-20 (fresh depth/inlinks emptied by stream-and-drop) → **SD-01/SD-02** in §13.6; EC-17 → §13.4 (it's
a finalize-parity assertion); FR-22/FR-23 → **EC-01**/**EC-08**.

---

### 13.4 Streaming/SQL + CSR finalize parity (§5.5) — **Phase 2**

Depth / inlinks / DiscoveredFrom / PageRank / dup / near-dup / issues derived in SQL/CSR over the `links`
table instead of the whole-map `LoadPages`. The `links` table is a **raw ungated superset** (no
`typeFlags`/nofollow/include-exclude/start-folder/`MaxLinksPerPage` filter at write); the SQL must re-apply
every gate. There are **three different self-link rules** and **two different MIN rules** — one filter cannot
serve all.

| ID | R/G | Sev | Failure mode | Test (type) — asserts |
|---|---|---|---|---|
| FIN-INLINK (EC1) | guard-new | C | SQL `COUNT` over ungated `links` over-counts inlinks (hyperlink rows that were not followed edges: crawl-off / excluded / out-of-folder). | `TestSQLInlinksMatchLiveCount_GatedLinks` (golden) — SQL inlinks per page byte-equal `RecomputeInlinks` over the same stored graph, with excluded/out-of-folder targets. |
| FIN-MLP (EC2) | guard-new | H | `MaxLinksPerPage` truncates the live count but `Page()` stores all links → SQL over-counts inlinks + out-degree. | `TestSQLEdges_RespectMaxLinksPerPage` (golden) — with the cap below a page's link count, SQL inlinks/unique_outlinks == live; edges past the cap contribute 0. |
| FIN-DFROM (EC4) | guard-new | C | DiscoveredFrom first-wins is **discovery-order `MIN(seq)`**, not lexical `MIN(src)`. | `TestDiscoveredFrom_DiscoveryOrderNotLexical` (behavioral) — where lexical-min ≠ first-discovered, SQL DiscoveredFrom == the seq/seed-locked source. |
| FIN-DFROM2 (EC3) | red-new | C | `links` rowid instability (re-crawl DELETE+reinsert) makes MIN(rowid) first-wins drift across refetch. | `TestDiscoveredFromStableAcrossRefetch` (behavioral) — after refetch, DiscoveredFrom unchanged; a `seq` column (not rowid) drives MIN. |
| FIN-SELF (EC5) | guard-new | C | Three self-link rules: inlinks EXCLUDE self, unique in/out INCLUDE self, PageRank excludes self from rank — one `dst!=src` filter breaks unique parity. | `TestSelfLink_InlinkVsUniqueVsPageRank` (golden) — on a self-linking page each metric matches its in-RAM value exactly. |
| FIN-UOUT (EC6) | guard-new | H | unique_outlinks must JOIN `links.dst→pages.url WHERE scope='internal'` (target present+internal), else external/uncrawled dsts inflate it. | `TestUniqueOutlinks_InternalCrawledTargetsOnly` (golden) — only distinct crawled-internal targets counted. |
| FIN-UDIST (EC7) | guard-new | H | unique in/out is a SET over distinct dst per src; SQL `COUNT(*)` counts rows (dup anchors / nav+footer). | `TestUniqueLinks_DistinctNotRowCount` (golden) — src→same dst ×3 = 1 unique edge; follow-filter applied per-row before dedup. |
| FIN-PR (EC8) | red-new | H | Map-iteration float accumulation is already non-bit-stable; CSR must DEFINE a canonical order, 1e-9 is a tolerance vs a jitter-bounded oracle. | `TestPageRankParity_CSRvsRAM_Within1e9` (property) — `max|pr_csr−pr_ram|<1e-9`; RAM oracle re-run to bound its own spread. |
| FIN-PRDEG (EC9) | guard-new | H | CSR mishandles empty / single-node / 2-cycle / dangling / disconnected components (dangling mass dropped, base each iter). | `TestPageRank_DegenerateGraphs` (table) — each degenerate shape == current RAM within 1e-9; node set excludes non-crawled/external. |
| FIN-PRNODE (EC10) | guard-new | M | CSR builds the node set from `links` endpoints instead of the `pages` predicate (`internal∧crawled`). | `TestPageRank_NodeSetFromPagesPredicate` (golden) — a non-crawled internal link target is excluded as a rank holder. |
| FIN-PRSCALE (EC27) | guard-new | M | `v/max·100` with max over the wrong node set / missing `max>0` guard → shift or div-by-zero; empty rank leaves `link_score` default 0. | `TestPageRankScaling_MaxOverNodeSet` (golden) — top == 100.0; all == rank/max·100 within 1e-9; zero-rank graph leaves default 0. |
| FIN-CSRID (EC28) | red-new | H | CSR `url→id` built without a deterministic ORDER BY → accumulation order unstable → link_score drift > 1e-9. | `TestCSRNodeIDs_DeterministicOrder` (property) — repeated finalize passes byte-identical link_score/unique columns. |
| FIN-DEPTH (EC11) | red-new | C | Depth CSR over raw `links` misses the `followsForDepth` gate AND the redirect-as-hop edge (redirect lives in `pages.redirect_url`). | `TestDepthCSR_FollowGateAndRedirectHop` (golden) — CSR BFS depths byte-equal `RecomputeDepths` over the same graph. |
| FIN-NODEPTH (EC12) | guard-new | H | Sitemap-only pages must stay `NoDepth(-1)→NULL`; CSR init to 0 flips sitemap-orphan detection. | `TestDepthCSR_SitemapOnlyKeepsNoDepth` (golden) — sitemap page → NULL depth, sitemap_orphan still fires. |
| FIN-DEPTHDF (EC13′) | guard-new | M | A CSR that emits a BFS parent and writes it as `discovered_from` corrupts first-wins. | `TestDepthCSR_DoesNotTouchDiscoveredFrom` (behavioral) — after the depth pass, `discovered_from` byte-identical; only depth changes. |
| FIN-REDIR (EC25) | guard-new | H | Redirect edge is in `pages.redirect_url`, not `links` → CSR misses the hop (depth) or counts it as a hyperlink inlink. | `TestRedirectHop_DepthYesInlinkNo` (golden) — B gets depth(A)+1, B's hyperlink inlinks exclude the redirect, `discovered_from(B)=A`. |
| FIN-AFOLLOW (EC26) | guard-new | M | `always_follow_canonicals/redirects` admit-time same-depth override must NOT survive the uniform +1 BFS recompute. | `TestAlwaysFollowFlags_BFSUsesPlusOne` (golden) — final depths == uniform-+1 BFS, not the admit-time value. |
| FIN-CYCLE (EC29) | guard-new | H | Generic shortest-path relaxes edges repeatedly / sets depth on unreachable cycle nodes ≠ first-visit BFS. | `TestDepthCSR_CyclesAndDisconnected` (table) — reachable cycle = min BFS depth; disconnected cluster stays NULL. |
| FIN-DUPH (EC14) | red-new | C | Dup H1/H2 is "either of first 2" (up to 2 keys/page) + same-page-two-identical-H1 self-pair — not a plain `GROUP BY pages.h1`. | `TestDupH1H2_EitherOfFirstTwo` (golden) — both-of-first-two match fires; identical-two-H1 page flagged as its own duplicate. |
| FIN-DUPGATE (EC15) | guard-new | C | Dup eligibility gate is multi-clause incl. `IsPaginated` (a Facts/JSON predicate with NO SQL column). | `TestDupEligibilityGate_AllClauses` (table) — each excluded class absent from every dup group; needs a precomputed `is_paginated` column. |
| FIN-DUPKEY (EC16) | guard-new | H | Empty/NULL dup keys grouped together → spurious "all title-less pages" duplicate group. | `TestDupKeys_EmptyAndNullExcluded` (golden) — empty title/desc/h1 produce no occurrences; only non-empty shared keys ≥2 fire. |
| FIN-DUPDET (EC17′) | guard-new | H | Issues PK is `(url,issue,detail)`; a page in two H1 groups yields TWO rows — SQL must not collapse to one per (url,issue). | `TestDupOccurrence_PerKeyDetailRows` (golden) — two distinct shared H1s → two `h1_duplicate` rows. |
| FIN-INAGG (EC18) | red-new | H | `inlinkAggregates` self-excluded; `canonical_unlinked` ref = **lexical `MIN(src)`** (contrast DiscoveredFrom seq-order); indexableSrc JOINs the SOURCE. | `TestInlinkAggregates_SQLJoinParity` (golden) — nofollow-only / follow+nofollow / non-indexable-only / canonical_unlinked (lexical-MIN detail) all match current. |
| FIN-UNLINK (EC30) | guard-new | H | hreflang/pagination/canonical-unlinked use a hyperlink-only self-excluded set + lexical-MIN annotated source; raw superset mismarks. | `TestUnlinkedChecks_HyperlinkSetParity` (golden) — fire on the same URLs with the same lexical-MIN source detail. |
| FIN-NDSIG (EC19) | red-new | H | Near-dup signature precomputed at crawl time decouples from the analyze-time gate (IndexableOnly / paginated / wordcount). | `TestNearDupSignature_GateAppliedAtAnalyzeTime` (golden) — gate applied at ANALYZE over the stored signature; toggling flags changes the set identically. |
| FIN-NDDET (EC20) | red-new | M | Near-dup `ClosestMatch` already non-deterministic (map-iter candidate order + strict-`>` keep) → no byte-identical column without a pinned order. | `TestNearDup_DeterministicClosestMatch` (property) — candidate order pinned (`ORDER BY url`); Count symmetric and stable across runs. |
| FIN-SAVEAN (EC21) | guard-new | H | `SaveAnalysis` is per-URL UPDATE (not DELETE+insert) → a page dropping out of the score map keeps a STALE value. | `TestReanalyze_StaleScoreColumns` (behavioral) — re-analyze reproduces current stale-vs-reset byte-for-byte; document the chosen semantics. |
| FIN-SAVEISS (EC22) | guard-new | H | Two-stage issue write (per-page DELETE+insert THEN analysis append); reorder/split across commits wipes analysis occurrences or half-empties the table. | `TestStreamingIssues_ReplaceAndCrashSafety` (integration) — full set == SaveIssues+AddIssues; a mid-finalize crash leaves old-complete OR new-complete, never partial. |
| FIN-CONV (EC23) | guard-existing | C | `finalize.Crawl` still consumes the live `res.Pages` for `UpdateInlinks` while resume uses LoadPages → fresh≠resume. | `TestFreshEqualsResume_SQLFinalize` (integration) — `TestResumeEquivalence` stays green with SQL finalize on depth/inlinks/unique/DiscoveredFrom/link_score/issues. |
| FIN-GOLDEN (EC24) | red-new | C | Resume==straight can't catch SQL diverging from the ORIGINAL RAM semantics in lockstep (both arms share impl; link_score only ±0.01). | `TestSQLFinalize_GoldenVsCapturedRAM` (golden) — capture current RAM finalize outputs as goldens; SQL/CSR reproduces byte-identically (PageRank within 1e-9). |
| EC-17 | guard-existing | C | `/hub` two-session inlink=4 regresses if SQL counts one session or skips the follow-gate. | `TestResumeEquivalence_HubInlinkFour` (behavioral) — both straight and resumed report `/hub.Inlinks==4` over the full two-session links table, self/nofollow excluded. |

---

### 13.5 Thin global limiter + parallel multi-crawl control (§5.6, §0.1/§0.2) — **Phase 7 (limiter) & Phase 8 (control)**

The single highest-probability impl bug is `max_global_threads=0`→`make(chan,0)` (GL-07). The single biggest
**unaddressed plan blocker** is registry.db lock contention (GL-09): `registryDB()` opens a fresh handle per
call with NO `_busy_timeout` and NO WAL, and the dispatcher silently parks a loop on a claim error.

| ID | R/G | Sev | Phase | Failure mode | Test (type) — asserts |
|---|---|---|---|---|---|
| GL-07 | red-new | C | 7 | `max_global_threads=0` (=unlimited) built as unbuffered `make(chan,0)` → every fetch blocks forever. | `TestGlobalCapZeroMeansUnlimited` (unit) — cap 0: single crawl reaches full per-crawl concurrency; cap 3: `SUM(in-flight)≤3` across M=2. |
| GL-09 | red-new | C | 7 | `registry.db` no `_busy_timeout`/WAL → W loops get "database is locked"; `dispatcher.go:137` treats it as "nothing to do" and parks. | `TestRegistryDBNoLockErrorUnderConcurrentJobOps` (race) — W=4 doing ClaimNext+SetCrawlID+Finish+Enqueue; ZERO errors, every job terminal exactly once. |
| GL-01 | red-new | C | 7 | Worker holds a global slot while blocked on its own full ready-buffer (slot scope wraps push, not just fetch) → all M wedge. | `TestGlobalLimiterNoDeadlockOnBufferBackpressure` (integration) — G=2, M=3, tiny buffer, high fan-out; all complete, ≤2 inside the network fetch at every sample. |
| GL-08 | red-new | H | 7 | Limiter constructed per-crawl (Option omitted on a path) → `SUM` across M is M·G not G. | `TestGlobalCapIsProcessWideAcrossMCrawls` (integration) — G=3, M=4×4 threads; global max observed ≤3, never 12. |
| GL-02 | red-new | H | 7 | Plain channel semaphore is not FIFO → a large crawl greedy-reacquires, starves a small one. | `TestGlobalLimiterFairShareNoStarvation` (stress) — small crawl completes within a bounded multiple of solo runtime while a large one saturates G. |
| GL-19 | red-new | H | 7 | Global slot leaked on cancel/panic mid-fetch (release not deferred) → semaphore permanently depletes. | `TestGlobalSlotReleasedOnCancelMidFetch` (stress) — dozens of start+cancel cycles; a fresh crawl afterward still gets full G. |
| GL-10 | red-new | H | 7 | Finalize cap `F` not enforced → M simultaneous CSR/PageRank passes stack RAM spikes (OOM at finalize). | `TestFinalizeConcurrencyCapBoundsParallelPasses` (integration) — F=1, M=3 finishing together; ≤1 finalize in-flight, all complete, no F-vs-slot deadlock. |
| GL-14 | guard-new | H | 7 | Effective per-crawl concurrency exceeds `speed.max_threads` (global replaces rather than composes with the pool). | `TestPerCrawlThreadCeilingStillHoldsUnderGlobalCap` (integration) — `min(per-crawl, global)`: threads=4 never exceeds 4; global=2 binds tighter. |
| GL-18 | red-new | H | 7/8 | No max-concurrent-CRAWLS knob → "crawl all project" starts all members; per-crawl fixed overhead (Bloom/buffers/DBs/conn pools) OOMs on a different axis than the fetch cap. | `TestMaxConcurrentCrawlsBounded` (integration) — 20 jobs, cap 4; ≤4 in-flight, open-DB/goroutine count bounded by 4·per-crawl. *(documents the missing knob if absent.)* |
| GL-03 | red-new | C | 8 | Crawl-id `Pause(A)` routed to single `e.cur`/`r.cancel` → pauses the wrong crawl / broadcasts. | `TestPauseAffectsOnlyTargetCrawl` (integration) — M=3; `Pause(B)` interrupts B, A+C keep progressing and complete. |
| GL-04 | red-new | C | 8 | `Cancel(running B)` falls to the queued-only store path → silent no-op (returns false). | `TestCancelRunningCrawlByIdOnlyStopsThatCrawl` (integration) — `Cancel(B)` ok=true, B stops, A+C unaffected; not dropped to queued-only path. |
| GL-05 | guard-new | C | 8 | W dispatcher loops claim the SAME job → crawl runs twice, double finalize, corrupted counts. | `TestClaimNextNoDoubleClaimUnderWLoops` (race) — W loops drain K jobs; each id claimed exactly once; both MemStore + SQLiteStore. |
| GL-06 | guard-new | H | 8 | MemStore `find()` returns `&m.jobs[i]`; Enqueue append reallocs the backing array → aliasing race under W loops + a `map[jobID]` cache. | `TestMemStoreConcurrentClaimEnqueueListRace` (race) — interleaved Enqueue/Claim/SetCrawlID/Finish/List; `-race` clean, every Finish lands on the right id. |
| GL-11 | red-new | H | 8 | `Snapshot` not crawl-id-addressed → desktop/MCP show the wrong crawl's live counters. | `TestSnapshotIsCrawlIdAddressed` (integration) — M=3; `Snapshot(id)` CrawlID matches, Total consistent with that crawl. |
| GL-12 | red-new | C | 8 | Shared `registry.db` coarse writes (CreateCrawl/SetStatus/markTerminal) interleave/clobber under M crawls (same root as GL-09). | `TestParallelCrawlsRegistryWritesConsistent` (integration) — M=4 real crawls; exactly M rows, correct terminal status, non-clobbered counts, no lock errors. |
| GL-13 | red-new | C | 8 | Whole-dispatcher `Shutdown` pauses only ONE crawl (single `doneCh`/`exec.Pause()`) → M−1 abandoned. | `TestShutdownPausesAllInFlightCrawls` (integration) — M=3, W≥2; ALL paused/resumable, Shutdown waits for every loop, no goroutine leak. |
| GL-17 | red-new | H | 8 | Single cap-1 `wakeCh` shared by W loops → a burst enqueue wakes one loop, throughput collapses to serial. | `TestWLoopsDrainConcurrentlyAfterBurstEnqueue` (integration) — W=3 idle, burst 3 jobs; peak concurrent runs == min(W,M) == 3. |
| GL-21 | red-new | H | 8 | Concurrent Finish vs Cancel/SetStatus on the same row (no busy_timeout) → a dropped update leaves a row stuck "running" → reconciled-interrupted though it completed. | `TestNoOrphanedRunningRowUnderConcurrentFinishAndCancel` (race) — after idle, ZERO job/crawl rows "running", no silently-dropped update. |
| GL-15 | guard-new | M | 8 | Stop/Pause first-wins latch is timing-dependent across M crawls + a global Shutdown. | `TestStopPauseLatchDeterministicPerCrawl` (race) — latched mode == first arrival per crawl; global Shutdown(pause) concurrent with targeted Stop(B) honors documented precedence. |
| GL-20 | guard-new | M | 8 | `Reconcile`-on-Start blanket-flips running rows belonging to a still-draining prior dispatcher (overlapping restart). | `TestReconcileDoesNotClobberLiveRunningJobs` (integration) — a generation/owner check prevents flipping live rows, or the 2nd Start is a no-op while loops are alive. |
| GL-22 | guard-new | M | 8 | Parity drift: a bug exists in only one store backend (MemStore↔SQLiteStore). | `TestParallelControlParityAcrossStores` (table) — claim-once / addressed-pause / shutdown-all run over `{MemStore, SQLiteStore}`, identical behaviour. |

*Merged:* GL-16 (worker-pool early termination under parallel) → **WP-01**; WP-12 (global FIFO/no-starvation)
→ **GL-02**; WP-19 (global slot leak on cancel/panic) → **GL-19**.

---

### 13.6 Stream-and-drop record release — `c.pages`/`Result.Pages` consumers + `seenContent` bound (§5.4) — **Phase 0/1 (+ Phase 5 for `seenContent`)**

`Result.Pages` becomes slim/nil and `c.pages[url]=rec` is dropped. Every consumer of the live map must be
re-sourced from the store, and `ContentText` must round-trip (three consumers beyond near-dup need the raw
text, not the minhash signature). `~30 internal/crawler unit tests assert against `res.Pages`` and break
wholesale — they are the guard layer and must be re-sourced FIRST.

| ID | R/G | Sev | Failure mode | Test (type) — asserts |
|---|---|---|---|---|
| SD-01 | red-new | C | Fresh crawl persists NO inlinks/discovered_from/depth (`UpdateInlinks(res.Pages)` iterates an empty map). | `TestFreshCrawlPersistsInlinksDiscoveredFromDepthAfterStreamDrop` (integration) — `LoadPages` shows exact inlinks, first-wins discovered_from, shortest-path depth == pre-rework golden. |
| SD-02 | guard-existing | C | Fresh depth all NULL — BFS iterates the emptied `c.pages`. | `TestFreshDepthBFSEqualsGoldenAfterStreamDrop` (golden) — per-URL depths byte-identical incl. sitemap-only NoDepth + redirect hop; `followsForDepth` gate still applied. |
| SD-03 | guard-existing | C | Fresh DiscoveredFrom empty / seed gets a spurious one (resolution iterates the dropped map). | `TestFreshDiscoveredFromFirstWinsAndSeedLockedAfterStreamDrop` (behavioral) — seed `''`, doubly-linked page == seq-first source. |
| SD-05/06/07 | guard-existing | C | `ContentText` dropped (not nilled-after-persist) → near-dup zeroes, lorem/soft-404 issues stop firing, `compare` content-change reports nothing. | `TestContentTextRoundTripsThroughStore` (golden + behavioral) — near-dup pairs (read from the persisted minhash column), `content_lorem_ipsum`/`content_soft_404` on the right URLs, `compare.Run` reports the content Change — all == golden. |
| SD-04 | red-new | H | list-mode CLI summary prints all-zeros (`printSummary(res.Pages)` with no LoadPages fallback). | `TestListModeSummaryReSourcedFromStore` (integration) — summary line shows true non-zero tallies from the store. |
| SD-08/16 | guard-new | C | R8 `claim=willExpand` gate inverted under the bounded backend → non-expanding outside-folder shell becomes canonical, shadows in-folder twin, loses outlinks. | `TestSeenContentBoundPreservesFirstWinsAndClaimGate` (table/property) — claim=false never INSERTs/becomes canonical; first willExpand=true caller wins; Bloom-FP resolves via DB to first=true; concurrent `-race`. |
| SD-09 | guard-new | H | Concurrent byte-identical pages resolve canonical NON-deterministically under the pool (first-INSERT-wins ≠ seq). | `TestIdenticalShellCanonicalDeterministicUnderPool` (race) — 50× N>1: canonical + every child's DuplicateOf identical and == single-thread golden. |
| SD-10 | guard-existing | H | ~30 crawler unit tests read `res.Pages` → nil-deref / fail wholesale, masking real regressions. | `TestCrawlerUnitTestsReSourcedFromStoreSink` (behavioral) — migrate assertions onto a capturing Sink/LoadPages; helper fails if `res.Pages` is read post-rework. |
| SD-11 | guard-existing | H | Rendered-mode facts (JSDiff, rendered-only links, rendered StructuredData) lost if a consumer re-reads the dropped rec instead of the store. | `TestRenderedFactsPersistAndReSourcedAfterStreamDrop` (integration) — `LoadPages` shows JSDiff + rendered link (origin=rendered); depth/inlinks for the JS-only target from the store graph. |
| SD-12 | red-new | C | RAM still grows per-page — a lingering `*PageRecord` ref (slim-copy / closure / finalization) pins Facts. | `TestNoPageRecordRetainedAfterWorkerReturns` (stress) — `*PageRecord`/Facts HeapAlloc does NOT scale with crawled-page count; per-page retention slope ~0. |
| SD-14 | red-new | M | `st.Counts()` error → fallback to `res.Crawled/Total` which are now 0 → "Found 0 URLs" / registry 0/0. | `TestFinalizeCountsFallbackNotZeroAfterStreamDrop` (unit) — injected Counts() error: fallback still correct (streamed tally/PageCount), never 0. |
| SD-15 | guard-existing | H | Resume `RecomputeInlinks/Depths` read `rec.Facts.Links`; a RAM-saving trim of links from the facts JSON (keeping only the table) → empty edges → under-count. | `TestResumeRecomputeReadsFullLinkGraphAfterStreamDrop` (integration) — `/hub` inlink=4 cross-session; either Facts.Links stays in JSON OR recompute is re-pointed at the links table. |
| SD-17 | guard-existing | M | desktop page-detail Outlink/Inlink refs empty if Facts.Links are trimmed (same dependency as SD-15). | `TestDesktopLinkRefsSurviveStreamDrop` (integration) — Out/InlinkRefs (From/To/Anchor/Type/Position/Nofollow/Origin) == golden. |

---

### 13.7 Test infrastructure needed

These harnesses are prerequisites; several edge cases are untestable without them. Build them in Phase 0
alongside the guard/RED layer.

- **Crash-injection harness** — kill the process / drop in-RAM state while keeping the DB, at injectable
  points (between page-Exec and links-tx; between FrontierAdd-of-children and FrontierDone-of-parent; mid-WAL,
  pre-checkpoint). Drives EC-01/02/04/05, WP-07, FIN-SAVEISS. *Today every resume test is a graceful pause —
  this is the single biggest coverage gap.*
- **High-fan-out / deep-graph fixture** — one shallow page → thousands of children, several levels (discovered
  ≫ crawled). Drives WP-01/03/14/21, SD-12, GL-16.
- **Exact-map oracle + property/fuzz runner** — a mutex-guarded `map[string]bool` Admit oracle and a randomised
  URL-stream generator (with dups, varied depth/host/path, crafted Bloom-collision URLs, forced FP/FN). Drives
  FR-01/02/04/19, SD-08/16.
- **Forced-Bloom harness** — construct a tiny/over-saturated Bloom (≈100% FP) and a zero-FP huge Bloom; force a
  resize mid-stream. Drives FR-01/02.
- **Atomic in-flight fetch gauge** — instrument the fetch client with an atomic counter + max-watermark, sampled
  concurrently. Drives WP-08/13, GL-01/07/08/14, the "SUM(in-flight)≤G" assertions.
- **M-crawl parallel harness** — spin up M dispatcher loops / executors against small fixtures end-to-end, with
  crawl-id-addressed Pause/Stop/Cancel/Snapshot and a watchdog that dumps goroutine stacks on timeout. Runs over
  **both** `{MemStore, SQLiteStore}`. Drives all GL-* and GL-22.
- **Goroutine-leak probe** — `NumGoroutine()` baseline snapshot before/after `Run()` and after Shutdown, with a
  short settle poll. Drives WP-05, GL-13/19.
- **Captured-RAM golden harness** — capture the CURRENT in-RAM finalize outputs (depth, inlinks, unique in/out,
  link_score, dup occurrences, near-dup, issue rows) as goldens on representative fixtures, to diff the SQL/CSR
  path against (1e-9 for PageRank). Drives FIN-GOLDEN and all of §13.4. *Without this, resume==straight passes
  even if SQL diverges from original RAM in lockstep.*
- **Capturing in-memory Sink** — a `Sink` that records every `Page()` so crawler-package assertions read
  persisted records, not `res.Pages`; plus a guard helper that fails if `res.Pages` is read post-rework. Drives
  SD-10 and the re-sourcing of ~30 unit tests.
- **Memory-slope probe** — `runtime.ReadMemStats` sampled across two page-count sizes to assert per-page
  retention slope ~0 (optionally a finalizer/weak-ref sentinel). Drives SD-12, the Phase-6 "done" gate.
- **`-race` in CI on every concurrency test** — non-negotiable; most critical rows only fail under the race
  detector. Wire the bounded-RAM/goroutine regression as the Phase-6 merge gate.

### 13.8 Gaps found by the completeness critic (additions)

A second adversarial pass found whole failure-mode classes and cross-subsystem interactions the per-subsystem
sweep missed. **Four of these are critical and must-pass-before-merge** (added to the §13.0 contract):
`TestGlobalLimiter_RenderSlotsBounded`, `TestCrawl_DiskFullMidWrite_FailsLoudlyResumable`,
`TestCancelDuringFinalize_ReleasesFSlot_LeavesReRunnable`, `TestResumeUnderParallelLoad_RehydrationCorrectAndIsolated`.

**A. Failure-mode classes with no representation**

| id | failure mode | test (type, red/guard) | sev |
|---|---|---|---|
| REN-01 | M parallel JS-mode crawls each spawn a Chrome pool — renderers are a distinct OOM/FD axis the fetch semaphore does **not** bound (a render ≠ a fetch slot). Explicit §11 invariant with zero rows. | `TestGlobalLimiter_RenderSlotsBounded` (integration, red-new): SUM(Chrome instances) ≤ render cap across crawls; no renderer-slot leak on cancel-mid-render; a held render slot doesn't also pin a fetch slot | **critical** |
| IO-01 | Disk-full / ENOSPC / read-only FS mid-crawl. Stream-and-drop makes SQLite the sole authority → a failed `FrontierAdd`/`Page`/WAL write is now **data loss**, not recoverable RAM. | `TestCrawl_DiskFullMidWrite_FailsLoudlyResumable` (integration, red-new): Run returns the error (no silent success); no page reported crawled that wasn't durably written; DB reopens clean for resume | **critical** |
| IO-02 | `SQLITE_BUSY` on the **per-crawl** DB. Feeder `SELECT…claimed` + worker `INSERT…RETURNING` + finalize CSR reads now contend on one `crawls/<id>.db` under `SetMaxOpenConns(1)`. Only `registry.db` busy-timeout is covered today. | `TestCrawlDB_NoBusyErrorUnderFeederWorkerFinalizeContention` (race, red-new): zero `SQLITE_BUSY` surfaces; no silent park | high |
| DL-01 | `context.DeadlineExceeded` (per-fetch HTTP timeout) vs pause-cancel are conflated → a timed-out fetch (should be a recorded error page) gets left PENDING, or a paused item gets recorded as an error. | `TestWorkerPool_FetchDeadlineVsPauseCancel_Distinct` (integration, red-new): timeout → error page + counter decrement; pause → item PENDING; not interchangeable | high |
| ENC-01 | Bloom-key vs SQLite `UNIQUE(url)` vs `urlutil.Host` disagree on IDN / percent-encoded / case-variant / trailing-dot URLs → false-novel double-crawl or false-dup drop. | `TestAdmit_UnicodeAndPercentEncodedKeys_BloomDBHostAgree` (table/property, red-new): admit-exactly-once + correct `perSub` bucket; Bloom-key == DB-key == oracle-key | high |
| BIG-01 | One pathological page (multi-MB body, 100k pre-truncation links) spikes the buffer push + the ungated links-table INSERT (links is a superset). | `TestWorkerPool_SingleHugePage_NoRAMSpikeNoWALBlowup` (stress, red-new): per-page slope stays flat; links write batched, not one statement that blocks the single conn | medium |
| MET-01 | Counters (`Crawled/Total/Found`, `c.fetched`) torn/non-monotonic under N concurrent workers + feeder admitting ahead; over-budget drop makes `c.fetched` exceed MaxURLs → progress reads >100%. | `TestSnapshot_CounterAccuracyUnderPool` (race, red-new): no torn/negative counter; Crawled ≤ true recorded count; progress never >100% | medium |
| CLK-01 | Rate-limiter under clock jump / sub-ms interval with many workers; `max_urls_per_sec` extremes (very high ≈ unbounded, 0 = unlimited path). | `TestRate_ExtremesAndMonotonicElapsed` (fuzz): observed rate uses monotonic elapsed, not wall clock | low |

**B. Under-used test types**

| id | gap | test (type) | sev |
|---|---|---|---|
| SOAK-01 | A leak (ticker, global-slot 1-per-N-cancels) only shows over thousands of cycles; all stress tests are short. | `TestWorkerPool_SoakManyCrawlCycles_NoLeak` (soak): hundreds of start→cancel→resume cycles; NumGoroutine, open DB handles, and global-sem available-count all return to baseline | high |
| KILL-01 | `SIGKILL` mid-WAL-write (partial frame) is a different recovery path than a clean exit-without-finalize; crash-injection is named but no test drives a real kill. | `TestCrash_SIGKILLMidWALWrite_RecoversToConsistentDB` (integration, crash-injection): kill child mid-write; reopened DB passes `PRAGMA integrity_check`; resume reaches straight-crawl parity | high |
| DET-01 | Per-output determinism rows exist, but no single end-to-end "100× identical full crawl under N>1 workers" diffing the **entire** artifact; cross-output ordering interactions can drift while each row passes. | `TestFullCrawl_ByteIdenticalAcross100Runs_PooledRace` (golden+stress) | high |
| FZ-01 | Fuzz is concentrated on Admit; finalize (CSR/PageRank/dup/near-dup) has only golden + one PageRank property test. | `TestFinalize_FuzzGraphTopologies_VsRAMOracle` (fuzz): random graphs (cycles, multi-component, self-loops, dangling) vs the captured RAM oracle within 1e-9 | high |

**C. §10 invariants with no test**

| id | gap | test (type) | sev |
|---|---|---|---|
| SIG-01 | "graceful pause on Ctrl-C" — the actual `SIGINT` signal path is untested (only programmatic cancel is). | `TestSIGINT_GracefulPauseResumable` (integration): real SIGINT → interrupted+resumable, not hard kill | high |
| CFG-01 | "resume refuses changed config unless `--force`" tests the refusal, not that `--force` **applies** the new config and rehydrates counters against the **new** limits. | `TestResume_ForceChangedConfig_RehydratesAgainstNewLimits` (behavioral) | medium |

**D. Cross-subsystem interactions (the biggest untested category)**

| id | interaction | test (type, red/guard) | sev |
|---|---|---|---|
| X-01 | Stop/Shutdown arriving **during finalize** (CSR/PageRank can take seconds under F-cap) — must complete or leave a re-runnable state, and **must release the F-slot**. | `TestCancelDuringFinalize_ReleasesFSlot_LeavesReRunnable` (integration, red-new) | **critical** |
| X-02 | Resume crawl A while B,C run: A's rehydration (perDepth/perSub, Bloom rebuild, `claimed=1→0` reset) happens under live contention on the shared limiter + registry. | `TestResumeUnderParallelLoad_RehydrationCorrectAndIsolated` (integration, red-new): A's counters/claimed-reset correct; doesn't touch or starve B/C | **critical** |
| X-03 | On resume after stream-and-drop, the Bloom must rebuild from `frontier∪pages` **and** `seenContent`/content_hash from the persisted minhash column — simultaneously. A bug where one reseeds and the other doesn't (or reads nilled `ContentText`) only appears with both reworks on. | `TestResume_BloomAndContentHashRehydrateTogetherFromStore` (integration, red-new) | high |
| X-04 | Crawl A hits MaxURLs and wants to finalize, but the F=1 slot is held by B → A's worker termination must not deadlock waiting for the finalize slot. | `TestMaxURLsTermination_WhileFinalizeSlotHeldBySibling_NoDeadlock` (integration) | high |
| X-05 | `seenContent` canonical-owner determinism when byte-identical twins straddle the resume boundary **and** are processed by N concurrent workers in session 2. | `TestSeenContent_CanonicalDeterministic_AcrossResumeUnderPool` (stress) | medium |
| X-06 | Crawl terminates at the cap with admitted-but-unclaimed frontier rows whose inlinks/`discovered_from` were only going to be finalized at end — do they leave correct partial aggregates for a later resume? | `TestMaxURLsCap_UnclaimedRowsAggregatesResumeRecoverable` (integration) | medium |

### 13.9 Sequencing & weakness flags on existing rows

- **`EC24` (captured-RAM golden) is a Phase-2 entry-gate, not just a row.** Every "guard" SQL-parity row in §13.4
  asserts against a contract that **does not exist until the current in-RAM finalize outputs are captured as goldens
  first**. Build EC24 before any other §13.4 work; mark the rest of §13.4 blocked-on-EC24. (Otherwise `resume==straight`
  can pass even if SQL diverges from the original RAM impl in lockstep.)
- **PageRank 1e-9 tolerance needs a precondition guard.** The RAM oracle's own float jitter (map-iteration accumulation
  on large graphs) must be proven `< 1e-9` first, else the whole tolerance is meaningless. Add
  `TestPageRankRAMOracle_JitterBoundedBelowTolerance` as a distinct gate.
- **Memory-probe must be de-flaked.** `runtime.ReadMemStats` HeapAlloc is GC-timing-sensitive and flaky in CI. Promote
  the **finalizer/weak-ref retention sentinel** to the *primary* "records are released" signal (the slope becomes
  secondary), and pin a forced-`runtime.GC()` + `GOGC` protocol before sampling; prefer `HeapInuse` over `HeapAlloc`.
