// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// DispatchTool and Catalog are the non-HTTP entry points the §9.1
// intra-pod platform MCP server reaches over GatewayControl. F-9.1.1.

func TestDispatchToolSuccess_spec_9_1(t *testing.T) {
	s := NewServer()
	var gotArgs string
	s.RegisterTool(Tool{Name: "lenny/output"}, func(_ context.Context, args json.RawMessage) (ToolResult, error) {
		gotArgs = string(args)
		return ToolResult{Content: []ToolContent{{Type: "text", Text: "done"}}}, nil
	})
	res, ok, err := s.DispatchTool(context.Background(), "lenny/output", json.RawMessage(`{"part":"x"}`))
	if err != nil || !ok {
		t.Fatalf("DispatchTool = (ok=%v, err=%v), want (true, nil)", ok, err)
	}
	if res.IsError {
		t.Errorf("result IsError = true, want false")
	}
	if gotArgs != `{"part":"x"}` {
		t.Errorf("handler args = %q", gotArgs)
	}
}

func TestDispatchToolUnknownToolReturnsNotOk_spec_9_1(t *testing.T) {
	s := NewServer()
	_, ok, err := s.DispatchTool(context.Background(), "lenny/missing", nil)
	if err != nil {
		t.Fatalf("DispatchTool err = %v, want nil", err)
	}
	if ok {
		t.Errorf("ok = true for an unregistered tool, want false")
	}
}

// A handler failure is carried as an isError ToolResult with the §15.2.1
// lenny error envelope, not as a Go error — the same form the agent
// receives over the gateway-edge /mcp surface.
func TestDispatchToolHandlerErrorIsIsErrorResult_spec_15_2_1(t *testing.T) {
	s := NewServer()
	s.RegisterTool(Tool{Name: "lenny/delegate_task"}, func(context.Context, json.RawMessage) (ToolResult, error) {
		return ToolResult{}, NewToolError("VALIDATION_ERROR", "bad input", map[string]any{"field": "runtime"})
	})
	res, ok, err := s.DispatchTool(context.Background(), "lenny/delegate_task", nil)
	if err != nil || !ok {
		t.Fatalf("DispatchTool = (ok=%v, err=%v), want (true, nil)", ok, err)
	}
	if !res.IsError {
		t.Fatalf("result IsError = false, want true")
	}
	if len(res.Content) != 2 || res.Content[1].Type != LennyErrorContentType {
		t.Fatalf("result content = %+v, want a text block and a lenny/error block", res.Content)
	}
	var env LennyErrorDetail
	if err := json.Unmarshal([]byte(res.Content[1].Text), &env); err != nil {
		t.Fatalf("decode lenny envelope: %v", err)
	}
	if env.Code != "VALIDATION_ERROR" || env.Category != "PERMANENT" || env.Retryable {
		t.Errorf("envelope = %+v, want VALIDATION_ERROR/PERMANENT/false", env)
	}
}

// A PreToolResult interceptor rejection surfaces through the same isError
// path with the INTERCEPTOR_REJECTED code. spec: §4.8 line 1053.
func TestDispatchToolInterceptorRejection_spec_4_8(t *testing.T) {
	s := NewServer()
	s.RegisterTool(Tool{Name: "lenny/output"}, func(context.Context, json.RawMessage) (ToolResult, error) {
		return ToolResult{Content: []ToolContent{{Type: "text", Text: "ok"}}}, nil
	})
	s.SetResultInterceptor(func(context.Context, string, string, ToolResult) (ToolResult, error) {
		return ToolResult{}, NewToolError("INTERCEPTOR_REJECTED", "blocked by policy", nil)
	})
	res, ok, err := s.DispatchTool(context.Background(), "lenny/output", nil)
	if err != nil || !ok {
		t.Fatalf("DispatchTool = (ok=%v, err=%v), want (true, nil)", ok, err)
	}
	if !res.IsError {
		t.Errorf("interceptor rejection did not set IsError")
	}
}

func TestCatalogReturnsRegistrationOrder_spec_9_1(t *testing.T) {
	s := NewServer()
	s.RegisterTool(Tool{Name: "lenny/delegate_task"}, func(context.Context, json.RawMessage) (ToolResult, error) { return ToolResult{}, nil })
	s.RegisterTool(Tool{Name: "lenny/await_children"}, func(context.Context, json.RawMessage) (ToolResult, error) { return ToolResult{}, nil })
	cat := s.Catalog()
	if len(cat) != 2 || cat[0].Name != "lenny/delegate_task" || cat[1].Name != "lenny/await_children" {
		t.Errorf("Catalog = %+v, want registration order", cat)
	}
}
