//go:build contract

// SPDX-License-Identifier: MIT

package adapter_jsonl_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// capturedNonce stands in for the manifest's mcpNonce (a random
// 256-bit hex string per §15.4.3, "Nonce wire format (v1 —
// intra-pod only)"). Its value is arbitrary for this test; only its
// placement on the wire matters.
const capturedNonce = "3f7a9c1e5b6d8f0a2c4e6b8d0f1a3c5e7b9d0f2a4c6e8b0d1f3a5c7e9b0d1f2a"

// dialAdapterMCPServer starts a fresh adapter-local MCP server on an
// in-memory listener and returns a connection to it plus a cleanup
// func. §15.4.3 states the transport is an abstract Unix socket in
// production, but "the listener type does not affect the protocol
// handling" (pkg/adapter/mcp.Server.Serve); an in-memory listener
// exercises the same wire-level JSON-RPC framing this test pins.
func dialAdapterMCPServer(t *testing.T) (net.Conn, func()) {
	t.Helper()
	srv := mcp.NewServer()
	lis := bufconn.Listen(1 << 16)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, lis, capturedNonce)
		close(done)
	}()
	conn, err := lis.Dial()
	if err != nil {
		cancel()
		t.Fatalf("dial adapter MCP server: %v", err)
	}
	cleanup := func() {
		_ = conn.Close()
		cancel()
		<-done
	}
	return conn, cleanup
}

// spec: §15.4.3 (Standard-Level MCP Integration — Authentication,
// "Nonce wire format (v1 — intra-pod only)": "The canonical injection
// location is the top-level `_lennyNonce` field in the MCP
// `initialize` request's `params` object")
// diagnosis: pins the exact on-the-wire field placement of the
// intra-pod MCP nonce against a captured initialize frame. The
// pkg/adapter/mcp unit tests (nonce_test.go, server_test.go) build
// requests by JSON-encoding a Go map, which always places
// mcp.NonceParamKey correctly and would not catch a regression that
// moved the canonical location while every helper kept compiling. This
// test instead drives the real Server over a socket with a literal,
// hand-written JSON-RPC frame, so a wire-format regression fails here
// even if it fails nowhere else.
func TestMCPNonceWirePlacementCanonicalAccepted(t *testing.T) {
	t.Parallel()
	conn, cleanup := dialAdapterMCPServer(t)
	defer cleanup()

	// Captured frame: _lennyNonce as the top-level field of the
	// initialize request's params object.
	frame := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"_lennyNonce":"` +
		capturedNonce + `","protocolVersion":"2025-03-26","capabilities":{}}}`)
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write initialize frame: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var resp map[string]json.RawMessage
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("adapter did not respond to a nonce at params._lennyNonce (canonical location): %v", err)
	}
	if rawErr, isErr := resp["error"]; isErr {
		t.Fatalf("initialize with the nonce at the canonical location was rejected: %s", rawErr)
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if result.ProtocolVersion != mcp.ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, mcp.ProtocolVersion)
	}
}

// spec: §15.4.3 ("The canonical injection location is the top-level
// `_lennyNonce` field in the MCP `initialize` request's `params`
// object" and "The adapter rejects — with an immediate close — any
// MCP connection that does not present a valid nonce before
// dispatching tools")
// diagnosis: a captured frame carrying _lennyNonce as a sibling of
// "params" (JSON-RPC top level) rather than inside it must be
// rejected with an immediate close, not merely ignored while some
// other field happens to satisfy the handshake. If a future change
// widened nonce lookup to scan the whole envelope instead of only
// params, this frame would start being wrongly accepted and this test
// would catch it.
func TestMCPNonceWirePlacementSiblingLocationRejected(t *testing.T) {
	t.Parallel()
	conn, cleanup := dialAdapterMCPServer(t)
	defer cleanup()

	// Captured frame: _lennyNonce placed as a sibling of "params" at
	// the top level of the JSON-RPC envelope instead of inside params.
	frame := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","_lennyNonce":"` +
		capturedNonce + `","params":{"protocolVersion":"2025-03-26","capabilities":{}}}`)
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write initialize frame: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var resp map[string]json.RawMessage
	if err := json.NewDecoder(conn).Decode(&resp); err == nil {
		t.Fatalf("adapter accepted a nonce placed at the JSON-RPC top level instead of params._lennyNonce; want an immediate close. Response: %v", resp)
	}
}
