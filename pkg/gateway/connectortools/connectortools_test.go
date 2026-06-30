// SPDX-License-Identifier: MIT

package connectortools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectorauthz"
	"github.com/lennylabs/lenny/pkg/gateway/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectortools"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// fakeSessions resolves a session id to a canned row, or ErrNotFound.
type fakeSessions struct {
	sess sessionstore.Session
	err  error
}

func (f *fakeSessions) GetByID(_ context.Context, id string) (sessionstore.Session, error) {
	if f.err != nil {
		return sessionstore.Session{}, f.err
	}
	s := f.sess
	s.ID = id
	return s, nil
}

// fakeAuthz denies the connector ids in its deny set, allows the rest.
type fakeAuthz struct {
	deny map[string]bool
	err  error // non-policy infra error to surface
}

func (f *fakeAuthz) AuthorizeConnector(_ context.Context, _, _, connectorID string, _ map[string]string) error {
	if f.err != nil {
		return f.err
	}
	if f.deny[connectorID] {
		return connectorauthz.ErrConnectorNotPermitted
	}
	return nil
}

// fakeInvoker records its arguments and replays canned tool catalogs and
// call results.
type fakeInvoker struct {
	tools    []connectorinvoke.ToolDescriptor
	toolsErr error
	result   json.RawMessage
	callErr  error
	gotTool  string
	gotEnv   string
	gotUser  string
}

func (f *fakeInvoker) ListTools(_ context.Context, _, _, _, _, environment string) ([]connectorinvoke.ToolDescriptor, error) {
	f.gotEnv = environment
	return f.tools, f.toolsErr
}

func (f *fakeInvoker) CallTool(_ context.Context, _, _, _, userID, environment, toolName string, _ json.RawMessage) (json.RawMessage, error) {
	f.gotTool = toolName
	f.gotEnv = environment
	f.gotUser = userID
	return f.result, f.callErr
}

func seedConnector(t *testing.T, store connectorstore.Store, id string) {
	t.Helper()
	if err := store.Create(context.Background(), connectorstore.Connector{
		TenantID: "acme", ID: id, DisplayName: id, MCPServerURL: "https://mcp." + id + ".example",
		Transport: "streamable_http", Visibility: "tenant",
		CreatedAt: time.Unix(1700000000, 0).UTC(), UpdatedAt: time.Unix(1700000000, 0).UTC(),
	}); err != nil {
		t.Fatalf("seed connector %s: %v", id, err)
	}
}

func sessionResolver() *fakeSessions {
	return &fakeSessions{sess: sessionstore.Session{TenantID: "acme", UserID: "alice", Environment: "prod"}}
}

// spec: §9.3 line 142 — ListSessionConnectors lists the tenant's
// connectors and filters them by the session's effective delegation
// policy, so a denied or soft-deleted connector is never advertised.
// F-9.1.2.
func TestBridgeListSessionConnectorsFiltersByPolicy_spec_9_3_142(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, "github")
	seedConnector(t, connectors, "slack")
	seedConnector(t, connectors, "jira")
	seedConnector(t, connectors, "deleted")
	if err := connectors.SoftDelete(context.Background(), "acme", "deleted", time.Unix(1700000001, 0).UTC()); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	authz := &fakeAuthz{deny: map[string]bool{"slack": true}}
	b := connectortools.New(sessionResolver(), connectors, authz, &fakeInvoker{})

	got, err := b.ListSessionConnectors(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("ListSessionConnectors: %v", err)
	}
	ids := map[string]bool{}
	for _, c := range got {
		ids[c.ID] = true
	}
	if !ids["github"] || !ids["jira"] {
		t.Errorf("permitted connectors missing: %+v", got)
	}
	if ids["slack"] {
		t.Error("policy-denied connector slack was advertised")
	}
	if ids["deleted"] {
		t.Error("soft-deleted connector was advertised")
	}
}

// spec: §9.3 — an unknown session maps to ErrSessionNotFound on every
// connector RPC. F-9.1.2.
func TestBridgeUnknownSession_spec_9_3_142(t *testing.T) {
	connectors := connectorstore.NewMemory()
	sessions := &fakeSessions{err: sessionstore.ErrNotFound}
	b := connectortools.New(sessions, connectors, &fakeAuthz{}, &fakeInvoker{})

	if _, err := b.ListSessionConnectors(context.Background(), "missing"); !errors.Is(err, leasecontrol.ErrSessionNotFound) {
		t.Errorf("ListSessionConnectors err = %v, want ErrSessionNotFound", err)
	}
	if _, err := b.ListConnectorTools(context.Background(), "missing", "github"); !errors.Is(err, leasecontrol.ErrSessionNotFound) {
		t.Errorf("ListConnectorTools err = %v, want ErrSessionNotFound", err)
	}
	if _, _, err := b.CallConnectorTool(context.Background(), "missing", "github", "t", nil); !errors.Is(err, leasecontrol.ErrSessionNotFound) {
		t.Errorf("CallConnectorTool err = %v, want ErrSessionNotFound", err)
	}
}

// spec: §9.3 lines 142-164 — ListConnectorTools forwards the session's
// owner/environment to the Invoker and maps the catalog. F-9.1.2.
func TestBridgeListConnectorTools_spec_9_3_142(t *testing.T) {
	inv := &fakeInvoker{tools: []connectorinvoke.ToolDescriptor{
		{Name: "list_repos", Description: "list", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}
	b := connectortools.New(sessionResolver(), connectorstore.NewMemory(), &fakeAuthz{}, inv)

	tools, err := b.ListConnectorTools(context.Background(), "sess-1", "github")
	if err != nil {
		t.Fatalf("ListConnectorTools: %v", err)
	}
	if inv.gotEnv != "prod" {
		t.Errorf("invoker got environment %q, want prod", inv.gotEnv)
	}
	if len(tools) != 1 || tools[0].Name != "list_repos" || string(tools[0].InputSchema) != `{"type":"object"}` {
		t.Errorf("tools = %+v, want the mapped catalog", tools)
	}
}

// spec: §9.3 line 164 — a policy denial from the Invoker maps to the
// leasecontrol ErrConnectorNotPermitted sentinel. F-9.1.2.
func TestBridgePolicyDenialMapped_spec_9_3_164(t *testing.T) {
	inv := &fakeInvoker{toolsErr: connectorauthz.ErrConnectorNotPermitted, callErr: connectorauthz.ErrConnectorNotPermitted}
	b := connectortools.New(sessionResolver(), connectorstore.NewMemory(), &fakeAuthz{}, inv)

	if _, err := b.ListConnectorTools(context.Background(), "sess-1", "github"); !errors.Is(err, leasecontrol.ErrConnectorNotPermitted) {
		t.Errorf("ListConnectorTools err = %v, want ErrConnectorNotPermitted", err)
	}
	if _, _, err := b.CallConnectorTool(context.Background(), "sess-1", "github", "t", nil); !errors.Is(err, leasecontrol.ErrConnectorNotPermitted) {
		t.Errorf("CallConnectorTool err = %v, want ErrConnectorNotPermitted", err)
	}
}

// spec: §9.3 line 142 — CallConnectorTool returns the external result
// verbatim and parses its isError flag. F-9.1.2.
func TestBridgeCallConnectorToolParsesIsError_spec_9_3_142(t *testing.T) {
	inv := &fakeInvoker{result: json.RawMessage(`{"content":[{"type":"text","text":"boom"}],"isError":true}`)}
	b := connectortools.New(sessionResolver(), connectorstore.NewMemory(), &fakeAuthz{}, inv)

	raw, isErr, err := b.CallConnectorTool(context.Background(), "sess-1", "github", "delete_repo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallConnectorTool: %v", err)
	}
	if !isErr {
		t.Error("isError = false, want true from the isError result")
	}
	if string(raw) != `{"content":[{"type":"text","text":"boom"}],"isError":true}` {
		t.Errorf("result = %s, want the external result verbatim", raw)
	}
	if inv.gotTool != "delete_repo" || inv.gotUser != "alice" {
		t.Errorf("invoker got (%q, %q), want (delete_repo, alice)", inv.gotTool, inv.gotUser)
	}

	// A success result (no isError) reports isError=false.
	inv.result = json.RawMessage(`{"content":[]}`)
	if _, isErr, _ := b.CallConnectorTool(context.Background(), "sess-1", "github", "list_repos", nil); isErr {
		t.Error("isError = true for a success result, want false")
	}
}
