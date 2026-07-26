// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the end-to-end linkage between a
// real playground logout and a live MCP WebSocket carrying the bearer
// that logout revokes. Every prior test either closed a WebSocket with
// a stubbed revocation checker (pkg/gateway/mcpfabric/mcp/websocket_auth_test.go)
// or asserted the REST 401 against a fake checker
// (pkg/gateway/middleware/auth/playground_revocation_test.go); this
// suite drives a real playground bearer, minted through
// POST /v1/playground/token in authMode=oidc against a real in-process
// OIDC provider stub, onto a real MCP WebSocket, then calls the real
// POST /playground/auth/logout endpoint and asserts the still-open
// connection closes with code 4401 — backed by a real Redis container
// rather than a fake so the write POST /playground/auth/logout depends
// on (playground.RedisSessionStore.RevokeSession) is the same one the
// WebSocket's revocation watch (playground.RedisSessionStore.IsBearerRevoked)
// reads.
//
// The suite composes the real production types (playground.Handler,
// the real pkg/gateway/middleware/auth bearer chain, and mcp.Server)
// in the same outer-to-inner order cmd/lenny-gateway/httpsurface.go
// wires them, rather than booting the compiled cmd/lenny-gateway
// binary with --playground-enabled: doing so currently crash-loops on
// an unrelated defect (pkg/gateway/mcpfabric/playground/metrics.go
// registers the lenny_playground_page_views_total counter with the
// camelCase label "authMode", which the §16.1.1 snake_case validator
// rejects fatally at startup under every playground.authMode). That
// defect is already tracked (BUILD-GAPS.md §16.1 Metrics Finding 8)
// and is the documented reason tests/tier4_integration/playground_ws_carrier_test.go
// and tests/tier4_integration/playground_authmode_matrix_test.go
// compose the real middleware and handler types directly instead.
// This suite follows the same convention.
//
// The mint endpoint POST /v1/playground/token is not a sub-path of the
// lenny_playground_session cookie's Path=/playground/ scope, so a
// real Path-aware cookie jar never attaches the session cookie to the
// mint request (the same gap playground_authmode_matrix_test.go's
// oidc cell documents and works around). This suite works around it
// the same way pkg/gateway/mcpfabric/playground/sessionrecord_test.go's
// mintWithCookie helper does: it reads the Set-Cookie header directly
// off the OIDC callback response and attaches the cookie value by
// hand to the mint and logout requests, rather than relying on a
// browser-realistic cookie jar for those two calls.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/mcpschema"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// playgroundSessionCookieName mirrors the §27.3 cookie name
// (spec/27_web-playground.md:81/82: "the gateway sets
// Set-Cookie: lenny_playground_session=<opaque-id>..."). It is a
// wire-level constant the spec fixes, not an internal implementation
// detail of pkg/gateway/mcpfabric/playground.
const playgroundSessionCookieName = "lenny_playground_session"

// spec: §27.3.1 lines 97 and 170 — "Every authenticated request
// carrying a playground-origin bearer ... MUST consult
// t:{tenant_id}:pg:revoked:{jti} on the auth hot path before the
// bearer is honored. A hit produces 401 UNAUTHORIZED ... on REST/MCP
// requests and WebSocket close code 4401 on in-flight upgrades" and
// the Failure modes summary row "Bearer presented on WebSocket after
// revocation | WebSocket close code 4401 | bearer_revoked".
//
// diagnosis: a failure here means the real POST /playground/auth/logout
// revocation write (through playground.RedisSessionStore.RevokeSession
// against a real Redis) does not reach the real MCP WebSocket
// revocation watch (mcp.Server.startRevocationWatch calling
// playground.RedisSessionStore.IsBearerRevoked) for a bearer that was
// itself minted through the real POST /v1/playground/token endpoint
// and used to open a real, live WebSocket connection — i.e. the
// end-to-end wiring from a real logout to a real in-flight WebSocket
// close is broken, even though the two halves each pass in isolation
// against fakes.
func TestPlaygroundLogoutClosesLiveWebSocketWith4401_spec_27_3_1_97(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})

	idp := oidcstub.New(t)
	oidcCfg, err := playground.DiscoverHTTPOIDCConfig(context.Background(), idp.Issuer(), "playground-ws-revocation-client", nil)
	if err != nil {
		t.Fatalf("OIDC discovery against the stub provider: %v", err)
	}

	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyAllowAll,
	}); err != nil {
		t.Fatalf("seed tenant registry: %v", err)
	}

	signer := jwt.NewHMACSigner("pg-ws-revocation-test", []byte("playground-ws-revocation-test-secret"))
	sessions := playground.NewRedisSessionStore(rd.Client)

	pg := playground.New(playground.Config{
		Enabled:        true,
		AuthMode:       playground.AuthModeOIDC,
		MultiTenant:    true,
		BearerTTL:      900 * time.Second,
		OIDCSessionTTL: time.Hour,
	}, playground.Options{
		Signer:   signer,
		Tenants:  tenants,
		Sessions: sessions,
		OIDC:     playground.NewHTTPOIDCExchanger(oidcCfg),
	})

	mcpSrv := mcp.NewServer()

	mux := http.NewServeMux()
	mux.Handle("/v1/playground/token", pg.TokenRoutes())
	mux.Handle("/playground", pg.PlaygroundRoutes())
	mux.Handle("/playground/", pg.PlaygroundRoutes())
	mux.Handle("/mcp/v1/ws", mcpSrv.WebSocketHandler())

	// The same authmw.Wrap bearer chain cmd/lenny-gateway/httpsurface.go
	// wraps the entire mux with (§10.2), carrying the §27.6-wired
	// PlaygroundRevocations check for REST/MCP requests.
	handler := authmw.Wrap(mux, authmw.Options{
		MultiTenant:           true,
		Verifier:              signer,
		Registry:              tenants,
		PlaygroundRevocations: pg,
	})

	// §27.5.4 — wire the MCP WebSocket transport's revocation watch
	// exactly as cmd/lenny-gateway/httpsurface.go does: the principal is
	// read from the authmw context the upgrade request already carries,
	// and pg itself is the RevocationChecker. A short poll interval
	// keeps the test fast without changing the production default
	// (which SetWebSocketAuth would select for a non-positive value).
	mcpSrv.SetWebSocketAuth(func(r *http.Request) (mcp.WSPrincipal, bool) {
		p, ok := authmw.FromContext(r.Context())
		if !ok {
			return mcp.WSPrincipal{}, false
		}
		return mcp.WSPrincipal{Tenant: p.TenantID, JTI: p.JTI, Origin: p.Origin}, true
	}, pg, 25*time.Millisecond)

	// The §27.3.1 session cookie is Secure-flagged; a real deployment
	// always terminates TLS upstream of the playground, so drive the
	// whole journey over a real (self-signed) TLS listener.
	httpSrv := httptest.NewTLSServer(handler)
	defer httpSrv.Close()

	client := &http.Client{
		Transport: httpSrv.Client().Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// 1. GET /playground/auth/login: starts the OIDC flow and sets the
	// short-lived signed state cookie (spec/27_web-playground.md:80).
	loginResp, err := client.Get(httpSrv.URL + "/playground/auth/login")
	if err != nil {
		t.Fatalf("GET /playground/auth/login: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusFound {
		t.Fatalf("GET /playground/auth/login: want 302, got %d", loginResp.StatusCode)
	}
	authorizeURL := loginResp.Header.Get("Location")
	if authorizeURL == "" {
		t.Fatal("login response carried no Location header")
	}
	var stateCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == "lenny_playground_oidc_state" {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("login response carried no lenny_playground_oidc_state cookie")
	}

	// 2. GET the provider's authorize endpoint: the stub redirects back
	// to the gateway callback with a code and the same state.
	authorizeResp, err := client.Get(authorizeURL)
	if err != nil {
		t.Fatalf("GET provider authorize endpoint: %v", err)
	}
	authorizeResp.Body.Close()
	if authorizeResp.StatusCode != http.StatusFound {
		t.Fatalf("provider authorize response: want 302, got %d", authorizeResp.StatusCode)
	}
	callbackURL := authorizeResp.Header.Get("Location")
	if !strings.Contains(callbackURL, "/playground/auth/callback") {
		t.Fatalf("provider redirected to %q, want the gateway callback path", callbackURL)
	}

	// 3. GET /playground/auth/callback, presenting the state cookie
	// captured in step 1 (spec/27_web-playground.md:81): the gateway
	// verifies state, exchanges the code, establishes the server-side
	// session record, and sets lenny_playground_session.
	callbackReq, err := http.NewRequest(http.MethodGet, callbackURL, nil)
	if err != nil {
		t.Fatalf("build callback request: %v", err)
	}
	callbackReq.AddCookie(stateCookie)
	callbackResp, err := client.Do(callbackReq)
	if err != nil {
		t.Fatalf("GET /playground/auth/callback: %v", err)
	}
	callbackResp.Body.Close()
	if callbackResp.StatusCode != http.StatusFound {
		t.Fatalf("GET /playground/auth/callback: want 302, got %d", callbackResp.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, c := range callbackResp.Cookies() {
		if c.Name == playgroundSessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("callback response carried no lenny_playground_session cookie")
	}

	// 4. POST /v1/playground/token, attaching the session cookie by
	// hand (see the package doc comment on the cookie-Path mismatch).
	mintReq, err := http.NewRequest(http.MethodPost, httpSrv.URL+"/v1/playground/token", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("build mint request: %v", err)
	}
	mintReq.Header.Set("Content-Type", "application/json")
	mintReq.AddCookie(&http.Cookie{Name: playgroundSessionCookieName, Value: sessionCookie.Value})
	mintResp, err := client.Do(mintReq)
	if err != nil {
		t.Fatalf("POST /v1/playground/token: %v", err)
	}
	defer mintResp.Body.Close()
	var minted struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.NewDecoder(mintResp.Body).Decode(&minted); err != nil {
		t.Fatalf("decode mint response: %v", err)
	}
	if mintResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/playground/token: status = %d, want 200", mintResp.StatusCode)
	}
	if minted.BearerToken == "" {
		t.Fatal("mint response carried no bearerToken")
	}

	// 5. Open a real MCP WebSocket carrying the minted bearer on the
	// standard Authorization header transport (spec/27_web-playground.md:145)
	// and prove it is genuinely live.
	wsURL := "wss" + strings.TrimPrefix(httpSrv.URL, "https") + "/mcp/v1/ws"
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPClient: httpSrv.Client(),
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + minted.BearerToken}},
	})
	if err != nil {
		t.Fatalf("dial /mcp/v1/ws with the minted bearer: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcpschema.CurrentVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "playground-ws-revocation-test", "version": "0.0.0"},
		},
	}
	frame, err := json.Marshal(initReq)
	if err != nil {
		t.Fatalf("marshal initialize frame: %v", err)
	}
	if err := conn.Write(dialCtx, websocket.MessageText, frame); err != nil {
		t.Fatalf("write initialize frame: %v", err)
	}
	_, data, err := conn.Read(dialCtx)
	if err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	var initResp map[string]any
	if err := json.Unmarshal(data, &initResp); err != nil {
		t.Fatalf("unmarshal initialize response: %v; frame %s", err, data)
	}
	if _, isErr := initResp["error"]; isErr {
		t.Fatalf("initialize over the live connection returned an error frame before logout: %s", data)
	}

	// 6. POST /playground/auth/logout while the WebSocket from step 5
	// is still open, attaching the same session cookie by hand. Per
	// spec/27_web-playground.md:82 the gateway does not return 200
	// until the revocation writes have committed to the shared store.
	logoutReq, err := http.NewRequest(http.MethodPost, httpSrv.URL+"/playground/auth/logout", nil)
	if err != nil {
		t.Fatalf("build logout request: %v", err)
	}
	logoutReq.AddCookie(&http.Cookie{Name: playgroundSessionCookieName, Value: sessionCookie.Value})
	logoutResp, err := client.Do(logoutReq)
	if err != nil {
		t.Fatalf("POST /playground/auth/logout: %v", err)
	}
	logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /playground/auth/logout: status = %d, want 200", logoutResp.StatusCode)
	}

	// 7. The still-open WebSocket from step 5 must close with code 4401
	// once the revocation watch's next poll observes the write logout
	// just committed. spec/27_web-playground.md:170.
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	_, _, err = conn.Read(closeCtx)
	if err == nil {
		t.Fatal("expected the live WebSocket connection to close after logout revoked its bearer")
	}
	if got := websocket.CloseStatus(err); got != websocket.StatusCode(4401) {
		t.Fatalf("post-logout close status = %v (err=%v), want 4401", got, err)
	}
}
