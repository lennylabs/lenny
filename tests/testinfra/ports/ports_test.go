// SPDX-License-Identifier: MIT

package ports_test

import (
	"net"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/ports"
)

// spec: 17.4 (every test that opens a port uses testinfra/ports)
// diagnosis: NewListener did not return a usable listener bound to
//
//	loopback. Net.Listen("tcp", "127.0.0.1:0") returned
//	something the kernel rejected.
func TestNewListenerBindsLoopback(t *testing.T) {
	t.Parallel()
	ln := ports.NewListener(t)
	if !strings.HasPrefix(ln.Addr().String(), "127.0.0.1:") {
		t.Errorf("listener should bind 127.0.0.1; got %q", ln.Addr().String())
	}
}

// spec: 17.4 (two concurrent NewListener calls get distinct ports)
// diagnosis: Two ephemeral ports collided — the OS handed out the
//
//	same port for two open sockets, which is impossible
//	unless one of them was closed prematurely.
func TestNewListenerReturnsDistinctPorts(t *testing.T) {
	t.Parallel()
	a := ports.NewListener(t)
	b := ports.NewListener(t)
	if a.Addr().String() == b.Addr().String() {
		t.Errorf("two listeners share an address: %s", a.Addr())
	}
}

// spec: 17.4 (Reserve returns a free address)
// diagnosis: A reserved port did not accept a subsequent bind. The
//
//	close-before-return race in Reserve is broken or the
//	address was already in use.
func TestReserveReturnsBindableAddress(t *testing.T) {
	t.Parallel()
	addr := ports.Reserve(t)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("could not bind reserved address %s: %v", addr, err)
	}
	_ = ln.Close()
}
