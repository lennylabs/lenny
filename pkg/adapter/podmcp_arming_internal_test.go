// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// mcpSocketPath returns a short filesystem socket path under a temporary
// directory, short enough for the sockaddr_un limit.
func mcpSocketPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-mcp-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

// initializeWithNonce dials the pod's platform MCP socket and runs the
// §15.4.3 nonce-authenticated initialize handshake, reporting whether the
// server accepted the nonce.
func initializeWithNonce(t *testing.T, socket, nonce string) bool {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial platform MCP socket %s: %v", socket, err)
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"_lennyNonce":     nonce,
			"protocolVersion": "2025-03-26",
		},
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	var resp map[string]json.RawMessage
	if err := dec.Decode(&resp); err != nil {
		return false
	}
	_, isErr := resp["error"]
	return !isErr && resp["result"] != nil
}

// spec: §15.4.3 (intra-pod MCP surface, once-per-pod arming), §5.2 (slot
// registry)
//
// The arming is taken once per pod, and the two terms of the claim's
// predicate are both load-bearing. A claim that holds the pod alone but
// arrives while a surface is still armed leaves that surface alone rather
// than binding the one pod-wide socket a second time: the release is the
// only canceller. The claimant learns it did not take the start, so the
// handler skips startPlatformMCP instead of failing its bind with
// EADDRINUSE.
func TestPodMCPArmingDeclinedWhileSurfaceIsArmed_spec_15_4_3(t *testing.T) {
	s := New("adapter-test")
	s.WorkspaceBase = t.TempDir()
	s.MCPSocket = mcpSocketPath(t, "p.sock")

	_, startMCP, err := s.claimSessionSlot("alice", false, false)
	if err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	if !startMCP {
		t.Fatal("the first claim on an idle pod did not take the once-per-pod MCP start")
	}
	if err := s.startPlatformMCP("nonce-alice"); err != nil {
		t.Fatalf("arm the platform MCP server for alice: %v", err)
	}
	s.noteRuntimeStarted("alice")

	// alice's Shutdown: the locked cancel-deregister step has run and the
	// runtime close is still in flight, so bob's claim holds the pod alone
	// while alice's surface is still up.
	if _, removed, _ := s.deregisterSlot("alice"); !removed {
		t.Fatal("deregister alice removed no entry")
	}
	_, startMCP, err = s.claimSessionSlot("bob", false, false)
	if err != nil {
		t.Fatalf("claim bob: %v", err)
	}
	if startMCP {
		t.Fatal("a claim took the once-per-pod MCP start while a surface was still armed, so its bind races the running server on the one pod-wide socket")
	}
	if !initializeWithNonce(t, s.MCPSocket, "nonce-alice") {
		t.Error("the claim tore down the armed surface, which is the release's decision rather than the claim's")
	}
}

// spec: §15.4.3 (intra-pod MCP surface cancellation), §5.2 (slot registry)
//
// The release-side cancellation is gated on one condition: that the
// release leaves the pod's shared runtime process serving no session. A
// surface armed by a session that never reached a start is cancelled by
// whichever release finds that process idle, including a co-tenant's
// rollback, because an idle process serves no session for the surface to
// act as and the next claim re-arms on its own nonce.
func TestPodMCPCancelledOnIdleRuntimeWhileArmingSessionHoldsSlot_spec_15_4_3(t *testing.T) {
	s := New("adapter-test")
	s.WorkspaceBase = t.TempDir()
	s.MCPSocket = mcpSocketPath(t, "p.sock")

	if _, startMCP, err := s.claimSessionSlot("alice", false, false); err != nil || !startMCP {
		t.Fatalf("claim alice: startMCP=%v err=%v", startMCP, err)
	}
	if err := s.startPlatformMCP("nonce-alice"); err != nil {
		t.Fatalf("arm the platform MCP server for alice: %v", err)
	}
	// alice armed the surface and never reached Runtime.Start, so the
	// shared process serves no session. bob claims and rolls back.
	if _, _, err := s.claimSessionSlot("bob", false, false); err != nil {
		t.Fatalf("claim bob: %v", err)
	}
	s.releaseSessionSlot("bob")

	s.mu.Lock()
	armed := s.mcpCancel != nil
	s.mu.Unlock()
	if armed {
		t.Fatal("a release that left the shared runtime process serving no session did not cancel the pod surface")
	}
	if _, err := net.Dial("unix", s.MCPSocket); err == nil {
		t.Error("the platform MCP socket still accepts connections after the release")
	}
	if _, held := s.slots["alice"]; !held {
		t.Fatal("alice lost its slot entry, so the case no longer covers a cancellation while the arming session holds one")
	}
}

// spec: §15.4.3 — a release that leaves no session holding the arming
// cancels the pod's MCP surface, so a rollback that armed the servers and
// never reached a start returns the pod to an unarmed state and the next
// claim re-arms on its own nonce.
func TestPodMCPArmingCancelledWhenNoSessionHoldsIt_spec_15_4_3(t *testing.T) {
	s := New("adapter-test")
	s.WorkspaceBase = t.TempDir()
	s.MCPSocket = mcpSocketPath(t, "p.sock")

	if _, startMCP, err := s.claimSessionSlot("alice", false, false); err != nil || !startMCP {
		t.Fatalf("claim alice: startMCP=%v err=%v", startMCP, err)
	}
	if err := s.startPlatformMCP("nonce-alice"); err != nil {
		t.Fatalf("arm the platform MCP server: %v", err)
	}
	s.releaseSessionSlot("alice")

	s.mu.Lock()
	armed := s.mcpCancel != nil
	s.mu.Unlock()
	if armed {
		t.Fatal("the rollback left the pod surface armed")
	}
	if _, err := net.Dial("unix", s.MCPSocket); err == nil {
		t.Error("the platform MCP socket still accepts connections after the release")
	}

	// The socket is free for the next claim, which arms on its own nonce.
	if _, startMCP, err := s.claimSessionSlot("bob", false, false); err != nil || !startMCP {
		t.Fatalf("claim bob: startMCP=%v err=%v", startMCP, err)
	}
	if err := s.startPlatformMCP("nonce-bob"); err != nil {
		t.Fatalf("re-arm the platform MCP server: %v", err)
	}
	if !initializeWithNonce(t, s.MCPSocket, "nonce-bob") {
		t.Error("the re-armed platform MCP server did not authenticate bob's manifest nonce")
	}
	if initializeWithNonce(t, s.MCPSocket, "nonce-alice") {
		t.Error("the re-armed platform MCP server accepted the retired session's nonce")
	}
}

// spec: §15.4.3; §5.2 — a claim on a co-tenanted pod does not re-arm the
// pod-wide surface, and the co-tenant's rollback does not cancel a surface
// the incumbent is using.
func TestPodMCPArmingDeclinedOnCoTenantedPod_spec_15_4_3(t *testing.T) {
	s := New("adapter-test")
	s.WorkspaceBase = t.TempDir()
	s.MCPSocket = mcpSocketPath(t, "p.sock")

	if _, startMCP, err := s.claimSessionSlot("alice", false, false); err != nil || !startMCP {
		t.Fatalf("claim alice: startMCP=%v err=%v", startMCP, err)
	}
	if err := s.startPlatformMCP("nonce-alice"); err != nil {
		t.Fatalf("arm the platform MCP server: %v", err)
	}
	s.noteRuntimeStarted("alice")

	_, startMCP, err := s.claimSessionSlot("bob", false, false)
	if err != nil {
		t.Fatalf("claim bob: %v", err)
	}
	if startMCP {
		t.Fatal("a claim on a co-tenanted pod took the pod-wide MCP start")
	}
	// bob rolls back; alice is still served by the shared runtime process.
	s.releaseSessionSlot("bob")
	s.mu.Lock()
	armed := s.mcpCancel != nil
	s.mu.Unlock()
	if !armed {
		t.Fatal("the co-tenant's rollback disarmed the surface the incumbent's live runtime is using")
	}
	if !initializeWithNonce(t, s.MCPSocket, "nonce-alice") {
		t.Error("the platform MCP server stopped authenticating the incumbent's nonce")
	}
}
