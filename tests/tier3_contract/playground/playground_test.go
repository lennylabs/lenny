// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §27 web playground. These tests
// drive the playground gateway-side endpoints (the §27.3.1 auth
// gatekeepers and the mode-polymorphic POST /v1/playground/token
// mint) via httptest, and exercise the §27.5 protocol path: a
// playground-minted session-capability JWT must be honored by the
// public REST surface exactly as any other session JWT, because the
// playground is a client of that surface and mints standard tokens.
//
// The harness composes the real pkg/gateway/playground handler with
// the real pkg/gateway/sessionserver handler behind the §10.2 auth
// middleware, the same wiring cmd/lenny-gateway uses. No browser is
// involved; the tests assert the HTTP and JWT contract.

package playground_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// fakeTenants is the in-test playground.TenantRegistry and the
// auth-middleware tenant registry.
type fakeTenants struct {
	registered map[string]bool
}

func (f fakeTenants) IsRegistered(id string) (bool, error) {
	return f.registered[id], nil
}

func devSigner() *jwt.HMACSigner {
	return jwt.NewHMACSigner("pg-contract", []byte("playground-contract-secret"))
}

// newPlayground builds a playground Handler in the requested auth
// mode with an in-memory session store.
func newPlayground(t *testing.T, mode playground.AuthMode) *playground.Handler {
	t.Helper()
	cfg := playground.Config{
		Enabled:        true,
		AuthMode:       mode,
		DevTenantID:    "acme",
		BearerTTL:      900 * time.Second,
		OIDCSessionTTL: time.Hour,
	}
	return playground.New(cfg, playground.Options{
		Signer:   devSigner(),
		Verifier: devSigner(),
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
		Sessions: playground.NewMemorySessionStore(),
	})
}

// decodeClaims decodes a compact JWT payload into a claim map.
func decodeClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return m
}

func postJSON(t *testing.T, url, body string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, raw
}

// spec: 27.3.1 (dev mode: POST /v1/playground/token mints with no admission material)
// diagnosis: A dev-mode mint with an empty body did not return 200
//
//	with a Bearer token. The mode-polymorphic mint endpoint's dev
//	branch is broken; inspect mintDev in pkg/gateway/playground.
func TestDevModeMintReturnsBearer(t *testing.T) {
	h := newPlayground(t, playground.AuthModeDev)
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	resp, raw := postJSON(t, srv.URL+"/v1/playground/token", "{}", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dev mint status = %d, want 200; body=%s", resp.StatusCode, raw)
	}
	var body struct {
		BearerToken      string `json:"bearerToken"`
		TokenType        string `json:"tokenType"`
		ExpiresInSeconds int64  `json:"expiresInSeconds"`
		Reusable         bool   `json:"reusable"`
		EffectiveScope   string `json:"effectiveScope"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode mint body: %v", err)
	}
	if body.TokenType != "Bearer" || body.BearerToken == "" || !body.Reusable {
		t.Fatalf("unexpected mint body: %+v", body)
	}
	if body.ExpiresInSeconds != 900 {
		t.Fatalf("expiresInSeconds = %d, want 900", body.ExpiresInSeconds)
	}
	// §27.3.1: the mint response carries effectiveScope, the
	// space-separated intersection(subject.scope, playground ceiling)
	// the SPA reads to gate the §27.4 delegation-policy affordance. It
	// must equal the minted bearer's scope claim on the wire. A
	// dev-mode mint synthesizes a subject carrying the full ceiling, so
	// the absent-restriction intersection yields the full ceiling
	// string (non-empty).
	if body.EffectiveScope == "" {
		t.Fatalf("mint response effectiveScope is empty, want the dev-mode full ceiling string")
	}
	claims := decodeClaims(t, body.BearerToken)
	if claimScope, _ := claims["scope"].(string); body.EffectiveScope != claimScope {
		t.Fatalf("effectiveScope = %q, want it to equal the bearer scope claim %q",
			body.EffectiveScope, claimScope)
	}
}

// spec: 27.3.1 (the mint response narrows effectiveScope to the subject scope across non-dev modes)
// diagnosis: The POST /v1/playground/token response no longer carries
//
//	the effective scope the §27.4 SPA gate reads, or the value no
//	longer matches the minted bearer's scope claim after narrowing
//	the subject scope against the playground ceiling. The mint
//	response effectiveScope carrier is broken; inspect completeMint
//	and tokenResponse in pkg/gateway/playground.
func TestMintResponseEffectiveScopeNarrowsToSubject(t *testing.T) {
	// apiKey mode admits a pasted user_bearer whose scope claim is the
	// subject scope. The mint intersects it against the playground
	// ceiling (tools:sessions:* ...), and Set.Intersect keeps the
	// narrower operand, so a subject scoped to tools:sessions:write
	// survives as tools:sessions:write on the minted bearer and in the
	// effectiveScope response field. This pins the narrowing on the
	// wire across the oidc/apiKey modes, distinct from the dev-mode
	// full-ceiling case.
	signer := devSigner()
	subjectToken, err := signer.Sign(jwt.Claims{
		Subject: "alice", TenantID: "acme",
		Typ:    auth.TokenUserBearer,
		Scope:  "tools:sessions:write",
		Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign subject token: %v", err)
	}
	h := newPlayground(t, playground.AuthModeAPIKey)
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	resp, raw := postJSON(t, srv.URL+"/v1/playground/token", "{}",
		map[string]string{"Authorization": "Bearer " + subjectToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apiKey mint status = %d, want 200; body=%s", resp.StatusCode, raw)
	}
	var body struct {
		BearerToken    string `json:"bearerToken"`
		EffectiveScope string `json:"effectiveScope"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode mint body: %v", err)
	}
	// The effectiveScope field must equal the minted bearer's scope
	// claim on the wire, and the intersection must keep the narrower
	// subject operand (tools:sessions:write), not the ceiling wildcard.
	claims := decodeClaims(t, body.BearerToken)
	claimScope, _ := claims["scope"].(string)
	if body.EffectiveScope != claimScope {
		t.Fatalf("effectiveScope = %q, want it to equal the bearer scope claim %q",
			body.EffectiveScope, claimScope)
	}
	if !strings.Contains(body.EffectiveScope, "tools:sessions:write") {
		t.Fatalf("effectiveScope = %q, want it to contain the narrower subject operand tools:sessions:write",
			body.EffectiveScope)
	}
}

// spec: 27.3 (mode-agnostic origin:"playground" claim on the minted JWT)
// diagnosis: The minted session-capability JWT did not carry the
//
//	origin:"playground" claim. The §27.6 idle-timeout override and
//	the §27.8 dashboard slice key on this claim; inspect
//	completeMint in pkg/gateway/playground.
func TestMintedJWTCarriesPlaygroundOrigin(t *testing.T) {
	h := newPlayground(t, playground.AuthModeDev)
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	resp, raw := postJSON(t, srv.URL+"/v1/playground/token", "{}", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mint status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		BearerToken string `json:"bearerToken"`
	}
	_ = json.Unmarshal(raw, &body)
	claims := decodeClaims(t, body.BearerToken)
	if claims["origin"] != "playground" {
		t.Fatalf("minted JWT origin = %v, want playground", claims["origin"])
	}
	if claims["typ"] != string(auth.TokenSessionCapability) {
		t.Fatalf("minted JWT typ = %v, want session_capability", claims["typ"])
	}
}

// spec: 27.5 (playground-minted JWT is honored by the public REST surface)
// diagnosis: A session-capability JWT minted by POST /v1/playground/token
//
//	was rejected by the public POST /v1/sessions surface. The
//	playground mints a standard token (§27.5); if the public
//	surface rejects it the mint claims or signer are wrong.
func TestPlaygroundJWTAcceptedByPublicSessionsAPI(t *testing.T) {
	signer := devSigner()
	// The playground and the public surface share a signer, the way
	// the gateway shares one JWTSigner across both.
	cfg := playground.Config{
		Enabled: true, AuthMode: playground.AuthModeDev, DevTenantID: "acme",
		BearerTTL: 900 * time.Second, OIDCSessionTTL: time.Hour,
	}
	h := playground.New(cfg, playground.Options{
		Signer:   signer,
		Verifier: signer,
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
		Sessions: playground.NewMemorySessionStore(),
	})
	pgSrv := httptest.NewServer(h.TokenRoutes())
	defer pgSrv.Close()

	// Mint a playground bearer.
	resp, raw := postJSON(t, pgSrv.URL+"/v1/playground/token", "{}", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mint status = %d, want 200", resp.StatusCode)
	}
	var minted struct {
		BearerToken string `json:"bearerToken"`
	}
	_ = json.Unmarshal(raw, &minted)

	// The public REST surface: the §15.1 sessionserver behind the
	// §10.2 auth middleware, verifying with the same signer.
	store := memstore.New()
	publicAPI := authmw.Wrap(
		sessionserver.New(store, sessionserver.Options{}).Handler(),
		authmw.Options{
			Verifier:    signer,
			MultiTenant: true,
			Registry:    fakeTenants{registered: map[string]bool{"acme": true}},
			RequireAuth: true,
		},
	)
	apiSrv := httptest.NewServer(publicAPI)
	defer apiSrv.Close()

	createResp, createRaw := postJSON(t, apiSrv.URL+"/v1/sessions",
		`{"runtimeRef":"claude-code"}`,
		map[string]string{"Authorization": "Bearer " + minted.BearerToken})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/sessions with playground JWT status = %d, want 201; body=%s",
			createResp.StatusCode, createRaw)
	}
}

// spec: 27.3.1 (apiKey mode rejects a non-user_bearer subject token)
// diagnosis: A session_capability JWT pasted into the API-key form
//
//	was not rejected with LENNY_PLAYGROUND_BEARER_TYPE_REJECTED.
//	The §10.2 playground mint invariant (subject typ ==
//	user_bearer) is not enforced; inspect mintAPIKey.
func TestAPIKeyModeRejectsCapabilitySubjectToken(t *testing.T) {
	signer := devSigner()
	capToken, err := signer.Sign(jwt.Claims{
		Subject: "alice", TenantID: "acme",
		Typ:    auth.TokenSessionCapability,
		Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign capability token: %v", err)
	}
	cfg := playground.Config{Enabled: true, AuthMode: playground.AuthModeAPIKey, BearerTTL: 900 * time.Second}
	h := playground.New(cfg, playground.Options{Signer: signer, Verifier: signer})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	resp, raw := postJSON(t, srv.URL+"/v1/playground/token", "{}",
		map[string]string{"Authorization": "Bearer " + capToken})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", resp.StatusCode, raw)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	if env.Error.Code != "LENNY_PLAYGROUND_BEARER_TYPE_REJECTED" {
		t.Fatalf("error code = %q, want LENNY_PLAYGROUND_BEARER_TYPE_REJECTED", env.Error.Code)
	}
}

// spec: 27.3.1 (oidc mode rejects an Authorization: Bearer on the mint endpoint)
// diagnosis: An Authorization: Bearer header presented to
//
//	POST /v1/playground/token in oidc mode was not rejected with
//	LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL. The route-stamped mode
//	enforcement is broken; inspect mintOIDC.
func TestOIDCModeRejectsBearerMaterial(t *testing.T) {
	h := newPlayground(t, playground.AuthModeOIDC)
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	resp, raw := postJSON(t, srv.URL+"/v1/playground/token", "{}",
		map[string]string{"Authorization": "Bearer pasted-token"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, raw)
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	if env.Error.Code != "LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL" {
		t.Fatalf("error code = %q, want LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL", env.Error.Code)
	}
	if env.Error.Details["configuredAuthMode"] != "oidc" {
		t.Fatalf("details.configuredAuthMode = %v, want oidc", env.Error.Details["configuredAuthMode"])
	}
}

// spec: 27.3.1 (apiKey mode rejects a cookie-only request, echoing presentedMaterial=cookie)
// diagnosis: A cookie-only request to POST /v1/playground/token in
//
//	apiKey mode was not rejected with LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL
//	and details.presentedMaterial=cookie. The apiKey admission gate no
//	longer rejects the cookie credential in this mode; inspect
//	mintAPIKey in pkg/gateway/mcpfabric/playground.
func TestAPIKeyModeRejectsCookieOnlyMaterial(t *testing.T) {
	h := newPlayground(t, playground.AuthModeAPIKey)
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "lenny_playground_session", Value: "opaque-session-id"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/playground/token: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, raw)
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	if env.Error.Code != "LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL" {
		t.Fatalf("error code = %q, want LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL", env.Error.Code)
	}
	if env.Error.Details["configuredAuthMode"] != "apiKey" {
		t.Fatalf("details.configuredAuthMode = %v, want apiKey", env.Error.Details["configuredAuthMode"])
	}
	if env.Error.Details["presentedMaterial"] != "cookie" {
		t.Fatalf("details.presentedMaterial = %v, want cookie", env.Error.Details["presentedMaterial"])
	}
}

// spec: 27.3.1 (the WRONG_AUTH_MATERIAL envelope reports presentedMaterial=both when a cookie and a bearer are presented together, oidc mode)
// diagnosis: A request carrying both the lenny_playground_session
//
//	cookie and an Authorization: Bearer header in oidc mode reported
//	details.presentedMaterial=bearer instead of both. The oidc
//	admission gate must inspect the cookie before labeling the
//	rejection; inspect mintOIDC in pkg/gateway/mcpfabric/playground.
func TestOIDCModeReportsBothWhenCookieAndBearerPresented(t *testing.T) {
	h := newPlayground(t, playground.AuthModeOIDC)
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer pasted-token")
	req.AddCookie(&http.Cookie{Name: "lenny_playground_session", Value: "opaque-session-id"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/playground/token: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, raw)
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	if env.Error.Code != "LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL" {
		t.Fatalf("error code = %q, want LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL", env.Error.Code)
	}
	if env.Error.Details["presentedMaterial"] != "both" {
		t.Fatalf("details.presentedMaterial = %v, want both", env.Error.Details["presentedMaterial"])
	}
}

// spec: 27.3.1 (dev mode ignores any presented cookie or bearer material and mints)
// diagnosis: A dev-mode mint carrying a stray Authorization: Bearer
//
//	header and a lenny_playground_session cookie was rejected, or the
//	stray material otherwise gated the mint, instead of minting 200.
//	Dev mode must never gate on caller material; inspect mintDev in
//	pkg/gateway/mcpfabric/playground.
func TestDevModeIgnoresPresentedMaterial(t *testing.T) {
	h := newPlayground(t, playground.AuthModeDev)
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer stray-token")
	req.AddCookie(&http.Cookie{Name: "lenny_playground_session", Value: "stray-session-id"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/playground/token: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dev mint with stray material status = %d, want 200; body=%s", resp.StatusCode, raw)
	}
	var body struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode mint body: %v", err)
	}
	if body.BearerToken == "" {
		t.Fatalf("dev mint body missing bearerToken")
	}
}

// spec: 27.7 (the playground CSP and security headers are applied to /playground/*)
// diagnosis: A /playground/* response lacked the §27.7
//
//	Content-Security-Policy, X-Content-Type-Options, or
//	Referrer-Policy header. The securityHeaders wrap is missing
//	or mis-scoped; inspect PlaygroundRoutes.
func TestPlaygroundResponsesCarryCSP(t *testing.T) {
	h := newPlayground(t, playground.AuthModeDev)
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/playground/")
	if err != nil {
		t.Fatalf("GET /playground/: %v", err)
	}
	defer resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "object-src 'none'") {
		t.Fatalf("CSP missing required directives: %q", csp)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", resp.Header.Get("X-Content-Type-Options"))
	}
	if resp.Header.Get("Referrer-Policy") != "same-origin" {
		t.Fatalf("Referrer-Policy = %q, want same-origin", resp.Header.Get("Referrer-Policy"))
	}
}

// spec: 27.7 (the exact §27.7 CSP directive-source-list set, byte-exact
// against the spec block): "Content-Security-Policy: default-src
// 'self'; script-src 'self'; style-src 'self' 'unsafe-inline';
// connect-src 'self' wss://<gateway-host>; img-src 'self' data:;
// object-src 'none'; media-src 'none'; frame-ancestors 'none';
// base-uri 'self'; form-action 'self'"
// diagnosis: The parsed CSP directive set on a /playground/* response
// does not match the §27.7 block exactly (a directive is missing, an
// extra directive is present, or a directive's source list differs).
// The gateway's contentSecurityPolicy() build drifted from the spec
// block; reconcile the directive list there.
func TestPlaygroundResponsesCarryExactSpecCSP(t *testing.T) {
	h := newPlayground(t, playground.AuthModeDev)
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/playground/")
	if err != nil {
		t.Fatalf("GET /playground/: %v", err)
	}
	defer resp.Body.Close()

	got := parseCSP(t, resp.Header.Get("Content-Security-Policy"))

	// The spec block substitutes the real gateway host for
	// <gateway-host>; newPlayground does not configure a
	// GatewayHost, so connect-src carries only 'self'.
	want := map[string]string{
		"default-src":     "'self'",
		"script-src":      "'self'",
		"style-src":       "'self' 'unsafe-inline'",
		"connect-src":     "'self'",
		"img-src":         "'self' data:",
		"object-src":      "'none'",
		"media-src":       "'none'",
		"frame-ancestors": "'none'",
		"base-uri":        "'self'",
		"form-action":     "'self'",
	}

	for directive, sources := range want {
		gotSources, ok := got[directive]
		if !ok {
			t.Fatalf("CSP missing directive %q; got %v", directive, got)
		}
		if gotSources != sources {
			t.Fatalf("CSP directive %q source list = %q, want %q", directive, gotSources, sources)
		}
	}
	for directive := range got {
		if _, ok := want[directive]; !ok {
			t.Fatalf("CSP has extra directive %q not in the §27.7 spec block: %v", directive, got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("CSP has %d directives, want %d: got %v", len(got), len(want), got)
	}
}

// parseCSP parses a Content-Security-Policy header value into a map
// of directive name to its source-list string, trimming whitespace
// introduced by "; " separators.
func parseCSP(t *testing.T, header string) map[string]string {
	t.Helper()
	if header == "" {
		t.Fatalf("Content-Security-Policy header is empty")
	}
	directives := make(map[string]string)
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.SplitN(part, " ", 2)
		name := fields[0]
		sources := ""
		if len(fields) == 2 {
			sources = fields[1]
		}
		if _, dup := directives[name]; dup {
			t.Fatalf("CSP header repeats directive %q: %q", name, header)
		}
		directives[name] = sources
	}
	return directives
}

// spec: 27.5 (the gatekeeper endpoints exist only in their applicable mode)
// diagnosis: GET /playground/auth/login was reachable in a non-oidc
//
//	mode, or absent in oidc mode. The §27.3.1 login/callback/logout
//	endpoints are oidc-mode-specific; inspect the mode guard at the
//	top of handleLogin.
func TestLoginEndpointIsOIDCModeOnly(t *testing.T) {
	// dev mode: the login endpoint returns 404.
	devH := newPlayground(t, playground.AuthModeDev)
	devSrv := httptest.NewServer(devH.PlaygroundRoutes())
	defer devSrv.Close()
	devResp, err := http.Get(devSrv.URL + "/playground/auth/login")
	if err != nil {
		t.Fatalf("GET login (dev): %v", err)
	}
	_ = devResp.Body.Close()
	if devResp.StatusCode != http.StatusNotFound {
		t.Fatalf("dev-mode login status = %d, want 404", devResp.StatusCode)
	}
}

// wsRoundTrip writes one JSON-RPC 2.0 request frame over conn and returns
// the decoded response frame.
func wsRoundTrip(t *testing.T, ctx context.Context, conn *websocket.Conn, method string) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	})
	if err != nil {
		t.Fatalf("marshal %s frame: %v", method, err)
	}
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatalf("write %s frame: %v", method, err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read %s response: %v", method, err)
	}
	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal %s response: %v; frame %s", method, err, data)
	}
	return resp
}

// wsInitialize sends the §4.1 initialize request over conn and fails the
// test if the gateway does not answer with a well-formed result frame,
// proving the connection is genuinely live under whatever credential
// authenticated the upgrade rather than merely accepted at the TCP level.
func wsInitialize(t *testing.T, ctx context.Context, conn *websocket.Conn) {
	t.Helper()
	resp := wsRoundTrip(t, ctx, conn, "initialize")
	if _, isErr := resp["error"]; isErr {
		t.Fatalf("initialize returned an error frame: %v", resp)
	}
	if _, ok := resp["result"].(map[string]any); !ok {
		t.Fatalf("initialize response missing result: %v", resp)
	}
}

// spec: 27.3 (spec/27_web-playground.md:139) — "`reusable: true` indicates
// the bearer MAY be reused across any number of concurrent MCP WebSocket
// connections for the same user within its TTL — opening a second chat tab
// in the same browser does not require a second exchange. The server does
// not track or limit concurrent WebSocket count against a single bearer."
//
// diagnosis: A second /mcp/v1/ws upgrade presenting the same still-valid
// playground bearer as an already-open connection was rejected, or opening
// it disturbed the first connection. Reusability is asserted nowhere else
// in the suite: every other WebSocket-auth test dials once per bearer. If
// this test fails, either the real production auth middleware
// (pkg/gateway/middleware/auth) started tracking single-use bearers (a
// behavior the spec explicitly disclaims), or the §27.5.4 revocation watch
// wiring in pkg/gateway/mcpfabric/mcp is closing a sibling connection it
// should not touch.
func TestMintedBearerIsReusableAcrossConcurrentWebSocketConnections(t *testing.T) {
	signer := devSigner()
	cfg := playground.Config{
		Enabled: true, AuthMode: playground.AuthModeDev, DevTenantID: "acme",
		BearerTTL: 900 * time.Second, OIDCSessionTTL: time.Hour,
	}
	h := playground.New(cfg, playground.Options{
		Signer:   signer,
		Verifier: signer,
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
		Sessions: playground.NewMemorySessionStore(),
	})
	pgSrv := httptest.NewServer(h.TokenRoutes())
	defer pgSrv.Close()

	// Mint exactly one playground bearer, the same way a single browser tab
	// exchanges its session cookie for a bearer once.
	resp, raw := postJSON(t, pgSrv.URL+"/v1/playground/token", "{}", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mint status = %d, want 200; body=%s", resp.StatusCode, raw)
	}
	var minted struct {
		BearerToken string `json:"bearerToken"`
		Reusable    bool   `json:"reusable"`
	}
	if err := json.Unmarshal(raw, &minted); err != nil {
		t.Fatalf("decode mint body: %v", err)
	}
	if !minted.Reusable {
		t.Fatalf("mint response reusable = %v, want true", minted.Reusable)
	}

	// The real /mcp/v1/ws production wiring: the standard §10.2 auth
	// middleware (validating the same bearer chain any non-playground MCP
	// client goes through, per §27.3 "the gateway validates the bearer
	// exactly as it would for any non-playground MCP client") in front of
	// the real MCP WebSocket transport, with the §27.5.4 revocation watch
	// wired from the auth-middleware principal exactly as
	// cmd/lenny-gateway/httpsurface.go wires it.
	mcpSrv := mcp.NewServer()
	mcpSrv.SetWebSocketAuth(func(r *http.Request) (mcp.WSPrincipal, bool) {
		p, ok := authmw.FromContext(r.Context())
		if !ok {
			return mcp.WSPrincipal{}, false
		}
		return mcp.WSPrincipal{Tenant: p.TenantID, JTI: p.JTI, Origin: p.Origin}, true
	}, h, 0)
	wsHandler := authmw.Wrap(mcpSrv.WebSocketHandler(), authmw.Options{
		Verifier:    signer,
		MultiTenant: true,
		Registry:    fakeTenants{registered: map[string]bool{"acme": true}},
		RequireAuth: true,
	})
	wsSrv := httptest.NewServer(wsHandler)
	defer wsSrv.Close()
	wsURL := "ws" + strings.TrimPrefix(wsSrv.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dial := func() *websocket.Conn {
		t.Helper()
		conn, upgradeResp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + minted.BearerToken}},
		})
		if err != nil {
			status := "n/a"
			if upgradeResp != nil {
				status = upgradeResp.Status
			}
			t.Fatalf("dial /mcp/v1/ws with the minted bearer: %v (upgrade response status %s)", err, status)
		}
		return conn
	}

	// Two concurrent connections presenting the identical bearer. Both must
	// be accepted: the spec disclaims any server-side tracking of
	// concurrent WebSocket count per bearer.
	connA := dial()
	defer func() { _ = connA.Close(websocket.StatusNormalClosure, "") }()
	connB := dial()
	defer func() { _ = connB.Close(websocket.StatusNormalClosure, "") }()

	wsInitialize(t, ctx, connA)
	wsInitialize(t, ctx, connB)

	// Closing one connection must not disturb the sibling connection
	// carrying the same bearer.
	if err := connA.Close(websocket.StatusNormalClosure, "done"); err != nil {
		t.Fatalf("close connA: %v", err)
	}
	pingResp := wsRoundTrip(t, ctx, connB, "ping")
	if _, ok := pingResp["result"]; !ok {
		t.Fatalf("connB ping after connA closed = %v, want a result frame (the sibling connection must stay live)", pingResp)
	}
}
