// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §4.7 / §15.4.3 intra-pod MCP manifest-nonce handshake, driven
// against the real adapter.Server serving live platform and connector
// MCP sockets. The pkg/adapter/mcp unit tests cover AuthenticateInitialize
// in isolation; the pkg/adapter component tests confirm a valid nonce
// completes the handshake and a wrong nonce is closed. This suite closes
// the security gap those tests do not reach: that a co-located adversary
// process which observes an intra-pod MCP socket but never read the
// manifest (so it holds no nonce) cannot reach a privileged platform tool
// (lenny/delegate_task, lenny/memory_write) or any connector tool. The
// adapter is wired to a forwarder that records every gateway-bound tool
// dispatch, so each adversarial connection asserts not only that the
// socket closes but that no privileged call was ever forwarded — the exact
// property §4.7 states the nonce exists to guarantee. A positive control
// confirms that a nonce-bearing client does reach the privileged tool, so
// the boundary under test is the nonce and nothing else.
//
// This runs in-process rather than on the Kind e2e cluster because the
// nonce rejection is transport-independent protocol logic: the adapter
// closes a nonce-less connection before dispatching a tool regardless of
// whether the socket is an abstract Unix socket in a pod netns or a
// filesystem socket. The pod netns / SO_PEERCRED reachability boundary is
// a separate control exercised by adapter_peercred_test.go.
//
// spec: §4.7, §15.4.3.
package tier9_security_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// privilegedPlatformTool is one of the §4.7 privileged platform tools the
// nonce is meant to keep out of a nonce-less child process's reach. The
// spec names lenny/delegate_task, lenny/memory_write, and
// lenny/request_elicitation as examples; the adversary attempts the first.
const privilegedPlatformTool = "lenny/delegate_task"

// nonceProbeConnector is the id of the single connector the recording
// forwarder resolves for the session, so the adapter opens a connector MCP
// socket the adversary can also probe.
const nonceProbeConnector = "github"

// noopRuntime is a RuntimeProcess that starts and closes cleanly and
// produces no output. StartSession needs a Runtime to claim the pod; this
// suite exercises the MCP sockets, not the runtime, so the runtime does
// nothing. It never writes a set_tracing_context frame, so the adapter
// makes no platform-tool call of its own during the session.
type noopRuntime struct{}

func (noopRuntime) Start(context.Context, string) error           { return nil }
func (noopRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (noopRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (noopRuntime) Close(context.Context, string) error           { return nil }

func (noopRuntime) Output(ctx context.Context, _ string) (<-chan []byte, error) {
	ch := make(chan []byte)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

// recordingForwarder stands in for the gateway link the platform and
// connector MCP servers forward to. It advertises a privileged platform
// tool and one connector, and records every Call it receives so a test can
// assert that no adversarial (nonce-less) connection ever produced a
// dispatch. It satisfies both adapter.PlatformToolForwarder and
// adapter.ConnectorToolForwarder.
type recordingForwarder struct {
	mu             sync.Mutex
	platformCalls  []string
	connectorCalls []string
}

func (f *recordingForwarder) ListPlatformTools(context.Context, string) ([]mcp.Tool, error) {
	return []mcp.Tool{{Name: privilegedPlatformTool, Description: "delegate a subtask"}}, nil
}

func (f *recordingForwarder) CallPlatformTool(_ context.Context, _, toolName string, _ json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	f.platformCalls = append(f.platformCalls, toolName)
	f.mu.Unlock()
	return json.RawMessage(`{"content":[{"type":"text","text":"delegated"}]}`), nil
}

func (f *recordingForwarder) ListSessionConnectors(context.Context, string) ([]mcp.ConnectorRef, error) {
	return []mcp.ConnectorRef{{ID: nonceProbeConnector, DisplayName: "GitHub"}}, nil
}

func (f *recordingForwarder) ListConnectorTools(context.Context, string, string) ([]mcp.Tool, error) {
	return []mcp.Tool{{Name: "list_repos", Description: "list repos"}}, nil
}

func (f *recordingForwarder) CallConnectorTool(_ context.Context, _, connectorID, toolName string, _ json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	f.connectorCalls = append(f.connectorCalls, connectorID+"/"+toolName)
	f.mu.Unlock()
	return json.RawMessage(`{"content":[{"type":"text","text":"forwarded"}]}`), nil
}

func (f *recordingForwarder) platformCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.platformCalls)
}

func (f *recordingForwarder) connectorCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.connectorCalls)
}

// nonceProbeManifest starts a real adapter session with the platform and
// connector MCP servers wired to fwd, and returns the parsed manifest so a
// test can read the platform socket, the connector socket, and the nonce
// the adversary is defined not to hold.
func nonceProbeManifest(t *testing.T, fwd *recordingForwarder) *adapter.Manifest {
	t.Helper()
	s := adapter.New("test")
	s.WorkspaceRoot = t.TempDir()
	s.Runtime = noopRuntime{}
	s.ManifestDir = t.TempDir()
	s.MCPSocket = shortMCPSocket(t)
	s.PlatformForwarder = fwd
	s.ConnectorForwarder = fwd

	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-nonce"},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-nonce"},
		})
	})

	b, err := os.ReadFile(filepath.Join(s.ManifestDir, adapter.ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m adapter.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.PlatformMcpServer == nil || m.PlatformMcpServer.Socket == "" {
		t.Fatalf("manifest carries no platform MCP socket: %+v", m.PlatformMcpServer)
	}
	if len(m.ConnectorServers) != 1 || m.ConnectorServers[0].Socket == "" {
		t.Fatalf("manifest carries no connector MCP socket: %+v", m.ConnectorServers)
	}
	if m.MCPNonce == "" {
		t.Fatal("manifest carries no MCP nonce")
	}
	return &m
}

// shortMCPSocket returns a filesystem Unix socket path under a short temp
// root. The derived per-connector sun_path (@lenny-connector-… on Linux,
// a sibling .sock in test) must fit the platform sun_path limit, and
// t.TempDir() embeds the long test name; a short MkdirTemp root keeps the
// path within the limit on darwin and Linux alike.
func shortMCPSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-nonce-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "m")
}

// spec: 4.7 (intra-pod MCP nonce backstop), 15.4.3 (nonce wire format)
// diagnosis: a failure means a process that never read the manifest — the
// exact compromised-child-process threat model §4.7 names — reached a
// privileged platform tool through the intra-pod MCP socket. Either the
// adapter stopped requiring the manifest nonce on the platform MCP socket,
// or it dispatched a tool before completing the nonce handshake, collapsing
// the §13 defense-in-depth boundary that keeps lenny/delegate_task and
// lenny/memory_write out of an un-nonced connection's reach.
func TestPlatformMCPRejectsNonceLessAdversary_spec_4_7(t *testing.T) {
	fwd := &recordingForwarder{}
	m := nonceProbeManifest(t, fwd)
	socket := m.PlatformMcpServer.Socket

	// Each adversarial connection observes the socket but holds no nonce.
	// The adapter must close it before any tool is dispatched.
	t.Run("missing nonce", func(t *testing.T) {
		assertHandshakeRejected(t, socket, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize",
			"params": map[string]any{"protocolVersion": "2025-03-26"},
		})
	})
	t.Run("wrong nonce", func(t *testing.T) {
		assertHandshakeRejected(t, socket, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize",
			"params": map[string]any{
				"_lennyNonce": "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			},
		})
	})
	// A process that skips the handshake entirely and tries to call the
	// privileged tool directly must be rejected the same way: the nonce
	// gate precedes any tool dispatch (§15.4.3 "before processing any tool
	// dispatch").
	t.Run("direct privileged call without initialize", func(t *testing.T) {
		assertHandshakeRejected(t, socket, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": privilegedPlatformTool, "arguments": map[string]any{}},
		})
	})

	// The security property: not one privileged platform tool was ever
	// forwarded to the gateway across every adversarial connection.
	if got := fwd.platformCallCount(); got != 0 {
		t.Errorf("adversarial connections forwarded %d platform tool call(s); §4.7 requires a nonce-less "+
			"connection to reach no privileged tool", got)
	}
}

// spec: 4.7 (per-connector MCP nonce), 15.4.3 (nonce wire format)
// diagnosis: a failure means a nonce-less connection reached a connector
// tool over the intra-pod connector MCP socket. §4.7 requires the same
// manifest-nonce handshake on every intra-pod MCP connection, platform and
// per-connector alike; a rejection gap here lets a compromised child
// process call a session's connector (e.g. a VCS integration) without
// manifest access.
func TestConnectorMCPRejectsNonceLessAdversary_spec_4_7(t *testing.T) {
	fwd := &recordingForwarder{}
	m := nonceProbeManifest(t, fwd)
	socket := m.ConnectorServers[0].Socket

	t.Run("missing nonce", func(t *testing.T) {
		assertHandshakeRejected(t, socket, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize",
			"params": map[string]any{"protocolVersion": "2025-03-26"},
		})
	})
	t.Run("wrong nonce", func(t *testing.T) {
		assertHandshakeRejected(t, socket, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize",
			"params": map[string]any{
				"_lennyNonce": "00000000000000000000000000000000000000000000000000000000deadbeef",
			},
		})
	})

	if got := fwd.connectorCallCount(); got != 0 {
		t.Errorf("adversarial connections forwarded %d connector tool call(s); §4.7 requires the "+
			"per-connector MCP socket to reject a nonce-less connection", got)
	}
}

// spec: 4.7 (nonce is the boundary), 15.4.3 (params._lennyNonce)
// diagnosis: a failure means a legitimate, nonce-bearing runtime could not
// reach the privileged platform tool. This positive control proves the
// rejection tests above are gated on the nonce and not on some unrelated
// misconfiguration that would reject every connection: with the manifest
// nonce presented, initialize completes and lenny/delegate_task forwards to
// the gateway.
func TestPlatformMCPNonceBearerReachesPrivilegedTool_spec_4_7(t *testing.T) {
	fwd := &recordingForwarder{}
	m := nonceProbeManifest(t, fwd)

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
		t.Fatalf("nonce-bearing tools/call errored: %s", callResp["error"])
	}
	if got := fwd.platformCallCount(); got != 1 {
		t.Errorf("nonce-bearing connection forwarded %d platform call(s), want 1 (the boundary under "+
			"test is the nonce, so a valid nonce must reach the privileged tool)", got)
	}
}

// assertHandshakeRejected sends one adversarial first message to the MCP
// socket and asserts the adapter closes the connection without answering:
// a nonce-less or nonce-invalid handshake receives no protocol reply. The
// read is bounded so a hang is a failure rather than a stall.
func assertHandshakeRejected(t *testing.T, socket string, firstMessage map[string]any) {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial MCP socket %q: %v", socket, err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(firstMessage); err != nil {
		t.Fatalf("send adversarial first message: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var resp map[string]json.RawMessage
	if err := json.NewDecoder(conn).Decode(&resp); err == nil {
		t.Errorf("adapter answered a nonce-less MCP handshake with %v; §4.7 requires the connection to "+
			"be closed without dispatching or responding", resp)
	}
}
