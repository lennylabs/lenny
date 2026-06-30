// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

const oauthCallbackURL = "https://gw.acme.com/v1/admin/connectors/oauth/callback"

// oauthFixture wires an admin Router with the connector OAuth flow and
// a fake provider token endpoint.
type oauthFixture struct {
	router      *admin.Router
	connectors  *connectorstore.Memory
	creds       *connectorcredstore.Memory
	stateStore  *connectoroauth.MemoryStateStore
	tokenServer *httptest.Server
	tokenStatus int
	tokenBody   string
	audit       *recordingAudit
}

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	f := &oauthFixture{
		tokenStatus: http.StatusOK,
		tokenBody:   `{"access_token":"at-xyz","refresh_token":"rt-abc","token_type":"Bearer","expires_in":3600,"scope":"repo"}`,
	}
	// The connector registry requires every endpoint to be HTTPS, so
	// the fake provider is served over TLS; its Client() trusts the
	// test certificate.
	f.tokenServer = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.tokenStatus)
		_, _ = w.Write([]byte(f.tokenBody))
	}))
	t.Cleanup(f.tokenServer.Close)

	f.connectors = connectorstore.NewMemory()
	f.creds = connectorcredstore.NewMemory(nil)
	f.stateStore = connectoroauth.NewMemoryStateStore()
	f.audit = &recordingAudit{}

	signer, err := connectoroauth.NewStateSigner(connectoroauth.SigningKey{
		KeyID: "test", Secret: []byte("connector-oauth-test-signing-key-01"),
	})
	if err != nil {
		t.Fatalf("NewStateSigner: %v", err)
	}
	tenants := tenantstore.NewMemory()
	f.router = admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC) },
		Audit: f.audit,
	}).WithConnectors(f.connectors).WithConnectorOAuth(&admin.ConnectorOAuth{
		StateSigner:   signer,
		StateStore:    f.stateStore,
		Credentials:   f.creds,
		ClientSecrets: admin.NewMemoryClientSecretResolver(map[string]string{"lenny-system/github-client-secret": "shh"}),
		HTTPClient:    f.tokenServer.Client(),
		CallbackURL:   oauthCallbackURL,
	})
	return f
}

// registerOAuthConnector creates a confidential OAuth connector whose
// token endpoint points at the fixture's fake provider.
func (f *oauthFixture) registerOAuthConnector(t *testing.T, public bool) {
	t.Helper()
	auth := &connectorstore.ConnectorAuth{
		Type:                  "oauth2",
		AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
		TokenEndpoint:         f.tokenServer.URL,
		ClientID:              "client-123",
		Scopes:                []string{"repo"},
	}
	if !public {
		auth.ClientSecretRef = "lenny-system/github-client-secret"
	}
	if err := f.connectors.Create(context.Background(), connectorstore.Connector{
		TenantID:     "acme",
		ID:           "github",
		MCPServerURL: "https://mcp.github.com",
		Transport:    "streamable_http",
		Auth:         auth,
	}); err != nil {
		t.Fatalf("connector Create: %v", err)
	}
}

// withUser attaches a non-admin authenticated principal — any
// authenticated caller may initiate a connector OAuth flow.
func withUser(req *http.Request) *http.Request {
	return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:   "alice@acme.com",
		TenantID:  "acme",
		SessionID: "sess_abc",
	}))
}

// initiate runs the authorization-initiation endpoint and returns the
// decoded response.
func (f *oauthFixture) initiate(t *testing.T) admin.AuthorizeConnectorResponse {
	t.Helper()
	req := withUser(httptest.NewRequest(http.MethodPost,
		"/v1/admin/connectors/github/oauth/authorize", nil))
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("initiate status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.AuthorizeConnectorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode initiate response: %v", err)
	}
	return resp
}

// callbackQuery parses the state value out of an authorization URL.
func stateFromAuthURL(t *testing.T, authURL string) string {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("authorization URL: %v", err)
	}
	return u.Query().Get("state")
}

func TestAuthorizeConnectorHappyPath(t *testing.T) {
	f := newOAuthFixture(t)
	f.registerOAuthConnector(t, false)

	resp := f.initiate(t)
	if resp.ConnectorID != "github" {
		t.Fatalf("ConnectorID = %q", resp.ConnectorID)
	}
	u, err := url.Parse(resp.AuthorizationURL)
	if err != nil {
		t.Fatalf("authorization URL: %v", err)
	}
	q := u.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want code", q.Get("response_type"))
	}
	if q.Get("code_challenge") == "" {
		t.Errorf("authorization URL is missing the PKCE code_challenge")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
	if q.Get("state") != resp.State {
		t.Errorf("state in URL %q does not match the response state %q", q.Get("state"), resp.State)
	}
	if q.Get("redirect_uri") != oauthCallbackURL {
		t.Errorf("redirect_uri = %q, want %q", q.Get("redirect_uri"), oauthCallbackURL)
	}
	// The signed state must verify under the gateway's signer.
	if len(f.audit.snapshot()) == 0 || f.audit.snapshot()[0].Type != "connector.oauth.authorization_initiated" {
		t.Errorf("expected an authorization_initiated audit event, got %+v", f.audit.snapshot())
	}
}

// spec: §9.2 line 87 — URL domain validation is a hard enforcement
// boundary. When a connector declares an expected_domain that its
// authorization URL host satisfies, the §9.3 flow proceeds; when the
// host falls outside the expected_domain the gateway drops the flow with
// URL_DOMAIN_NOT_ALLOWED rather than emitting a url-mode URL that could
// phish the user. F-9.2.7.
func TestAuthorizeConnectorExpectedDomainEnforced_spec_9_2_87(t *testing.T) {
	t.Run("authorization URL within expected_domain proceeds", func(t *testing.T) {
		f := newOAuthFixture(t)
		if err := f.connectors.Create(context.Background(), connectorstore.Connector{
			TenantID:     "acme",
			ID:           "github",
			MCPServerURL: "https://mcp.github.com",
			Transport:    "streamable_http",
			Auth: &connectorstore.ConnectorAuth{
				Type:                  "oauth2",
				AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
				TokenEndpoint:         f.tokenServer.URL,
				ClientID:              "client-123",
				ExpectedDomain:        "github.com",
			},
		}); err != nil {
			t.Fatalf("connector Create: %v", err)
		}
		resp := f.initiate(t) // initiate asserts a 200
		u, err := url.Parse(resp.AuthorizationURL)
		if err != nil || u.Host != "github.com" {
			t.Fatalf("authorization URL host = %v (err %v), want github.com", u, err)
		}
	})

	t.Run("authorization URL outside expected_domain is dropped", func(t *testing.T) {
		f := newOAuthFixture(t)
		// authorizationEndpoint host (github.com) does not match the
		// declared expected_domain (accounts.example.com): a
		// misconfiguration or a mutated connector. The connector
		// registers (the boundary is an emit-time control), but the
		// authorize flow drops the url-mode URL.
		if err := f.connectors.Create(context.Background(), connectorstore.Connector{
			TenantID:     "acme",
			ID:           "github",
			MCPServerURL: "https://mcp.github.com",
			Transport:    "streamable_http",
			Auth: &connectorstore.ConnectorAuth{
				Type:                  "oauth2",
				AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
				TokenEndpoint:         f.tokenServer.URL,
				ClientID:              "client-123",
				ExpectedDomain:        "accounts.example.com",
			},
		}); err != nil {
			t.Fatalf("connector Create: %v", err)
		}
		req := withUser(httptest.NewRequest(http.MethodPost,
			"/v1/admin/connectors/github/oauth/authorize", nil))
		rr := httptest.NewRecorder()
		f.router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "URL_DOMAIN_NOT_ALLOWED") {
			t.Errorf("body = %s, want URL_DOMAIN_NOT_ALLOWED", rr.Body.String())
		}
		// The drop happens before the flow records state or emits the
		// authorization_initiated audit event, so a dropped URL leaves no
		// audit trail of a flow that never started.
		for _, e := range f.audit.snapshot() {
			if e.Type == "connector.oauth.authorization_initiated" {
				t.Errorf("a dropped flow must not emit authorization_initiated")
			}
		}
	})
}

func TestAuthorizeConnectorRequiresAuthentication(t *testing.T) {
	f := newOAuthFixture(t)
	f.registerOAuthConnector(t, false)
	// No principal on the context.
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/oauth/authorize", nil)
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated initiate: got %d, want 401", rr.Code)
	}
}

func TestAuthorizeConnectorUnknownConnector(t *testing.T) {
	f := newOAuthFixture(t)
	req := withUser(httptest.NewRequest(http.MethodPost,
		"/v1/admin/connectors/missing/oauth/authorize", nil))
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown connector: got %d, want 404", rr.Code)
	}
}

func TestAuthorizeConnectorNotOAuth(t *testing.T) {
	f := newOAuthFixture(t)
	// A connector with no auth block is not an OAuth connector.
	if err := f.connectors.Create(context.Background(), connectorstore.Connector{
		TenantID: "acme", ID: "github",
		MCPServerURL: "https://mcp.github.com", Transport: "streamable_http",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	req := withUser(httptest.NewRequest(http.MethodPost,
		"/v1/admin/connectors/github/oauth/authorize", nil))
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("non-OAuth connector: got %d, want 400", rr.Code)
	}
}

// spec: §9.3 line 140 — audit logging is the prescribed forensic
// surface for the connector OAuth flow. F-9.3.11 — the
// `connector.oauth.credential_stored` audit detail must carry the
// initiator IP + user agent (captured at authorize time) and the
// completer IP + user agent (captured at callback time) so an
// operator can investigate a state replay or session hijack between
// authorize and callback. The pair is recorded verbatim.
func TestConnectorOAuthCredentialStoredAuditCarriesInitiatorAndCompleterIP_spec_9_3_140(t *testing.T) {
	f := newOAuthFixture(t)
	f.registerOAuthConnector(t, false)

	// Initiate the flow from a specific authorize-time IP / UA.
	authorizeReq := withUser(httptest.NewRequest(http.MethodPost,
		"/v1/admin/connectors/github/oauth/authorize", nil))
	authorizeReq.RemoteAddr = "10.0.0.7:54321"
	authorizeReq.Header.Set("User-Agent", "Alice/Browser-1.0")
	authorizeRR := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(authorizeRR, authorizeReq)
	if authorizeRR.Code != http.StatusOK {
		t.Fatalf("authorize status: %d, body=%s", authorizeRR.Code, authorizeRR.Body.String())
	}
	var resp admin.AuthorizeConnectorResponse
	if err := json.Unmarshal(authorizeRR.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode authorize: %v", err)
	}
	state := stateFromAuthURL(t, resp.AuthorizationURL)

	// Complete the flow from a different IP / UA — simulating the
	// browser-rendered redirect arriving from a different network or
	// after a hijack.
	cbReq := httptest.NewRequest(http.MethodGet,
		"/v1/admin/connectors/oauth/callback?code=code-1&state="+url.QueryEscape(state), nil)
	cbReq.RemoteAddr = "10.0.0.99:33333"
	cbReq.Header.Set("User-Agent", "Different/Browser-2.0")
	cbRR := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(cbRR, cbReq)
	if cbRR.Code != http.StatusOK {
		t.Fatalf("callback status: %d, body=%s", cbRR.Code, cbRR.Body.String())
	}

	var got admin.AuditEvent
	var found bool
	for _, ev := range f.audit.snapshot() {
		if ev.Type == "connector.oauth.credential_stored" {
			got = ev
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected connector.oauth.credential_stored event, got %+v", f.audit.snapshot())
	}
	if got.Detail["initiated_ip"] != "10.0.0.7" {
		t.Errorf("initiated_ip = %v, want 10.0.0.7", got.Detail["initiated_ip"])
	}
	if got.Detail["initiated_user_agent"] != "Alice/Browser-1.0" {
		t.Errorf("initiated_user_agent = %v, want Alice/Browser-1.0", got.Detail["initiated_user_agent"])
	}
	if got.Detail["completed_ip"] != "10.0.0.99" {
		t.Errorf("completed_ip = %v, want 10.0.0.99", got.Detail["completed_ip"])
	}
	if got.Detail["completed_user_agent"] != "Different/Browser-2.0" {
		t.Errorf("completed_user_agent = %v, want Different/Browser-2.0", got.Detail["completed_user_agent"])
	}
}

// spec: §9.3 line 140 — F-9.3.11. When a deployment terminates
// TLS at a proxy, the authorize-time / callback-time IP must be
// taken from the first-hop client in X-Forwarded-For so the audit
// trail records the user's client IP instead of the proxy's.
func TestConnectorOAuthCallbackIPHonoursXForwardedFor_spec_9_3_140(t *testing.T) {
	f := newOAuthFixture(t)
	f.registerOAuthConnector(t, false)
	authorizeReq := withUser(httptest.NewRequest(http.MethodPost,
		"/v1/admin/connectors/github/oauth/authorize", nil))
	authorizeReq.Header.Set("X-Forwarded-For", "203.0.113.10, 10.0.0.1")
	authorizeRR := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(authorizeRR, authorizeReq)
	if authorizeRR.Code != http.StatusOK {
		t.Fatalf("authorize: %d", authorizeRR.Code)
	}
	var resp admin.AuthorizeConnectorResponse
	_ = json.Unmarshal(authorizeRR.Body.Bytes(), &resp)
	state := stateFromAuthURL(t, resp.AuthorizationURL)

	cbReq := httptest.NewRequest(http.MethodGet,
		"/v1/admin/connectors/oauth/callback?code=code-1&state="+url.QueryEscape(state), nil)
	cbReq.Header.Set("X-Forwarded-For", "203.0.113.20")
	cbRR := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(cbRR, cbReq)
	if cbRR.Code != http.StatusOK {
		t.Fatalf("callback: %d, body=%s", cbRR.Code, cbRR.Body.String())
	}

	var got admin.AuditEvent
	for _, ev := range f.audit.snapshot() {
		if ev.Type == "connector.oauth.credential_stored" {
			got = ev
		}
	}
	if got.Detail["initiated_ip"] != "203.0.113.10" {
		t.Errorf("initiated_ip = %v, want 203.0.113.10 (first XFF hop)", got.Detail["initiated_ip"])
	}
	if got.Detail["completed_ip"] != "203.0.113.20" {
		t.Errorf("completed_ip = %v, want 203.0.113.20 (first XFF hop)", got.Detail["completed_ip"])
	}
}

// TestConnectorOAuthCallbackHappyPath runs the full flow: initiate,
// then call the callback with the real state and a code, and confirm
// the connector credential is stored for the flow's triple.
func TestConnectorOAuthCallbackHappyPath(t *testing.T) {
	f := newOAuthFixture(t)
	f.registerOAuthConnector(t, false)

	resp := f.initiate(t)
	state := stateFromAuthURL(t, resp.AuthorizationURL)

	cbReq := httptest.NewRequest(http.MethodGet,
		"/v1/admin/connectors/oauth/callback?code=auth-code-1&state="+url.QueryEscape(state), nil)
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, cbReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("callback status: %d, body=%s", rr.Code, rr.Body.String())
	}

	// The exchanged tokens must be stored for (acme, github, alice).
	cred, err := f.creds.Get(context.Background(), "acme", "github", "alice@acme.com", "")
	if err != nil {
		t.Fatalf("connector credential not stored: %v", err)
	}
	if cred.AccessToken != "at-xyz" || cred.RefreshToken != "rt-abc" {
		t.Fatalf("stored credential = %+v", cred)
	}
	if cred.TokenType != "Bearer" {
		t.Errorf("TokenType = %q, want Bearer", cred.TokenType)
	}
	// A credential-stored audit event must have fired against the
	// flow's principal.
	var sawStored bool
	for _, ev := range f.audit.snapshot() {
		if ev.Type == "connector.oauth.credential_stored" {
			sawStored = true
			if ev.ActorSubject != "alice@acme.com" || ev.ActorTenantID != "acme" {
				t.Errorf("audit actor = %q/%q", ev.ActorTenantID, ev.ActorSubject)
			}
		}
	}
	if !sawStored {
		t.Errorf("expected a credential_stored audit event, got %+v", f.audit.snapshot())
	}
}

func TestConnectorOAuthCallbackPublicClient(t *testing.T) {
	f := newOAuthFixture(t)
	f.tokenBody = `{"access_token":"at-public","token_type":"Bearer"}`
	f.registerOAuthConnector(t, true) // public client, no clientSecretRef

	resp := f.initiate(t)
	state := stateFromAuthURL(t, resp.AuthorizationURL)
	cbReq := httptest.NewRequest(http.MethodGet,
		"/v1/admin/connectors/oauth/callback?code=code-1&state="+url.QueryEscape(state), nil)
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, cbReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("public-client callback status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if _, err := f.creds.Get(context.Background(), "acme", "github", "alice@acme.com", ""); err != nil {
		t.Fatalf("public-client credential not stored: %v", err)
	}
}

// TestConnectorOAuthCallbackForgedStateRejected covers the §9.3
// anti-CSRF control: a callback whose state was not minted by this
// gateway must be rejected before any token exchange, and no
// credential must be stored.
func TestConnectorOAuthCallbackForgedStateRejected(t *testing.T) {
	f := newOAuthFixture(t)
	f.registerOAuthConnector(t, false)

	// An attacker fabricates a state under their own signing key.
	attacker, err := connectoroauth.NewStateSigner(connectoroauth.SigningKey{
		KeyID: "evil", Secret: []byte("an-attacker-controlled-signing-key"),
	})
	if err != nil {
		t.Fatalf("NewStateSigner: %v", err)
	}
	forged, err := attacker.Mint()
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	cbReq := httptest.NewRequest(http.MethodGet,
		"/v1/admin/connectors/oauth/callback?code=code-1&state="+url.QueryEscape(forged), nil)
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, cbReq)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("forged state: got %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "CONNECTOR_OAUTH_STATE_INVALID") {
		t.Errorf("forged-state body = %s, want CONNECTOR_OAUTH_STATE_INVALID", rr.Body.String())
	}
	// No credential may be stored from a forged callback.
	if rows, _ := f.creds.ListByConnector(context.Background(), "acme", "github"); len(rows) != 0 {
		t.Errorf("a forged callback stored %d credentials, want 0", len(rows))
	}
}

// TestConnectorOAuthCallbackUnknownStateRejected covers a
// signature-valid state with no stored flow — the state was minted by
// this gateway's signer but never Put into the store (or it expired
// out). It must be rejected.
func TestConnectorOAuthCallbackUnknownStateRejected(t *testing.T) {
	f := newOAuthFixture(t)
	f.registerOAuthConnector(t, false)

	// Mint a state through the router's own signer but never store a
	// flow for it: re-create a signer with the same key the fixture
	// used so the signature verifies.
	signer, err := connectoroauth.NewStateSigner(connectoroauth.SigningKey{
		KeyID: "test", Secret: []byte("connector-oauth-test-signing-key-01"),
	})
	if err != nil {
		t.Fatalf("NewStateSigner: %v", err)
	}
	orphan, err := signer.Mint()
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	cbReq := httptest.NewRequest(http.MethodGet,
		"/v1/admin/connectors/oauth/callback?code=code-1&state="+url.QueryEscape(orphan), nil)
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, cbReq)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown state: got %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "CONNECTOR_OAUTH_STATE_UNKNOWN") {
		t.Errorf("unknown-state body = %s, want CONNECTOR_OAUTH_STATE_UNKNOWN", rr.Body.String())
	}
}

// TestConnectorOAuthCallbackReplayedStateRejected covers a replayed
// callback: the same state used twice. The second use must be rejected
// and must not exchange a code again.
func TestConnectorOAuthCallbackReplayedStateRejected(t *testing.T) {
	f := newOAuthFixture(t)
	f.registerOAuthConnector(t, false)

	resp := f.initiate(t)
	state := stateFromAuthURL(t, resp.AuthorizationURL)
	cbURL := "/v1/admin/connectors/oauth/callback?code=code-1&state=" + url.QueryEscape(state)

	// First callback succeeds.
	rr1 := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr1, httptest.NewRequest(http.MethodGet, cbURL, nil))
	if rr1.Code != http.StatusOK {
		t.Fatalf("first callback: %d, body=%s", rr1.Code, rr1.Body.String())
	}
	// Replaying the same state must be rejected.
	rr2 := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, cbURL, nil))
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("replayed callback: got %d, want 400", rr2.Code)
	}
	if !strings.Contains(rr2.Body.String(), "CONNECTOR_OAUTH_STATE_REPLAYED") {
		t.Errorf("replayed-state body = %s, want CONNECTOR_OAUTH_STATE_REPLAYED", rr2.Body.String())
	}
}

// TestConnectorOAuthCallbackExchangeFailureHandled covers a
// code-exchange failure: the provider's token endpoint returns a
// non-2xx. The callback must return 502 and store no credential.
func TestConnectorOAuthCallbackExchangeFailureHandled(t *testing.T) {
	f := newOAuthFixture(t)
	f.registerOAuthConnector(t, false)

	resp := f.initiate(t)
	state := stateFromAuthURL(t, resp.AuthorizationURL)

	// The provider rejects the code exchange.
	f.tokenStatus = http.StatusBadRequest
	f.tokenBody = `{"error":"invalid_grant"}`

	cbReq := httptest.NewRequest(http.MethodGet,
		"/v1/admin/connectors/oauth/callback?code=stale-code&state="+url.QueryEscape(state), nil)
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, cbReq)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("exchange failure: got %d, want 502", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "CONNECTOR_OAUTH_EXCHANGE_FAILED") {
		t.Errorf("exchange-failure body = %s, want CONNECTOR_OAUTH_EXCHANGE_FAILED", rr.Body.String())
	}
	if rows, _ := f.creds.ListByConnector(context.Background(), "acme", "github"); len(rows) != 0 {
		t.Errorf("a failed exchange stored %d credentials, want 0", len(rows))
	}
	// The provider's error body must not leak to the client.
	if strings.Contains(rr.Body.String(), "invalid_grant") {
		t.Errorf("callback echoed the provider error body to the client")
	}
}

func TestConnectorOAuthCallbackMissingParams(t *testing.T) {
	f := newOAuthFixture(t)
	f.registerOAuthConnector(t, false)
	for _, q := range []string{"", "?code=x", "?state=y"} {
		rr := httptest.NewRecorder()
		f.router.Handler().ServeHTTP(rr,
			httptest.NewRequest(http.MethodGet, "/v1/admin/connectors/oauth/callback"+q, nil))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("callback %q: got %d, want 400", q, rr.Code)
		}
	}
}

func TestConnectorOAuthCallbackProviderDenied(t *testing.T) {
	f := newOAuthFixture(t)
	f.registerOAuthConnector(t, false)
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet,
		"/v1/admin/connectors/oauth/callback?error=access_denied&state=anything", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("provider error callback: got %d, want 400", rr.Code)
	}
}

// TestConnectorOAuthRoutesAbsentWithoutWiring confirms the OAuth
// routes register only when the connector OAuth flow is wired.
func TestConnectorOAuthRoutesAbsentWithoutWiring(t *testing.T) {
	tenants := tenantstore.NewMemory()
	// Connectors wired, but no WithConnectorOAuth.
	router := admin.NewRouter(tenants, admin.Options{}).
		WithConnectors(connectorstore.NewMemory())
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withUser(httptest.NewRequest(http.MethodPost,
		"/v1/admin/connectors/github/oauth/authorize", nil)))
	if rr.Code == http.StatusOK {
		t.Fatalf("OAuth route is live without WithConnectorOAuth wiring")
	}
}
