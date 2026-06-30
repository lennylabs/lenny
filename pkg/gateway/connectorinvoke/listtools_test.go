// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/capabilityinference"
)

// toolsListResponses is the three-response transcript a tools/list
// expects: initialize, the initialized notification 202, then the list.
func toolsListResponses(toolsJSON string) []*http.Response {
	return []*http.Response{
		jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
		jsonResp(202, ``, nil),
		jsonResp(200, `{"jsonrpc":"2.0","id":2,"result":{"tools":`+toolsJSON+`}}`, nil),
	}
}

// spec: §9.3 lines 142-164 — ListTools dials the connector as the MCP
// client and returns its tools/list catalog for the intra-pod
// per-connector MCP server to advertise. F-9.1.2.
func TestInvokerListToolsReturnsCatalog_spec_9_3_142(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
	})
	doer := &fakeDoer{responses: toolsListResponses(
		`[{"name":"list_repos","description":"list","inputSchema":{"type":"object"}}]`,
	)}
	iv := NewInvoker(connectors, nil, New(doer), nil, nil)

	tools, err := iv.ListTools(context.Background(), "acme", "sess-1", "github", "alice", "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "list_repos" {
		t.Fatalf("tools = %+v, want the connector catalog", tools)
	}
}

// spec: §9.3 line 164 — ListTools enforces the connector-access boundary
// before any outbound dial; a denied connector is never reached. F-9.1.2.
func TestInvokerListToolsPolicyDenied_spec_9_3_164(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
	})
	doer := &fakeDoer{responses: toolsListResponses(`[]`)}
	authz := &fakeAuthz{err: errors.New("denied by policy")}
	iv := NewInvoker(connectors, nil, New(doer), nil, authz)

	if _, err := iv.ListTools(context.Background(), "acme", "sess-1", "github", "alice", ""); err == nil {
		t.Fatal("ListTools should reject a policy-denied connector")
	}
	if len(doer.reqs) != 0 {
		t.Errorf("a denied connector was dialled %d times, want 0", len(doer.reqs))
	}
}

// spec: §9.3 — a soft-deleted connector is not dialable. F-9.1.2.
func TestInvokerListToolsInactiveConnector_spec_9_3_142(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
	})
	if err := connectors.SoftDelete(context.Background(), "acme", "github", clock()); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	iv := NewInvoker(connectors, nil, New(&fakeDoer{}), nil, nil)
	if _, err := iv.ListTools(context.Background(), "acme", "sess-1", "github", "alice", ""); !errors.Is(err, ErrConnectorInactive) {
		t.Errorf("err = %v, want ErrConnectorInactive", err)
	}
}

// spec: §10.6 line 607 — ListTools drops the tools the environment
// connectorSelector capability filter denies so the intra-pod tools/list
// advertises only callable tools. F-9.1.2.
func TestInvokerListToolsFiltersByCapability_spec_10_6_607(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
		Labels: map[string]string{"team": "security"},
		ToolCapabilities: map[string][]capabilityinference.Capability{
			"search_repos": {capabilityinference.CapRead},
			"delete_repo":  {capabilityinference.CapWrite, capabilityinference.CapDelete},
		},
	})
	envs := environmentstore.NewMemory()
	seedEnvironment(t, envs, securityEnv())
	doer := &fakeDoer{responses: toolsListResponses(
		`[{"name":"search_repos"},{"name":"delete_repo"}]`,
	)}
	iv := NewInvoker(connectors, nil, New(doer), nil, nil).WithEnvironments(envs)

	tools, err := iv.ListTools(context.Background(), "acme", "sess-1", "github", "alice", "security-team")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "search_repos" {
		t.Errorf("tools = %+v, want only the read/search tool admitted", tools)
	}
}
