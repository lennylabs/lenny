// SPDX-License-Identifier: MIT

// Package egress is the journey agent's egress proxy. The agent container
// sits on an internal Docker network with no route out; the proxy is the
// one other container on that network, and it opens a tunnel only to the
// host and port pairs it allows: the Anthropic API and the trial cluster's
// API server. It refuses every plain HTTP request and every CONNECT to
// any other address.
//
// The proxy is contributor tooling for the persona journey tier (change
// 0157). No specification section governs it, so it carries no spec
// annotation.
package egress

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Proxy is an HTTP CONNECT proxy with an allow list.
type Proxy struct {
	allow map[string]bool
	// Dial opens the upstream connection; nil uses net.Dialer.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// Log receives one line per decision; nil discards.
	Log io.Writer

	mu sync.Mutex
}

// New returns a proxy that allows CONNECT to each "host:port" in allow.
func New(allow []string) (*Proxy, error) {
	p := &Proxy{allow: map[string]bool{}}
	for _, a := range allow {
		host, port, err := net.SplitHostPort(a)
		if err != nil || host == "" || port == "" {
			return nil, fmt.Errorf("allowed address %q is not host:port", a)
		}
		p.allow[strings.ToLower(net.JoinHostPort(host, port))] = true
	}
	if len(p.allow) == 0 {
		return nil, fmt.Errorf("the proxy allows nothing")
	}
	return p, nil
}

// Allowed reports whether a CONNECT target is on the allow list.
func (p *Proxy) Allowed(target string) bool {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return false
	}
	return p.allow[strings.ToLower(net.JoinHostPort(host, port))]
}

func (p *Proxy) logf(format string, args ...any) {
	if p.Log == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.Log, format+"\n", args...)
}

// ServeHTTP tunnels an allowed CONNECT and refuses everything else.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		p.logf("refused %s %s: only CONNECT is proxied", r.Method, r.URL)
		http.Error(w, "only CONNECT to an allowed host is proxied", http.StatusForbidden)
		return
	}
	target := r.Host
	if !p.Allowed(target) {
		p.logf("refused CONNECT %s", target)
		http.Error(w, "CONNECT to "+target+" is not allowed", http.StatusForbidden)
		return
	}
	dial := p.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 15 * time.Second}).DialContext
	}
	upstream, err := dial(r.Context(), "tcp", target)
	if err != nil {
		p.logf("CONNECT %s: %v", target, err)
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	p.logf("allowed CONNECT %s", target)
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	go pipe(upstream, buf)
	pipe(client, upstream)
}

// pipe copies src to dst and closes dst when src ends.
func pipe(dst io.WriteCloser, src io.Reader) {
	_, _ = io.Copy(dst, src)
	_ = dst.Close()
}
