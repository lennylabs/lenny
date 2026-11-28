// SPDX-License-Identifier: MIT

package stack

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// heldListeners holds the loopback listeners the tests opened for a
// forwarder leg to bind, by their address, until listenHeld hands one over.
var heldListeners = struct {
	sync.Mutex
	byAddr map[string]net.Listener
}{byAddr: map[string]net.Listener{}}

// The test binary binds every forwarder leg through listenHeld, so a leg
// whose address a test holds receives the test's open listener.
func init() { listenTCP = listenHeld }

// listenHeld returns the held listener registered for addr and removes it
// from the register, or binds addr through net.Listen when no test holds it.
func listenHeld(network, addr string) (net.Listener, error) {
	heldListeners.Lock()
	ln, ok := heldListeners.byAddr[addr]
	delete(heldListeners.byAddr, addr)
	heldListeners.Unlock()
	if ok {
		return ln, nil
	}
	return net.Listen(network, addr)
}

// heldLoopbackPort opens a loopback listener on an OS-chosen port, keeps it
// open, and registers it for the forwarder leg that binds 127.0.0.1 on that
// port, so no other process can take the port before the leg binds it. The
// test's cleanup closes the listener when no leg took it. A leg that took it
// closes it when the leg shuts down.
func heldLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := listenLoopback()
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	heldListeners.Lock()
	heldListeners.byAddr[addr] = ln
	heldListeners.Unlock()
	t.Cleanup(func() {
		heldListeners.Lock()
		defer heldListeners.Unlock()
		if held, ok := heldListeners.byAddr[addr]; ok && held == ln {
			delete(heldListeners.byAddr, addr)
			_ = ln.Close()
		}
	})
	return ln.Addr().(*net.TCPAddr).Port
}

// heldLoopbackAddr returns the 127.0.0.1 address of a held port
// (heldLoopbackPort).
func heldLoopbackAddr(t *testing.T) string {
	t.Helper()
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(heldLoopbackPort(t)))
}

// listenLoopback opens a loopback TCP listener on an OS-chosen port.
func listenLoopback() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}

// portOf returns the port component of a host:port address.
func portOf(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return port
}

// contains reports whether s contains sub.
func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
