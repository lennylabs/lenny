// SPDX-License-Identifier: MIT

package mcp_test

import (
	"encoding/json"
	"net"
	"testing"

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
