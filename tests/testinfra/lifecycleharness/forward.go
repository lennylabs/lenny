// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"bytes"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// harnessAuthOptions are the auth options of every harness replica's public
// path and forward listener: the dev-header transport, with the
// X-Lenny-Tenant-ID header naming the tenant, so that header is the
// credential the auth middleware records and a forward relays.
//
// spec: §29.3 (Interactive message send) Forwarding rules 2 and 3
var harnessAuthOptions = authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}

// forwardListener is one replica's CH-HOLDERFORWARD listener on
// 127.0.0.1:0. It serves handler over TLS and carries the two faults a
// scenario arms: refusing every connection before the TLS handshake, and
// closing a connection with no response after a request was read.
//
// spec: §28.5.4 (Inter-replica), §29.3 (Interactive message send)
// Forwarding rules 6 and 7
type forwardListener struct {
	lis    net.Listener
	srv    *http.Server
	refuse atomic.Bool
	fault  atomic.Pointer[func(*http.Request) bool]

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// newForwardListener opens a listener on 127.0.0.1:0. serve starts serving
// handler with cfg on it; the address is known before the handler exists, so
// the replica's holder value can name it.
func newForwardListener(t testing.TB) *forwardListener {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("lifecycleharness: open forward listener: %v", err)
	}
	f := &forwardListener{conns: map[net.Conn]struct{}{}}
	f.lis = &refusingListener{Listener: lis, refuse: &f.refuse}
	return f
}

// Addr is the listener's host:port.
func (f *forwardListener) Addr() string { return f.lis.Addr().String() }

// serve serves handler over TLS with cfg in the background.
func (f *forwardListener) serve(cfg *tls.Config, handler http.Handler) {
	f.srv = &http.Server{
		Handler:           f.faulting(handler),
		TLSConfig:         cfg,
		ReadHeaderTimeout: 10 * time.Second,
		ConnState:         f.track,
		ErrorLog:          discardLog,
	}
	go func() { _ = f.srv.ServeTLS(f.lis, "", "") }()
}

// track records the open connections, so arming the refusal closes them.
func (f *forwardListener) track(c net.Conn, st http.ConnState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch st {
	case http.StateNew:
		f.conns[c] = struct{}{}
	case http.StateClosed, http.StateHijacked:
		delete(f.conns, c)
	}
}

// setRefuse arms or disarms the refusal. Arming it closes every open
// connection, so the next forward to this listener dials and is refused.
func (f *forwardListener) setRefuse(on bool) {
	f.refuse.Store(on)
	if !on {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for c := range f.conns {
		_ = c.Close()
	}
}

// setFault arms fn, or disarms the fault when fn is nil.
func (f *forwardListener) setFault(fn func(*http.Request) bool) {
	if fn == nil {
		f.fault.Store(nil)
		return
	}
	f.fault.Store(&fn)
}

// faulting runs the armed fault after the request, its body included, was
// read and before next, and closes the connection with no response when the
// fault returns true.
func (f *forwardListener) faulting(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fn := f.fault.Load()
		if fn == nil {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			dropConnection(w)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if (*fn)(r) {
			dropConnection(w)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

// dropConnection closes the request's connection without writing a response.
func dropConnection(w http.ResponseWriter) {
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	_ = conn.Close()
}

// close stops the listener and closes its connections.
func (f *forwardListener) close() {
	if f.srv == nil {
		_ = f.lis.Close()
		return
	}
	_ = f.srv.Close()
}

// refusingListener closes each accepted connection before the TLS handshake
// while refuse is set.
type refusingListener struct {
	net.Listener
	refuse *atomic.Bool
}

func (l *refusingListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if !l.refuse.Load() {
			return c, nil
		}
		_ = c.Close()
	}
}

// discardLog drops the forward listener's server log, which reports the
// connections a fault closes.
var discardLog = log.New(io.Discard, "", 0)
