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

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
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

// spec: §15.5 item 6 — every MCP tool descriptor MUST carry an
// `x-lenny-stability` label so a consumer can programmatically discover
// whether the tool is `stable`, `beta`, or `alpha`. An unannotated
// registration falls through to `stable` (covered by the §15.5
// versioning guarantees). F-15.5.10.
func TestToolsListStampsStabilityTier_spec_15_5_2447(t *testing.T) {
	s := mcp.NewServer()
	s.RegisterTool(mcp.Tool{
		Name:        "lenny/create_session",
		Description: "Create a session",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		// Stability left empty so the default kicks in.
	}, func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
		return mcp.ToolResult{}, nil
	})
	s.RegisterTool(mcp.Tool{
		Name:        "lenny/experimental_op",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Stability:   mcp.StabilityAlpha,
	}, func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
		return mcp.ToolResult{}, nil
	})
	resp := rpc(t, s.Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools: got %d, want 2", len(tools))
	}
	got := map[string]string{}
	for _, raw := range tools {
		entry, _ := raw.(map[string]any)
		name, _ := entry["name"].(string)
		stab, _ := entry["x-lenny-stability"].(string)
		got[name] = stab
	}
	if got["lenny/create_session"] != string(mcp.StabilityStable) {
		t.Errorf("lenny/create_session stability: got %q, want %q",
			got["lenny/create_session"], mcp.StabilityStable)
	}
	if got["lenny/experimental_op"] != string(mcp.StabilityAlpha) {
		t.Errorf("lenny/experimental_op stability: got %q, want %q",
			got["lenny/experimental_op"], mcp.StabilityAlpha)
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

// spec: §4.8 line 1053 — the result interceptor hook (the PreToolResult
// seam) receives the call id and tool name and may replace the result.
func TestResultInterceptorModifiesResult(t *testing.T) {
	s := mcp.NewServer()
	s.RegisterTool(mcp.Tool{Name: "echo", InputSchema: json.RawMessage(`{}`)},
		func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "raw"}}}, nil
		})
	var gotID, gotName string
	s.SetResultInterceptor(func(_ context.Context, callID, name string, _ mcp.ToolResult) (mcp.ToolResult, error) {
		gotID, gotName = callID, name
		return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "scrubbed"}}}, nil
	})
	resp := rpc(t, s.Handler(),
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"echo","arguments":{}}}`)
	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	c0, _ := content[0].(map[string]any)
	if c0["text"] != "scrubbed" {
		t.Errorf("result text = %v, want scrubbed", c0["text"])
	}
	if gotID != "7" || gotName != "echo" {
		t.Errorf("hook saw id=%q name=%q, want 7/echo", gotID, gotName)
	}
}

// spec: §4.8 line 1053 — a result interceptor error rejects delivery; the
// dispatcher surfaces it as an isError result with the interceptor's code.
func TestResultInterceptorRejectIsErrorResult(t *testing.T) {
	s := mcp.NewServer()
	s.RegisterTool(mcp.Tool{Name: "echo", InputSchema: json.RawMessage(`{}`)},
		func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "raw"}}}, nil
		})
	s.SetResultInterceptor(func(context.Context, string, string, mcp.ToolResult) (mcp.ToolResult, error) {
		return mcp.ToolResult{}, mcp.NewToolError("INTERCEPTOR_REJECTED", "blocked", nil)
	})
	resp := rpc(t, s.Handler(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{}}}`)
	if _, hasErr := resp["error"]; hasErr {
		t.Fatalf("a result-interceptor REJECT should be a tool result, not a transport error: %+v", resp)
	}
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("result.isError should be true: %+v", result)
	}
}

// spec: §4.8 line 1053 — a handler that already failed is not passed to the
// result interceptor; the original tool error is delivered unchanged.
func TestResultInterceptorSkippedOnHandlerError(t *testing.T) {
	s := mcp.NewServer()
	s.RegisterTool(mcp.Tool{Name: "boom", InputSchema: json.RawMessage(`{}`)},
		func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
			return mcp.ToolResult{}, errors.New("handler exploded")
		})
	called := false
	s.SetResultInterceptor(func(context.Context, string, string, mcp.ToolResult) (mcp.ToolResult, error) {
		called = true
		return mcp.ToolResult{}, nil
	})
	rpc(t, s.Handler(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"boom","arguments":{}}}`)
	if called {
		t.Error("result interceptor ran on a handler error path; it must only see successful results")
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

// TestTransportErrorsCarryLennyEnvelope asserts that every JSON-RPC
// transport-error path now populates error.data with the §15.2.1 lenny
// envelope (code, category, retryable) so a client switches on the same
// fields it reads from a REST error body or an MCP tool error. Before
// F-15.2.6 these paths carried only {code, message}. spec: §15.2.1 rule
// 3 line 1384. F-15.2.6.
func TestTransportErrorsCarryLennyEnvelope(t *testing.T) {
	s := mcp.NewServer()
	s.RegisterTool(mcp.Tool{Name: "echo", InputSchema: json.RawMessage(`{}`)},
		func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
			return mcp.ToolResult{}, nil
		})
	cases := []struct {
		name      string
		body      string
		wantCode  string
		wantCat   string
		wantRetry bool
	}{
		{"parse", `not json`, "VALIDATION_ERROR", "PERMANENT", false},
		{"bad_jsonrpc", `{"jsonrpc":"1.0","id":1,"method":"initialize"}`, "VALIDATION_ERROR", "PERMANENT", false},
		{"unknown_method", `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, "RESOURCE_NOT_FOUND", "PERMANENT", false},
		{"invalid_params", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":42}`, "VALIDATION_ERROR", "PERMANENT", false},
		{"unknown_tool", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"missing","arguments":{}}}`, "RESOURCE_NOT_FOUND", "PERMANENT", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := rpc(t, s.Handler(), c.body)
			errObj, ok := resp["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected transport error, got %+v", resp)
			}
			data, ok := errObj["data"].(map[string]any)
			if !ok {
				t.Fatalf("error.data is not the lenny envelope: %+v", errObj)
			}
			if data["code"] != c.wantCode {
				t.Errorf("code = %v, want %s", data["code"], c.wantCode)
			}
			if data["category"] != c.wantCat {
				t.Errorf("category = %v, want %s", data["category"], c.wantCat)
			}
			if data["retryable"] != c.wantRetry {
				t.Errorf("retryable = %v, want %v", data["retryable"], c.wantRetry)
			}
		})
	}
}

func TestPing(t *testing.T) {
	s := mcp.NewServer()
	resp := rpc(t, s.Handler(), `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if _, ok := resp["result"]; !ok {
		t.Errorf("ping should return a result: %+v", resp)
	}
}
