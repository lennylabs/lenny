// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
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
