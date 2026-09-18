package fetch

import (
	"fmt"
	"os"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/proxypool"
)

// BuildPool turns the http.proxy / http.proxies configuration into the egress
// pool. It is exported because internal/render needs the SAME egresses as the
// raw fetches — a renderer that resolved the config independently could drift
// from the client and silently send Chrome somewhere else.
//
// Environment lookups for password_env happen here rather than in
// internal/config, matching how http.auth.basic already resolves its password:
// the schema describes where a secret lives, the client is what reads it.
func BuildPool(cfg *config.Config) (*proxypool.Pool, error) {
	entries := cfg.HTTP.ProxyPool()
	out := make([]proxypool.Entry, 0, len(entries))
	for i, e := range entries {
		pe := proxypool.Entry{URL: e.URL, MaxConcurrent: e.MaxConcurrent}
		if e.PasswordEnv != "" {
			pw := os.Getenv(e.PasswordEnv)
			if pw == "" {
				// Loud: a proxy silently dialled without its password answers
				// 407 on every request, which reads as a site-wide block rather
				// than the missing environment variable it is.
				return nil, fmt.Errorf("http.proxies[%d].password_env: %s is unset or empty", i, e.PasswordEnv)
			}
			pe.Password = pw
		}
		out = append(out, pe)
	}
	pool, err := proxypool.New(out, proxypool.Strategy(cfg.ResolvedProxyStrategy()))
	if err != nil {
		return nil, fmt.Errorf("http.proxies: %w", err)
	}
	return pool, nil
}

// Pool exposes the egress pool so the renderer can route Chrome through the
// same egresses as the raw fetches. A crawl whose raw fetch is proxied and
// whose render is not leaks the origin IP and makes every raw-vs-rendered diff
// an artefact of two different network paths.
func (c *Client) Pool() *proxypool.Pool { return c.pool }

// Traffic reports the wire bytes this client has moved, which is what a per-GB
// proxy provider bills for.
func (c *Client) Traffic() Traffic { return c.meter.snapshot() }

// TrafficByEgress breaks the same tally down per proxy, so a crawl's cost can
// be attributed rather than estimated.
func (c *Client) TrafficByEgress() []Traffic { return c.meter.byEgress() }
