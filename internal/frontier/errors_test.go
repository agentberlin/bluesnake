package frontier

// #74 R6/D4: every dedup-authority error arm. Until now no test could reach
// them — memDedup cannot error — so each swallowed error was invisible. The
// erroring fake drives all five: Admit, the cap-overflow Remove rollback,
// Readmit, MarkSeen, and Seen.

import (
	"errors"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

var errAuthority = errors.New("authority failed")

// errDedup wraps the exact in-memory dedup, failing whichever methods the test
// arms. Admit can also be set to succeed-then-fail-rollback via failRemove.
type errDedup struct {
	inner                                              Dedup
	failAdmit, failRemove, failSeen, failMark, tripped bool
}

func (d *errDedup) Admit(it Item) (bool, error) {
	if d.failAdmit {
		d.tripped = true
		return false, errAuthority
	}
	return d.inner.Admit(it)
}
func (d *errDedup) Remove(url string) error {
	if d.failRemove {
		d.tripped = true
		return errAuthority
	}
	return d.inner.Remove(url)
}
func (d *errDedup) Seen(url string) (bool, error) {
	if d.failSeen {
		d.tripped = true
		return false, errAuthority
	}
	return d.inner.Seen(url)
}
func (d *errDedup) MarkSeen(urls []string) error {
	if d.failMark {
		d.tripped = true
		return errAuthority
	}
	return d.inner.MarkSeen(urls)
}
func (d *errDedup) Count() (int, error) { return d.inner.Count() }

// erringFrontier builds a frontier over an armed errDedup with an error
// collector, returning both.
func erringFrontier(t *testing.T, arm func(*errDedup), mutate func(*config.Config)) (*Frontier, *errDedup, *[]error) {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	d := &errDedup{inner: newMemDedup()}
	arm(d)
	var got []error
	f := New(cfg, WithDedup(d), WithErrorSink(func(err error) { got = append(got, err) }))
	return f, d, &got
}

func TestAdmitErrorDeclinesAndSurfaces(t *testing.T) {
	f, _, got := erringFrontier(t, func(d *errDedup) { d.failAdmit = true }, nil)
	if f.Admit(Item{URL: "https://ex.com/a"}) {
		t.Error("an erroring Admit must decline the URL (never double-admit)")
	}
	if len(*got) != 1 || !errors.Is((*got)[0], errAuthority) {
		t.Errorf("Admit error not surfaced: %v", *got)
	}
}

func TestRemoveRollbackErrorSurfaced(t *testing.T) {
	// Cap 0 at depth 0: the novel URL passes dedup, overflows the cap, and the
	// rollback Remove fails — leaking a durable frontier row a later resume
	// would crawl cap-free. That leak must be loud.
	f, d, got := erringFrontier(t,
		func(d *errDedup) { d.failRemove = true },
		func(c *config.Config) { c.Limits.MaxURLsPerDepth = 0 })
	if f.Admit(Item{URL: "https://ex.com/a", Depth: 0}) {
		t.Error("over-cap URL must be declined")
	}
	if !d.tripped {
		t.Fatal("test wiring: the rollback Remove never ran")
	}
	if len(*got) != 1 || !errors.Is((*got)[0], errAuthority) {
		t.Errorf("rollback Remove error not surfaced: %v", *got)
	}
}

func TestReadmitErrorSurfaced(t *testing.T) {
	f, _, got := erringFrontier(t, func(d *errDedup) { d.failAdmit = true }, nil)
	if !f.Readmit(Item{URL: "https://ex.com/pending"}) {
		t.Error("Readmit always re-queues")
	}
	if len(*got) != 1 || !errors.Is((*got)[0], errAuthority) {
		t.Errorf("Readmit authority error not surfaced: %v", *got)
	}
}

func TestMarkSeenErrorSurfaced(t *testing.T) {
	f, _, got := erringFrontier(t, func(d *errDedup) { d.failMark = true }, nil)
	f.MarkSeen([]string{"https://ex.com/done"})
	if len(*got) != 1 || !errors.Is((*got)[0], errAuthority) {
		t.Errorf("MarkSeen error not surfaced: %v", *got)
	}
}

func TestSeenErrorSurfaced(t *testing.T) {
	f, _, got := erringFrontier(t, func(d *errDedup) { d.failSeen = true }, nil)
	if f.Seen("https://ex.com/a") {
		t.Error("an erroring Seen must read as not-seen (conservative)")
	}
	if len(*got) != 1 || !errors.Is((*got)[0], errAuthority) {
		t.Errorf("Seen error not surfaced: %v", *got)
	}
}
