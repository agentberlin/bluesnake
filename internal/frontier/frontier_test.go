package frontier

import (
	"fmt"
	"testing"

	"github.com/hhsecond/acrawler/internal/config"
)

func newFrontier(t *testing.T, mutate func(*config.Config)) *Frontier {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	return New(cfg)
}

func TestDedup(t *testing.T) {
	f := newFrontier(t, nil)
	if !f.Admit(Item{URL: "https://ex.com/a", Depth: 0}) {
		t.Fatal("first admit must succeed")
	}
	if f.Admit(Item{URL: "https://ex.com/a", Depth: 1}) {
		t.Error("duplicate URL must be rejected")
	}
	if !f.Admit(Item{URL: "https://ex.com/b", Depth: 1}) {
		t.Error("new URL must be admitted")
	}
}

func TestDepthLimit(t *testing.T) {
	f := newFrontier(t, func(c *config.Config) { c.Limits.MaxDepth = 2 })
	if !f.Admit(Item{URL: "https://ex.com/d2", Depth: 2}) {
		t.Error("depth at limit must be admitted")
	}
	if f.Admit(Item{URL: "https://ex.com/d3", Depth: 3}) {
		t.Error("depth beyond limit must be rejected")
	}
}

func TestUnlimitedDepthByDefault(t *testing.T) {
	f := newFrontier(t, nil)
	if !f.Admit(Item{URL: "https://ex.com/deep", Depth: 9999}) {
		t.Error("default depth is unlimited")
	}
}

func TestPerDepthLimit(t *testing.T) {
	f := newFrontier(t, func(c *config.Config) { c.Limits.MaxURLsPerDepth = 2 })
	admitted := 0
	for i := range 5 {
		if f.Admit(Item{URL: fmt.Sprintf("https://ex.com/p%d", i), Depth: 1}) {
			admitted++
		}
	}
	if admitted != 2 {
		t.Errorf("admitted %d at depth 1, want 2", admitted)
	}
	if !f.Admit(Item{URL: "https://ex.com/other-depth", Depth: 2}) {
		t.Error("other depths must not be affected")
	}
}

func TestURLLengthLimit(t *testing.T) {
	f := newFrontier(t, func(c *config.Config) { c.Limits.MaxURLLength = 30 })
	if !f.Admit(Item{URL: "https://ex.com/short"}) {
		t.Error("short URL must pass")
	}
	if f.Admit(Item{URL: "https://ex.com/" + string(make([]byte, 100))}) {
		t.Error("long URL must be rejected")
	}
}

func TestQueryStringLimit(t *testing.T) {
	f := newFrontier(t, func(c *config.Config) { c.Limits.MaxQueryStrings = 2 })
	if !f.Admit(Item{URL: "https://ex.com/p?a=1&b=2"}) {
		t.Error("2 params must pass")
	}
	if f.Admit(Item{URL: "https://ex.com/q?a=1&b=2&c=3"}) {
		t.Error("3 params must be rejected")
	}
}

func TestFolderDepthLimit(t *testing.T) {
	f := newFrontier(t, func(c *config.Config) { c.Limits.MaxFolderDepth = 2 })
	if !f.Admit(Item{URL: "https://ex.com/a/b/"}) {
		t.Error("folder depth 2 must pass")
	}
	if f.Admit(Item{URL: "https://ex.com/a/b/c/"}) {
		t.Error("folder depth 3 must be rejected")
	}
}

func TestPerSubdomainLimit(t *testing.T) {
	f := newFrontier(t, func(c *config.Config) { c.Limits.MaxPerSubdomain = 2 })
	f.Admit(Item{URL: "https://a.ex.com/1"})
	f.Admit(Item{URL: "https://a.ex.com/2"})
	if f.Admit(Item{URL: "https://a.ex.com/3"}) {
		t.Error("third URL on subdomain must be rejected")
	}
	if !f.Admit(Item{URL: "https://b.ex.com/1"}) {
		t.Error("other subdomain must be admitted")
	}
}

func TestPerPathLimit(t *testing.T) {
	f := newFrontier(t, func(c *config.Config) {
		c.Limits.ByPath = []config.PathLimit{{Pattern: "/blog/", Max: 2}}
	})
	admitted := 0
	for i := range 5 {
		if f.Admit(Item{URL: fmt.Sprintf("https://ex.com/blog/p%d", i)}) {
			admitted++
		}
	}
	if admitted != 2 {
		t.Errorf("admitted %d under /blog/, want 2", admitted)
	}
	if !f.Admit(Item{URL: "https://ex.com/shop/p1"}) {
		t.Error("paths not matching the limit must be admitted")
	}
}

func TestSeenAndCount(t *testing.T) {
	f := newFrontier(t, nil)
	f.Admit(Item{URL: "https://ex.com/a"})
	if !f.Seen("https://ex.com/a") {
		t.Error("admitted URL must be seen")
	}
	if f.Seen("https://ex.com/b") {
		t.Error("unknown URL must not be seen")
	}
	if f.Admitted() != 1 {
		t.Errorf("admitted count = %d", f.Admitted())
	}
}
