// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
)

func clock() time.Time { return time.Unix(1700000000, 0).UTC() }

func seedConnector(t *testing.T, store connectorstore.Store, c connectorstore.Connector) {
	t.Helper()
	if c.Transport == "" {
		c.Transport = "streamable_http"
	}
	if c.Visibility == "" {
		c.Visibility = "tenant"
	}
	c.CreatedAt = clock()
	c.UpdatedAt = clock()
	if err := store.Create(context.Background(), c); err != nil {
		t.Fatalf("seed connector: %v", err)
	}
}

// TestInvokerCallToolUsesStoredCredential_spec_9_3_142 verifies the
// invoker resolves the connector, attaches the gateway-held credential
// as the bearer token, and issues an authenticated tools/call — the
// path §9.3 lines 142-164 describe and the gap F-9.1.5 named ("the
// credential is never used to make a tools/call").
func TestInvokerCallToolUsesStoredCredential_spec_9_3_142(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID:     "acme",
		ID:           "github",
		MCPServerURL: "https://mcp.github.example",
		Auth: &connectorstore.ConnectorAuth{
			Type:                  "oauth2",
			AuthorizationEndpoint: "https://github.example/authorize",
			TokenEndpoint:         "https://github.example/token",
			ClientID:              "client-1",
			ClientSecretRef:       "lenny-system/github-secret",
		},
	})
	creds := connectorcredstore.NewMemory(clock)
	if err := creds.Put(context.Background(), connectorcredstore.ConnectorCredential{
		TenantID: "acme", ConnectorID: "github", UserID: "alice", Environment: "",
		AccessToken: "secret-token", TokenType: "Bearer", CreatedAt: clock(),
	}); err != nil {
		t.Fatalf("put cred: %v", err)
	}
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			jsonResp(200, `{"jsonrpc":"2.0","id":2,"result":{"content":[]}}`, nil),
		},
	}
	iv := NewInvoker(connectors, creds, New(doer), nil, nil)
	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "", "list_repos", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got := doer.reqs[0].Header.Get("Authorization"); got != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want Bearer secret-token", got)
	}
	if !strings.Contains(doer.bodies[2], "list_repos") {
		t.Errorf("tools/call did not name the tool: %s", doer.bodies[2])
	}
}

// TestInvokerPublicConnectorNoBearer_spec_9_3_142 confirms a connector
// with no auth block dials unauthenticated.
func TestInvokerPublicConnectorNoBearer_spec_9_3_142(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "public", MCPServerURL: "https://mcp.public.example",
	})
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			jsonResp(200, `{"jsonrpc":"2.0","id":2,"result":{}}`, nil),
		},
	}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil)
	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "public", "alice", "", "ping", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got := doer.reqs[0].Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
}

// TestInvokerRejectsUnregisteredConnector_spec_9_3_140 confirms an
// unregistered connector id is not dialed — §9.3 line 140 only allows
// dialing endpoints that resolve to a registered connector.
func TestInvokerRejectsUnregisteredConnector_spec_9_3_140(t *testing.T) {
	connectors := connectorstore.NewMemory()
	doer := &fakeDoer{}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil)
	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "ghost", "alice", "", "x", nil); err == nil {
		t.Fatal("expected error for unregistered connector")
	}
	if len(doer.reqs) != 0 {
		t.Errorf("dialed %d times for an unregistered connector, want 0", len(doer.reqs))
	}
}

// TestInvokerRejectsSoftDeletedConnector_spec_9_3_140 confirms a
// soft-deleted connector is not dialable.
func TestInvokerRejectsSoftDeletedConnector_spec_9_3_140(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "gone", MCPServerURL: "https://mcp.gone.example",
	})
	if err := connectors.SoftDelete(context.Background(), "acme", "gone", clock()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	doer := &fakeDoer{}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil)
	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "gone", "alice", "", "x", nil); err == nil {
		t.Fatal("expected ErrConnectorInactive")
	}
	if len(doer.reqs) != 0 {
		t.Errorf("dialed a soft-deleted connector %d times, want 0", len(doer.reqs))
	}
}

// fakeAuthz records its call and replays a fixed verdict, standing in
// for *connectorauthz.Authorizer.
type fakeAuthz struct {
	err       error
	gotConn   string
	gotSess   string
	callCount int
}

func (f *fakeAuthz) AuthorizeConnector(_ context.Context, _, sessionID, connectorID string, _ map[string]string) error {
	f.callCount++
	f.gotConn = connectorID
	f.gotSess = sessionID
	return f.err
}

// TestInvokerCallToolDeniedByPolicy_spec_9_3_164 verifies the §9.3 line
// 164 boundary: a connector the calling session's effective delegation
// policy denies is rejected before any outbound dial.
func TestInvokerCallToolDeniedByPolicy_spec_9_3_164(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
	})
	doer := &fakeDoer{}
	authz := &fakeAuthz{err: errors.New("denied by policy")}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, authz)
	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "", "list_repos", nil); err == nil {
		t.Fatal("expected a policy denial error")
	}
	if len(doer.reqs) != 0 {
		t.Errorf("dialed a policy-denied connector %d times, want 0", len(doer.reqs))
	}
	if authz.callCount != 1 || authz.gotConn != "github" || authz.gotSess != "sess-1" {
		t.Errorf("authz call = %+v, want one call for (sess-1, github)", authz)
	}
}

// TestInvokerCallToolPermittedByPolicy_spec_9_3_164 verifies a connector
// the policy permits proceeds to the outbound dial.
func TestInvokerCallToolPermittedByPolicy_spec_9_3_164(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
	})
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			jsonResp(200, `{"jsonrpc":"2.0","id":2,"result":{}}`, nil),
		},
	}
	authz := &fakeAuthz{}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, authz)
	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "", "ping", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if authz.callCount != 1 {
		t.Errorf("authz called %d times, want 1", authz.callCount)
	}
	if len(doer.reqs) == 0 {
		t.Error("a permitted connector was not dialed")
	}
}

var _ = json.RawMessage(nil)
