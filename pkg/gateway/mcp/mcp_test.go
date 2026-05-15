// SPDX-License-Identifier: MIT

package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcp"
)

func rpc(t *testing.T, h http.Handler, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rr.Body.String())
	}
	return resp
}

func TestInitialize(t *testing.T) {
	s := mcp.NewServer()
	resp := rpc(t, s.Handler(), `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %+v", resp)
	}
	if result["protocolVersion"] != mcp.ProtocolVersion {
		t.Errorf("protocolVersion: %v", result["protocolVersion"])
	}
	si, _ := result["serverInfo"].(map[string]any)
	if si["name"] != "lenny-gateway" {
		t.Errorf("serverInfo.name: %v", si["name"])
	}
}

func TestToolsList(t *testing.T) {
	s := mcp.NewServer()
	s.RegisterTool(mcp.Tool{
		Name:        "lenny/create_session",
		Description: "Create a session",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
		return mcp.ToolResult{}, nil
	})
	resp := rpc(t, s.Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(tools))
	}
	first, _ := tools[0].(map[string]any)
	if first["name"] != "lenny/create_session" {
		t.Errorf("tool name: %v", first["name"])
	}
}

func TestToolsCallDispatches(t *testing.T) {
	s := mcp.NewServer()
	s.RegisterTool(mcp.Tool{Name: "echo", InputSchema: json.RawMessage(`{}`)},
		func(_ context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			var a struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(args, &a)
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "got:" + a.Text}}}, nil
		})
	resp := rpc(t, s.Handler(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)
	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content: %+v", result)
	}
	c0, _ := content[0].(map[string]any)
	if c0["text"] != "got:hi" {
		t.Errorf("tool output: %v", c0["text"])
	}
}

func TestToolsCallUnknownTool(t *testing.T) {
	s := mcp.NewServer()
	resp := rpc(t, s.Handler(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"missing","arguments":{}}}`)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error, got %+v", resp)
	}
	if int(errObj["code"].(float64)) != -32601 {
		t.Errorf("error code: %v", errObj["code"])
	}
}

func TestToolsCallToolErrorIsResultNotTransportError(t *testing.T) {
	s := mcp.NewServer()
	s.RegisterTool(mcp.Tool{Name: "boom", InputSchema: json.RawMessage(`{}`)},
		func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
			return mcp.ToolResult{}, errors.New("tool failed internally")
		})
	resp := rpc(t, s.Handler(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"boom","arguments":{}}}`)
	// Tool failure → result with isError=true, NOT a JSON-RPC error.
	if _, hasErr := resp["error"]; hasErr {
		t.Fatalf("tool failure should be a result, not a transport error: %+v", resp)
	}
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("result.isError should be true: %+v", result)
	}
}

func TestRejectsBadJSONRPC(t *testing.T) {
	s := mcp.NewServer()
	resp := rpc(t, s.Handler(), `{"jsonrpc":"1.0","id":1,"method":"initialize"}`)
	if _, ok := resp["error"]; !ok {
		t.Errorf("jsonrpc!=2.0 should error: %+v", resp)
	}
}

func TestRejectsMalformedJSON(t *testing.T) {
	s := mcp.NewServer()
	resp := rpc(t, s.Handler(), `not json`)
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil || int(errObj["code"].(float64)) != -32700 {
		t.Errorf("malformed JSON should be parse error: %+v", resp)
	}
}

func TestUnknownMethod(t *testing.T) {
	s := mcp.NewServer()
	resp := rpc(t, s.Handler(), `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil || int(errObj["code"].(float64)) != -32601 {
		t.Errorf("unknown method should be -32601: %+v", resp)
	}
}

func TestPing(t *testing.T) {
	s := mcp.NewServer()
	resp := rpc(t, s.Handler(), `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if _, ok := resp["result"]; !ok {
		t.Errorf("ping should return a result: %+v", resp)
	}
}
