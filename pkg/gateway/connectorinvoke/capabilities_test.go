// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/capabilityinference"
)

// dialResponses is the three-response transcript a permitted tools/call
// expects: initialize, the initialized notification 202, then the call.
func dialResponses() []*http.Response {
	return []*http.Response{
		jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
		jsonResp(202, ``, nil),
		jsonResp(200, `{"jsonrpc":"2.0","id":2,"result":{"content":[]}}`, nil),
	}
}

func seedEnvironment(t *testing.T, envs *environmentstore.Memory, e environmentstore.Environment) {
	t.Helper()
	if err := envs.Create(context.Background(), e); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
}

// securityEnv is the §10.6 example environment: a connectorSelector that
// admits team:security connectors and denies write/delete/execute/admin.
func securityEnv() environmentstore.Environment {
	return environmentstore.Environment{
		Name:     "security-team",
		TenantID: "acme",
		ConnectorSelector: environmentstore.ConnectorSelector{
			Selector:            environment.Selector{MatchLabels: map[string]string{"team": "security"}},
			AllowedCapabilities: []string{"read", "search"},
			DeniedCapabilities:  []string{"write", "delete", "execute", "admin"},
		},
	}
}

// TestInvokerConnectorCapabilityDenied_spec_10_6_607 verifies the §10.6
// connectorSelector capability filter rejects a tool whose inferred
// capability the environment denies, before any outbound dial. F-10.6.2.
func TestInvokerConnectorCapabilityDenied_spec_10_6_607(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
		Labels: map[string]string{"team": "security"},
		ToolCapabilities: map[string][]capabilityinference.Capability{
			"delete_repo": {capabilityinference.CapWrite, capabilityinference.CapDelete},
		},
	})
	envs := environmentstore.NewMemory()
	seedEnvironment(t, envs, securityEnv())

	doer := &fakeDoer{}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).
		WithEnvironments(envs)

	_, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "security-team", "delete_repo", nil)
	var capErr *CapabilityDeniedError
	if !errors.As(err, &capErr) {
		t.Fatalf("err = %v, want *CapabilityDeniedError", err)
	}
	if capErr.Capability != "write" || capErr.Environment != "security-team" || capErr.Tool != "delete_repo" {
		t.Errorf("denial = %+v, want write/security-team/delete_repo", capErr)
	}
	if len(doer.reqs) != 0 {
		t.Errorf("dialed a capability-denied connector %d times, want 0", len(doer.reqs))
	}
}

// TestInvokerConnectorCapabilityPermitted_spec_10_6_607 verifies a tool
// whose capability the environment permits proceeds to the dial.
func TestInvokerConnectorCapabilityPermitted_spec_10_6_607(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
		Labels: map[string]string{"team": "security"},
		ToolCapabilities: map[string][]capabilityinference.Capability{
			"list_repos": {capabilityinference.CapRead},
		},
	})
	envs := environmentstore.NewMemory()
	seedEnvironment(t, envs, securityEnv())

	doer := &fakeDoer{responses: dialResponses()}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).
		WithEnvironments(envs)

	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "security-team", "list_repos", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(doer.reqs) == 0 {
		t.Error("a capability-permitted connector was not dialed")
	}
}

// TestInvokerConnectorOutsideSelectorUnfiltered_spec_10_6_607 confirms
// the capability filter governs only connectors the environment's
// connectorSelector admits: a connector with a non-matching label is not
// filtered.
func TestInvokerConnectorOutsideSelectorUnfiltered_spec_10_6_607(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
		Labels: map[string]string{"team": "platform"}, // not admitted by the security selector
		ToolCapabilities: map[string][]capabilityinference.Capability{
			"delete_repo": {capabilityinference.CapWrite, capabilityinference.CapDelete},
		},
	})
	envs := environmentstore.NewMemory()
	seedEnvironment(t, envs, securityEnv())

	doer := &fakeDoer{responses: dialResponses()}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).
		WithEnvironments(envs)

	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "security-team", "delete_repo", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(doer.reqs) == 0 {
		t.Error("a connector outside the connectorSelector was filtered; want unfiltered dial")
	}
}

// TestInvokerUnknownEnvironmentUnfiltered_spec_10_6_607 confirms a
// session naming an environment that does not exist is left unfiltered —
// the capability filter is environment-defined.
func TestInvokerUnknownEnvironmentUnfiltered_spec_10_6_607(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
		Labels: map[string]string{"team": "security"},
		ToolCapabilities: map[string][]capabilityinference.Capability{
			"delete_repo": {capabilityinference.CapWrite},
		},
	})
	envs := environmentstore.NewMemory() // no environment seeded

	doer := &fakeDoer{responses: dialResponses()}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).
		WithEnvironments(envs)

	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "ghost-env", "delete_repo", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(doer.reqs) == 0 {
		t.Error("a call into an unknown environment was filtered; want unfiltered dial")
	}
}

// TestInvokerUninferredToolFailsClosed_spec_5_1 confirms a tool with no
// inferred capability set falls back to the §5.1 conservative admin
// default, so a restrictive allow-list denies it.
func TestInvokerUninferredToolFailsClosed_spec_5_1(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
		Labels: map[string]string{"team": "security"},
		// ToolCapabilities empty — never refreshed.
	})
	envs := environmentstore.NewMemory()
	seedEnvironment(t, envs, securityEnv()) // allow-list [read, search]

	doer := &fakeDoer{}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).
		WithEnvironments(envs)

	_, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "security-team", "mystery", nil)
	var capErr *CapabilityDeniedError
	if !errors.As(err, &capErr) {
		t.Fatalf("err = %v, want *CapabilityDeniedError for an un-inferred tool", err)
	}
	if capErr.Capability != "admin" {
		t.Errorf("blocked capability = %q, want admin (conservative default)", capErr.Capability)
	}
	if len(doer.reqs) != 0 {
		t.Errorf("dialed an un-inferred tool under a restrictive filter %d times, want 0", len(doer.reqs))
	}
}

// TestInvokerNoEnvironmentResolverGateOpen_spec_10_6_607 confirms that
// without an environment resolver wired the capability gate is open.
func TestInvokerNoEnvironmentResolverGateOpen_spec_10_6_607(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
		Labels: map[string]string{"team": "security"},
		ToolCapabilities: map[string][]capabilityinference.Capability{
			"delete_repo": {capabilityinference.CapWrite, capabilityinference.CapDelete},
		},
	})
	doer := &fakeDoer{responses: dialResponses()}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil) // no WithEnvironments

	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "security-team", "delete_repo", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(doer.reqs) == 0 {
		t.Error("the capability gate blocked a call with no environment resolver wired")
	}
}
