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
// The arming decision and the pod-surface cancellation are two writers of
// the same state, and they interleave: a successor's claim runs after the
// departing session's entry is deregistered but before that session's
// Runtime.Close has returned, so the departing release finds the pod's
// shared runtime process idle. Deciding the cancellation on that idleness
// alone tears down the surface the successor has already armed, and the
// successor's own claim has returned, so nothing re-arms for the life of
// the pod and every call authenticated with its manifest nonce fails.
//
// The claim therefore takes the arming whenever the registry holds no
// entry but its own, tearing down the departed session's surface first,
// and the cancellation refuses while the session that armed the surface
// still holds a slot.
func TestPodMCPArmingSurvivesDepartingSessionRelease_spec_15_4_3(t *testing.T) {
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
	// runtime close is still in flight.
	if _, removed, _ := s.deregisterSlot("alice"); !removed {
		t.Fatal("deregister alice removed no entry")
	}

	// bob's start claims the pod in that window.
	_, startMCP, err = s.claimSessionSlot("bob", false, false)
	if err != nil {
		t.Fatalf("claim bob: %v", err)
	}
	if !startMCP {
		t.Fatal("a claim holding the pod alone did not take the MCP start, so bob's manifest nonce would reach no server")
	}
	if err := s.startPlatformMCP("nonce-bob"); err != nil {
		t.Fatalf("arm the platform MCP server for bob: %v", err)
	}

	// alice's Runtime.Close returns and its release evaluates the
	// pod-surface gate while bob's Runtime.Start has not yet returned.
	s.noteRuntimeClosed("alice")
	s.cancelPodMCPIfRuntimeIdle()

	s.mu.Lock()
	armed, owner := s.mcpCancel != nil, s.mcpSession
	s.mu.Unlock()
	if !armed {
		t.Fatal("alice's release cancelled the surface bob armed")
	}
	if owner != "bob" {
		t.Errorf("arming session = %q, want bob", owner)
	}
	if !initializeWithNonce(t, s.MCPSocket, "nonce-bob") {
		t.Error("the platform MCP server did not authenticate bob's manifest nonce")
	}
	// The claim took over the departed session's surface rather than
	// leaving it bound to the pod-wide socket, so the retired nonce is
	// refused.
	if initializeWithNonce(t, s.MCPSocket, "nonce-alice") {
		t.Error("the socket still answers the departed session's nonce, so bob's claim did not stop the stale server")
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
	armed, owner := s.mcpCancel != nil, s.mcpSession
	s.mu.Unlock()
	if armed || owner != "" {
		t.Fatalf("the rollback left the surface armed (cancel set = %v, owner = %q)", armed, owner)
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
	armed, owner := s.mcpCancel != nil, s.mcpSession
	s.mu.Unlock()
	if !armed || owner != "alice" {
		t.Fatalf("the co-tenant's rollback disarmed the incumbent's surface (cancel set = %v, owner = %q)", armed, owner)
	}
	if !initializeWithNonce(t, s.MCPSocket, "nonce-alice") {
		t.Error("the platform MCP server stopped authenticating the incumbent's nonce")
	}
}

// spec: §15.4.3 (intra-pod MCP surface, once-per-pod arming)
//
// PodMCPArming is the reading of the live arming a caller outside the
// package cannot take from the pod's one manifest file, which every start
// republishes by renaming a freshly staged document over it. It reports
// the session whose claim took the start
// together with the nonce the running servers authenticate, and both go
// empty when the release that ends the generation cancels them.
func TestPodMCPArmingReportsTheLiveArming_spec_15_4_3(t *testing.T) {
	s := New("adapter-test")
	s.WorkspaceBase = t.TempDir()
	s.MCPSocket = mcpSocketPath(t, "p.sock")

	if session, nonce := s.PodMCPArming(); session != "" || nonce != "" {
		t.Errorf("PodMCPArming on an unarmed pod = (%q, %q), want both empty", session, nonce)
	}

	if _, startMCP, err := s.claimSessionSlot("alice", false, false); err != nil || !startMCP {
		t.Fatalf("claim alice: startMCP=%v err=%v", startMCP, err)
	}
	if err := s.startPlatformMCP("nonce-alice"); err != nil {
		t.Fatalf("arm the platform MCP server for alice: %v", err)
	}
	s.noteRuntimeStarted("alice")

	session, nonce := s.PodMCPArming()
	if session != "alice" {
		t.Errorf("PodMCPArming session = %q, want alice", session)
	}
	if nonce != "nonce-alice" {
		t.Errorf("PodMCPArming nonce = %q, want the nonce the running server authenticates", nonce)
	}
	if !initializeWithNonce(t, s.MCPSocket, nonce) {
		t.Error("the nonce PodMCPArming reports does not open the pod's platform MCP socket")
	}

	// The release that leaves the pod's shared runtime process serving no
	// session cancels the surface, so the arming it reported is gone.
	if _, removed, _ := s.deregisterSlot("alice"); !removed {
		t.Fatal("deregister alice removed no entry")
	}
	s.noteRuntimeClosed("alice")
	s.cancelPodMCPIfRuntimeIdle()
	if session, nonce := s.PodMCPArming(); session != "" || nonce != "" {
		t.Errorf("PodMCPArming after the pod's surface was cancelled = (%q, %q), want both empty", session, nonce)
	}
}
