// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §9.3 connector-access boundary end to
// end across the real production chain the pod's GatewayControl
// CallConnectorTool RPC actually drives — leasecontrol.Service,
// connectortools.Bridge, connectorauthz.Authorizer, the delegation
// Service's EffectiveDelegationPolicy resolution, and
// connectorinvoke.Invoker's outbound dial — rather than the fake
// PolicyResolver/Authorizer/Invoker doubles the package-level unit
// suites use. It registers a connector at the root, delegates to a
// child whose runtime resolves to a DelegationPolicy that omits the
// connector, and asserts the child's CallConnectorTool is rejected
// with PermissionDenied before any outbound dial reaches the external
// endpoint, even though a root-level connector credential exists.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorauthz"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectortools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/externalmcp"
)

const connScopeTenant = "acme"

// connectorScopeFixture wires the real §9.3 connector-invocation chain
// (no fakes): a session store, a runtime registry, a delegation-policy
// registry, a connector registry, a connector-credential store, a
// connectorauthz.Authorizer backed by the real delegation.Service, a
// connectorinvoke.Invoker dialing the external stub, and the
// leasecontrol.Service the GatewayControl RPCs are served from.
type connectorScopeFixture struct {
	client adapterv1.GatewayControlClient
	stub   *externalmcp.Stub
}

// newConnectorScopeFixture seeds a root runtime with no
// DelegationPolicyRef (unrestricted) and a child runtime whose
// DelegationPolicyRef resolves to a policy that permits `agent` targets
// but carries no rule for `connector` targets — the §8.3 default-deny
// rule set (delegationpolicystore.DelegationPolicy.Evaluate) denies a
// candidate no rule matches. It registers one connector pointed at the
// stub and stores a connector credential for the shared owning user,
// standing in for a completed root-level OAuth flow.
func newConnectorScopeFixture(t *testing.T) *connectorScopeFixture {
	t.Helper()
	stub := externalmcp.Start(t, "attacker-controlled tool output")

	sessions := memstore.New()
	runtimes := runtimestore.NewMemory()
	policies := delegationpolicystore.NewMemory()
	connectors := connectorstore.NewMemory()
	creds := connectorcredstore.NewMemory(func() time.Time { return time.Now().UTC() })

	ctx := context.Background()

	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "root-agent"}); err != nil {
		t.Fatalf("create root-agent runtime: %v", err)
	}
	if err := runtimes.Create(ctx, runtimestore.Runtime{
		Name:                "child-agent",
		DelegationPolicyRef: "no-connectors",
	}); err != nil {
		t.Fatalf("create child-agent runtime: %v", err)
	}
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: connScopeTenant,
		Name:     "no-connectors",
		Rules: []delegationpolicystore.Rule{
			{Target: delegationpolicystore.Target{Types: []string{"agent"}}, Allow: true},
		},
	}); err != nil {
		t.Fatalf("create no-connectors policy: %v", err)
	}

	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "sess-root", TenantID: connScopeTenant, UserID: "alice",
		RuntimeRef: "root-agent",
	}); err != nil {
		t.Fatalf("create root session: %v", err)
	}
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "sess-child", TenantID: connScopeTenant, UserID: "alice",
		RuntimeRef: "child-agent", ParentSessionID: "sess-root",
	}); err != nil {
		t.Fatalf("create child session: %v", err)
	}

	if err := connectors.Create(ctx, connectorstore.Connector{
		TenantID: connScopeTenant, ID: "github", DisplayName: "GitHub",
		MCPServerURL: stub.URL(), Transport: "streamable_http", Visibility: "tenant",
		Auth: &connectorstore.ConnectorAuth{
			Type: "oauth2", ClientID: "test-client",
			AuthorizationEndpoint: "https://auth.example.com/authorize",
			TokenEndpoint:         "https://auth.example.com/token",
		},
	}); err != nil {
		t.Fatalf("create github connector: %v", err)
	}
	// A root-level connector credential: the §9.3 OAuth flow completed
	// for the owning user, keyed by (tenant, connector, user,
	// environment) rather than by session. It exists regardless of
	// which session in the tree calls the connector.
	if err := creds.Put(ctx, connectorcredstore.ConnectorCredential{
		TenantID: connScopeTenant, ConnectorID: "github", UserID: "alice",
		AccessToken: "root-level-secret-token", TokenType: "Bearer",
	}); err != nil {
		t.Fatalf("store root-level connector credential: %v", err)
	}

	delegationSvc := delegation.NewService(sessions, delegation.Options{
		Runtimes: runtimes,
		Policies: policies,
	})
	authz := connectorauthz.New(delegationSvc, nil, nil)
	invoker := connectorinvoke.NewInvoker(connectors, creds, connectorinvoke.New(stub.Client()), nil, authz)
	bridge := connectortools.New(sessions, connectors, authz, invoker)

	budgets := leasecontrol.NewMemoryBudgetSource()
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:        budgets,
		Tenants:        budgets,
		ConnectorTools: bridge,
	})
	if err != nil {
		t.Fatalf("leasecontrol.NewService: %v", err)
	}

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterGatewayControlServer(gs, svc)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return &connectorScopeFixture{client: adapterv1.NewGatewayControlClient(conn), stub: stub}
}

// spec: 9.3 ("The gateway validates the connector_id in every external
// tool call against the calling pod's effective delegation policy
// before proxying. A child cannot use connectors not permitted by its
// policy, even if tokens exist for them at the root level.")
//
// diagnosis: a failure means the live GatewayControl.CallConnectorTool
// proxy path — leasecontrol.Service, connectortools.Bridge,
// connectorauthz.Authorizer, and connectorinvoke.Invoker wired
// together as production wires them, rather than a fake resolver —
// does not re-evaluate connector_id against the calling session's own
// effective delegation policy, so a child with a restrictive policy
// could reach a connector only its root session was ever permitted to
// use.
func TestChildCallConnectorToolDeniedByEffectivePolicy_spec_9_3(t *testing.T) {
	fx := newConnectorScopeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Positive control: the root session's runtime carries no
	// DelegationPolicyRef, so no policy layer restricts it. The call
	// must reach the external stub and return its tool result verbatim,
	// proving the fixture's wiring genuinely dials out when permitted
	// (rather than the denial below passing vacuously because nothing
	// is wired to dial at all).
	rootResp, err := fx.client.CallConnectorTool(ctx, &adapterv1.CallConnectorToolRequest{
		SessionId:   &adapterv1.SessionId{Value: "sess-root"},
		ConnectorId: "github",
		ToolName:    "list_repos",
		Arguments:   []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("root CallConnectorTool: %v", err)
	}
	var rootResult struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rootResp.GetResult(), &rootResult); err != nil {
		t.Fatalf("decode root result: %v", err)
	}
	if len(rootResult.Content) != 1 || rootResult.Content[0].Text != "attacker-controlled tool output" {
		t.Fatalf("root result = %s, want the stub's tool output verbatim", rootResp.GetResult())
	}
	afterRoot := fx.stub.RequestCount()
	if afterRoot == 0 {
		t.Fatal("stub received no requests for the permitted root call")
	}

	// The child session's runtime resolves to the no-connectors policy,
	// which carries no rule matching a `connector` candidate — the
	// §8.3 default-deny rule set denies it. The call must be rejected
	// with PermissionDenied before any outbound dial, so the stub's
	// request count must not advance past the root call above, despite
	// the root-level credential for this connector/user existing in
	// connectorcredstore.
	_, err = fx.client.CallConnectorTool(ctx, &adapterv1.CallConnectorToolRequest{
		SessionId:   &adapterv1.SessionId{Value: "sess-child"},
		ConnectorId: "github",
		ToolName:    "list_repos",
		Arguments:   []byte(`{}`),
	})
	if err == nil {
		t.Fatal("child CallConnectorTool succeeded, want PermissionDenied")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("child CallConnectorTool code = %v, want PermissionDenied (err=%v)", status.Code(err), err)
	}
	if got := fx.stub.RequestCount(); got != afterRoot {
		t.Errorf("stub request count = %d after the denied child call, want unchanged from %d (no outbound dial)", got, afterRoot)
	}
}

// spec: 9.3 (ListSessionConnectors "filters them by the session's
// effective delegation policy") — the discovery-time filter agrees
// with the call-time re-check exercised above: a connector the child's
// policy denies is not advertised to it either.
//
// diagnosis: a failure means the discovery-time filter
// (connectortools.Bridge.ListSessionConnectors) and the call-time gate
// disagree about which connectors the child's effective policy
// permits, which would let a pod's adapter open an intra-pod MCP
// server for a connector CallConnectorTool then rejects.
func TestChildListSessionConnectorsOmitsPolicyDenied_spec_9_3(t *testing.T) {
	fx := newConnectorScopeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rootList, err := fx.client.ListSessionConnectors(ctx, &adapterv1.ListSessionConnectorsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-root"},
	})
	if err != nil {
		t.Fatalf("root ListSessionConnectors: %v", err)
	}
	if len(rootList.GetConnectors()) != 1 || rootList.GetConnectors()[0].GetId() != "github" {
		t.Errorf("root connectors = %+v, want [github]", rootList.GetConnectors())
	}

	childList, err := fx.client.ListSessionConnectors(ctx, &adapterv1.ListSessionConnectorsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-child"},
	})
	if err != nil {
		t.Fatalf("child ListSessionConnectors: %v", err)
	}
	if len(childList.GetConnectors()) != 0 {
		t.Errorf("child connectors = %+v, want none advertised", childList.GetConnectors())
	}
}
