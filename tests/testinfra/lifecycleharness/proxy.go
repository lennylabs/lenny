// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"io"
	"net"
	"sync"
	"testing"
)

// Proxy is a TCP proxy in front of one backend that can partition the path:
// while partitioned it accepts connections and reads from them but forwards
// nothing in either direction, so a client's request hangs until its own
// deadline rather than failing fast, as a network partition does. Heal
// restores forwarding for new and existing connections.
type Proxy struct {
	lis     net.Listener
	backend string

	mu          sync.Mutex
	partitioned bool
	healed      chan struct{}
	conns       []net.Conn
}

// NewProxy starts a proxy to backend and closes it when the test ends.
func NewProxy(t testing.TB, backend string) *Proxy {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("proxy listen: %v", err)
	}
	p := &Proxy{lis: lis, backend: backend, healed: make(chan struct{})}
	close(p.healed)
	go p.serve()
	t.Cleanup(p.close)
	return p
}

// Addr is the proxy's listening address.
func (p *Proxy) Addr() string { return p.lis.Addr().String() }

// Partition stops forwarding.
func (p *Proxy) Partition() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.partitioned {
		p.partitioned = true
		p.healed = make(chan struct{})
	}
}

// Heal resumes forwarding.
func (p *Proxy) Heal() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.partitioned {
		p.partitioned = false
		close(p.healed)
	}
}

// gate returns a channel that is closed while the path is healed.
func (p *Proxy) gate() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.healed
}

func (p *Proxy) serve() {
	for {
		c, err := p.lis.Accept()
		if err != nil {
			return
		}
		b, err := net.Dial("tcp", p.backend)
		if err != nil {
			_ = c.Close()
			continue
		}
		p.mu.Lock()
		p.conns = append(p.conns, c, b)
		p.mu.Unlock()
		go p.pipe(b, c)
		go p.pipe(c, b)
	}
}

// pipe copies src to dst, holding each chunk while the path is
// partitioned.
func (p *Proxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			<-p.gate()
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				_ = dst.Close()
			}
			return
		}
	}
}

func (p *Proxy) close() {
	_ = p.lis.Close()
	p.Heal()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
}
