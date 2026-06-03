// SPDX-License-Identifier: MIT

package gatewaycontrol_test

import (
	"context"
	"encoding/json"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §9.1 lines 14-31 — the client maps the GatewayControl
// ListPlatformTools response into mcp.Tool descriptors the intra-pod
// platform MCP server advertises. F-9.1.1.
func TestClientListPlatformTools_spec_9_1(t *testing.T) {
	stub := &stubGatewayControl{listResp: &adapterv1.ListPlatformToolsResponse{
		Tools: []*adapterv1.PlatformTool{
			{Name: "lenny/delegate_task", Description: "delegate", InputSchema: []byte(`{"type":"object"}`)},
			{Name: "lenny/await_children"},
		},
	}}
	client := dialStub(t, stub)

	tools, err := client.ListPlatformTools(context.Background(), "sess_1")
	if err != nil {
		t.Fatalf("ListPlatformTools: %v", err)
	}
	if stub.gotListReq.GetSessionId().GetValue() != "sess_1" {
		t.Errorf("server got session %q", stub.gotListReq.GetSessionId().GetValue())
	}
	if len(tools) != 2 || tools[0].Name != "lenny/delegate_task" || string(tools[0].InputSchema) != `{"type":"object"}` {
		t.Errorf("tools = %+v, want the mapped catalog", tools)
	}
	if tools[1].Name != "lenny/await_children" {
		t.Errorf("tools[1] = %+v", tools[1])
	}
}

// spec: §9.1 line 14 — the client forwards the session, tool name, and
// arguments and returns the gateway's MCP result bytes verbatim.
func TestClientCallPlatformTool_spec_9_1(t *testing.T) {
	stub := &stubGatewayControl{callResp: &adapterv1.CallPlatformToolResponse{
		Result:  []byte(`{"content":[{"type":"text","text":"ok"}]}`),
		IsError: false,
	}}
	client := dialStub(t, stub)

	result, err := client.CallPlatformTool(context.Background(), "sess_1", "lenny/delegate_task", json.RawMessage(`{"runtime":"x"}`))
	if err != nil {
		t.Fatalf("CallPlatformTool: %v", err)
	}
	if stub.gotCallReq.GetSessionId().GetValue() != "sess_1" || stub.gotCallReq.GetToolName() != "lenny/delegate_task" {
		t.Errorf("server got (%q, %q)", stub.gotCallReq.GetSessionId().GetValue(), stub.gotCallReq.GetToolName())
	}
	if string(stub.gotCallReq.GetArguments()) != `{"runtime":"x"}` {
		t.Errorf("server got args %q", stub.gotCallReq.GetArguments())
	}
	if string(result) != `{"content":[{"type":"text","text":"ok"}]}` {
		t.Errorf("result = %s, want the gateway result verbatim", result)
	}
}

func TestClientCallPlatformToolError_spec_9_1(t *testing.T) {
	stub := &stubGatewayControl{callErr: context.Canceled}
	client := dialStub(t, stub)
	if _, err := client.CallPlatformTool(context.Background(), "s", "lenny/output", nil); err == nil {
		t.Fatal("expected error from a failing CallPlatformTool")
	}
}
