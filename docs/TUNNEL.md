# Reverse tunnel — public HTTPS URLs for the local MCP server

bluesnake's MCP server normally binds to localhost only. The reverse tunnel
gives it an **opt-in** public HTTPS URL so a remote MCP client (e.g. Claude
running elsewhere) can reach a user's local crawler without any port-forwarding,
router config, or inbound firewall holes.

This is **off by default**. Users can run the MCP server with no public URL at
all; the tunnel is a separate toggle (desktop) / flag (`bluesnake mcp --public`).

```
                         ┌──────────────── VPS (tunnelserver) ────────────────┐
 MCP client              │   :443  TLS + ALPN router                          │
 (remote)                │     ├─ ALPN bluesnake-tunnel/1 → gateway (connect) │
   │ https               │     └─ http/1.1 ──┬─ api.snake.blue   → control    │
   ▼                     │                   └─ *.t.snake.blue   → data plane  │
 <id>.t.snake.blue ──────┼──► gateway ──(yamux stream)──┐                     │
   /mcp                  │                              │  Postgres (hashes)  │
                         └──────────────────────────────┼─────────────────────┘
                                                         │ outbound TLS (ALPN)
                                                         │ held open by client
                                                ┌────────▼─────────┐
                                                │  bluesnake app    │
                                                │  tunnel client    │
                                                │     │ http        │
                                                │     ▼             │
                                                │  127.0.0.1:8473   │  ← local MCP
                                                └───────────────────┘
```

The app dials **out** (beating NAT/firewalls) and holds one persistent TLS
connection. `yamux` multiplexes it: the server opens one stream per inbound
public request, the client serves each by reverse-proxying to the local MCP
server. This is the same trick every tunnel tool uses; the value here is that
the control plane (identity ↔ credential binding) and the data-plane behaviour
(Host rewrite, SSE flush) are ours to shape for MCP.

## Code layout

| Path | Role | Open source? |
|------|------|--------------|
| `internal/tunnel/wire/` | Wire protocol (ALPN name, auth frames). Shared. | yes |
| `internal/tunnel/` | Embedded **client** (identity, register, reconnect, proxy). | yes |
| `tunnelserver/` | The **server** — self-contained, own `internal/`. | extractable |

`tunnelserver/` only imports the open-source app for the `wire` package. To move
the server into a private repo, copy `tunnelserver/` plus `internal/tunnel/wire/`
and update the import path of `wire`. No other coupling exists.

## Wire protocol (`internal/tunnel/wire`)

1. Client dials `connect.t.snake.blue:443` over TLS, negotiating ALPN
   `bluesnake-tunnel/1`.
2. Client sends one newline-delimited JSON `AuthRequest`
   `{v, tunnel_id, connect_secret}`.
3. Server replies with one `AuthResponse` `{ok, host}` (or `{ok:false, error}`).
4. On success the same connection becomes a yamux session (client = yamux
   server, gateway = yamux client). The gateway opens a stream per public HTTP
   request; the client serves it.

Frames are read byte-by-byte up to the newline so the reader never consumes into
the yamux bytes that follow.

## The credential (phase 1: one secret)

Each install holds **one secret**, generated server-side at registration and
returned exactly once:

- **connect secret** — authenticates the tunnel connection. Never leaves the
  machine; travels only inside the TLS tunnel handshake. Only its sha256 *hash*
  is stored; the plaintext is shown once and never persisted server-side.

The public URL itself carries no token — it is just `https://<id>.t.snake.blue/mcp`.
What keeps it private is the **unguessable random subdomain** (a 12-char nanoid),
exactly like an unguessable share link. This is a deliberate phase-1 simplification
(a per-request URL token was considered and dropped); see phase 2 for the path to
real per-request auth.

> Why sha256 and not bcrypt/argon? The connect secret is a 32-byte random token,
> not a user-chosen password. There is nothing to brute-force; a fast hash with a
> constant-time compare is the correct tool.

## Security model (phase 1)

What's defended:

- **Subdomain hijack** — claiming a subdomain requires the connect secret, which
  is never in the URL. Verified constant-time; unknown-id and wrong-secret paths
  cost the same (no timing oracle on id existence).
- **URL guessing** — subdomains are 12-char random ids (36¹² ≈ 4.7·10¹⁸), which
  is what gates access to a live tunnel in phase 1. Brute-forcing the space is
  infeasible; there is no enumeration endpoint.
- **DNS rebinding** — the gateway rewrites `Host` to the local server's address
  and strips `Origin` before forwarding, satisfying the local MCP server's
  localhost-only Host/Origin guard.
- **Header spoofing** — client-supplied `X-Forwarded-*` / `Forwarded` are
  stripped at the edge; the gateway sets its own.
- **Registration flood** — per-IP (checked first) and global token-bucket rate
  limits on `/v1/register`; Cloudflare in front of the api host.
  `CF-Connecting-IP` is trusted for the client IP **only** when the request
  arrives from a configured trusted-proxy range (Cloudflare's published ranges by
  default), so a direct-to-origin caller cannot spoof it to mint fresh buckets.
- **Connect-path abuse** — the tunnel-connect (ALPN) path has its own per-IP rate
  limit and a global concurrent-handshake cap, applied before the DB lookup, so
  unauthenticated connects can't exhaust goroutines/DB.
- **Resource exhaustion** — bounded auth-frame size, handshake deadline, 8 MB
  public request-body cap, slowloris `ReadHeaderTimeout`, server `IdleTimeout`, a
  global accepted-connection cap, a per-session concurrent-stream cap, and yamux
  keepalives.
- **Read-only SQL escape** — the MCP `query` tool (now remotely reachable) opens
  the DB `mode=ro` + `PRAGMA query_only/trusted_schema`, and rejects anything
  that isn't a single `SELECT`/`WITH`/`EXPLAIN`/`VALUES`/`PRAGMA` statement, which
  blocks `ATTACH`-based local file reads.
- **MITM of the connect secret** — TLS verification is only ever skipped for
  loopback dev servers; the dev override is inert against a real remote endpoint.
- **DB compromise** — only the connect-secret hash is stored; a dump yields no
  usable credential.

What is **NOT** defended in phase 1, by design:

- **A leaked URL.** With no per-request token, anyone who learns the full URL can
  drive the crawler and run read-only SQL over the crawl data until the user
  takes the tunnel offline (or loses `tunnel.json` and re-registers a new
  subdomain). The randomized subdomain is the only barrier. This is the main
  trade-off of dropping the URL token; closing it is a phase-2 item.

Other out-of-scope-for-phase-1 items (documented, accepted): user accounts/OAuth,
subdomain recovery after losing `tunnel.json`, an idle-id reaper, proof-of-work,
multi-VPS scale-out, and OS-keychain storage for the secret.

**Revocation caveat:** the `revoked` flag is enforced at connect time only. There
is no revoke API and no push-to-live-session kill yet. Setting `revoked=true` in
the DB stops *future* connects (and takes effect for a live tunnel on its next
reconnect), but does not tear down an already-connected session. A determined
connected adversary keeps serving until they disconnect. Wiring a revoke endpoint
that closes the live `registry.Session` is a phase-2 fix.

## Client identity persistence

Stored at `<store-dir>/tunnel.json` (default `~/.bluesnake/tunnel.json`), mode
`0600`:

```json
{
  "tunnel_id": "k3x9qzpw04ab",
  "connect_secret": "…",
  "public_host": "k3x9qzpw04ab.t.snake.blue",
  "connect_addr": "connect.t.snake.blue:443",
  "api_base": "https://api.snake.blue"
}
```

The same file is used by the CLI and the desktop app, so both show the same
stable URL. Deleting it means registering a fresh identity (new URL).

## Control-plane API

- `POST /v1/register` → `{tunnel_id, connect_secret, public_host, connect_addr}`
- `GET /v1/health` → `200 ok`

## Using it

**CLI:**

```sh
bluesnake mcp --public
# prints: public MCP URL: https://<id>.t.snake.blue/mcp
```

**Desktop:** Settings → MCP Server → enable the server, then toggle **Public
URL**. Copy the URL. The toggle is disabled until the MCP server is running, and
follows the MCP server up/down and across port changes.

## Database

The server uses **GORM with AutoMigrate against a direct Postgres connection**
(a standard DSN — not the Supabase Data API / PostgREST). On startup it creates
and maintains a single table in the default (`public`) schema:

```sql
public.tunnels(
  id                  text primary key,   -- 12-char nanoid (== subdomain)
  connect_secret_hash bytea not null,     -- sha256 of the connect secret
  created_at          timestamptz,
  last_connected_at   timestamptz,
  connect_count       bigint not null default 0,
  revoked             boolean not null default false
)
```

The id format is guaranteed by the server (`store.NewID`), so there is no
DB-level CHECK constraint. Only the connect-secret hash is stored (HMAC'd with
`SECRET_PEPPER` when set) — never the plaintext. Point `DATABASE_URL` at any
Postgres (Supabase's **direct** connection string, port 5432); with no DSN the
server falls back to an in-memory store for local dev.

> **Use the direct connection (5432), not the transaction pooler (6543).** The
> GORM/pgx driver caches prepared statements, which the Supabase pooler's
> transaction mode rejects. If you must use the pooler, append
> `?default_query_exec_mode=simple_protocol` to `DATABASE_URL` (or switch it to
> session mode).

## Deployment runbook

The server is configured entirely through environment variables.

The first block is the **infra/ops contract** (names agreed with the deploy
team); the second block are our extra operational knobs.

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| `DATABASE_URL` | prod | — | Postgres DSN (Supabase **direct** connection, 5432); empty → in-memory (dev) |
| `CF_API_TOKEN` | prod | — | Cloudflare DNS-01 token, `Zone.DNS:Edit` on the `snake.blue` zone |
| `ACME_EMAIL` | prod | — | ACME account email |
| `SECRET_PEPPER` | prod | — | mixed into stored credential hashes (HMAC key); changing it invalidates all stored secrets |
| `BASE_DOMAIN` | no | `t.snake.blue` | tunnels at `<id>.<base>` |
| `API_DOMAIN` | no | `api.snake.blue` | control-plane host |
| `CERT_DIR` | no | certmagic default | cert storage path (persist this, e.g. `/certs`) |
| `BLUESNAKE_TUNNEL_TRUST_PROXY` | **prod** | `false` | **set `true`** behind Cloudflare so `/register` rate-limiting keys on the real client IP, not the CF edge IP |
| `BLUESNAKE_TUNNEL_TRUSTED_PROXIES` | no | Cloudflare ranges | comma-separated CIDRs allowed to set `CF-Connecting-IP` |
| `BLUESNAKE_TUNNEL_CONNECT_ADDR` | no | `connect.<base>:443` | returned to clients |
| `BLUESNAKE_TUNNEL_LISTEN` | no | `:443` | listen address |
| `BLUESNAKE_TUNNEL_ACME_CA` | no | production | `staging` for Let's Encrypt staging |
| `BLUESNAKE_TUNNEL_DEV_TLS` | no | `false` | self-signed cert (dev only) |

> **Behind Cloudflare, `BLUESNAKE_TUNNEL_TRUST_PROXY=true` is effectively
> required.** With `api.snake.blue` proxied, every `/register` arrives from a
> Cloudflare edge IP; without this flag the per-IP limiter sees one IP for all
> users and throttles everyone. It is opt-in (not defaulted) because trusting a
> client header is unsafe when the origin is reachable directly — which is why
> the trusted-proxy CIDR check also guards it.

### DNS (Cloudflare)

| Record | Type | Value | Proxy |
|--------|------|-------|-------|
| `api.snake.blue` | A | VPS IP | **orange** (proxied) — Cloudflare absorbs floods |
| `*.t.snake.blue` | A | VPS IP | **grey** (DNS only) — direct TLS + ALPN to origin |
| `connect.t.snake.blue` | A | VPS IP | **grey** (DNS only) — covered by the wildcard cert |

The wildcard subdomains and the connect host **must be grey-clouded**: Cloudflare
proxy would terminate TLS and break the tunnel ALPN. Set
`BLUESNAKE_TUNNEL_TRUST_PROXY=true` only because the api host is proxied (so the
real client IP arrives in `CF-Connecting-IP`).

TLS is obtained in-process via ACME **DNS-01** (one wildcard `*.t.snake.blue`
plus `api.snake.blue`), so no inbound port 80 is needed and there is no
per-tunnel certificate work.

### systemd (sketch)

```ini
[Service]
ExecStart=/opt/bluesnake/bin/bluesnake-tunnelserver
Environment=DATABASE_URL=postgres://...@db.<ref>.supabase.co:5432/postgres
Environment=CF_API_TOKEN=...
Environment=ACME_EMAIL=ops@snake.blue
Environment=SECRET_PEPPER=...
Environment=CERT_DIR=/certs
Environment=BLUESNAKE_TUNNEL_TRUST_PROXY=true
AmbientCapabilities=CAP_NET_BIND_SERVICE   # bind :443 as non-root
Restart=always
```

Build: `make tunnel-server` → `bin/bluesnake-tunnelserver`.

### Local end-to-end without a VPS

```sh
# terminal 1: dev server (self-signed TLS, in-memory store). API_DOMAIN is set
# to 127.0.0.1 so the CLI's register call routes to the control plane.
BLUESNAKE_TUNNEL_DEV_TLS=1 BLUESNAKE_TUNNEL_LISTEN=127.0.0.1:8443 \
  API_DOMAIN=127.0.0.1 \
  BASE_DOMAIN=t.snake.blue \
  BLUESNAKE_TUNNEL_CONNECT_ADDR=127.0.0.1:8443 \
  go run ./tunnelserver

# terminal 2: CLI against it (registers, connects, prints the public URL)
BLUESNAKE_TUNNEL_API=https://127.0.0.1:8443 \
  bluesnake mcp --public --tunnel-insecure --tunnel-server-name localhost

# terminal 3: hit the public URL — the data plane routes by Host, so override it
curl -k --resolve <id>.t.snake.blue:8443:127.0.0.1 \
  https://<id>.t.snake.blue:8443/mcp -d '{}'
```

(The in-process equivalent — register, connect, proxy, offline — is covered end
to end by `tunnelserver/internal/server/e2e_test.go`.)

## Phase 2 (what's left)

Carried over from phase 1, roughly in priority order:

1. **Real per-request auth on the public URL.** The biggest gap: a leaked URL is
   currently a full grant. Options: a capability token (re-introduced, but
   server-issued and rotatable), MCP-level auth (bearer/OAuth), or signed/expiring
   URLs. This is what makes the tunnel safe to share casually.
2. **Revocation that kills live sessions.** A `POST /v1/revoke` (authenticated by
   the connect secret) that sets `revoked=true` **and** closes the live
   `registry.Session` immediately, plus periodic re-validation of connected
   sessions against the store.
3. **Identity / subdomain recovery.** Today losing `tunnel.json` means a new
   subdomain. Needs an account or a recovery secret to re-claim an id.
4. **Idle-id reaper.** Reclaim subdomains (and DB rows) for installs that never
   reconnect, so the namespace and table don't grow without bound.
5. **Abuse hardening.** Proof-of-work or a light challenge on `/v1/register` if
   the rate limits prove insufficient; per-tunnel request metering.
6. **Operational maturity.** Multi-VPS scale-out (shared session routing),
   metrics/observability, structured audit logging, OS-keychain storage for the
   connect secret instead of a `0600` file.
7. **Live-session smoke test against real Postgres.** The GORM store is covered by
   the model + an in-memory e2e; a CI job (or one-off) that runs AutoMigrate and
   the CRUD path against a real Postgres would close the last untested seam.
