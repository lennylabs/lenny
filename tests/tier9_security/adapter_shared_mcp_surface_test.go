// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §9.1 / §15.4.3 intra-pod MCP surface identity, driven against the
// real adapter.Server serving a live platform MCP socket.
//
// One runtime process serves every slot on a pod and it dials one pod-wide
// platform MCP socket. Every tools/list and tools/call on that socket is
// forwarded to the gateway under a session identifier, and the gateway
// installs that session's user and tenant as the authenticated principal
// for the call. A surface that carried an identifier captured when the
// server started would therefore execute a co-tenant's tool calls under
// the first session's user. Tenant pinning does not bound that: a
// concurrent pod is pinned to one tenant rather than to one user.
//
// The adapter resolves the calling session at call time instead, and
// refuses when the pod's shared runtime process has been given more than
// one session since it was last serving none, which is the condition under
// which a forwarded identifier cannot name a session other than the
// caller's.
//
// spec: §9.1; §15.4.3; §13.1.
package tier9_security_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// sharedSurfacePod starts sessions on one adapter in order and returns the
// server together with the forwarder recording every gateway-bound
// dispatch and the parsed manifest the first start wrote.
func sharedSurfacePod(t *testing.T, fwd *recordingForwarder, sessions ...string) *adapter.Manifest {
	t.Helper()
	s := adapter.New("test")
	s.WorkspaceBase = t.TempDir()
	s.Runtime = noopRuntime{}
	s.ManifestDir = t.TempDir()
	s.MCPSocket = shortMCPSocket(t)
	s.PlatformForwarder = fwd
	s.ConnectorForwarder = fwd

	var first *adapter.Manifest
	for _, sessionID := range sessions {
		if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: sessionID},
			Runtime:   "echo",
		}); err != nil {
			t.Fatalf("StartSession(%s): %v", sessionID, err)
		}
		if first == nil {
			first = readProbeManifest(t, s)
		}
	}
	t.Cleanup(func() {
		for _, sessionID := range sessions {
			_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
				SessionId: &adapterv1.SessionId{Value: sessionID},
			})
		}
	})
	return first
}

// readProbeManifest parses the §15.4 manifest the adapter wrote, which
// carries the pod-wide platform socket and the nonce a conforming runtime
// authenticates with.
func readProbeManifest(t *testing.T, s *adapter.Server) *adapter.Manifest {
	t.Helper()
	m := decodeManifestFile(t, s.ManifestDir)
	if m.PlatformMcpServer == nil || m.PlatformMcpServer.Socket == "" {
		t.Fatalf("manifest carries no platform MCP socket: %+v", m.PlatformMcpServer)
	}
	if m.MCPNonce == "" {
		t.Fatal("manifest carries no MCP nonce")
	}
	return m
}

// spec: 9.1 (platform tool surface), 15.4.3 (nonce-authenticated intra-pod
// MCP), 13.1 (isolation boundaries)
//
// diagnosis: a failure means the intra-pod platform MCP surface forwarded a
// call under a session identifier it cannot know belongs to the caller. One
// runtime process serves every slot, so on a pod holding a second session
// that is a co-tenant's tool call executing under the first session's user
// and its delegation budget, which no gateway-side check catches: the
// gateway installs whatever session the adapter names as the authenticated
// principal.
func TestSharedPlatformMCPRefusesWhenItCannotNameTheCaller_spec_9_1(t *testing.T) {
	fwd := &recordingForwarder{}
	m := sharedSurfacePod(t, fwd, "sess-alice", "sess-bob")

	conn, err := net.Dial("unix", m.PlatformMcpServer.Socket)
	if err != nil {
		t.Fatalf("dial platform MCP socket: %v", err)
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)

	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"_lennyNonce": m.MCPNonce, "protocolVersion": "2025-03-26"},
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	var initResp map[string]json.RawMessage
	if err := dec.Decode(&initResp); err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	if _, isErr := initResp["error"]; isErr {
		t.Fatalf("nonce-bearing initialize errored: %s", initResp["error"])
	}

	for id, method := range map[int]string{2: "tools/list", 3: "tools/call"} {
		params := map[string]any{}
		if method == "tools/call" {
			params = map[string]any{"name": privilegedPlatformTool, "arguments": map[string]any{}}
		}
		if err := enc.Encode(map[string]any{
			"jsonrpc": "2.0", "id": id, "method": method, "params": params,
		}); err != nil {
			t.Fatalf("send %s: %v", method, err)
		}
		var resp map[string]json.RawMessage
		if err := dec.Decode(&resp); err != nil {
			t.Fatalf("read %s response: %v", method, err)
		}
		if _, isErr := resp["error"]; !isErr {
			t.Errorf("%s on a pod serving two sessions succeeded; the surface cannot name the caller "+
				"and must refuse rather than forward under a co-tenant's principal", method)
		}
	}
	if got := fwd.platformCallCount(); got != 0 {
		t.Errorf("the surface forwarded %d platform call(s) on a pod serving two sessions, want 0", got)
	}
}

// spec: 9.1, 15.4.3
//
// The positive control: on a pod whose shared runtime process has been
// given one session and no other, the surface names that session and
// forwards. Without it the case above would pass against a surface that
// refuses everything.
//
// diagnosis: a failure means the resolution refuses a call it can
// attribute, so every Standard- and Full-level runtime loses the platform
// tool surface on a pod serving one session.
func TestSharedPlatformMCPForwardsWhenItCanNameTheCaller_spec_9_1(t *testing.T) {
	fwd := &recordingForwarder{}
	m := sharedSurfacePod(t, fwd, "sess-alice")

	conn, err := net.Dial("unix", m.PlatformMcpServer.Socket)
	if err != nil {
		t.Fatalf("dial platform MCP socket: %v", err)
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)

	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"_lennyNonce": m.MCPNonce, "protocolVersion": "2025-03-26"},
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	var initResp map[string]json.RawMessage
	if err := dec.Decode(&initResp); err != nil {
		t.Fatalf("read initialize response: %v", err)
	}

	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": privilegedPlatformTool, "arguments": map[string]any{}},
	}); err != nil {
		t.Fatalf("send tools/call: %v", err)
	}
	var callResp map[string]json.RawMessage
	if err := dec.Decode(&callResp); err != nil {
		t.Fatalf("read tools/call response: %v", err)
	}
	if _, isErr := callResp["error"]; isErr {
		t.Fatalf("tools/call on a sole-occupied pod errored: %s", callResp["error"])
	}
	if got := fwd.platformCallSessions(); len(got) != 1 || got[0] != "sess-alice" {
		t.Errorf("forwarded platform call sessions = %v, want [sess-alice]", got)
	}
}
