// SPDX-License-Identifier: MIT

package mcp_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// serverPipe runs s.ServeConn over an in-memory connection and returns
// the client end's JSON encoder and decoder.
func serverPipe(t *testing.T, s *mcp.Server, nonce string) (*json.Encoder, *json.Decoder) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		_ = s.ServeConn(server, nonce)
		close(done)
	}()
	t.Cleanup(func() {
		_ = client.Close()
		<-done
	})
	return json.NewEncoder(client), json.NewDecoder(client)
}

func sendRequest(t *testing.T, enc *json.Encoder, id int, method string, params any) {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if err := enc.Encode(req); err != nil {
		t.Fatalf("send %s: %v", method, err)
	}
}

func readResponse(t *testing.T, dec *json.Decoder) map[string]json.RawMessage {
	t.Helper()
	var resp map[string]json.RawMessage
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp
}

func initParams(nonce string) map[string]any {
	return map[string]any{
		mcp.NonceParamKey: nonce,
		"protocolVersion": mcp.ProtocolVersion,
		"capabilities":    map[string]any{},
	}
}

func TestServerInitializeHandshake(t *testing.T) {
	enc, dec := serverPipe(t, mcp.NewServer(), testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	resp := readResponse(t, dec)
	if _, isErr := resp["error"]; isErr {
		t.Fatalf("initialize returned an error: %s", resp["error"])
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if result.ProtocolVersion != mcp.ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, mcp.ProtocolVersion)
	}
	if result.ServerInfo.Name != mcp.ServerName {
		t.Errorf("serverInfo.name = %q, want %q", result.ServerInfo.Name, mcp.ServerName)
	}
}

func TestServerRejectsBadNonce(t *testing.T) {
	enc, dec := serverPipe(t, mcp.NewServer(), testNonce)
	sendRequest(t, enc, 1, "initialize", initParams("the-wrong-nonce"))
	var resp map[string]json.RawMessage
	if err := dec.Decode(&resp); err == nil {
		t.Error("server responded to an initialize with a bad nonce; want an immediate close")
	}
}

// spec: §5.1 — OnHandshake fires once after a successful nonce-authenticated
// initialize so the adapter can observe that the runtime reached the
// platform MCP server (Standard level). F-5.1.11.
func TestServerOnHandshakeFiresAfterInitialize_spec_5_1(t *testing.T) {
	s := mcp.NewServer()
	fired := make(chan struct{}, 1)
	s.OnHandshake = func() { fired <- struct{}{} }
	enc, dec := serverPipe(t, s, testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	readResponse(t, dec)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("OnHandshake did not fire after a successful initialize")
	}
}

// spec: §5.1 — OnHandshake does not fire when the nonce handshake fails,
// so a process that never authenticated is not counted as Standard.
func TestServerOnHandshakeNotFiredOnBadNonce_spec_5_1(t *testing.T) {
	s := mcp.NewServer()
	var fired bool
	s.OnHandshake = func() { fired = true }
	enc, dec := serverPipe(t, s, testNonce)
	sendRequest(t, enc, 1, "initialize", initParams("the-wrong-nonce"))
	var resp map[string]json.RawMessage
	_ = dec.Decode(&resp) // expected to error/close
	if fired {
		t.Error("OnHandshake fired for a connection that failed the nonce handshake")
	}
}

func TestServerToolsList(t *testing.T) {
	s := mcp.NewServer()
	s.Register(mcp.Tool{
		Name: "lenny/output", Description: "emit an output part",
		Handler: func(json.RawMessage) (any, error) { return nil, nil },
	})
	enc, dec := serverPipe(t, s, testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	readResponse(t, dec)

	sendRequest(t, enc, 2, "tools/list", nil)
	resp := readResponse(t, dec)
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "lenny/output" {
		t.Errorf("tools/list = %+v, want [lenny/output]", result.Tools)
	}
}

func TestServerToolsCall(t *testing.T) {
	s := mcp.NewServer()
	var gotArgs string
	s.Register(mcp.Tool{Name: "echo", Handler: func(args json.RawMessage) (any, error) {
		gotArgs = string(args)
		return map[string]any{"echoed": true}, nil
	}})
	enc, dec := serverPipe(t, s, testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	readResponse(t, dec)

	sendRequest(t, enc, 2, "tools/call", map[string]any{
		"name": "echo", "arguments": map[string]any{"k": "v"},
	})
	resp := readResponse(t, dec)
	if _, isErr := resp["error"]; isErr {
		t.Fatalf("tools/call returned an error: %s", resp["error"])
	}
	var result struct {
		Echoed bool `json:"echoed"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil || !result.Echoed {
		t.Errorf("tools/call result = %s (err %v), want echoed", resp["result"], err)
	}
	if gotArgs != `{"k":"v"}` {
		t.Errorf("handler received arguments %q, want {\"k\":\"v\"}", gotArgs)
	}
}

func TestServerCallUnknownTool(t *testing.T) {
	enc, dec := serverPipe(t, mcp.NewServer(), testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	readResponse(t, dec)
	sendRequest(t, enc, 2, "tools/call", map[string]any{"name": "nope"})
	resp := readResponse(t, dec)
	if _, isErr := resp["error"]; !isErr {
		t.Error("tools/call for an unknown tool did not return an error")
	}
}

func TestServerUnknownMethod(t *testing.T) {
	enc, dec := serverPipe(t, mcp.NewServer(), testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	readResponse(t, dec)
	sendRequest(t, enc, 2, "frobnicate", nil)
	resp := readResponse(t, dec)
	if _, isErr := resp["error"]; !isErr {
		t.Error("an unknown method did not return an error")
	}
}

// serveOnBufconn runs s.Serve on an in-memory listener and returns the
// listener so tests can dial it.
func serveOnBufconn(t *testing.T, s *mcp.Server, nonce string) *bufconn.Listener {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = s.Serve(ctx, lis, nonce)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return lis
}

func TestServerServeAcceptsConnections(t *testing.T) {
	s := mcp.NewServer()
	s.Register(mcp.Tool{
		Name: "lenny/output", Handler: func(json.RawMessage) (any, error) { return nil, nil },
	})
	lis := serveOnBufconn(t, s, testNonce)

	conn, err := lis.Dial()
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)

	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	if resp := readResponse(t, dec); resp["result"] == nil {
		t.Fatalf("initialize over Serve returned no result: %v", resp)
	}
	sendRequest(t, enc, 2, "tools/list", nil)
	if resp := readResponse(t, dec); resp["result"] == nil {
		t.Errorf("tools/list over Serve returned no result: %v", resp)
	}
}

func TestServerServeHandlesMultipleConnections(t *testing.T) {
	lis := serveOnBufconn(t, mcp.NewServer(), testNonce)
	for i := 0; i < 3; i++ {
		conn, err := lis.Dial()
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
		sendRequest(t, enc, 1, "initialize", initParams(testNonce))
		if resp := readResponse(t, dec); resp["result"] == nil {
			t.Errorf("connection %d: initialize returned no result", i)
		}
		_ = conn.Close()
	}
}

func TestServerServeStopsOnContextCancel(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mcp.NewServer().Serve(ctx, lis, testNonce) }()

	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Error("Serve returned nil after context cancel, want a cancellation error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}
}

func TestServerNotificationGetsNoResponse(t *testing.T) {
	enc, dec := serverPipe(t, mcp.NewServer(), testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	readResponse(t, dec)
	// A JSON-RPC notification has no id and draws no response. If the
	// server wrongly replied, that reply would be read below in place
	// of the tools/list response.
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	}); err != nil {
		t.Fatalf("send notification: %v", err)
	}
	sendRequest(t, enc, 2, "tools/list", nil)
	resp := readResponse(t, dec)
	var id int
	if err := json.Unmarshal(resp["id"], &id); err != nil || id != 2 {
		t.Errorf("response id = %d (err %v), want 2 — the notification may have drawn a reply", id, err)
	}
}
