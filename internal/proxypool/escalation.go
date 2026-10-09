package proxypool

import "sync"

// Escalation is one crawl's one-way switch from the direct route to the proxy
// pool (http.proxy_on_block). It is shared by everything in the crawl that
// opens connections — the fetch client and the Chrome renderer(s) — so a single
// Escalate moves all of them at once. A nil *Escalation is a valid "never
// escalates" switch.
type Escalation struct {
	mu    sync.Mutex
	up    bool
	done  chan struct{}
	hooks []func()
}

// NewEscalation returns a switch, already escalated when start is true (a
// resumed crawl that tripped in an earlier session).
func NewEscalation(start bool) *Escalation {
	e := &Escalation{done: make(chan struct{})}
	if start {
		e.up = true
		close(e.done)
	}
	return e
}

// Escalated reports whether the switch has happened.
func (e *Escalation) Escalated() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.up
}

// Done is closed once the switch has happened.
func (e *Escalation) Done() <-chan struct{} {
	if e == nil {
		return nil
	}
	return e.done
}

// Escalate flips the switch and runs every OnEscalate hook, in registration
// order, on the caller's goroutine. Only the first call does anything; it
// returns true for that caller alone.
func (e *Escalation) Escalate() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	if e.up {
		e.mu.Unlock()
		return false
	}
	e.up = true
	close(e.done)
	hooks := e.hooks
	e.hooks = nil
	e.mu.Unlock()
	for _, h := range hooks {
		h()
	}
	return true
}

// OnEscalate registers fn to run at the switch. Registering after the switch
// runs fn immediately, so a component built late can never miss it.
func (e *Escalation) OnEscalate(fn func()) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.up {
		e.mu.Unlock()
		fn()
		return
	}
	e.hooks = append(e.hooks, fn)
	e.mu.Unlock()
}
