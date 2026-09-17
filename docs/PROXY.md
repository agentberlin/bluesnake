# Proxy support — requirements & implementation spec

Status: **specification**. Nothing here is implemented beyond the single
`http.proxy` field described in §3.

bluesnake crawls sites it does not control — competitor audits, pre-sales
audits, agency portfolios — so a WAF allowlist is not always available. This
document specifies proxy support: what we build, in what order, what it buys us,
and (importantly) what it does not.

**Scope note:** all crawls are of *public* URLs. Site-level authentication
(`http.auth.*`) is out of scope for the driving use case, but is *not* out of
scope for the design — see §7.4, where it constrains rotation.

---

## 1. Decisions

Settled before writing this. Rationale in the sections named.

| # | Decision | §  |
|---|---|---|
| D1 | Ship a **single rotating gateway** (Bright Data) as the supported, documented path. | §4 |
| D2 | Build the **per-request selection seam** for N proxies from day one even though we ship with N=1. The marginal cost is small; retrofitting it is not. | §6.2 |
| D3 | **Degraded crawls fail loud.** A crawl that silently completes with a dead proxy pool is a defective audit. | §8.5 |
| D4 | Proxy rotation **never auto-raises** `speed.max_urls_per_sec` or `speed.max_threads`. The operator raises them deliberately. | §8.6 |
| D5 | `http.trusted_cert_dirs` must be **implemented first** — Bright Data's native proxy MITMs TLS and does not work without it. | §5.3 |
| D6 | Fix the **renderer proxy leak** before anything else. It is a live correctness and privacy bug today. | §3.2 |
| D7 | Record the proxy **per page** in the store. Without attribution, diagnosing a partially-blocked crawl is guesswork. | §7.3 |

---

## 2. Why — and the honest limits

### 2.1 What proxies actually buy

Rotation raises throughput **only when the constraint is keyed on source IP**:
`nginx limit_req`, a Cloudflare rate-limiting rule keyed on IP, an AWS WAF
rate-based rule on its default IP aggregation. That covers most ordinary sites,
and there N proxies buys roughly N× headroom.

### 2.2 What they do not buy

Against managed bot platforms (Cloudflare Bot Management, Akamai, DataDome,
PerimeterX) rotation buys **nothing**, because the discriminator is not the IP:

- **TLS fingerprint (JA3/JA4).** Go's `crypto/tls` ClientHello is a distinctive
  non-browser fingerprint. AWS WAF now supports rate-based rules aggregated on
  JA3/JA4 directly, which defeats IP rotation by design.
- **HTTP/2 fingerprint.** SETTINGS frame order/values differ between Go's
  `net/http2` and Chrome.
- **Header-order and mismatch signals.** bluesnake deliberately sends
  Screaming Frog's browser-shaped `Accept` / `Cache-Control` / `Pragma`
  ([`internal/fetch/fetch.go:39`](../internal/fetch/fetch.go#L39)) over a Go TLS
  stack. A browser `Accept` with a Go JA4 is itself a flag.
- **ASN reputation.** Datacenter proxy ranges are widely pre-blocked. A cheap DC
  pool can perform *worse* than one clean origin IP.

**Consequence:** rotation is necessary but not sufficient for hard targets. §9.4
covers the fingerprint question as its own phase, because for competitor audits
it is the binding constraint, not the IP.

### 2.3 Cheaper wins that are not proxies

Two findings from the current code that likely affect throughput more than
proxies will:

- **`MaxIdleConnsPerHost` is the stdlib default of 2.** The transport is built
  as a bare `&http.Transport{}`
  ([`fetch.go:77`](../internal/fetch/fetch.go#L77)), so at `max_threads: 10`
  against one host we are already tearing down and re-handshaking connections
  constantly. That costs latency *and* multiplies our TLS-handshake footprint
  (more ClientHellos = more fingerprint samples for the defender). With a pool
  the cap becomes 2 per (proxy, host) pair, so it gets worse, not better.
- **`HTTP_PROXY` / `HTTPS_PROXY` env vars are ignored** for the same reason
  (`Transport.Proxy` is nil unless `http.proxy` is set). Undocumented today.

---

## 3. Current state

### 3.1 What exists

One field, `http.proxy`, applied once at client construction:

```go
// internal/fetch/fetch.go:78
if cfg.HTTP.Proxy != "" {
    pu, err := url.Parse(cfg.HTTP.Proxy)
    ...
    transport.Proxy = http.ProxyURL(pu)
}
```

Surfaced in the desktop settings
([`config-schema.js:112`](../desktop/frontend/src/views/config-schema.js#L112))
and the MCP catalog. No validation beyond URL parseability — a malformed proxy
fails at `crawler.New` (covered by
[`internal/runner/executor_status_test.go`](../internal/runner/executor_status_test.go)).

### 3.2 Known defects (fix regardless of this feature)

| ID | Defect | Impact |
|---|---|---|
| **P0-1** | **The renderer ignores `http.proxy` entirely.** `render.New` builds Chrome exec options with no `--proxy-server` ([`render.go:91`](../internal/render/render.go#L91)). | With `rendering.mode: javascript` the raw fetch goes through the proxy and the Chrome render goes **direct** — leaking the origin IP, and producing raw-vs-rendered diffs that are artefacts of two different network paths. |
| **P0-2** | **`http.trusted_cert_dirs` is declared and never read.** Only occurrence is the struct field ([`types.go:391`](../internal/config/types.go#L391)); documented as a known no-op in DESIGN.md §4. | Blocks Bright Data's native proxy outright (§5.3). |
| **P0-3** | **5xx retries reuse the same proxy.** `FetchWith` loops `doOnce` on one client ([`fetch.go:169`](../internal/fetch/fetch.go#L169)). | A 503 emitted *by* a blocked proxy retries through that same proxy, burning the retry budget and writing phantom errors into the audit. |
| **P0-4** | `MaxIdleConnsPerHost` / `IdleConnTimeout` unset (§2.3). | Throughput and fingerprint churn. |

### 3.3 What the architecture gets right

The core is well-shaped for this. Three properties matter:

1. **One choke point.** Every request in the product reaches the network via
   `fetch.Client.doOnce` → `c.hc.Do(req)`. Five `fetch.New` call sites (crawler,
   sitemap, MCP site tools, CLI tools, desktop tools) all take the same
   `*config.Config`, so a pool configured once applies everywhere.

2. **`Transport.Proxy` is already a per-request hook.** Verified in the Go
   1.26 source: `cm.proxyURL, err = t.Proxy(treq.Request)` is evaluated per
   request, and `connectMethodKey` includes the proxy string — so **connection
   pooling is correctly partitioned per proxy with zero work on our side**.
   One transport multiplexes the whole pool with proper keep-alive per
   (proxy, host, scheme).

3. **Concurrency is already bounded and layered.** `newWorkPool` + N persistent
   workers ([`workpool.go:51`](../internal/crawler/workpool.go#L51)), a
   crawl-wide token bucket ([`crawler.go:405`](../internal/crawler/crawler.go#L405)),
   and a process-wide `limiter`. Proxy selection slots into `crawlOne` with no
   restructuring.

---

## 4. Provider landscape

### 4.1 Gateway availability across plans

All three evaluated providers expose a **rotating gateway endpoint on their
entry tier**, so D1 does not lock us to a large commitment.

| Provider | Gateway | Rotation | Entry tier | Notes |
|---|---|---|---|---|
| **Bright Data** | `brd.superproxy.io:44445` (native; `:33335`/`:22225` legacy) | Zone setting: per-request or sticky via `-session-<id>` in the username | Pay-as-you-go, no commitment (~$4–8.40/GB residential, ~$0.60/GB DC) | Gateway is the *only* access method for residential/DC/ISP/mobile. KYC required for residential. **Requires their root CA — see §5.3.** |
| **Oxylabs** | `pr.oxylabs.io:7777` (residential) | Per-request by default; sticky via session in username | ~$6/GB residential, ~$0.65/GB DC. 5 free DC IPs on signup | Residential trial typically needs a sales call. DC offers both gateway and IP list. |
| **Decodo** (ex-Smartproxy) | `gate.decodo.com:7000` | Per-request or sticky | ~$3.75/GB, self-serve, 3-day/100 MB trial, no sales call | Easiest self-serve signup of the three. |

**Answer to "is the gateway available on all plans": yes, for all three.** It is
the standard integration path, not a premium feature. The tiering is on
bandwidth price and on residential-vs-datacenter access, not on whether rotation
works.

### 4.2 Cost model — this matters more than it looks

All three price **per gigabyte of traffic through the proxy**, and bandwidth is
the thing a crawler consumes. The unit being billed is **wire bytes** —
compressed, both directions, including headers. It is not the page's
uncompressed size, and it is not what `pages.size` records (§4.2.4).

#### 4.2.1 Per-page inputs

Source: [HTTP Archive Web Almanac 2025, Page Weight](https://almanac.httparchive.org/en/2025/page-weight).
HTML document **transfer** size (desktop), i.e. post-gzip/brotli wire bytes:

| p10 | p25 | p50 | p75 | p90 |
|---|---|---|---|---|
| 6 KB | 14 KB | **35 KB** | 78 KB | 152 KB |

Median **total** page weight (all subresources) is 2,412 KB desktop /
2,164 KB mobile — roughly 70× the HTML alone.

Per-request overhead on top of the body: request headers ~0.5 KB, response
headers ~1 KB, plus amortised TLS handshake (~4–6 KB per new connection, which
today is far more often than it should be — see P0-4). Round the raw path to
**~40 KB/page at p50, ~85 KB at p75, ~160 KB at p90**.

#### 4.2.2 Raw path (the default: `resources.*.crawl` all `false`, [`defaults.go:16`](../internal/config/defaults.go#L16))

10,000 pages:

| Site profile | Bytes/page | Total | Residential PAYG ($8.40/GB) | Residential committed ($3/GB) | Datacenter ($0.60/GB) |
|---|---|---|---|---|---|
| p50 | 40 KB | 0.40 GB | $3.4 | $1.2 | $0.24 |
| p75 | 85 KB | 0.85 GB | $7.1 | $2.6 | $0.51 |
| p90 | 160 KB | 1.60 GB | $13.4 | $4.8 | $0.96 |

**The raw path is cheap on any tier.** Single-digit dollars per 10k pages even
on the most expensive residential PAYG rate; cents on datacenter. This is not
the number to optimise.

#### 4.2.3 Render path — and the cache finding that changes it

Naively, median total page weight × 10k = **24 GB ⇒ $120–200**. That is the
number to worry about, and it is why REQ-R4 exists.

But it overstates the steady state. chromedp's `ExecAllocator` creates one
temp profile per `render.New` (i.e. per crawl), every render is a new *tab* in
that shared browser process ([`render.go:537`](../internal/render/render.go#L537)),
and bluesnake sets neither `--disable-cache` nor `Network.setCacheDisabled`.
**Chrome's HTTP cache is therefore shared across every page of a crawl**, so a
site's framework bundle, CSS, fonts and logo are fetched once, not 10,000
times. Only page-unique bytes (HTML, page-specific images, XHR) recur.

| Scenario | Bytes/page | 10k total | @ $8/GB |
|---|---|---|---|
| Cold cache every page (worst case) | ~2.4 MB | 24 GB | $190 |
| Warm shared cache (realistic today) | ~500 KB | 5 GB | $40 |
| Warm + REQ-R4 resource blocking | ~150–250 KB | 1.5–2.5 GB | **$12–20** |

**Design consequence — this constrains §7.5.** Per-proxy *browser contexts*
(`Target.createBrowserContext`) are cache-partitioned, as is one Chrome process
per proxy. Either form of renderer rotation therefore **destroys the shared
cache** and pushes the bill back toward the cold-cache row, potentially 4–5×.
With D1's single gateway this is moot — one proxy, one browser, one cache — and
that is an argument for D1 that has nothing to do with implementation effort.
If renderer rotation across N proxies is ever added, REQ-R4 stops being an
optimisation and becomes a precondition.

#### 4.2.4 Two traps when estimating from an existing crawl

- **`pages.size` is decompressed.** `rec.Size = len(res.Body)`
  ([`crawler.go:762`](../internal/crawler/crawler.go#L762)) measures the body
  *after* the transport transparently gunzips it (bluesnake deliberately leaves
  `Accept-Encoding` unset so the transport handles it,
  [`fetch.go:36`](../internal/fetch/fetch.go#L36)). `SUM(size)` over a crawl
  therefore **overstates billable bytes by roughly 3–4× for HTML**. Do not quote
  it as a proxy estimate.
- **`limits.max_page_size_kb` defaults to 51200 — 50 MB**
  ([`defaults.go:73`](../internal/config/defaults.go#L73)). Internal links are
  crawled by default (`links.internal.crawl: true`), and that includes PDFs and
  other large documents, which are fetched but not parsed
  (`extraction.pdf.*` is a documented no-op). One linked 50 MB file costs
  50 MB of proxy traffic. On a proxied crawl, lower this cap.

→ **REQ-R4** (§6.4): block `Image`, `Font`, `Media` and `Stylesheet` resource
types in the render path when a proxy is active. bluesnake needs the *DOM*, not
the pixels — it already ignores `Media` for settle purposes
([`render.go:323`](../internal/render/render.go#L323)). Screenshots
(`rendering.screenshots`) are the one feature that needs images; gate the
blocking off when screenshots are on.

→ **REQ-S11**: record billable bytes per request (wire bytes, not
`len(Body)`) and report a per-crawl proxy-traffic total. Without it, cost is
unknowable before the invoice arrives.

### 4.3 Bright Data's higher tiers (relevant to §2.2)

Worth knowing, since we are buying from them anyway:

- **Web Unlocker** (~$1.50–3.00/1k successful requests): handles fingerprinting,
  CAPTCHA and unblocking server-side. At 10k pages that is **$15–30** — 2–3×
  plain residential on the raw path (§4.2.2), so not the cheapest option, but
  per-*request* billing makes it **predictable** (immune to a site's page
  weight and to the 50 MB-PDF tail risk), and it replaces the whole of Phase 4
  rather than adding to it. For hard targets, compare it against
  residential + uTLS + our own ban handling, not against residential alone.
- **Scraping Browser / Browser API** ($5–8/GB): a remote CDP endpoint with proxy
  and fingerprint handling server-side. chromedp supports this directly via
  `chromedp.NewRemoteAllocator(ctx, wsURL)` (verified present in v0.15.1,
  [`allocate.go:532`](https://pkg.go.dev/github.com/chromedp/chromedp#NewRemoteAllocator)).
  This would **sidestep the entire per-context-proxy problem** in §7.5 — but at
  render-path bandwidth prices (§4.2), so only with REQ-R4 in place.

Both are deferred to Phase 4 (§9.4) but shape the design: the renderer's
allocator must be swappable, not hard-wired to `NewExecAllocator`.

---

## 5. Correction: what the live spike is actually for

An earlier note suggested the Chrome per-context proxy spike was about
authentication. **That was imprecise, and the distinction changes the plan.**

### 5.1 Proxy auth ≠ site auth

Two unrelated things:

- **Site authentication** — logging into the crawled site. Out of scope; all
  crawls are public URLs.
- **Proxy authentication** — credentials for the *gateway*. Bright Data's
  username carries the customer ID and zone
  (`brd-customer-<id>-zone-<zone>:<password>@brd.superproxy.io:44445`), and all
  targeting and session control is done by mutating that username.

**Bright Data's gateway requires proxy auth on every request, on public URLs,
always.** So the spike is squarely on the critical path.

### 5.2 Chrome does not accept proxy credentials

Confirmed: Chrome ignores credentials in `--proxy-server` — the
`user:pass@host:port` form produces "no supported proxies". The options are:

1. **CDP:** `Fetch.enable{handleAuthRequests:true}` + respond to
   `Fetch.authRequired` with `Fetch.continueWithAuth`. cdproto has all of it
   (`fetch.ContinueWithAuthParams`, `AuthChallengeResponse` — verified in the
   pinned version). But there are credible reports of `authRequired` not firing
   reliably, and of `Target.createBrowserContext`'s `proxyServer` silently
   falling back to a direct connection.
2. **Local forwarding proxy:** run an unauthenticated loopback listener that
   injects `Proxy-Authorization` and forwards upstream. Chrome gets a
   credential-free `--proxy-server=http://127.0.0.1:<port>`. ~150 LOC in Go,
   no CDP flakiness, and it also gives per-context rotation for free (one
   listener per proxy). Ugly but boring, and boring is the right trade here.
3. **Remote browser** (§4.3): credentials live in the WebSocket URL.

**The spike must answer, in this order:** (a) does `Fetch.authRequired` fire
reliably against a real Bright Data gateway under chromedp; (b) does per-context
`proxyServer` actually route, or silently fall back. If either fails, take
option 2 — it is the lower-risk default and should be the assumed plan until
the spike says otherwise.

### 5.3 The blocker — Bright Data MITMs TLS, and the clock is running

Bright Data's native proxy terminates TLS and re-signs with its own root CA.
Their own quickstart uses `curl --insecure`. To verify properly you must load
`brightdata_root_ca_44445.crt`.

**bluesnake cannot do this today.** `http.trusted_cert_dirs` is declared and
never read (P0-2). The only alternative is `WithInsecureTLS()`, which is a test
hook and must not become a production path — an SEO auditor that reports on
HSTS and mixed content cannot itself skip certificate verification.

**Timing — act on this now:**

| Port | Certificate | Status |
|---|---|---|
| `22225` (DC), `33335` (residential) | legacy | **expire 2026-09-25 00:00 UTC** — cannot be renewed; traffic on them fails afterwards |
| `44445` | `brightdata_root_ca_44445.crt` | current; use for all new setups |

That is **eight days from this document's date (2026-09-17)**. Any integration
work must target `:44445` from the first line of code. Do not write `:33335`
into a config example, a test fixture, or a doc.

→ **REQ-C1**: implement `http.trusted_cert_dirs` (load PEMs from the configured
directories into a `tls.Config.RootCAs` pool, appended to the system pool) as a
Phase 0 blocker. It is a small, self-contained, independently useful feature
that closes a documented no-op.

---

## 6. Requirements

### 6.1 Phase 0 — defects (REQ-P)

| ID | Requirement |
|---|---|
| REQ-P1 | The Chrome renderer honours the configured proxy. No configuration in which the raw fetch is proxied and the render is not. |
| REQ-P2 | `http.trusted_cert_dirs` loads PEM certificates from each listed directory and appends them to the system root pool for all HTTPS connections. Unreadable dir or unparseable PEM = config error at construction, not a silent skip. |
| REQ-P3 | 5xx retries re-select a proxy rather than reusing the failed one. |
| REQ-P4 | `MaxIdleConnsPerHost` and `IdleConnTimeout` are set explicitly and derived from `speed.max_threads`. |
| REQ-P5 | The documented behaviour of `HTTP_PROXY`/`HTTPS_PROXY` (currently ignored) is stated in DESIGN.md §4, whichever way we settle it. |

### 6.2 Phase 1 — pool and selection (REQ-S)

| ID | Requirement |
|---|---|
| REQ-S1 | A new `internal/proxypool` package owns proxy selection. Pure and table-testable — no network, no config parsing. |
| REQ-S2 | Config accepts a list. `http.proxy` (string) remains the one-proxy shorthand and is equivalent to a one-element list. Both set = config error. |
| REQ-S3 | Selection is per request, via `Transport.Proxy` reading a value the caller placed on the request context. Never a mutable field on the shared transport. |
| REQ-S4 | Strategies: `round_robin` (default), `sticky_host` (stable proxy per target authority), `random`. |
| REQ-S5 | An optional direct (no-proxy) entry may participate in rotation (Crawlee's null-proxy pattern), off by default. |
| REQ-S6 | Per-proxy concurrency cap, so one endpoint does not absorb all workers. Residential providers enforce their own concurrency limits; exceeding them returns errors that look like bans. |
| REQ-S7 | `fetch.Result` and `PageRecord` carry the proxy that served the request. Persisted as a `pages` column via the migration ladder ([`store.go:414`](../internal/store/store.go#L414)). |
| REQ-S8 | Proxy credentials are redactable: never logged, never exported, never rendered in the desktop UI or MCP responses in full. Store and display `host:port` only. |
| REQ-S9 | Proxy config supports `password_env`, matching the existing `http.auth.basic` convention ([`types.go:370`](../internal/config/types.go#L370)). Credentials should not have to live in a YAML profile. |
| REQ-S11 | Billable bytes (wire bytes in both directions, not `len(Body)`) are metered per request and totalled per crawl. Surfaced in the crawl summary. See §4.2.4. |

### 6.3 Phase 2 — health (REQ-H)

| ID | Requirement |
|---|---|
| REQ-H1 | **Preflight.** Before the crawl opens, each proxy makes one request to the seed host's `robots.txt` and is classified alive/dead/slow. Zero alive = the crawl fails to start with a named error. |
| REQ-H2 | **Passive scoring.** Health is derived from real request outcomes — consecutive failures, rolling success rate, p50 latency. No synthetic probe traffic during the crawl. |
| REQ-H3 | **Ban detection** is a named, testable policy, not inline conditionals. Hard signals: 403, 429, 503, connection reset, proxy 407/502. Soft signals: §6.3.1. |
| REQ-H4 | **Quarantine and re-dispatch.** A banned proxy is quarantined; the URL is re-queued to a *different* proxy and is **not** recorded as a page error. Distinguishing "proxy failed" from "page failed" is the whole point — without it a dead proxy writes phantom 5xx rows into the audit. |
| REQ-H5 | **Reanimation** on *randomized* exponential backoff (base ~5 min, cap ~60 min — scrapy-rotating-proxies' proven numbers). Randomised, or all proxies retry in the same second and are re-banned together. |
| REQ-H6 | **Per-URL attempt budget** bounds proxy-attributed retries (default 5, cf. `ROTATING_PROXY_PAGE_RETRY_TIMES`). Exhausted = a genuine page error. |
| REQ-H7 | **Degradation is loud** (D3). See §8.5. |
| REQ-H8 | Pool state is observable: a periodic log line (alive/dead/quarantined counts) and a per-crawl summary persisted with the crawl. |

#### 6.3.1 Soft-ban detection — we have an unusual advantage

Status codes miss the important cases: a `200` carrying a challenge page, or a
`200` with a suspiciously short body. bluesnake already computes a raw-body
content hash per page for duplicate detection
([`crawler.go:1309`](../internal/crawler/crawler.go#L1309)).

**If N distinct URLs suddenly return byte-identical bodies, that is a challenge
page.** We detect it for free by reusing machinery that already exists. Make
this an explicit signal in the ban policy:

- ≥3 distinct URLs sharing one content hash within a short window, **and** that
  hash not already claimed as a legitimate canonical → soft ban.
- A sharp collapse in mean body size against the crawl's running baseline →
  soft ban.

### 6.4 Phase 3 — renderer (REQ-R)

| ID | Requirement |
|---|---|
| REQ-R1 | The renderer routes through the same pool as the fetch client, with the same health state. |
| REQ-R2 | Proxy credentials work without exposing them to page JavaScript. |
| REQ-R3 | The renderer's allocator is swappable (`ExecAllocator` today, `RemoteAllocator` in Phase 4) behind an interface. |
| REQ-R4 | When a proxy is active and `rendering.screenshots` is off, block `Image`, `Font`, `Media` and `Stylesheet` resource types. Bandwidth is what we are billed for (§4.2). |
| REQ-R5 | Memory stays inside the `MEMORY-SCALING.md` budget. If the fallback is one Chrome allocator per proxy (~100–300 MB each), the proxy count must be hard-capped and the interaction with `rendering.max_global_renders` specified. |

---

## 7. Design

### 7.1 Package

```
internal/proxypool/          selection + health. Pure; no net, no config parsing.
  pool.go                    Pool, Proxy, Strategy
  select.go                  round_robin | sticky_host | random
  health.go                  state machine, scoring, backoff
  ban.go                     BanPolicy interface + default policy
```

`internal/fetch` depends on `internal/proxypool`. Never the reverse.

### 7.2 The selection seam

```go
// internal/fetch/fetch.go
type ctxProxyKey struct{}

transport.Proxy = func(req *http.Request) (*url.URL, error) {
    if p, ok := req.Context().Value(ctxProxyKey{}).(*url.URL); ok {
        return p, nil
    }
    return nil, nil // explicit direct
}
```

The caller picks, stamps the context, and therefore *knows* which proxy served
the response — attribution without parsing anything back out. Signatures of
`Fetch` / `FetchWith` are unchanged; `Result` gains one field.

This is the whole of D2: **the difference between one proxy and N is which
value `pool.Select()` returns.** One proxy is a pool of size 1. Supporting N
costs the `Strategy` switch (~40 LOC) and the health state being a map rather
than a scalar. The expensive parts — the seam itself, attribution, renderer
plumbing, ban detection, config, UI, docs — are identical either way.

**So: no, multi-proxy is not meaningfully harder, provided the seam is built
per-request from the start.** What we defer by shipping N=1 first is
`sticky_host`, cross-proxy re-dispatch, and per-proxy health — all of which are
Phase 2 anyway. Shipping a static `transport.Proxy` and retrofitting later would
mean touching every one of those call sites twice; that is the cost we are
avoiding, and it is the only reason D2 exists.

### 7.3 Attribution

`fetch.Result.Proxy` → `PageRecord.Proxy` → `pages.proxy` column → exports and
the per-URL drawer. Host:port only (REQ-S8). This is what makes "why did these
200 URLs 403?" a query instead of a re-crawl.

### 7.4 Cookies and identity — the non-obvious constraint

`fetch.Client` holds **one** `cookiejar` shared by every worker
([`fetch.go:117`](../internal/fetch/fetch.go#L117)), and `http.auth.cookies` are
applied to every matching request regardless of route
([`fetch.go:271`](../internal/fetch/fetch.go#L271)).

Rotate proxies under that and **one session identity emerges from N source
IPs** — a stronger bot signal than the traffic spike we set out to avoid, and on
an authenticated crawl it reads as session hijacking.

Public-URL crawls with `cookie_storage: session` (the default) are unaffected.
But the rule must be in the code, not in the docs, because the config that
triggers it is one line away:

→ **REQ-S10**: when `advanced.cookie_storage == "persistent"` **or**
`http.auth.cookies` is non-empty, rotation is forced to `sticky_host`, or
refused with a named config error if the operator asked for `round_robin`
explicitly. One identity per IP, enforced.

### 7.5 Renderer

Preference order, cheapest-risk first:

1. **Local forwarding proxy** (§5.2 option 2). One loopback listener per proxy,
   injecting `Proxy-Authorization`. Chrome gets a credential-free
   `--proxy-server`. No CDP dependency, no per-context uncertainty, and it
   sidesteps both spike risks at once. **Assume this until the spike says
   otherwise.**
2. **Per-browser-context proxy.** `chromedp.WithNewBrowserContext()` +
   `target.CreateBrowserContextParams.WithProxyServer()` — both verified present
   in the pinned chromedp v0.15.1 / cdproto. Rotates inside one Chrome process,
   which is the memory-efficient answer. Gated on the spike.
3. **One allocator per proxy.** Simple, correct, expensive (REQ-R5). Fallback
   only.

### 7.6 Interaction with existing concurrency

No change to the model (D4). `speed.max_urls_per_sec` remains the **politeness
contract** — what the target sees in aggregate, which is what politeness
actually means. Proxies relieve the *per-IP* ceiling only. Auto-multiplying the
rate because a pool exists would turn an SEO auditor into a load generator by
accident, on someone else's site, without the operator having asked.

Add per-proxy concurrency (REQ-S6) as a distinct axis, alongside
`speed.max_threads` (per crawl) and `speed.max_global_threads` (per process).

### 7.7 Config shape

```yaml
http:
  # one-proxy shorthand (existing; unchanged)
  proxy: http://user:pass@brd.superproxy.io:44445

  # OR the pool form (mutually exclusive with the above)
  proxies:
    - url: http://brd-customer-x-zone-y@brd.superproxy.io:44445
      password_env: BRIGHTDATA_ZONE_PASSWORD
      max_concurrent: 10
  proxy_strategy: round_robin      # round_robin | sticky_host | random
  proxy_include_direct: false
  proxy_preflight: true
  proxy_min_alive: 1               # below this the crawl fails (D3)

  trusted_cert_dirs:
    - /etc/bluesnake/certs          # brightdata_root_ca_44445.crt
```

---

## 8. Health model

### 8.1 States

`unchecked → alive ⇄ quarantined → dead`

- **unchecked** — configured, not yet exercised.
- **alive** — serving.
- **quarantined** — banned or failing; not selected; a reanimation timer runs.
- **dead** — quarantined and reanimation has failed `N` times. Still retried at
  the backoff cap; never silently dropped.

### 8.2 Transitions

| From | To | Trigger |
|---|---|---|
| unchecked | alive / quarantined | preflight (REQ-H1) |
| alive | quarantined | ban policy fires, or consecutive-failure threshold reached |
| quarantined | alive | reanimation probe succeeds |
| quarantined | dead | reanimation fails `N` times |
| dead | alive | reanimation probe succeeds (backoff stays at cap) |

### 8.3 Ban policy

Pluggable (`BanPolicy` interface), one default implementation. Signals:

- **Hard:** 403, 429, 503; connection reset/refused; proxy 407 (auth failure —
  a *configuration* error, so fail the crawl, do not quarantine and carry on);
  proxy 502.
- **Soft:** the content-hash convergence and body-size-collapse signals of
  §6.3.1.
- **Latency:** p50 above a multiple of the pool median → degrade, do not ban.

Each returns ban / not-ban / unknown, mirroring scrapy's three-valued model, so
"unknown" never silently counts as healthy.

### 8.4 Backoff

Randomised exponential, base 300 s, cap 3600 s, jitter ±50%.

### 8.5 Degradation policy (D3)

Three thresholds, all loud:

1. **Preflight, zero alive** → crawl does not start. Named error listing each
   proxy and its failure.
2. **Mid-crawl, alive < `proxy_min_alive`** → the crawl **fails** via the
   existing `noteSinkErr` latch ([`crawler.go:688`](../internal/crawler/crawler.go#L688)).
   This reuses the "errors are loud" doctrine already established for the dedup
   authority ([`loud_failures_test.go`](../internal/crawler/loud_failures_test.go)):
   silent incompleteness is the wrong default for an audit product, and a crawl
   that reports success while a WAF ate 40% of it is exactly that failure.
3. **Any degradation at all** → recorded in the crawl summary and surfaced in
   the desktop, even when the crawl completes: *"ran with 3/10 proxies alive;
   412 URLs re-dispatched after proxy bans."*

### 8.6 What we deliberately do not do (D4)

- Do not auto-scale `max_threads` or `max_urls_per_sec` with pool size.
- Do not retry indefinitely across proxies — REQ-H6 bounds it.
- Do not treat a proxy 407 as a ban. It is a config error; fail loudly.

---

## 9. Phases

### Phase 0 — defects and the cert blocker
**Ships independently. Do this first regardless of the rest.**

REQ-P1…P5, REQ-C1. Definition of done:
- `http.trusted_cert_dirs` loads PEMs into the root pool; acceptance scenario in
  `features/fetch.feature` against an httptest server with a private CA.
- Renderer honours `http.proxy`; regression test asserts no direct connection
  when a proxy is configured.
- Retry re-selects; `MaxIdleConnsPerHost`/`IdleConnTimeout` set from
  `speed.max_threads`.
- Bright Data reachable end-to-end on `:44445` with a real zone (manual, once).

*Estimate: ~1 day.* **Do the `:44445` verification before 2026-09-25** (§5.3).

### Phase 1 — pool and selection
REQ-S1…S11. Definition of done:
- `internal/proxypool` at ≥90% statement coverage (Makefile `COVER_MIN`).
- `features/proxy.feature` covers: single proxy, list rotation, sticky-host
  stability, direct-entry participation, per-page attribution.
- `pages.proxy` migration + export column + desktop drawer field.
- Credentials redacted everywhere (assert in a test, not by inspection).
- Config error when `proxy` and `proxies` are both set, and when rotation is
  requested with persistent cookies (REQ-S10).
- Billable-byte metering (REQ-S11) verified against a known payload, so §4.2's
  estimates can be replaced with measurements from a real crawl.

*Estimate: ~3–4 days including tests.*

### Phase 2 — health
REQ-H1…H8 + §6.3.1. Definition of done:
- Preflight blocks a crawl with an all-dead pool; named error.
- Ban policy table-tested over hard, soft and unknown signals.
- Re-dispatch verified: a banned proxy's URL is retried elsewhere and **not**
  recorded as a page error.
- Reanimation backoff is deterministic under an injected clock.
- Degraded-pool summary persisted and surfaced.

*Estimate: ~3 days.*

### Phase 3 — renderer
REQ-R1…R5. **Gated on the spike (§5.2).** Timebox the spike to half a day: run
chromedp against a real Bright Data zone, answer the two questions, then pick
the path. Default to the local forwarding proxy.

*Estimate: unknown until the spike; 2–4 days after.*

### Phase 4 — the fingerprint question
Not scheduled. Opened here because §2.2 says it is the binding constraint for
competitor audits, and shipping Phases 0–3 will not change that on a
Cloudflare-protected target.

Options, in ascending order of cost and descending order of risk:

1. **Bright Data Web Unlocker** (§4.3). Per-request pricing suits an HTML-only
   crawler. Least code: a different endpoint, not a different client. **Evaluate
   this first** — it may make options 2 and 3 unnecessary.
2. **Remote browser** (`NewRemoteAllocator`) for the render path. Needs REQ-R4
   first or the bandwidth bill is punishing.
3. **uTLS** (`utls`-backed transport) to present a Chrome ClientHello. Real
   caveat, and it is the reason this is ranked last: **uTLS fixes the TLS
   handshake and stops there.** The HTTP/2 layer is a separate package, so a
   client can present a flawless Chrome JA4 and then send a Go SETTINGS frame —
   and a detector that checks both sees a contradiction that is *more*
   distinctive than either signal alone. A half-measure here is worse than none.

Whatever we choose, it must not compromise bluesnake's own audit integrity: we
report on HSTS, mixed content and certificate validity, so we do not ship a
production path that skips verification.

---

## 10. Testing

Per DESIGN.md §6–7, BDD-first and ≥90% statement coverage across
`./internal/...` and `./cmd/...`.

- **Unit (`internal/proxypool`)** — table-driven over strategies, state
  transitions, backoff (injected clock), ban policy signals. Pure package; no
  network.
- **Unit (`internal/fetch`)** — per-request selection against multiple
  `httptest` proxies; attribution correctness; retry re-selection; trusted-cert
  loading against a private CA.
- **Acceptance (`features/proxy.feature`)** — rotation observable across
  requests, preflight failure, mid-crawl quarantine and re-dispatch, degraded
  summary, credential redaction.
- **Integration (`@proxy`, excluded by default)** — like the existing `@chrome`
  tag: real provider credentials from the environment, skipped in CI.

**Test proxies:** an in-process HTTP CONNECT proxy in `internal/proxypool` test
helpers (~80 LOC) with injectable behaviour — always-403, always-timeout,
slow, healthy. That covers every ban path without a provider account.

---

## 11. Open questions

| # | Question | Owner |
|---|---|---|
| Q1 | Does `Fetch.authRequired` fire reliably under chromedp against a real Bright Data zone? Does per-context `proxyServer` route, or silently fall back? | Phase 3 spike |
| Q2 | Should proxy health persist across pause/resume, or should each session preflight cold? Leaning cold — simpler, and health is only minutes-fresh anyway. | Phase 2 |
| Q3 | Should WARC records carry the proxy used? Arguably provenance metadata. | Phase 1, low priority |
| Q4 | Is Web Unlocker (§9.4 option 1) cheaper per audit than residential + uTLS? Needs one real measurement on a Cloudflare-protected target. | Phase 4 |
| Q5 | `HTTP_PROXY`/`HTTPS_PROXY` currently ignored — keep ignoring (explicit config only) or honour as a fallback? Leaning keep ignoring; a crawler silently inheriting an ambient proxy is a surprising audit result. | Phase 0 |

---

## 12. References

- [Crawlee — proxy management](https://crawlee.dev/js/docs/guides/proxy-management)
  (tiered proxies, sticky sessions, the null-proxy pattern)
- [scrapy-rotating-proxies](https://github.com/TeamHG-Memex/scrapy-rotating-proxies)
  (the dead/alive/reanimated model and backoff numbers adopted in §8)
- [Bright Data — SSL certificate](https://docs.brightdata.com/general/account/ssl-certificate)
  (§5.3, ports and expiry)
- [AWS WAF — JA3/JA4 rate-based rules](https://aws.amazon.com/about-aws/whats-new/2025/03/aws-waf-ja4-fingerprinting-aggregation-ja3-ja4-fingerprints-rate-based-rules/)
  (§2.2, why rotation alone is not enough)
- [Screaming Frog proxy configuration](https://www.zenrows.com/blog/screaming-frog-proxy/)
  (parity target: one proxy, no rotation)
- [chromedp issue #645 — proxy authentication](https://github.com/chromedp/chromedp/issues/645)
- [chrome-remote-interface issue #478 — per-context proxyServer](https://github.com/cyrus-and/chrome-remote-interface/issues/478)
- [uTLS and the HTTP/2 mismatch](https://blog.crawlex.net/blog/utls-browser-clienthello/) (§9.4)
