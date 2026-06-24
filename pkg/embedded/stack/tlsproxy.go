// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

// tlsProxy terminates HTTPS for §17.4 Embedded Mode and forwards plain
// HTTP to the gateway. The production gateway serves plaintext HTTP;
// in a cluster an ingress terminates TLS. Embedded Mode has no
// ingress, so lenny runs this reverse proxy to present
// https://localhost:8443 with the self-signed leaf certificate.
//
// The listener binds loopback only. §17.4 requires that any attempt to
// expose the gateway outside localhost fails closed; binding 127.0.0.1
// enforces that for the HTTPS listener.
type tlsProxy struct {
	srv *http.Server
}

// startTLSProxy builds and starts the HTTPS reverse proxy. listenAddr
// is the loopback host:port to serve HTTPS on, upstream is the
// gateway's plaintext base URL, and certPath and keyPath are the
// self-signed leaf certificate and key. It returns once the listener
// is bound.
func startTLSProxy(listenAddr, upstream, certPath, keyPath string) (*tlsProxy, error) {
	if err := EmbeddedModeLocalOnly(listenAddr); err != nil {
		// §17.4 EMBEDDED_MODE_LOCAL_ONLY: the gateway must not be
		// reachable outside localhost on the TLS leg.
		return nil, err
	}
	target, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("embedded tls-proxy: parse upstream %q: %w", upstream, err)
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("embedded tls-proxy: load certificate: %w", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("embedded tls-proxy: listen on %s: %w", listenAddr, err)
	}
	p := &tlsProxy{srv: srv}
	go func() {
		// ServeTLS with empty cert/key paths uses TLSConfig.Certificates.
		_ = srv.ServeTLS(ln, "", "")
	}()
	return p, nil
}

// Shutdown gracefully stops the HTTPS proxy.
func (p *tlsProxy) Shutdown(ctx context.Context) error {
	if p == nil || p.srv == nil {
		return nil
	}
	return p.srv.Shutdown(ctx)
}

// tcpRelay is the §17.4 Embedded Mode 127.0.0.1:8080 HTTP leg: a plain TCP
// relay that forwards every accepted loopback connection byte-for-byte to the
// gateway node port. It is a raw byte relay rather than an httputil reverse
// proxy, so it terminates no TLS and parses no HTTP; the in-cluster gateway
// serves plaintext HTTP on the node port, which is what the host port presents
// here. spec: §17.4 (the 127.0.0.1:8080 HTTP leg is a plain TCP relay to the
// same node port).
type tcpRelay struct {
	ln       net.Listener
	upstream string

	mu    sync.Mutex
	conns map[net.Conn]struct{}
	done  bool
}

// startTCPRelay binds the loopback host:port listenAddr and relays every
// accepted connection to upstream (the gateway node port host:port). It
// returns once the listener is bound. listenAddr must be loopback: a
// non-loopback bind fails closed under §17.4 EMBEDDED_MODE_LOCAL_ONLY, the
// same check the TLS leg holds, so the HTTP leg cannot expose the gateway
// outside localhost either.
func startTCPRelay(listenAddr, upstream string) (*tcpRelay, error) {
	if err := EmbeddedModeLocalOnly(listenAddr); err != nil {
		// §17.4 EMBEDDED_MODE_LOCAL_ONLY: the gateway must not be
		// reachable outside localhost on the HTTP leg either.
		return nil, err
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("embedded http-relay: listen on %s: %w", listenAddr, err)
	}
	r := &tcpRelay{
		ln:       ln,
		upstream: upstream,
		conns:    make(map[net.Conn]struct{}),
	}
	go r.acceptLoop()
	return r, nil
}

// acceptLoop accepts loopback connections and relays each to the upstream
// node port until the listener is closed.
func (r *tcpRelay) acceptLoop() {
	for {
		client, err := r.ln.Accept()
		if err != nil {
			// A closed listener (Shutdown) ends the loop; any other accept
			// error is transient and the loop is torn down with the listener,
			// so return on the first error.
			return
		}
		go r.relay(client)
	}
}

// relay dials the upstream node port and copies bytes in both directions
// between the accepted client connection and the upstream. It closes both
// ends when either direction completes, so neither connection leaks.
func (r *tcpRelay) relay(client net.Conn) {
	if !r.track(client) {
		_ = client.Close()
		return
	}
	defer r.untrack(client)

	upstream, err := net.DialTimeout("tcp", r.upstream, 10*time.Second)
	if err != nil {
		// The gateway node port is unreachable; drop the client connection
		// rather than block it. The CLI surfaces the connection failure.
		_ = client.Close()
		return
	}
	if !r.track(upstream) {
		_ = upstream.Close()
		_ = client.Close()
		return
	}
	defer r.untrack(upstream)

	var wg sync.WaitGroup
	wg.Add(2)
	copyDir := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		// Half-close the write side so the peer observes EOF and finishes its
		// own copy direction, then the other goroutine returns.
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}
	go copyDir(upstream, client)
	go copyDir(client, upstream)
	wg.Wait()
	_ = client.Close()
	_ = upstream.Close()
}

// track registers an open connection so Shutdown can close it. It returns
// false when the relay is already shutting down, so the caller closes the
// connection rather than leaking it.
func (r *tcpRelay) track(c net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return false
	}
	r.conns[c] = struct{}{}
	return true
}

// untrack removes a connection from the open set once it closes.
func (r *tcpRelay) untrack(c net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, c)
}

// Shutdown stops accepting new connections and closes every connection still
// open, so Stack.Stop tears the HTTP leg down alongside the TLS proxy.
func (r *tcpRelay) Shutdown(_ context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return nil
	}
	r.done = true
	open := make([]net.Conn, 0, len(r.conns))
	for c := range r.conns {
		open = append(open, c)
	}
	r.mu.Unlock()

	err := r.ln.Close()
	for _, c := range open {
		_ = c.Close()
	}
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("embedded http-relay: close listener: %w", err)
	}
	return nil
}

// Addr returns the loopback host:port the relay listens on.
func (r *tcpRelay) Addr() string {
	if r == nil || r.ln == nil {
		return ""
	}
	return r.ln.Addr().String()
}

// isLoopbackHost reports whether host names the loopback interface. It
// accepts the literal localhost name and any loopback IP literal.
func isLoopbackHost(host string) bool {
	if host == "localhost" || host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// EmbeddedModeLocalOnly enforces the §17.4 EMBEDDED_MODE_LOCAL_ONLY
// fail-closed constraint on a host:port listen address: it returns a
// non-nil error wrapping the EMBEDDED_MODE_LOCAL_ONLY code unless the host
// is loopback. Both host-side forwarder legs (the TLS proxy on
// 127.0.0.1:8443 and the plain-TCP HTTP relay on 127.0.0.1:8080) gate their
// bind on it, so neither leg can expose the in-cluster gateway outside
// localhost. The §17.4 security battery drives it directly to assert the
// fail-closed bind on a non-loopback address.
//
// spec: §17.4 (EMBEDDED_MODE_LOCAL_ONLY: any attempt to bind the host-side
// forwarder outside loopback fails closed).
func EmbeddedModeLocalOnly(listenAddr string) error {
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return fmt.Errorf("EMBEDDED_MODE_LOCAL_ONLY: parse listen address %q: %w", listenAddr, err)
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("EMBEDDED_MODE_LOCAL_ONLY: forwarder listener host %q is not loopback", host)
	}
	return nil
}
