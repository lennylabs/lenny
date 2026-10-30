// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

const discoveryTenantClaim = "https://acme.example/tenant"

// discoveredExchanger returns the HTTP exchanger over the stub's discovery
// document, with ID tokens validated by a jwt.OIDCVerifier for audience.
func discoveredExchanger(t *testing.T, idp *oidcstub.Stub, audience string) OIDCExchanger {
	t.Helper()
	v, err := jwt.NewOIDCVerifier(jwt.OIDCConfig{IssuerURL: idp.Issuer(), Audience: audience})
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}
	return NewHTTPOIDCExchanger(HTTPOIDCConfig{
		IssuerURL:        idp.Issuer(),
		ClientID:         "lenny-playground",
		TenantIDClaim:    discoveryTenantClaim,
		IDTokenValidator: IDTokenValidatorFromVerifier(v),
	})
}

// noRedirect is a client that returns redirects instead of following them.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// authorize follows an authorization URL to the stub and returns the code
// and state the stub redirects back with.
func authorize(t *testing.T, authURL string) (code, state string) {
	t.Helper()
	resp, err := noRedirect.Get(authURL)
	if err != nil {
		t.Fatalf("GET authorization URL: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorization endpoint status %d, want 302", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	return loc.Query().Get("code"), loc.Query().Get("state")
}

// spec: 27.3.1 (OIDC cookie-to-MCP-bearer exchange) `GET /playground/auth/login`
// and `GET /playground/auth/callback`
//
// The exchanger takes the authorization and token endpoints from the
// issuer's discovery document, sends an S256 PKCE challenge, redeems the
// code with the verifier, validates the ID token, and extracts the user and
// the tenant from the configured claim. A wrong verifier, an ID token for
// another audience, and an unreachable or mismatched discovery document
// each fail with an *OIDCError or an error rather than a subject.
func TestDiscoveredOIDCExchangerRunsTheAuthorizationCodeFlow_spec_27_3_1(t *testing.T) {
	ctx := context.Background()
	idp := oidcstub.New(t)
	idp.SetLoginIdentity(oidcstub.MintOptions{Subject: "alice@acme.com", Extra: map[string]any{discoveryTenantClaim: "acme"}})
	const redirect = "https://gw.acme.com/playground/auth/callback"
	verifier, err := generateCodeVerifier()
	if err != nil {
		t.Fatalf("generateCodeVerifier: %v", err)
	}

	x := discoveredExchanger(t, idp, "lenny-playground")
	authURL, err := x.AuthorizationURL(ctx, "state-1", codeChallengeS256(verifier), redirect)
	if err != nil {
		t.Fatalf("AuthorizationURL: %v", err)
	}
	if !strings.HasPrefix(authURL, idp.Issuer()+"/authorize?") {
		t.Fatalf("AuthorizationURL = %s, want the discovered authorization endpoint", authURL)
	}
	code, state := authorize(t, authURL)
	if state != "state-1" || code == "" {
		t.Fatalf("authorization redirect carried code %q state %q", code, state)
	}
	subject, err := x.Exchange(ctx, code, verifier, redirect)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if subject.UserID != "alice@acme.com" || subject.TenantID != "acme" {
		t.Errorf("subject = %+v, want alice@acme.com in acme", subject)
	}

	// A wrong PKCE verifier is refused by the provider.
	authURL, _ = x.AuthorizationURL(ctx, "state-2", codeChallengeS256(verifier), redirect)
	code, _ = authorize(t, authURL)
	var oe *OIDCError
	if _, err := x.Exchange(ctx, code, verifier+"-wrong", redirect); !errors.As(err, &oe) || oe.Code != "oidc_exchange_failed" {
		t.Errorf("wrong verifier: err %v, want OIDCError oidc_exchange_failed", err)
	}

	// An ID token whose audience is not the validator's is refused.
	strict := discoveredExchanger(t, idp, "another-client")
	authURL, _ = strict.AuthorizationURL(ctx, "state-3", codeChallengeS256(verifier), redirect)
	code, _ = authorize(t, authURL)
	if _, err := strict.Exchange(ctx, code, verifier, redirect); !errors.As(err, &oe) || oe.Code != "oidc_id_token_invalid" {
		t.Errorf("foreign audience ID token: err %v, want OIDCError oidc_id_token_invalid", err)
	}

	// An unreachable provider and a discovery document naming another
	// issuer fail both calls.
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	mismatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"https://elsewhere.example","authorization_endpoint":"https://elsewhere.example/a","token_endpoint":"https://elsewhere.example/t"}`))
	}))
	defer mismatch.Close()
	for name, issuer := range map[string]string{"unreachable": down.URL, "issuer mismatch": mismatch.URL} {
		bad := NewHTTPOIDCExchanger(HTTPOIDCConfig{IssuerURL: issuer, ClientID: "lenny-playground", IDTokenValidator: IDTokenValidatorFromVerifier(nil)})
		if u, err := bad.AuthorizationURL(ctx, "s", "c", redirect); err == nil {
			t.Errorf("%s: AuthorizationURL = %s, want an error", name, u)
		}
		if _, err := bad.Exchange(ctx, "code", verifier, redirect); !errors.As(err, &oe) || oe.Code != "oidc_exchange_failed" {
			t.Errorf("%s: Exchange err %v, want OIDCError oidc_exchange_failed", name, err)
		}
	}
}

// spec: 27.3.1 (OIDC cookie-to-MCP-bearer exchange) `GET /playground/auth/login`,
// `GET /playground/auth/callback`, and `POST /v1/playground/token`, 27.3
// (Authentication) the `playground.authMode=oidc` bullet
//
// An oidc-mode playground wired to the discovered provider signs a user in:
// login redirects to the provider with the PKCE challenge and sets the
// state cookie, the callback exchanges the code and sets the session
// cookie, and the cookie mints an origin "playground" bearer for the user
// and tenant the ID token names. With the provider unreachable, login
// answers 503 LENNY_PLAYGROUND_OIDC_UNAVAILABLE.
func TestOIDCModeSignsInThroughTheDiscoveredProvider_spec_27_3_1(t *testing.T) {
	idp := oidcstub.New(t)
	idp.SetLoginIdentity(oidcstub.MintOptions{Subject: "alice@acme.com", Extra: map[string]any{discoveryTenantClaim: "acme"}})
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC}, Options{
		Signer:   jwtsign.HMAC(devSigner()),
		Verifier: devSigner(),
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
		Sessions: NewMemorySessionStore(),
		OIDC:     discoveredExchanger(t, idp, "lenny-playground"),
	})
	mux := http.NewServeMux()
	mux.Handle("/playground/", h.PlaygroundRoutes())
	mux.Handle("/v1/playground/", h.TokenRoutes())
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := noRedirect.Get(srv.URL + "/playground/auth/login")
	if err != nil {
		t.Fatalf("GET login: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login status %d, want 302", resp.StatusCode)
	}
	var stateCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == oidcStateCookie {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("login set no state cookie")
	}
	code, state := authorize(t, resp.Header.Get("Location"))

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/playground/auth/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), nil)
	req.AddCookie(stateCookie)
	// The callback URL the handler derives must match the one login sent,
	// so the test presents the same Host and scheme.
	resp, err = noRedirect.Do(req)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	_ = resp.Body.Close()
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			session = c
		}
	}
	if resp.StatusCode != http.StatusFound || session == nil {
		t.Fatalf("callback status %d Location %q session cookie %v, want 302 with a session cookie",
			resp.StatusCode, resp.Header.Get("Location"), session)
	}

	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(session)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cookie mint status %d, want 200", resp.StatusCode)
	}

	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	offline := New(Config{Enabled: true, AuthMode: AuthModeOIDC}, Options{
		Signer:   jwtsign.HMAC(devSigner()),
		Verifier: devSigner(),
		Sessions: NewMemorySessionStore(),
		OIDC:     NewHTTPOIDCExchanger(HTTPOIDCConfig{IssuerURL: down.URL, ClientID: "lenny-playground", IDTokenValidator: IDTokenValidatorFromVerifier(nil)}),
	})
	offSrv := httptest.NewServer(offline.PlaygroundRoutes())
	defer offSrv.Close()
	resp, err = noRedirect.Get(offSrv.URL + "/playground/auth/login")
	if err != nil {
		t.Fatalf("GET login (provider down): %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("login with the provider down: status %d, want 503", resp.StatusCode)
	}
}

// spec: 27.3.1 (OIDC cookie-to-MCP-bearer exchange) "validates the returned
// ID token (signature, `iss`, `aud`, `exp`, `nbf`)"
//
// IDTokenValidatorFromVerifier returns the claims of a token the verifier
// accepts, including claims outside jwt.Claims such as a custom tenant
// claim, and refuses a token the verifier refuses, a malformed token, and
// any token when no verifier is configured.
func TestIDTokenValidatorFromVerifier_spec_27_3_1(t *testing.T) {
	ctx := context.Background()
	idp := oidcstub.New(t)
	v, err := jwt.NewOIDCVerifier(jwt.OIDCConfig{IssuerURL: idp.Issuer(), Audience: "lenny-playground"})
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}
	validate := IDTokenValidatorFromVerifier(v)
	claims, err := validate(ctx, idp.MintToken(oidcstub.MintOptions{
		Subject: "alice@acme.com", Audience: "lenny-playground", Extra: map[string]any{discoveryTenantClaim: "acme"},
	}))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if claims["sub"] != "alice@acme.com" || claims[discoveryTenantClaim] != "acme" {
		t.Errorf("claims = %v, want sub alice@acme.com and the tenant claim", claims)
	}
	if _, err := validate(ctx, idp.MintToken(oidcstub.MintOptions{Subject: "alice@acme.com", Audience: "other"})); err == nil {
		t.Error("an ID token for another audience validates")
	}
	if _, err := validate(ctx, "not-a-jwt"); err == nil {
		t.Error("a malformed token validates")
	}
	if _, err := IDTokenValidatorFromVerifier(nil)(ctx, "a.b.c"); err == nil {
		t.Error("a nil verifier validates a token")
	}
}
