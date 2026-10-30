// SPDX-License-Identifier: MIT

// Package tcpfault is an in-process TCP proxy that a test places between
// a Lenny process and a backing store to inject a store outage and end
// it. Cut closes the listener and every proxied connection, so the
// process's open connections break and every new dial is refused, as
// when the store's endpoint disappears. Restore listens again on the
// same address, so the process reconnects to the same store with its
// data intact. Terminating a testcontainers container instead loses both
// the data and the address.
package tcpfault

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"
)

// Proxy forwards TCP connections from Addr to an upstream address.
type Proxy struct {
	upstream string
	addr     string

	// mu guards ln and conns: Cut and Restore swap the listener, and
	// every accepted or dialled connection is tracked so Cut can close it.
	mu    sync.Mutex
	ln    net.Listener
	conns map[net.Conn]struct{}
}

// Start listens on a free loopback port and forwards every connection to
// upstream. The cleanup of t closes the proxy.
func Start(t testing.TB, upstream string) *Proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcpfault: listen: %v", err)
	}
	p := &Proxy{upstream: upstream, addr: ln.Addr().String(), ln: ln, conns: map[net.Conn]struct{}{}}
	go p.serve(ln)
	t.Cleanup(p.Cut)
	return p
}

// Addr returns the host:port a process dials in place of the upstream.
func (p *Proxy) Addr() string { return p.addr }

// Cut closes the listener and every proxied connection. Until Restore,
// a dial to Addr is refused. Cut is idempotent.
func (p *Proxy) Cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln != nil {
		_ = p.ln.Close()
		p.ln = nil
	}
	for c := range p.conns {
		_ = c.Close()
		delete(p.conns, c)
	}
}

// Restore listens on Addr again after a Cut. It retries briefly, because
// the kernel can hold the port for a moment after the close.
func (p *Proxy) Restore(t testing.TB) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln != nil {
		return
	}
	var (
		ln  net.Listener
		err error
	)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if ln, err = net.Listen("tcp", p.addr); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("tcpfault: relisten on %s: %v", p.addr, err)
	}
	p.ln = ln
	go p.serve(ln)
}

// serve accepts connections on ln until it is closed.
func (p *Proxy) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go p.forward(c)
	}
}

// forward dials the upstream for c and copies bytes both ways until
// either side closes or Cut closes both.
func (p *Proxy) forward(c net.Conn) {
	up, err := net.DialTimeout("tcp", p.upstream, 5*time.Second)
	if err != nil {
		_ = c.Close()
		return
	}
	if !p.track(c, up) {
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go pipe(&wg, up, c)
	go pipe(&wg, c, up)
	wg.Wait()
	p.untrack(c, up)
}

// track records both ends of a proxied connection. It reports false, and
// closes both, when a Cut has already closed the listener that accepted c.
func (p *Proxy) track(conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln == nil {
		for _, c := range conns {
			_ = c.Close()
		}
		return false
	}
	for _, c := range conns {
		p.conns[c] = struct{}{}
	}
	return true
}

// untrack forgets and closes both ends of a finished connection.
func (p *Proxy) untrack(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
		delete(p.conns, c)
	}
}

// pipe copies src to dst and closes dst's write side when src ends, so
// the peer sees the end of the stream.
func pipe(wg *sync.WaitGroup, dst, src net.Conn) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
	if tc, ok := dst.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
		return
	}
	_ = dst.Close()
}

// RewriteURL returns rawURL with its host:port replaced by addr, so a
// process given the result dials the proxy. It accepts a postgres:// DSN
// and a redis:// URL alike.
func RewriteURL(rawURL, addr string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("tcpfault: parse %q: %w", rawURL, err)
	}
	if u.Host == "" {
		return "", errors.New("tcpfault: URL carries no host")
	}
	u.Host = addr
	return u.String(), nil
}

// HostPort returns the host:port of rawURL.
func HostPort(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("tcpfault: parse %q: %w", rawURL, err)
	}
	if u.Host == "" {
		return "", errors.New("tcpfault: URL carries no host")
	}
	return u.Host, nil
}

// StartURL starts a proxy to the host:port of rawURL and returns it with
// rawURL rewritten to dial the proxy.
func StartURL(t testing.TB, rawURL string) (*Proxy, string) {
	t.Helper()
	upstream, err := HostPort(rawURL)
	if err != nil {
		t.Fatalf("%v", err)
	}
	p := Start(t, upstream)
	proxied, err := RewriteURL(rawURL, p.Addr())
	if err != nil {
		t.Fatalf("%v", err)
	}
	return p, proxied
}
