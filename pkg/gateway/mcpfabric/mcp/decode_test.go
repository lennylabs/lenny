// SPDX-License-Identifier: MIT

package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
)

// callTool posts a tools/call for name to srv and returns the response body.
func callTool(t *testing.T, srv *mcp.Server, name string) []byte {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":{}}}`
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))
	return rr.Body.Bytes()
}

// spec: 15.2.1 (REST/MCP consistency contract) rule 3, 29.3 (Interactive message send) Forwarding rule 2
func TestDecodeToolCallResponseInvertsTheEncoders_spec_15_2_1(t *testing.T) {
	srv := mcp.NewServer()
	srv.RegisterTool(mcp.Tool{Name: "lenny/ok"}, func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
		return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "done"}}}, nil
	})
	srv.RegisterTool(mcp.Tool{Name: "lenny/fail"}, func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
		return mcp.ToolResult{}, mcp.NewToolError("SCOPE_DENIED", "not in scope", map[string]any{"scope": "tree"})
	})

	res, te, err := mcp.DecodeToolCallResponse(callTool(t, srv, "lenny/ok"))
	if err != nil || te != nil || len(res.Content) != 1 || res.Content[0].Text != "done" {
		t.Errorf("result: %+v, %v, %v; want the tool's result", res, te, err)
	}
	_, te, err = mcp.DecodeToolCallResponse(callTool(t, srv, "lenny/fail"))
	if err != nil || te == nil || te.Code != "SCOPE_DENIED" || te.Msg != "not in scope" || te.Details["scope"] != "tree" {
		t.Errorf("tool error: %+v, %v; want SCOPE_DENIED with its message and details", te, err)
	}
	_, te, err = mcp.DecodeToolCallResponse(callTool(t, srv, "lenny/absent"))
	if err != nil || te == nil || te.Code != "RESOURCE_NOT_FOUND" {
		t.Errorf("JSON-RPC error: %+v, %v; want RESOURCE_NOT_FOUND", te, err)
	}
}

// spec: 15.2.1 (REST/MCP consistency contract) rule 3
func TestDecodeToolCallResponseRejectsOtherBodies_spec_15_2_1(t *testing.T) {
	for name, body := range map[string]string{
		"not json":                  "not json-rpc",
		"wrong version":             `{"jsonrpc":"1.0","result":{"content":[]}}`,
		"no result and no error":    `{"jsonrpc":"2.0","id":1}`,
		"error without an envelope": `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"x"}}`,
		"isError without a block":   `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"x"}],"isError":true}}`,
		"bad lenny/error block":     `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"lenny/error","text":"{}"}],"isError":true}}`,
		"result not an object":      `{"jsonrpc":"2.0","id":1,"result":"x"}`,
	} {
		if _, _, err := mcp.DecodeToolCallResponse([]byte(body)); err == nil {
			t.Errorf("%s: decoded without error", name)
		}
	}
}
