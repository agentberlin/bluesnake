package config

// Proxy pool resolution. Kept beside the schema rather than in internal/fetch
// so every surface that reports configuration — `config show`, the MCP catalog,
// the desktop settings — agrees with what the crawl will actually do, instead of
// each re-deriving the shorthand rules.

// ProxyPool returns the configured egresses in order: the http.proxies list, or
// the single http.proxy shorthand, plus a direct egress when
// http.proxy_include_direct is on. An empty result means no proxy is configured
// at all, which is the pre-proxy behaviour.
func (h HTTPConfig) ProxyPool() []ProxyEntry {
	var out []ProxyEntry
	switch {
	case len(h.Proxies) > 0:
		out = append(out, h.Proxies...)
	case h.Proxy != "":
		out = append(out, ProxyEntry{URL: h.Proxy})
	}
	if len(out) > 0 && h.ProxyIncludeDirect {
		out = append(out, ProxyEntry{}) // empty URL = direct
	}
	return out
}

// SharedIdentity reports whether this crawl reuses one identity across every
// request: a persistent cookie jar, or auth cookies replayed on each request.
// Rotating source IPs under a shared identity is worse than not rotating — one
// session seen from many IPs is a strong bot signal, and on an authenticated
// crawl it reads as session hijacking — so it forces sticky_host.
func (c *Config) SharedIdentity() bool {
	return c.Advanced.CookieStorage == "persistent" || len(c.HTTP.Auth.Cookies) > 0
}

// ResolvedProxyStrategy is the strategy the crawl will actually use: the
// configured one, or — when left empty (auto) — round_robin, downgraded to
// sticky_host when the crawl carries a shared identity.
func (c *Config) ResolvedProxyStrategy() string {
	if c.HTTP.ProxyStrategy != "" {
		return c.HTTP.ProxyStrategy
	}
	if c.SharedIdentity() {
		return "sticky_host"
	}
	return "round_robin"
}
