// SPDX-License-Identifier: MIT

// Package ports gives every test a fresh, OS-assigned listener.
//
// §17.4 forbids port hardcoding — every test that needs to open a
// port allocates one through NewListener. NewListener calls
// net.Listen with port 0, which asks the kernel for an ephemeral
// port, then registers a t.Cleanup that closes the listener.
//
// Two patterns:
//
//	ln := ports.NewListener(t)       // a net.Listener already bound
//	addr := ports.Reserve(t)         // an address; the caller binds
package ports

import (
	"net"
	"testing"
)

// NewListener returns a TCP listener bound to a free local port. It
// registers a t.Cleanup that closes the listener. The listener is
// IPv4 loopback only (127.0.0.1) so it never collides with another
// host process listening on the same port across all interfaces.
func NewListener(t testing.TB) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ports.NewListener: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// Reserve returns an address of the form "127.0.0.1:<port>" that is
// not currently in use. The reservation is racy in principle —
// another process could grab the port between Reserve and the
// caller's bind — so it is best for tests where the caller binds
// immediately. Prefer NewListener when the caller does not need to
// pass an address through a config struct.
func Reserve(t testing.TB) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ports.Reserve: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
