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

// intraPodMCPOutcome dials one of the pod's intra-pod MCP sockets,
// presents the nonce, and issues the named request. It reports whether the
// socket was reachable at all, whether the nonce authenticated, and whether
// the request was answered rather than refused. It is the one probe both
// the platform socket and the per-connector sockets are read through,
// because §15.4.3 gives them the same handshake and §9.1 gives them the
// same session gate.
func intraPodMCPOutcome(t *testing.T, socket, nonce, method string, params map[string]any) (reachable, authenticated, dispatched bool) {
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
		"jsonrpc": "2.0", "id": 2, "method": method, "params": params,
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

// platformToolCallOutcome probes the pod's platform MCP socket with a
// tools/call for a privileged platform tool.
func platformToolCallOutcome(t *testing.T, socket, nonce string) (reachable, authenticated, dispatched bool) {
	t.Helper()
	return intraPodMCPOutcome(t, socket, nonce, "tools/call",
		map[string]any{"name": privilegedPlatformTool, "arguments": map[string]any{}})
}

// connectorToolCallOutcome probes one of the pod's per-connector MCP
// sockets with a tools/call. The per-connector servers are armed and
// cancelled on their own code path, so a teardown that reaches the
// platform socket alone still leaves this one serving.
func connectorToolCallOutcome(t *testing.T, socket, nonce string) (reachable, authenticated, dispatched bool) {
	t.Helper()
	return intraPodMCPOutcome(t, socket, nonce, "tools/call",
		map[string]any{"name": connectorProbeTool, "arguments": map[string]any{}})
}

// connectorToolListOutcome probes a per-connector MCP socket with a
// tools/list, which §9.1 gates on the same sole-session test as a call.
func connectorToolListOutcome(t *testing.T, socket, nonce string) (reachable, authenticated, dispatched bool) {
	t.Helper()
	return intraPodMCPOutcome(t, socket, nonce, "tools/list", map[string]any{})
}

// connectorProbeTool is the tool the recording forwarder advertises on the
// resolved connector.
const connectorProbeTool = "list_repos"

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
		connectorSocket := soleConnectorSocket(t, m)
		if _, _, dispatched := platformToolCallOutcome(t, m.PlatformMcpServer.Socket, m.MCPNonce); !dispatched {
			t.Fatal("the platform tool surface refused the sole session before its teardown")
		}
		if _, _, dispatched := connectorToolCallOutcome(t, connectorSocket, m.MCPNonce); !dispatched {
			t.Fatal("the connector tool surface refused the sole session before its teardown")
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

		// The per-connector servers are armed on their own code path and
		// cancelled through their own cancels, so the teardown is checked
		// against the connector socket separately: an ended session's nonce
		// must open no connector tool surface either.
		reachable, authenticated, dispatched = connectorToolCallOutcome(t, connectorSocket, m.MCPNonce)
		if authenticated || dispatched {
			t.Errorf("after the teardown the connector socket authenticated=%v dispatched=%v for the ended session's nonce, want both false",
				authenticated, dispatched)
		}
		if reachable && authenticated {
			t.Error("the ended session's manifest nonce still opens the pod's connector tool surface")
		}
		if got := fwd.connectorCallCount(); got != 1 {
			t.Errorf("forwarded connector calls = %d, want 1 (the pre-teardown control call only)", got)
		}
	})

	t.Run("co_tenanted_pod", func(t *testing.T) {
		fwd := &recordingForwarder{}
		s, m := teardownPod(t, fwd, "sess-alice", "sess-bob")
		connectorSocket := soleConnectorSocket(t, m)
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
		// process both were given to. The refusal covers tools/list as well
		// as tools/call, and the connector surface as well as the platform
		// one: each is gated on the same sole-session test.
		if _, _, dispatched := platformToolCallOutcome(t, m.PlatformMcpServer.Socket, m.MCPNonce); dispatched {
			t.Error("the surviving session's own tools/call was dispatched while its neighbour's code may still be resident")
		}
		connReachable, _, connDispatched := connectorToolCallOutcome(t, connectorSocket, m.MCPNonce)
		if !connReachable {
			t.Error("the co-tenant's pod-wide connector endpoint was cancelled by its neighbour's teardown")
		}
		if connDispatched {
			t.Error("the connector tool surface dispatched a call after one of the pod's two sessions ended")
		}
		if _, _, listed := connectorToolListOutcome(t, connectorSocket, m.MCPNonce); listed {
			t.Error("the connector tool surface answered tools/list while the pod's shared process may hold two sessions' code")
		}
		if got := fwd.connectorCallCount(); got != 0 {
			t.Errorf("forwarded connector calls = %d on a pod that has served two sessions, want 0", got)
		}

		revoke(t, s, "sess-bob")
		if reachable, authenticated, _ := platformToolCallOutcome(t, m.PlatformMcpServer.Socket, m.MCPNonce); reachable && authenticated {
			t.Error("the pod-wide platform endpoint still authenticates the pod's manifest nonce after its last session ended")
		}
		if reachable, authenticated, _ := connectorToolCallOutcome(t, connectorSocket, m.MCPNonce); reachable && authenticated {
			t.Error("the pod-wide connector endpoint still authenticates the pod's manifest nonce after its last session ended")
		}
		if got := fwd.platformCallCount(); got != 0 {
			t.Errorf("forwarded platform calls = %d across the whole co-tenanted episode, want 0", got)
		}
		if got := fwd.connectorCallCount(); got != 0 {
			t.Errorf("forwarded connector calls = %d across the whole co-tenanted episode, want 0", got)
		}
	})
}

// soleConnectorSocket returns the socket of the one §9.3 connector the
// recording forwarder resolves for the pod's sessions.
func soleConnectorSocket(t *testing.T, m *adapter.Manifest) string {
	t.Helper()
	if len(m.ConnectorServers) != 1 || m.ConnectorServers[0].Socket == "" {
		t.Fatalf("manifest carries no connector MCP socket: %+v", m.ConnectorServers)
	}
	return m.ConnectorServers[0].Socket
}
