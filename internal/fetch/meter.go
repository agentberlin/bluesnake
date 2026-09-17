package fetch

import (
	"net"
	"sync/atomic"

	"github.com/agentberlin/bluesnake/internal/proxypool"
)

// Traffic is a byte tally for one egress, or for a whole client.
type Traffic struct {
	Proxy string // the egress's redacted label, or "" on the client total
	In    int64  // bytes read off the socket
	Out   int64  // bytes written to the socket
}

// Total is what a per-GB provider bills for.
func (t Traffic) Total() int64 { return t.In + t.Out }

type counter struct {
	in  atomic.Int64
	out atomic.Int64
}

// meter counts WIRE bytes, which is the unit proxy providers bill in and the
// only unit worth reporting. It sits at the dial layer, below TLS, so it sees
// exactly the ciphertext that crosses the proxy — not the decompressed body the
// pipeline works with. (pages.size is the decompressed body and overstates
// billable traffic by roughly 3-4x for HTML, so it must never be used as a cost
// estimate.)
//
// Attribution is per CONNECTION, not per request: connections are pooled and
// HTTP/2 multiplexes many requests onto one, so a per-request split would be a
// fiction. A connection is attributed by the address it dialled — a known proxy
// egress, or direct — which is exactly the granularity an invoice has.
type meter struct {
	total  counter
	byAddr map[string]*counter // proxy dial address -> counter (read-only after build)
	labels map[string]string   // proxy dial address -> redacted label
	direct counter
}

func newMeter(pool *proxypool.Pool) *meter {
	m := &meter{
		byAddr: map[string]*counter{},
		labels: map[string]string{},
	}
	for _, p := range pool.Proxies() {
		if addr := p.DialAddr(); addr != "" {
			m.byAddr[addr] = &counter{}
			m.labels[addr] = p.Label()
		}
	}
	return m
}

func (m *meter) forAddr(addr string) *counter {
	if c, ok := m.byAddr[addr]; ok {
		return c
	}
	return &m.direct
}

// total traffic across every egress.
func (m *meter) snapshot() Traffic {
	return Traffic{In: m.total.in.Load(), Out: m.total.out.Load()}
}

// byEgress reports traffic per egress, proxies first then direct. Egresses that
// carried nothing are omitted — a pool entry that never dialled says nothing
// useful, and listing it invites the reader to mistake zero for an error.
func (m *meter) byEgress() []Traffic {
	var out []Traffic
	for addr, c := range m.byAddr {
		if in, o := c.in.Load(), c.out.Load(); in != 0 || o != 0 {
			out = append(out, Traffic{Proxy: m.labels[addr], In: in, Out: o})
		}
	}
	if in, o := m.direct.in.Load(), m.direct.out.Load(); in != 0 || o != 0 {
		out = append(out, Traffic{Proxy: proxypool.DirectLabel, In: in, Out: o})
	}
	return out
}

// countingConn tallies every byte crossing one connection, into both the
// client-wide total and its egress's own counter.
type countingConn struct {
	net.Conn
	total *counter
	own   *counter
}

func (c *countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.total.in.Add(int64(n))
		c.own.in.Add(int64(n))
	}
	return n, err
}

func (c *countingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.total.out.Add(int64(n))
		c.own.out.Add(int64(n))
	}
	return n, err
}
