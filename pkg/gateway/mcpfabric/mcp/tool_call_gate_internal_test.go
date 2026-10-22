// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gatedServer registers one tool that records whether it ran, behind a
// gate that refuses every call.
func gatedServer(ran *bool) *Server {
	s := NewServer()
	s.RegisterTool(Tool{Name: "probe"}, func(context.Context, json.RawMessage) (ToolResult, error) {
		*ran = true
		return ToolResult{Content: []ToolContent{{Type: "text", Text: "ran"}}}, nil
	})
	s.SetToolCallGate(func(context.Context) *ToolError {
		return NewToolError("UNAUTHORIZED", "tool call requires an authenticated principal", nil)
	})
	return s
}

// TestToolCallGateRefusesOnEveryTransport asserts that the pre-dispatch
// gate refuses a tools/call with its error before the handler runs on the
// POST /mcp transport, the WebSocket frame dispatcher, and DispatchTool.
//
// spec: §10.2 (Authentication), §15.2.1 (REST/MCP consistency).
func TestToolCallGateRefusesOnEveryTransport(t *testing.T) {
	frame := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"probe","arguments":{}}}`

	ran := false
	s := gatedServer(&ran)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(frame)))
	if ran || !strings.Contains(rr.Body.String(), "UNAUTHORIZED") {
		t.Fatalf("POST /mcp: handler ran %t, body %s; want UNAUTHORIZED without running", ran, rr.Body.String())
	}

	out, _ := s.dispatchFrameBytes(context.Background(), []byte(frame))
	if ran || !strings.Contains(string(out), "UNAUTHORIZED") {
		t.Fatalf("WebSocket frame: handler ran %t, frame %s; want UNAUTHORIZED without running", ran, out)
	}

	res, ok, err := s.DispatchTool(context.Background(), "probe", json.RawMessage(`{}`))
	if ran || !ok || err != nil || !res.IsError {
		t.Fatalf("DispatchTool: handler ran %t ok %t err %v isError %t; want an isError result without running", ran, ok, err, res.IsError)
	}

	s.SetToolCallGate(nil)
	if _, _, err := s.DispatchTool(context.Background(), "probe", json.RawMessage(`{}`)); err != nil || !ran {
		t.Fatalf("no gate: err %v ran %t; want the handler to run", err, ran)
	}
}
