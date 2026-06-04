// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func TestPlatformMCP(t *testing.T) {
	s, _, _ := sessionServer(t)
	manifestDir := t.TempDir()
	s.ManifestDir = manifestDir
	s.MCPSocket = filepath.Join(t.TempDir(), "m")

	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-1"},
		})
	})

	// The manifest advertises the platform MCP socket.
	b, err := os.ReadFile(filepath.Join(manifestDir, adapter.ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m adapter.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.PlatformMcpServer == nil || m.PlatformMcpServer.Socket != s.MCPSocket {
		t.Fatalf("manifest platformMcpServer = %+v, want socket %q", m.PlatformMcpServer, s.MCPSocket)
	}

	// The platform MCP server is listening: a connection presenting the
	// manifest nonce completes the initialize handshake.
	conn, err := net.Dial("unix", s.MCPSocket)
	if err != nil {
		t.Fatalf("dial platform MCP socket: %v", err)
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"_lennyNonce":     m.MCPNonce,
			"protocolVersion": "2025-03-26",
		},
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	var resp map[string]json.RawMessage
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	if _, isErr := resp["error"]; isErr {
		t.Errorf("platform MCP initialize errored: %s", resp["error"])
	}
	if resp["result"] == nil {
		t.Error("platform MCP initialize returned no result")
	}
}

// spec: §4.7 lines 879-883 — with SO_PEERCRED disabled (NonceOnlyMode),
// the platform MCP server supplements the manifest nonce with a
// per-connection HMAC challenge before completing initialize.
func TestPlatformMCPNonceOnlyChallenge_spec_4_7(t *testing.T) {
	s, _, _ := sessionServer(t)
	s.ManifestDir = t.TempDir()
	// The Unix sun_path is ~104 bytes on darwin; t.TempDir() with a long
	// test name overflows it, so bind the socket under a short temp dir.
	sockDir, err := os.MkdirTemp("", "mcp-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	s.MCPSocket = filepath.Join(sockDir, "m")
	s.NonceOnlyMode = true

	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-1"},
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

	conn, err := net.Dial("unix", s.MCPSocket)
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

	// The server's next frame is the adapterChallenge, not the initialize
	// result.
	var challengeFrame map[string]string
	if err := dec.Decode(&challengeFrame); err != nil {
		t.Fatalf("read challenge frame: %v", err)
	}
	challenge := challengeFrame[mcp.ChallengeParamKey]
	if challenge == "" {
		t.Fatalf("nonce-only server sent %v, want a %s frame", challengeFrame, mcp.ChallengeParamKey)
	}
	if err := enc.Encode(map[string]string{
		mcp.ChallengeResponseParamKey: mcp.ExpectedChallengeResponse(m.MCPNonce, challenge),
	}); err != nil {
		t.Fatalf("send challenge response: %v", err)
	}

	var resp map[string]json.RawMessage
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("read initialize response after challenge: %v", err)
	}
	if resp["result"] == nil {
		t.Errorf("initialize after challenge returned no result: %v", resp)
	}
}

// fakePlatformForwarder records the session id it is forwarded with and
// returns a canned catalog / result. The mutex guards the captured call
// so a test reading it after a concurrent server-goroutine forward (the
// Attach loop's set_tracing_context path) stays race-free. spec: §9.1
// lines 14-31. F-9.1.1.
type fakePlatformForwarder struct {
	list       []mcp.Tool
	result     json.RawMessage
	mu         sync.Mutex
	gotSession string
	gotTool    string
	gotArgs    json.RawMessage
}

func (f *fakePlatformForwarder) ListPlatformTools(_ context.Context, sessionID string) ([]mcp.Tool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotSession = sessionID
	return f.list, nil
}

func (f *fakePlatformForwarder) CallPlatformTool(_ context.Context, sessionID, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotSession = sessionID
	f.gotTool = toolName
	f.gotArgs = append(json.RawMessage(nil), arguments...)
	return f.result, nil
}

// lastCall returns the most recent CallPlatformTool arguments under the
// lock, for assertions in tests that forward from a server goroutine.
func (f *fakePlatformForwarder) lastCall() (session, tool string, args json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotSession, f.gotTool, f.gotArgs
}

// spec: §9.1 lines 8-31 — when a PlatformForwarder is wired, the platform
// MCP server advertises the gateway's platform tool catalog on tools/list
// and forwards a runtime's tools/call to the gateway scoped to this pod's
// session. F-9.1.1.
func TestPlatformMCPForwardsToGateway_spec_9_1(t *testing.T) {
	s, _, _ := sessionServer(t)
	s.ManifestDir = t.TempDir()
	s.MCPSocket = filepath.Join(t.TempDir(), "m")
	fwd := &fakePlatformForwarder{
		list:   []mcp.Tool{{Name: "lenny/delegate_task", Description: "delegate"}},
		result: json.RawMessage(`{"content":[{"type":"text","text":"forwarded"}]}`),
	}
	s.PlatformForwarder = fwd

	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-1"},
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

	conn, err := net.Dial("unix", s.MCPSocket)
	if err != nil {
		t.Fatalf("dial platform MCP socket: %v", err)
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	mustSend := func(id int, method string, params any) {
		if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			t.Fatalf("send %s: %v", method, err)
		}
	}
	mustRead := func() map[string]json.RawMessage {
		var resp map[string]json.RawMessage
		if err := dec.Decode(&resp); err != nil {
			t.Fatalf("read response: %v", err)
		}
		return resp
	}

	mustSend(1, "initialize", map[string]any{"_lennyNonce": m.MCPNonce, "protocolVersion": "2025-03-26"})
	mustRead()

	// tools/list returns the gateway catalog (fetched via the forwarder).
	mustSend(2, "tools/list", nil)
	listResp := mustRead()
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(listResp["result"], &list); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != "lenny/delegate_task" {
		t.Errorf("tools/list = %+v, want the forwarder catalog", list.Tools)
	}
	if fwd.gotSession != "sess-1" {
		t.Errorf("forwarder list session = %q, want sess-1", fwd.gotSession)
	}

	// tools/call forwards to the gateway scoped to this session.
	mustSend(3, "tools/call", map[string]any{"name": "lenny/delegate_task", "arguments": map[string]any{}})
	callResp := mustRead()
	if _, isErr := callResp["error"]; isErr {
		t.Fatalf("tools/call errored: %s", callResp["error"])
	}
	if fwd.gotSession != "sess-1" || fwd.gotTool != "lenny/delegate_task" {
		t.Errorf("forwarder call = (%q, %q), want (sess-1, lenny/delegate_task)", fwd.gotSession, fwd.gotTool)
	}
	if string(callResp["result"]) != `{"content":[{"type":"text","text":"forwarded"}]}` {
		t.Errorf("tools/call result = %s, want the gateway result verbatim", callResp["result"])
	}
}

func TestPlatformMCPRejectsBadNonce(t *testing.T) {
	s, _, _ := sessionServer(t)
	s.ManifestDir = t.TempDir()
	s.MCPSocket = filepath.Join(t.TempDir(), "m")

	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-1"},
		})
	})

	conn, err := net.Dial("unix", s.MCPSocket)
	if err != nil {
		t.Fatalf("dial platform MCP socket: %v", err)
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"_lennyNonce": "the-wrong-nonce"},
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	// The server rejects the bad nonce with an immediate close.
	var resp map[string]json.RawMessage
	if err := dec.Decode(&resp); err == nil {
		t.Error("platform MCP server answered an initialize with a bad nonce")
	}
}
