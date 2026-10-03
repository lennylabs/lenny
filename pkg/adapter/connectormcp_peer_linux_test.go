// SPDX-License-Identifier: MIT

//go:build linux

package adapter

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mcpListenerAdmits binds an intra-pod MCP listener on s, dials it from the
// test process, and reports whether the listener yielded the connection. A
// refused dial reads EOF and Accept keeps waiting until the listener closes.
// SO_PEERCRED is a Linux socket option, so the file is Linux-only.
func mcpListenerAdmits(t *testing.T, s *Server) bool {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcppc")
	if err != nil {
		t.Fatalf("temp MCP socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	lis, err := s.listenIntraPodMCP(filepath.Join(dir, "m.sock"))
	if err != nil {
		t.Fatalf("listenIntraPodMCP: %v", err)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := lis.Accept(); err == nil {
			accepted <- c
		}
	}()
	t.Cleanup(func() { _ = lis.Close() })

	conn, err := net.Dial("unix", lis.Addr().String())
	if err != nil {
		t.Fatalf("dial MCP socket: %v", err)
	}
	defer conn.Close()
	select {
	case c := <-accepted:
		_ = c.Close()
		return true
	case <-time.After(500 * time.Millisecond):
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := conn.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("a dial the listener did not yield read (%d, %v), want (0, EOF)", n, err)
	}
	return false
}

// spec: 4.7.11 (Separate UIDs and connection authentication)
func TestIntraPodMCPListenerAdmitsOnlyTheAgentUID_spec_4_7_11(t *testing.T) {
	self := uint32(os.Getuid())
	if !mcpListenerAdmits(t, &Server{PeerAuth: SocketPeerAuth{ExpectedUID: self}}) {
		t.Error("the MCP listener refused a peer running as the agent UID")
	}
	if mcpListenerAdmits(t, &Server{PeerAuth: SocketPeerAuth{ExpectedUID: self + 1}}) {
		t.Error("the MCP listener admitted a peer whose SO_PEERCRED UID is not the agent UID")
	}
}

// spec: 4.7.11 (Separate UIDs and connection authentication)
func TestIntraPodMCPListenerFailsClosedWithoutAnAgentUID_spec_4_7_11(t *testing.T) {
	// The zero posture names no agent UID and admits only UID 0, so an
	// unconfigured listener admits no unprivileged process.
	got := mcpListenerAdmits(t, &Server{})
	if want := os.Getuid() == 0; got != want {
		t.Errorf("unconfigured MCP listener admitted the test process (uid %d) = %v, want %v",
			os.Getuid(), got, want)
	}
}

// spec: 4.7.11 (Nonce-only fallback), 28.5.3 (CH-MSGSOCK)
func TestIntraPodMCPListenerNonceOnlyModeAppliesNoPeerCheck_spec_4_7_11(t *testing.T) {
	s := &Server{PeerAuth: SocketPeerAuth{ExpectedUID: uint32(os.Getuid()) + 1, NonceOnly: true}}
	if !mcpListenerAdmits(t, s) {
		t.Error("the MCP listener applied a peer check in nonce-only mode, where the check is unavailable")
	}
}
