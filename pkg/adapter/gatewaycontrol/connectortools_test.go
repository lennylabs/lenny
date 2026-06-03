// SPDX-License-Identifier: MIT

package gatewaycontrol_test

import (
	"context"
	"encoding/json"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §9.3 line 142 — the client maps the GatewayControl
// ListSessionConnectors response into mcp.ConnectorRef descriptors the
// adapter renders into the manifest connectorServers array. F-9.1.2.
func TestClientListSessionConnectors_spec_9_3_142(t *testing.T) {
	stub := &stubGatewayControl{connListResp: &adapterv1.ListSessionConnectorsResponse{
		Connectors: []*adapterv1.SessionConnector{
			{Id: "github", DisplayName: "GitHub"},
			{Id: "slack"},
		},
	}}
	client := dialStub(t, stub)

	refs, err := client.ListSessionConnectors(context.Background(), "sess_1")
	if err != nil {
		t.Fatalf("ListSessionConnectors: %v", err)
	}
	if stub.gotConnListReq.GetSessionId().GetValue() != "sess_1" {
		t.Errorf("server got session %q", stub.gotConnListReq.GetSessionId().GetValue())
	}
	if len(refs) != 2 || refs[0].ID != "github" || refs[0].DisplayName != "GitHub" {
		t.Errorf("refs = %+v, want the mapped connector list", refs)
	}
	if refs[1].ID != "slack" {
		t.Errorf("refs[1] = %+v", refs[1])
	}
}

// spec: §9.3 lines 142-164 — the client maps the connector tool catalog
// into mcp.Tool descriptors. F-9.1.2.
func TestClientListConnectorTools_spec_9_3_142(t *testing.T) {
	stub := &stubGatewayControl{connToolsResp: &adapterv1.ListConnectorToolsResponse{
		Tools: []*adapterv1.PlatformTool{
			{Name: "list_repos", Description: "list", InputSchema: []byte(`{"type":"object"}`)},
		},
	}}
	client := dialStub(t, stub)

	tools, err := client.ListConnectorTools(context.Background(), "sess_1", "github")
	if err != nil {
		t.Fatalf("ListConnectorTools: %v", err)
	}
	if stub.gotConnToolsReq.GetConnectorId() != "github" {
		t.Errorf("server got connector %q", stub.gotConnToolsReq.GetConnectorId())
	}
	if len(tools) != 1 || tools[0].Name != "list_repos" || string(tools[0].InputSchema) != `{"type":"object"}` {
		t.Errorf("tools = %+v, want the mapped catalog", tools)
	}
}

// spec: §9.3 lines 142-164 — the client forwards the session, connector,
// tool, and arguments and returns the gateway's MCP result verbatim.
func TestClientCallConnectorTool_spec_9_3_142(t *testing.T) {
	stub := &stubGatewayControl{connCallResp: &adapterv1.CallConnectorToolResponse{
		Result:  []byte(`{"content":[{"type":"text","text":"ok"}]}`),
		IsError: false,
	}}
	client := dialStub(t, stub)

	result, err := client.CallConnectorTool(context.Background(), "sess_1", "github", "list_repos", json.RawMessage(`{"owner":"acme"}`))
	if err != nil {
		t.Fatalf("CallConnectorTool: %v", err)
	}
	got := stub.gotConnCallReq
	if got.GetSessionId().GetValue() != "sess_1" || got.GetConnectorId() != "github" || got.GetToolName() != "list_repos" {
		t.Errorf("server got (%q, %q, %q)", got.GetSessionId().GetValue(), got.GetConnectorId(), got.GetToolName())
	}
	if string(got.GetArguments()) != `{"owner":"acme"}` {
		t.Errorf("server got args %q", got.GetArguments())
	}
	if string(result) != `{"content":[{"type":"text","text":"ok"}]}` {
		t.Errorf("result = %s, want the gateway result verbatim", result)
	}
}

// A gRPC error from the gateway surfaces as an error from each connector
// RPC rather than a silent empty result.
func TestClientConnectorTransportErrors_spec_9_3_142(t *testing.T) {
	stub := &stubGatewayControl{
		connListErr:  context.Canceled,
		connToolsErr: context.Canceled,
		connCallErr:  context.Canceled,
	}
	client := dialStub(t, stub)
	if _, err := client.ListSessionConnectors(context.Background(), "s"); err == nil {
		t.Error("ListSessionConnectors should return the gateway error")
	}
	if _, err := client.ListConnectorTools(context.Background(), "s", "github"); err == nil {
		t.Error("ListConnectorTools should return the gateway error")
	}
	if _, err := client.CallConnectorTool(context.Background(), "s", "github", "t", nil); err == nil {
		t.Error("CallConnectorTool should return the gateway error")
	}
}
