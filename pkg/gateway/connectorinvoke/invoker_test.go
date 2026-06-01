// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"encoding/json"
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
	iv := NewInvoker(connectors, creds, New(doer), nil)
	if _, err := iv.CallTool(context.Background(), "acme", "github", "alice", "", "list_repos", nil); err != nil {
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
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil)
	if _, err := iv.CallTool(context.Background(), "acme", "public", "alice", "", "ping", nil); err != nil {
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
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil)
	if _, err := iv.CallTool(context.Background(), "acme", "ghost", "alice", "", "x", nil); err == nil {
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
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil)
	if _, err := iv.CallTool(context.Background(), "acme", "gone", "alice", "", "x", nil); err == nil {
		t.Fatal("expected ErrConnectorInactive")
	}
	if len(doer.reqs) != 0 {
		t.Errorf("dialed a soft-deleted connector %d times, want 0", len(doer.reqs))
	}
}

var _ = json.RawMessage(nil)
