# Proxy support — requirements & implementation spec

Status: **Phase 1 delivered** (§3, §9); Phases 2–4 specified and not yet built.

bluesnake crawls sites it does not control — competitor audits, pre-sales
audits, agency portfolios — so a WAF allowlist is not always available. This
document specifies proxy support: what we build, in what order, what it buys us,
and (importantly) what it does not.

**Scope note:** all crawls are of *public* URLs. Site-level authentication
(`http.auth.*`) is out of scope for the driving use case, but is *not* out of
scope for the design — see §7.4, where it constrains rotation.

---

## 1. Decisions

Settled before the first line of code. Rationale in the sections named.

| # | Decision | §  |
|---|---|---|
| D1 | Ship a **single rotating gateway** (Bright Data) as the supported, documented path. | §4 |
| D2 | Build the **per-request selection seam** for N proxies from day one even though we ship with N=1. The marginal cost is small; retrofitting it is not. | §6.1 |
| D3 | **Degraded crawls fail loud.** A crawl that silently completes with a dead proxy pool is a defective audit. | §8.5 |
| D4 | Proxy rotation **never auto-raises** `speed.max_urls_per_sec` or `speed.max_threads`. The operator raises them deliberately. | §8.6 |
| D5 | Trust MITM proxies via `http.trusted_cert_dirs`, **never** by skipping verification. Bright Data's native proxy re-signs every response, and an auditor that does not verify its own connections cannot audit anyone else's. | §5.3 |
| D6 | The renderer uses the **same egress** as the raw fetch, always. A crawl that proxies its fetch and renders direct leaks the origin IP and makes every raw-vs-rendered diff an artefact of two network paths. | §7.5 |
| D7 | Record the proxy **per page** in the store. Without attribution, diagnosing a partially-blocked crawl is guesswork. | §7.3 |
| D8 | `HTTP_PROXY`/`HTTPS_PROXY` are **ignored**. A crawler that silently inherited an ambient proxy would produce audit results nobody could explain. Egress is explicit configuration only. | §7.7 |

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

**Consequence:** rotation is necessary but not sufficient for hard targets. Phase 4 (§9)
covers the fingerprint question on its own, because for competitor audits
it is the binding constraint, not the IP.

---

## 3. What exists today

Phase 1 is implemented (§9). The shipped behaviour:

- **`http.proxy`** — the single-proxy shorthand, and **`http.proxies`** — a pool
  of `{url, password_env, max_concurrent}` entries. Setting both is a config
  error rather than a silent precedence rule.
- **`http.proxy_strategy`** — `round_robin`, `sticky_host`, `random`, or empty
  for auto (§7.4).
- **`http.proxy_include_direct`** — put an unproxied egress into the rotation.
- **`http.trusted_cert_dirs`** — extra roots for TLS-terminating proxies (§5.3).
- Selection is **per request**; the egress that served each page is recorded on
  `pages.proxy`, exported in the `response_codes` tab, and shown per URL.
- Wire bytes are metered per egress, so a crawl's proxy cost is measured rather
  than estimated (§4.2).
- The renderer routes Chrome through the same egress, via a loopback
  credential-injecting forwarder (§7.5).

`http.proxies` is a list of objects, so — like `http.auth.basic` — it is set in
YAML rather than through `--set`, whose dotted paths cannot address list
elements.

### 3.1 What the architecture made easy

Three properties of the existing core did most of the work:

1. **One choke point.** Every request in the product reaches the network via
   `fetch.Client.doOnce` → `c.hc.Do(req)`. Five `fetch.New` call sites (crawler,
   sitemap, MCP site tools, CLI tools, desktop tools) all take the same
   `*config.Config`, so a pool configured once applies everywhere.

2. **`Transport.Proxy` is already a per-request hook.** In the Go 1.26 source,
   `cm.proxyURL, err = t.Proxy(treq.Request)` is evaluated per request, and
   `connectMethodKey` includes the proxy string — so **connection pooling is
   correctly partitioned per proxy with zero work on our side**. One transport
   multiplexes the whole pool with proper keep-alive per (proxy, host, scheme).

3. **Concurrency is already bounded and layered.** `newWorkPool` + N persistent
   workers ([`workpool.go:51`](../internal/crawler/workpool.go#L51)), a
   crawl-wide token bucket ([`crawler.go:405`](../internal/crawler/crawler.go#L405)),
   and a process-wide `limiter`. Per-egress caps slot in beside them as a third
   axis without touching either.

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
headers ~1 KB, plus amortised TLS handshake (~4–6 KB per new connection —
keep-alive is sized to `speed.max_threads`, so this amortises over a run rather
than recurring per page). Round the raw path to **~40 KB/page at p50, ~85 KB at
p75, ~160 KB at p90**.

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
number to worry about, and it is why REQ-R3 exists.

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
| Warm + REQ-R3 resource blocking | ~150–250 KB | 1.5–2.5 GB | **$12–20** |

**Design consequence — this constrains §7.5.** Per-proxy *browser contexts*
(`Target.createBrowserContext`) are cache-partitioned, as is one Chrome process
per proxy. Either form of renderer rotation therefore **destroys the shared
cache** and pushes the bill back toward the cold-cache row, potentially 4–5×.
With D1's single gateway this is moot — one proxy, one browser, one cache — and
that is an argument for D1 that has nothing to do with implementation effort.
If renderer rotation across N proxies is ever added, REQ-R3 stops being an
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

→ **REQ-R3** (§6.3): block `Image`, `Font`, `Media` and `Stylesheet` resource
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
  render-path bandwidth prices (§4.2), so only with REQ-R3 in place.

Both are deferred to Phase 4 (§9) but shape the design: the renderer's
allocator must be swappable, not hard-wired to `NewExecAllocator`.

---

## 5. Chrome, credentials, and the one real blocker

### 5.1 Proxy auth ≠ site auth

Two unrelated things, and conflating them hides the problem:

- **Site authentication** — logging into the crawled site. Out of scope; all
  crawls are of public URLs.
- **Proxy authentication** — credentials for the *gateway*. Bright Data's
  username IS the configuration
  (`brd-customer-<id>-zone-<zone>:<password>@brd.superproxy.io:44445`); all
  targeting and session control is done by mutating it.

**Bright Data's gateway requires proxy auth on every request, on public URLs,
always.** So credential handling is on the critical path, not an edge case.

### 5.2 Chrome does not accept proxy credentials

Chrome ignores credentials in `--proxy-server` — the `user:pass@host:port` form
produces "no supported proxies". Three ways out were considered:

1. **CDP:** `Fetch.enable{handleAuthRequests:true}` answering
   `Fetch.authRequired`. cdproto carries all of it, but enabling the Fetch
   domain with no patterns pauses *every* request in the tab awaiting an
   explicit continue — a large behavioural change to the render path for a
   small problem — and `authRequired` is reported not to fire reliably.
2. **Local forwarding proxy:** an unauthenticated loopback listener that injects
   `Proxy-Authorization` and forwards upstream, so Chrome gets a
   credential-free `--proxy-server=http://127.0.0.1:<port>`. No CDP dependency,
   nothing intercepted, and Chrome never sees a credential.
3. **Remote browser** (§4.3): credentials live in the WebSocket URL.

**Option 2 shipped** (`internal/proxypool/forward.go`). It relays both shapes a
browser uses — absolute-form requests for `http://` targets and CONNECT tunnels
for `https://` — and surfaces an upstream 407 rather than flattening it to a
502, because wrong proxy credentials are a config error the operator needs to
see, not a site that blocked us.

### 5.3 The blocker — Bright Data MITMs TLS, and the clock is running

Bright Data's native proxy terminates TLS and re-signs with its own root CA.
Their own quickstart uses `curl --insecure`. To verify properly you must load
`brightdata_root_ca_44445.crt`.

`http.trusted_cert_dirs` exists for exactly this (REQ-S12): PEM roots from the
configured directories are appended to the system pool. The alternative —
`WithInsecureTLS()` — is a test hook and must never become a production path:
an auditor that reports on HSTS and mixed content cannot itself skip
certificate verification (D5).

**Timing — act on this now:**

| Port | Certificate | Status |
|---|---|---|
| `22225` (DC), `33335` (residential) | legacy | **expire 2026-09-25 00:00 UTC** — cannot be renewed; traffic on them fails afterwards |
| `44445` | `brightdata_root_ca_44445.crt` | current; use for all new setups |

Always target `:44445`. Do not write `:33335` into a config example, a test
fixture, or a doc.

---

## 6. Requirements

### 6.1 Egress and selection — REQ-S *(delivered)*

| ID | Requirement |
|---|---|
| REQ-S1 | `internal/proxypool` owns selection. Pure and table-testable — no network, no config parsing. |
| REQ-S2 | Config accepts a list. `http.proxy` remains the one-proxy shorthand and is equivalent to a one-element list. Both set = config error. |
| REQ-S3 | Selection is per request, via `Transport.Proxy` reading a value the caller placed on the request context. Never a mutable field on the shared transport. |
| REQ-S4 | Strategies: `round_robin` (default), `sticky_host`, `random`. |
| REQ-S5 | An optional direct (no-proxy) entry may participate in rotation, off by default. |
| REQ-S6 | Per-egress concurrency cap. Providers enforce their own limits and answer a breach with errors indistinguishable from a ban, so exceeding one corrupts results, not just manners. |
| REQ-S7 | `fetch.Result` and `PageRecord` carry the egress that served the request, persisted as `pages.proxy`. |
| REQ-S8 | Credentials are never logged, exported, or displayed. `scheme://host:port` is the only printable form. |
| REQ-S9 | `password_env`, matching the existing `http.auth.basic` convention. An unset variable fails loudly at construction — a proxy dialled without its password answers 407 on every request, which reads as a site-wide block. |
| REQ-S10 | Rotation under a shared identity (persistent cookies, auth cookies) resolves to `sticky_host`; asking for spread explicitly is refused. §7.4. |
| REQ-S11 | Wire bytes metered per egress and totalled per crawl. Attribution is per connection, which is the granularity an invoice has. |
| REQ-S12 | `http.trusted_cert_dirs` loads PEM roots for TLS-terminating proxies. Unreadable directory, unparseable PEM, or a directory with nothing to load is a config error at construction — never a silent skip that resurfaces as an x509 failure on every URL. |
| REQ-S13 | The renderer uses the same egress as the raw fetch, with credentials handled without exposing them to page JavaScript. §7.5. |

### 6.2 Health — REQ-H *(Phase 2)*

| ID | Requirement |
|---|---|
| REQ-H1 | **Preflight.** Before the crawl opens, each proxy makes one request to the seed host's `robots.txt` and is classified alive/dead/slow. Zero alive = the crawl fails to start with a named error. |
| REQ-H2 | **Passive scoring.** Health is derived from real request outcomes — consecutive failures, rolling success rate, p50 latency. No synthetic probe traffic during the crawl. |
| REQ-H3 | **Ban detection** is a named, testable policy, not inline conditionals. Hard signals: 403, 429, 503, connection reset, proxy 407/502. Soft signals: §6.2.1. |
| REQ-H4 | **Quarantine and re-dispatch.** A banned proxy is quarantined; the URL is re-queued to a *different* proxy and is **not** recorded as a page error. Without that split, a dead proxy writes phantom 5xx rows into the audit. |
| REQ-H5 | **Reanimation** on *randomized* exponential backoff (base ~5 min, cap ~60 min). Randomised, or all proxies retry in the same second and are re-banned together. |
| REQ-H6 | **Per-URL attempt budget** bounds proxy-attributed retries (default 5). Exhausted = a genuine page error. |
| REQ-H7 | **Degradation is loud** (D3). §8.5. |
| REQ-H8 | Pool state is observable: a periodic log line and a per-crawl summary persisted with the crawl. |

#### 6.2.1 Soft-ban detection — we have an unusual advantage

Status codes miss the important cases: a `200` carrying a challenge page, or a
`200` with a suspiciously short body. bluesnake already computes a raw-body
content hash per page for duplicate detection
([`crawler.go:1309`](../internal/crawler/crawler.go#L1309)).

**If N distinct URLs suddenly return byte-identical bodies, that is a challenge
page.** We detect it for free by reusing machinery that already exists:

- ≥3 distinct URLs sharing one content hash within a short window, **and** that
  hash not already claimed as a legitimate canonical → soft ban.
- A sharp collapse in mean body size against the crawl's running baseline →
  soft ban.

### 6.3 Renderer rotation — REQ-R *(Phase 3)*

| ID | Requirement |
|---|---|
| REQ-R1 | The renderer rotates across the pool with the same health state as the fetch client, rather than pinning to one egress. |
| REQ-R2 | The renderer's allocator is swappable (`ExecAllocator` today, `RemoteAllocator` for a hosted browser) behind an interface. |
| REQ-R3 | When a proxy is active and `rendering.screenshots` is off, block `Image`, `Font`, `Media` and `Stylesheet` resource types. Bandwidth is what we are billed for (§4.2). |
| REQ-R4 | Memory stays inside the `MEMORY-SCALING.md` budget, and the interaction with `rendering.max_global_renders` is specified. |

---

## 7. Design

### 7.1 Package

```
internal/proxypool/
  pool.go        Pool, Proxy, Strategy, selection      (delivered)
  forward.go     loopback credential shim for Chrome   (delivered)
  health.go      state machine, scoring, backoff       (Phase 2)
  ban.go         BanPolicy interface + default policy  (Phase 2)
```

`pool.go` is pure — no network, no config parsing — which is what makes the
strategies and (later) the health state machine table-testable. `forward.go` is
the one part that opens sockets, and it is separated for that reason.

`internal/fetch` and `internal/render` depend on `internal/proxypool`; never the
reverse. `internal/render` resolves its egress through `fetch.BuildPool` rather
than re-reading the config, so the renderer can never drift from the client and
silently send Chrome somewhere else.

### 7.2 The selection seam

```go
// internal/fetch/fetch.go
type ctxProxyKey struct{}

transport.Proxy = func(req *http.Request) (*url.URL, error) {
    if p, ok := req.Context().Value(ctxProxyKey{}).(*proxypool.Proxy); ok {
        return p.URL, nil
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

**So: multi-proxy is not meaningfully harder, provided the seam is built
per-request from the start** — which is why all three strategies shipped in
Phase 1 even though the supported deployment is one gateway. What genuinely
waits for Phase 2 is per-proxy *health*: quarantine, re-dispatch and
reanimation. Shipping a static `transport.Proxy` and retrofitting later would
have meant touching every call site twice; that is the cost D2 avoids.

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

**Delivered:** Chrome is launched with `--proxy-server` pointed at the pool's
first proxy — through the loopback forwarder (§5.2) when that proxy carries
credentials, directly when it does not. A SOCKS proxy with credentials is
refused with a named error at construction rather than silently ignored, because
there is no header to inject them into and a browser that quietly went direct
would reopen the leak this exists to close.

Chrome takes one egress per browser process, so **renders pin to one proxy**
while raw fetches rotate. That asymmetry is deliberate for now, not an oversight:

1. **Per-browser-context proxy** (`chromedp.WithNewBrowserContext()` +
   `target.CreateBrowserContextParams.WithProxyServer()`, both present in the
   pinned chromedp/cdproto) would rotate inside one process — but browser
   contexts are **cache-partitioned**, and §4.2.3 shows the shared HTTP cache is
   worth more than the rotation. It is also reported to fall back silently to a
   direct connection, which is the worst possible failure mode here.
2. **One allocator per proxy** is simple and correct but costs ~100–300 MB each
   (REQ-R4).

Phase 3 picks between them with the bandwidth maths in hand, not before.

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
  # one-proxy shorthand
  proxy: http://user:pass@brd.superproxy.io:44445

  # OR the pool form (mutually exclusive with the above)
  proxies:
    - url: http://brd-customer-x-zone-y@brd.superproxy.io:44445
      password_env: BRIGHTDATA_ZONE_PASSWORD   # preferred over an inline password
      max_concurrent: 10                        # 0 = unbounded
  proxy_strategy: ""                # "" = auto | round_robin | sticky_host | random
  proxy_include_direct: false

  trusted_cert_dirs:
    - /etc/bluesnake/certs          # brightdata_root_ca_44445.crt

  # Phase 2:
  # proxy_preflight: true
  # proxy_min_alive: 1              # below this the crawl fails (D3)
```

`proxies` is a list of objects, so — like `http.auth.basic` — it is set in YAML,
not through `--set`, whose dotted paths cannot address list elements. The
single-proxy shorthand works either way.

Profiles are plain-text YAML that get shared and committed, so prefer
`password_env` over an inline password. An unset variable fails at construction
rather than dialling without it: a proxy reached without its password answers
407 on every request, which reads as a site-wide block.

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
  §6.2.1.
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

---

## 9. Phases

### Phase 1 — egress, selection, attribution *(delivered)*

REQ-S1…S13. Shipped:
- `internal/proxypool`: pool, strategies, per-egress concurrency, credential
  redaction, and the loopback forwarder that lets Chrome use a credentialed
  proxy.
- Per-request selection through the request context; retries re-select.
- `pages.proxy` (crawl-DB migration v6), the `response_codes` export column,
  the MCP catalog and the desktop settings.
- Wire-byte metering per egress.
- `http.trusted_cert_dirs`, closing a documented no-op and unblocking Bright
  Data's native proxy.
- `features/proxy.feature` plus config-validation scenarios; unit tables in
  `internal/proxypool` and `internal/fetch`.

**Still to do before a real Bright Data run:** verify the account end to end on
port **44445** (§5.3).

### Phase 2 — health

REQ-H1…H8 + §6.2.1. Definition of done:
- Preflight blocks a crawl with an all-dead pool; named error.
- Ban policy table-tested over hard, soft and unknown signals.
- Re-dispatch verified: a banned proxy's URL is retried elsewhere and **not**
  recorded as a page error.
- Reanimation backoff deterministic under an injected clock.
- Degraded-pool summary persisted and surfaced.

*Estimate: ~3 days.*

### Phase 3 — renderer rotation

REQ-R1…R4. Chrome takes one egress per browser process, so today renders pin to
the pool's first proxy. Rotating them needs per-browser-context proxies, which
are **cache-partitioned** — §4.2.3 — so REQ-R3 is a precondition, not an
optimisation, and the bandwidth maths has to come out before the work is worth
doing. Spike `Target.createBrowserContext`'s `proxyServer` against a real zone
first; it is reported to fall back silently to a direct connection.

*Estimate: 2–4 days after the spike.*

### Phase 4 — the fingerprint question

Not scheduled. Opened here because §2.2 says it is the binding constraint for
competitor audits, and Phases 1–3 will not change that on a Cloudflare-protected
target.

Options, ascending cost, descending risk:

1. **Bright Data Web Unlocker** (§4.3). Per-request pricing suits an HTML-only
   crawler. Least code: a different endpoint, not a different client.
   **Evaluate this first** — it may make 2 and 3 unnecessary.
2. **Remote browser** (`NewRemoteAllocator`) for the render path. Needs REQ-R3
   first or the bandwidth bill is punishing.
3. **uTLS** to present a Chrome ClientHello. Ranked last for a real reason:
   **uTLS fixes the TLS handshake and stops there.** The HTTP/2 layer is a
   separate package, so a client can present a flawless Chrome JA4 and then
   send a Go SETTINGS frame — and a detector that checks both sees a
   contradiction *more* distinctive than either signal alone. A half-measure
   here is worse than none.

Whatever we choose must not compromise bluesnake's own audit integrity: we
report on HSTS, mixed content and certificate validity, so no production path
skips verification (D5).

---

## 10. Testing

Per DESIGN.md §6–7, BDD-first and ≥90% statement coverage across
`./internal/...` and `./cmd/...`.

**Delivered:**

- **Unit (`internal/proxypool`)** — table-driven over strategies, selection,
  exclusion, per-egress concurrency, credential redaction, and the forwarder
  against a fake gateway that demands Basic credentials (plain HTTP, CONNECT
  tunnel, 407, open upstream, dead upstream).
- **Unit (`internal/fetch`)** — per-request selection against multiple
  `httptest` proxies; attribution matching the egress that actually served the
  response; retry re-selection; wire-byte metering per egress; trusted-cert
  loading against a private CA, including a negative case proving the pass is
  the configured root and not verification being off.
- **Acceptance (`features/proxy.feature`)** — direct recorded as direct, single
  proxy, rotation across a pool, sticky-host pinning, retry through a different
  egress, per-egress metering, credential redaction. Plus config-validation
  scenarios in `features/config.feature`.

**Phase 2 adds:** ban-policy tables over hard/soft/unknown signals, reanimation
backoff under an injected clock, and re-dispatch asserting a banned proxy's URL
is retried elsewhere and **not** recorded as a page error.

**Test doubles:** the recording proxies live in `internal/proxypool` and `test/`
helpers. They take injectable behaviour (always-403, always-timeout, slow,
healthy), which covers every Phase 2 ban path without a provider account.

**Not covered by any of this:** whether a real gateway behaves as documented.
Only a live run against a real Bright Data zone on `:44445` answers that.

---

## 11. Open questions

| # | Question | Owner |
|---|---|---|
| Q1 | Does per-context `proxyServer` actually route under chromedp, or fall back silently to direct? | Phase 3 spike |
| Q2 | Should proxy health persist across pause/resume, or should each session preflight cold? Leaning cold — simpler, and health is only minutes-fresh anyway. | Phase 2 |
| Q3 | Should WARC records carry the egress used? Arguably provenance metadata. | low priority |
| Q4 | Is Web Unlocker (§9, Phase 4) cheaper per audit than residential + uTLS? Needs one real measurement on a Cloudflare-protected target. | Phase 4 |
| Q5 | Should a per-egress byte budget be able to *stop* a crawl, not just report it? Relevant once bills are attributable. | Phase 2 |

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
- [uTLS and the HTTP/2 mismatch](https://blog.crawlex.net/blog/utls-browser-clienthello/) (Phase 4)
