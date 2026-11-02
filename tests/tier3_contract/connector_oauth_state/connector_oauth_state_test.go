//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the §9.3 connector OAuth `state` over the admin
// REST wire. The authorize response carries a random unpadded base64url
// value with no signature segment, and the callback answers every refused
// `state` with 400 and a CONNECTOR_OAUTH_STATE_* code in the error envelope.
// StateStore.Consume is the single authority, so a forged value answers
// CONNECTOR_OAUTH_STATE_UNKNOWN.
//
// spec: §9.3 (Connector Definition and OAuth/OIDC) "`state` parameter (anti-CSRF)"
package connector_oauth_state_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

const callbackPath = "/v1/admin/connectors/oauth/callback"

// fixture serves the production admin router over an in-memory state store,
// with a clock the expiry case advances and a fake provider token endpoint.
type fixture struct {
	handler   http.Handler
	exchanges atomic.Int64
	now       atomic.Pointer[time.Time]
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f.now.Store(&start)

	// The connector registry requires HTTPS endpoints, so the fake provider
	// is served over TLS and its Client() trusts the test certificate.
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.exchanges.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-xyz","token_type":"Bearer"}`))
	}))
	t.Cleanup(provider.Close)

	connectors := connectorstore.NewMemory()
	if err := connectors.Create(context.Background(), connectorstore.Connector{
		TenantID:     "acme",
		ID:           "github",
		MCPServerURL: "https://mcp.github.com",
		Transport:    "streamable_http",
		Auth: &connectorstore.ConnectorAuth{
			Type:                  "oauth2",
			AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
			TokenEndpoint:         provider.URL,
			ClientID:              "client-123",
			Scopes:                []string{"repo"},
		},
	}); err != nil {
		t.Fatalf("connector Create: %v", err)
	}

	f.handler = admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return *f.now.Load() },
	}).WithConnectors(connectors).WithConnectorOAuth(&admin.ConnectorOAuth{
		StateStore:  connectoroauth.NewMemoryStateStore(),
		Credentials: connectorcredstore.NewMemory(nil),
		HTTPClient:  provider.Client(),
		CallbackURL: "https://gw.acme.com" + callbackPath,
	}).Handler()
	return f
}

// authorize starts a flow and returns the decoded authorize response.
func (f *fixture) authorize(t *testing.T) admin.AuthorizeConnectorResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/oauth/authorize", nil)
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "alice@acme.com", TenantID: "acme",
	}))
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("authorize: status %d: %s", rr.Code, rr.Body.String())
	}
	var resp admin.AuthorizeConnectorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode authorize response: %v", err)
	}
	return resp
}

// callback delivers the provider redirect with no credentials and returns
// the status and the error envelope code.
func (f *fixture) callback(t *testing.T, state string) (int, string) {
	t.Helper()
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet,
		callbackPath+"?code=code-1&state="+url.QueryEscape(state), nil))
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return rr.Code, body.Error.Code
}

// assertRefused asserts the wire outcome of a refused `state`.
func assertRefused(t *testing.T, gotStatus int, gotCode, wantCode string) {
	t.Helper()
	if gotStatus != http.StatusBadRequest || gotCode != wantCode {
		t.Fatalf("callback: status %d, code %q, want 400 %s", gotStatus, gotCode, wantCode)
	}
}

// spec: §9.3 (Connector Definition and OAuth/OIDC) "`state` parameter (anti-CSRF)"
// diagnosis: the callback answered a value with no stored flow with
// something other than 400 CONNECTOR_OAUTH_STATE_UNKNOWN. A different code
// means a format or signature check runs ahead of StateStore.Consume.
func TestForgedStateAnswersUnknown_spec_9_3(t *testing.T) {
	for name, forged := range map[string]string{
		"random value":       connectoroauth.NewState(),
		"two-segment format": connectoroauth.NewState() + "." + connectoroauth.NewState(),
		"arbitrary text":     "forged-state",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			status, code := f.callback(t, forged)
			assertRefused(t, status, code, "CONNECTOR_OAUTH_STATE_UNKNOWN")
			if got := f.exchanges.Load(); got != 0 {
				t.Errorf("a forged callback reached the provider %d times, want 0", got)
			}
		})
	}
}

// spec: §9.3 (Connector Definition and OAuth/OIDC) "`state` parameter (anti-CSRF)"
// diagnosis: a second callback with one state did not answer 400
// CONNECTOR_OAUTH_STATE_REPLAYED, so the single-use consume no longer
// separates a replay from an unknown value, or a replay reached the
// provider.
func TestReplayedStateAnswersReplayed_spec_9_3(t *testing.T) {
	f := newFixture(t)
	state := f.authorize(t).State
	if status, code := f.callback(t, state); status != http.StatusOK {
		t.Fatalf("first callback: status %d, code %q, want 200", status, code)
	}
	status, code := f.callback(t, state)
	assertRefused(t, status, code, "CONNECTOR_OAUTH_STATE_REPLAYED")
	if got := f.exchanges.Load(); got != 1 {
		t.Errorf("provider exchanges = %d, want 1", got)
	}
}

// spec: §9.3 (Connector Definition and OAuth/OIDC) "`state` parameter (anti-CSRF)"
// diagnosis: a callback past the 10-minute TTL did not answer 400
// CONNECTOR_OAUTH_STATE_EXPIRED, so the store or the handler's error
// mapping stopped reporting expiry, or an expired flow reached the provider.
func TestExpiredStateAnswersExpired_spec_9_3(t *testing.T) {
	f := newFixture(t)
	state := f.authorize(t).State
	late := f.now.Load().Add(connectoroauth.DefaultStateTTL + time.Second)
	f.now.Store(&late)
	status, code := f.callback(t, state)
	assertRefused(t, status, code, "CONNECTOR_OAUTH_STATE_EXPIRED")
	if got := f.exchanges.Load(); got != 0 {
		t.Errorf("an expired callback reached the provider %d times, want 0", got)
	}
}

// spec: §9.3 (Connector Definition and OAuth/OIDC) "`state` parameter (anti-CSRF)"
// diagnosis: the authorize response's state is not a bare base64url value
// of at least 128 bits. A dot in the value means a signature segment
// returned to the wire format.
func TestAuthorizeStateIsUnpaddedBase64URL_spec_9_3(t *testing.T) {
	f := newFixture(t)
	resp := f.authorize(t)
	if strings.Contains(resp.State, ".") {
		t.Fatalf("state %q carries a signature segment", resp.State)
	}
	raw, err := base64.RawURLEncoding.DecodeString(resp.State)
	if err != nil {
		t.Fatalf("state %q is not unpadded base64url: %v", resp.State, err)
	}
	if len(raw)*8 < 128 {
		t.Fatalf("state decodes to %d bits, want at least 128", len(raw)*8)
	}
	authURL, err := url.Parse(resp.AuthorizationURL)
	if err != nil {
		t.Fatalf("authorization URL: %v", err)
	}
	if got := authURL.Query().Get("state"); got != resp.State {
		t.Fatalf("authorization URL state = %q, want the response state %q", got, resp.State)
	}
}
