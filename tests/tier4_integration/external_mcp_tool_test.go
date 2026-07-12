// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §9.1 "Gateway ↔ external MCP tools"
// boundary and the §9.3 per-delegation connector-access boundary. It
// wires the real gateway connector stack — the connectortools.Bridge,
// the connectorinvoke.Invoker, and the outbound Streamable-HTTP MCP
// client — against a real external third-party MCP server stub, then
// drives one session that lists and invokes the external server's tools
// and asserts that a policy-scoped child is denied the same connector
// with the documented error while its parent is admitted.
//
// This exercises the mix of platform-hosted and external MCP tooling
// end to end at the integration tier; tier 5 (full chart on Kind) is the
// promotion target for the same behavior driven through a live pod
// adapter and the GatewayControl gRPC surface.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorauthz"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectortools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/mcpserver"
)

// policyResolver maps a session id to its runtime-level effective §8.3
// delegation policy. It is the seam connectorauthz consults; every other
// component in the flow (the Bridge, the Invoker, the outbound client,
// the external server) is the real production type.
type policyResolver struct {
	effective map[string]delegationpolicystore.DelegationPolicy
}

func (r *policyResolver) EffectiveDelegationPolicy(_ context.Context, _, sessionID string) (delegationpolicystore.DelegationPolicy, bool, error) {
	p, ok := r.effective[sessionID]
	return p, ok, nil
}

func (r *policyResolver) ResolveActivePolicy(_ context.Context, _, _ string) (delegationpolicystore.DelegationPolicy, bool, error) {
	return delegationpolicystore.DelegationPolicy{}, false, nil
}

// allowConnectors builds an allow-list (default-deny) §8.3 policy that
// permits exactly the named connector ids.
func allowConnectors(ids ...string) delegationpolicystore.DelegationPolicy {
	return delegationpolicystore.DelegationPolicy{
		Rules: []delegationpolicystore.Rule{{
			Target: delegationpolicystore.Target{Types: []string{connectorauthz.CandidateType}, IDs: ids},
			Allow:  true,
		}},
	}
}

// spec: 9.1 (Gateway ↔ external MCP tools — tool invocation), 9.3 (connector access is scoped per delegation level)
// diagnosis: the §9.1/§9.3 external-connector path broke. The gateway
//
//	either could not list and invoke a real external MCP server's tools
//	through the connectortools.Bridge → connectorinvoke.Invoker →
//	outbound Streamable-HTTP client, or it failed to enforce the §9.3
//	line 164 per-delegation connector boundary (a policy-scoped child
//	reached a connector its effective delegation policy denies, or a
//	permitted parent was wrongly refused). The stub external server is
//	in tests/testinfra/stubs/mcpserver.
func TestExternalMCPToolPerDelegationScoping(t *testing.T) {
	ctx := context.Background()

	// A real external third-party MCP server advertising one tool. The
	// stub speaks the Streamable-HTTP JSON-RPC the gateway's connector
	// client drives (initialize / tools/list / tools/call) over HTTPS,
	// which §9.3 requires of a connector endpoint.
	extResult := json.RawMessage(`{"content":[{"type":"text","text":"issue-42 opened"}]}`)
	ext := mcpserver.New(
		t,
		mcpserver.WithTool(mcpserver.Tool{
			Name:        "create_issue",
			Description: "open an issue in the external tracker",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}}}`),
		}),
		mcpserver.WithToolResult("create_issue", extResult),
	)

	// Register the stub as a tenant connector. The registry is the SSRF
	// allowlist for external MCP traffic; only a registered endpoint is
	// dialable. §9.3 requires the endpoint to be HTTPS, which the stub is.
	connectors := connectorstore.NewMemory()
	const connectorID = "acme-tracker"
	if err := connectors.Create(ctx, connectorstore.Connector{
		TenantID: "acme", ID: connectorID, DisplayName: "Acme Tracker",
		MCPServerURL: ext.URL(), Transport: "streamable_http", Visibility: "tenant",
		CreatedAt: time.Unix(1700000000, 0).UTC(), UpdatedAt: time.Unix(1700000000, 0).UTC(),
	}); err != nil {
		t.Fatalf("register connector: %v", err)
	}

	// Two sessions under the same tenant and owner. The parent's
	// effective delegation policy permits the connector; the scoped
	// child's policy permits a different connector only, so it denies
	// this one by default-deny. §9.3 line 164: connector access is
	// scoped per delegation level.
	sessions := memstore.New()
	seedSession(t, sessions, "acme", "alice", "parent-session", "prod")
	seedSession(t, sessions, "acme", "alice", "child-session", "prod")
	resolver := &policyResolver{effective: map[string]delegationpolicystore.DelegationPolicy{
		"parent-session": allowConnectors(connectorID),
		"child-session":  allowConnectors("some-other-connector"),
	}}

	// The real authorizer and the real invoker dialing the real stub
	// with a client that trusts the stub's TLS certificate. The Bridge
	// is the GatewayControl-side dispatch a pod adapter forwards to.
	authz := connectorauthz.New(resolver, sessions, nil)
	invoker := connectorinvoke.NewInvoker(connectors, nil, connectorinvoke.New(ext.Client()), nil, authz)
	bridge := connectortools.New(sessions, connectors, authz, invoker)

	// ---- parent: admitted, lists and invokes the external tool ----

	advertised, err := bridge.ListSessionConnectors(ctx, "parent-session")
	if err != nil {
		t.Fatalf("parent ListSessionConnectors: %v", err)
	}
	if len(advertised) != 1 || advertised[0].ID != connectorID {
		t.Fatalf("parent advertised connectors = %+v, want exactly %q", advertised, connectorID)
	}

	tools, err := bridge.ListConnectorTools(ctx, "parent-session", connectorID)
	if err != nil {
		t.Fatalf("parent ListConnectorTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "create_issue" {
		t.Fatalf("parent tools/list = %+v, want the external create_issue tool", tools)
	}

	raw, isErr, err := bridge.CallConnectorTool(ctx, "parent-session", connectorID, "create_issue", json.RawMessage(`{"title":"bug"}`))
	if err != nil {
		t.Fatalf("parent CallConnectorTool: %v", err)
	}
	if isErr {
		t.Errorf("parent tools/call reported isError, want success")
	}
	if string(raw) != string(extResult) {
		t.Errorf("parent tools/call result = %s, want the external result verbatim %s", raw, extResult)
	}

	// The call actually reached the external server: initialize, the
	// initialized notification, and tools/call are all recorded.
	if !sawMethod(ext.Requests(), "tools/call") {
		t.Errorf("external server never received tools/call; requests = %+v", ext.Requests())
	}

	// ---- child: denied the same connector ----

	// §9.3 line 164: a connector outside the child's effective delegation
	// policy is never advertised to the child.
	childAdvertised, err := bridge.ListSessionConnectors(ctx, "child-session")
	if err != nil {
		t.Fatalf("child ListSessionConnectors: %v", err)
	}
	for _, c := range childAdvertised {
		if c.ID == connectorID {
			t.Fatalf("policy-denied connector %q was advertised to the scoped child", connectorID)
		}
	}

	// §9.3 line 164: even a direct tools/list or tools/call for the
	// connector is rejected before any outbound dial, with the documented
	// per-delegation-denial sentinel.
	if _, err := bridge.ListConnectorTools(ctx, "child-session", connectorID); !errors.Is(err, leasecontrol.ErrConnectorNotPermitted) {
		t.Errorf("child ListConnectorTools err = %v, want ErrConnectorNotPermitted", err)
	}
	if _, _, err := bridge.CallConnectorTool(ctx, "child-session", connectorID, "create_issue", json.RawMessage(`{}`)); !errors.Is(err, leasecontrol.ErrConnectorNotPermitted) {
		t.Errorf("child CallConnectorTool err = %v, want ErrConnectorNotPermitted", err)
	}

	// The child's denied calls never reached the external server: no
	// tools/call carried the child's attempt. Count the tools/call
	// requests and confirm the child added none beyond the parent's one.
	if got := countMethod(ext.Requests(), "tools/call"); got != 1 {
		t.Errorf("external server saw %d tools/call requests, want 1 (only the parent's); the child's denied call leaked to the endpoint", got)
	}
}

// spec: 9.3 (the gateway validates the connector_id in every external tool call ... a child cannot use connectors not permitted by its policy)
// diagnosis: the §9.3 line 164 boundary is evaluated only at discovery,
//
//	not at call time. A connector that was advertised while a policy was
//	loose must still be refused once the policy tightens, because the
//	Invoker re-checks every ListTools/CallTool. A failure here means a
//	stale connector reference could be exercised after the policy change.
func TestExternalMCPConnectorRecheckedAtCallTime(t *testing.T) {
	ctx := context.Background()

	ext := mcpserver.New(
		t,
		mcpserver.WithTool(mcpserver.Tool{Name: "read_doc", Description: "read"}),
		mcpserver.WithToolResult("read_doc", json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)),
	)

	connectors := connectorstore.NewMemory()
	const connectorID = "acme-docs"
	if err := connectors.Create(ctx, connectorstore.Connector{
		TenantID: "acme", ID: connectorID, DisplayName: "Acme Docs",
		MCPServerURL: ext.URL(), Transport: "streamable_http", Visibility: "tenant",
		CreatedAt: time.Unix(1700000000, 0).UTC(), UpdatedAt: time.Unix(1700000000, 0).UTC(),
	}); err != nil {
		t.Fatalf("register connector: %v", err)
	}

	sessions := memstore.New()
	seedSession(t, sessions, "acme", "alice", "sess", "prod")

	// A mutable resolver: the policy permits the connector, then tightens
	// to deny it. Both reads go through the same real authorizer.
	resolver := &policyResolver{effective: map[string]delegationpolicystore.DelegationPolicy{
		"sess": allowConnectors(connectorID),
	}}
	authz := connectorauthz.New(resolver, sessions, nil)
	invoker := connectorinvoke.NewInvoker(connectors, nil, connectorinvoke.New(ext.Client()), nil, authz)
	bridge := connectortools.New(sessions, connectors, authz, invoker)

	if _, _, err := bridge.CallConnectorTool(ctx, "sess", connectorID, "read_doc", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("call under permissive policy: %v", err)
	}

	// Tighten the policy: the connector is no longer permitted.
	resolver.effective["sess"] = allowConnectors("unrelated")

	if _, _, err := bridge.CallConnectorTool(ctx, "sess", connectorID, "read_doc", json.RawMessage(`{}`)); !errors.Is(err, leasecontrol.ErrConnectorNotPermitted) {
		t.Fatalf("call after policy tightened = %v, want ErrConnectorNotPermitted (re-checked at call time)", err)
	}
	if got := countMethod(ext.Requests(), "tools/call"); got != 1 {
		t.Errorf("external server saw %d tools/call, want 1 (the post-tightening call must not reach the endpoint)", got)
	}
}

// seedSession creates a session row scoped to a tenant, owner, and
// environment.
func seedSession(t *testing.T, s sessionstore.Store, tenant, user, id, env string) {
	t.Helper()
	if err := s.Create(context.Background(), sessionstore.Session{
		TenantID: tenant, UserID: user, ID: id, Environment: env,
		CreatedAt: time.Unix(1700000000, 0).UTC(),
	}); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

func sawMethod(reqs []mcpserver.Request, method string) bool {
	return countMethod(reqs, method) > 0
}

func countMethod(reqs []mcpserver.Request, method string) int {
	n := 0
	for _, r := range reqs {
		if r.Method == method {
			n++
		}
	}
	return n
}
