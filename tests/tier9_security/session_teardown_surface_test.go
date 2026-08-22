// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 coverage for what a session's teardown leaves reachable on the
// pod it ran on.
//
// A §11.4 revoke and an ordinary terminal release run the same adapter
// teardown, and the surfaces that teardown must reach are pod-wide: one
// platform MCP socket and one per-connector socket serve every slot, and
// the runtime process behind them serves every slot too. The teardown
// therefore has two obligations that pull in opposite directions. An ended
// session must keep no reachable, authenticated surface. A co-tenant's
// surface must survive its neighbour's teardown.
//
// The adapter meets both by cancelling the pod-wide endpoints only when
// the release leaves the pod's shared runtime process serving no session,
// and by refusing every intra-pod call it cannot attribute to one session
// in the meantime. On a co-tenanted pod that means the ending session's
// access is closed by the refusal rather than by the cancellation, and the
// ended session's nonce outlives it on a socket that answers nobody.
//
// spec: §9.1; §11.4; §15.4.3; §13.1.
package tier9_security_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// teardownPod starts the named sessions in order on one adapter and
// returns the server and the manifest the first start wrote, which carries
// the pod-wide sockets and the nonce a conforming runtime presents.
func teardownPod(t *testing.T, fwd *recordingForwarder, sessions ...string) (*adapter.Server, *adapter.Manifest) {
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
	return s, first
}

// revoke drives the §11.4 teardown for one session.
func revoke(t *testing.T, s *adapter.Server, sessionID string) {
	t.Helper()
	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Reason:    "operator",
	}); err != nil {
		t.Fatalf("Shutdown(%s): %v", sessionID, err)
	}
}

// platformToolCallOutcome dials the pod's platform MCP socket, presents
// the nonce, and issues a tools/call. It reports whether the socket was
// reachable at all, whether the nonce authenticated, and whether the call
// was answered rather than refused.
func platformToolCallOutcome(t *testing.T, socket, nonce string) (reachable, authenticated, dispatched bool) {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return false, false, false
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"_lennyNonce": nonce, "protocolVersion": "2025-03-26"},
	}); err != nil {
		return true, false, false
	}
	var initResp map[string]json.RawMessage
	if err := dec.Decode(&initResp); err != nil {
		return true, false, false
	}
	if _, isErr := initResp["error"]; isErr {
		return true, false, false
	}
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": privilegedPlatformTool, "arguments": map[string]any{}},
	}); err != nil {
		return true, true, false
	}
	var callResp map[string]json.RawMessage
	if err := dec.Decode(&callResp); err != nil {
		return true, true, false
	}
	_, isErr := callResp["error"]
	return true, true, !isErr
}

// spec: 9.1 (the surface names one session or none), 11.4 (full revoke
// teardown), 15.4.3 (nonce-authenticated intra-pod MCP), 13.1 (isolation
// boundaries)
//
// diagnosis: a session's teardown left a reachable surface authenticated
// with that session's §15.4.3 nonce. On a pod whose shared runtime process
// the release left serving nobody, that is an ended session's manifest
// nonce still opening the pod's platform tool surface, which is the
// credential a compromised process in the pod would replay. A failure on
// the co-tenanted arm is the mirror defect: either the teardown cancelled
// a pod-wide endpoint a live co-tenant is still using, or it left the
// surface dispatching under a session that has ended.
func TestSessionTeardownLeavesNoSurfaceForTheEndedSession_spec_11_4(t *testing.T) {
	t.Run("sole_session_pod", func(t *testing.T) {
		fwd := &recordingForwarder{}
		s, m := teardownPod(t, fwd, "sess-alice")
		if _, _, dispatched := platformToolCallOutcome(t, m.PlatformMcpServer.Socket, m.MCPNonce); !dispatched {
			t.Fatal("the platform tool surface refused the sole session before its teardown")
		}
		revoke(t, s, "sess-alice")

		// The release left the pod's shared runtime process serving no
		// session, so the pod-wide endpoints are cancelled and the ended
		// session's nonce opens nothing.
		reachable, authenticated, dispatched := platformToolCallOutcome(t, m.PlatformMcpServer.Socket, m.MCPNonce)
		if authenticated || dispatched {
			t.Errorf("after the teardown the platform socket authenticated=%v dispatched=%v for the ended session's nonce, want both false",
				authenticated, dispatched)
		}
		if reachable && authenticated {
			t.Error("the ended session's manifest nonce still opens the pod's platform tool surface")
		}
		if got := fwd.platformCallCount(); got != 1 {
			t.Errorf("forwarded platform calls = %d, want 1 (the pre-teardown control call only)", got)
		}
	})

	t.Run("co_tenanted_pod", func(t *testing.T) {
		fwd := &recordingForwarder{}
		s, m := teardownPod(t, fwd, "sess-alice", "sess-bob")
		revoke(t, s, "sess-alice")

		// The pod's shared runtime process is still serving bob, so the
		// pod-wide endpoints stay up. What closes alice's access is the
		// refusal, because the surface cannot name one session.
		reachable, _, dispatched := platformToolCallOutcome(t, m.PlatformMcpServer.Socket, m.MCPNonce)
		if !reachable {
			t.Error("the co-tenant's pod-wide platform endpoint was cancelled by its neighbour's teardown")
		}
		if dispatched {
			t.Error("the platform tool surface dispatched a call after one of the pod's two sessions ended")
		}
		if got := fwd.platformCallCount(); got != 0 {
			t.Errorf("forwarded platform calls = %d on a pod that has served two sessions, want 0", got)
		}
		// The co-tenant's own stream survives its neighbour's teardown.
		if _, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
			SessionId:    &adapterv1.SessionId{Value: "sess-bob"},
			EnvelopeJson: []byte(`{"type":"message"}`),
		}); err != nil {
			t.Errorf("the surviving co-tenant's stream did not survive its neighbour's teardown: %v", err)
		}
		// The co-tenant's own calls stay refused until its release ends the
		// generation, because alice's code may still be resident in the one
		// process both were given to.
		if _, _, dispatched := platformToolCallOutcome(t, m.PlatformMcpServer.Socket, m.MCPNonce); dispatched {
			t.Error("the surviving session's own tools/call was dispatched while its neighbour's code may still be resident")
		}

		revoke(t, s, "sess-bob")
		if reachable, authenticated, _ := platformToolCallOutcome(t, m.PlatformMcpServer.Socket, m.MCPNonce); reachable && authenticated {
			t.Error("the pod-wide platform endpoint still authenticates the pod's manifest nonce after its last session ended")
		}
		if got := fwd.platformCallCount(); got != 0 {
			t.Errorf("forwarded platform calls = %d across the whole co-tenanted episode, want 0", got)
		}
	})
}
