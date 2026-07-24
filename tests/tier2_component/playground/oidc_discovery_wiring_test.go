//go:build component

// SPDX-License-Identifier: MIT

// Component-tier suite for the §27.3.1 OIDC discovery wiring the
// gateway performs when playground.authMode=oidc. TESTING.md names
// the OIDC stub as a tier-2 component-tier backing service (§3, tier
// 2 "Backing services" column). This suite wires
// playground.NewDiscoveredHTTPOIDCExchanger — the constructor
// cmd/lenny-gateway/httpsurface.go calls to build
// playground.Options.OIDC when playground.authMode=oidc — against a
// real running OIDC stub over real HTTP (discovery document, JWKS
// fetch, RS256 signature verification), then confirms GET
// /playground/auth/login redirects to the discovered authorization
// endpoint rather than serving the LENNY_PLAYGROUND_OIDC_UNAVAILABLE
// 503 a nil Options.OIDC produces.
package playground_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// noRedirectClient returns each hop's response instead of following
// Location, so the test can inspect the redirect the handler or the
// stub provider issues.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// spec: §27.3.1 ("GET /playground/auth/login — Initiates the OIDC
// authorization-code flow. The gateway generates a per-login `state`
// and PKCE `code_verifier` ... and redirects the browser to the
// configured OIDC provider's authorization endpoint.")
// diagnosis: A failure here means the gateway's OIDC-discovery wiring
// (playground.NewDiscoveredHTTPOIDCExchanger, called from
// cmd/lenny-gateway/httpsurface.go when playground.authMode=oidc) no
// longer produces a working OIDCExchanger from a real provider's
// discovery document: either the constructor failed and left
// playground.Options.OIDC nil — which pkg/gateway/mcpfabric/
// playground/auth.go's handleLogin reports as 503
// LENNY_PLAYGROUND_OIDC_UNAVAILABLE instead of redirecting — or the
// discovered authorization_endpoint / PKCE parameters are wrong.
func TestDiscoveredOIDCExchangerLoginRedirectsToDiscoveredProvider(t *testing.T) {
	stub := oidcstub.New(t)

	exchanger, err := playground.NewDiscoveredHTTPOIDCExchanger(context.Background(), stub.Issuer(), "playground-component-test", nil)
	if err != nil {
		t.Fatalf("NewDiscoveredHTTPOIDCExchanger: %v", err)
	}

	h := playground.New(playground.Config{Enabled: true, AuthMode: playground.AuthModeOIDC}, playground.Options{
		Signer:   jwt.NewHMACSigner("pg-component-test", []byte("component-test-signing-secret")),
		Sessions: playground.NewMemorySessionStore(),
		OIDC:     exchanger,
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()

	resp, err := noRedirectClient().Get(srv.URL + "/playground/auth/login")
	if err != nil {
		t.Fatalf("GET /playground/auth/login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 (a nil Options.OIDC would instead return 503 LENNY_PLAYGROUND_OIDC_UNAVAILABLE)", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, stub.Issuer()+"/authorize") {
		t.Fatalf("Location = %q, want it to start with the discovered authorization_endpoint %q", loc, stub.Issuer()+"/authorize")
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect Location %q: %v", loc, err)
	}
	if got := u.Query().Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	if got := u.Query().Get("client_id"); got != "playground-component-test" {
		t.Errorf("client_id = %q, want playground-component-test", got)
	}
	if u.Query().Get("code_challenge") == "" {
		t.Error("code_challenge is empty, want a PKCE challenge")
	}
}

// spec: §27.3.1 ("performs the PKCE-protected token exchange with the
// provider, validates the returned ID token (signature, `iss`, `aud`,
// `exp`, `nbf`), extracts standard Lenny claims (`user_id`,
// `tenant_id` ...)")
// diagnosis: A failure here means the discovered exchanger's
// IDTokenValidator (built from the provider's real, fetched JWKS
// document by playground.NewDiscoveredHTTPOIDCExchanger) no longer
// performs a genuine RS256/JWKS-backed exchange end to end against a
// real provider: either a valid ID token from the real stub is
// rejected, or the extracted subject claims (user_id, tenant_id) are
// wrong.
func TestDiscoveredOIDCExchangerCompletesRealPKCEExchange(t *testing.T) {
	stub := oidcstub.New(t)
	exchanger, err := playground.NewDiscoveredHTTPOIDCExchanger(context.Background(), stub.Issuer(), "playground-component-test", nil)
	if err != nil {
		t.Fatalf("NewDiscoveredHTTPOIDCExchanger: %v", err)
	}

	const redirectURI = "https://gateway.acme.example/playground/auth/callback"
	state := "component-test-state"
	verifier := "component-test-verifier-0123456789abcdefghijklmno"
	challenge := oidcstub.ChallengeS256(verifier)

	authURL := exchanger.AuthorizationURL(state, challenge, redirectURI)
	resp, err := noRedirectClient().Get(authURL + "&sub=alice%40acme.com&tenant_id=acme")
	if err != nil {
		t.Fatalf("GET %s: %v", authURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("stub /authorize status = %d, want 302", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize redirect: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("stub /authorize issued no code")
	}

	subject, err := exchanger.Exchange(context.Background(), code, verifier, redirectURI)
	if err != nil {
		t.Fatalf("Exchange with a real discovered validator: %v", err)
	}
	if subject.UserID != "alice@acme.com" {
		t.Errorf("subject.UserID = %q, want alice@acme.com", subject.UserID)
	}
	if subject.TenantID != "acme" {
		t.Errorf("subject.TenantID = %q, want acme", subject.TenantID)
	}
}

// spec: §27.3.1 ("validates the returned ID token (signature, `iss`,
// `aud`, `exp`, `nbf`)")
// diagnosis: A failure here means the discovered validator accepted
// an ID token whose signature does not verify against the real
// provider's published JWKS, which would let a forged or tampered
// token establish a playground session.
func TestDiscoveredOIDCExchangerRejectsBadSignature(t *testing.T) {
	stub := oidcstub.New(t)
	exchanger, err := playground.NewDiscoveredHTTPOIDCExchanger(context.Background(), stub.Issuer(), "playground-component-test", nil)
	if err != nil {
		t.Fatalf("NewDiscoveredHTTPOIDCExchanger: %v", err)
	}

	const redirectURI = "https://gateway.acme.example/playground/auth/callback"
	state := "component-test-state"
	verifier := "component-test-verifier-0123456789abcdefghijklmno"
	challenge := oidcstub.ChallengeS256(verifier)

	authURL := exchanger.AuthorizationURL(state, challenge, redirectURI)
	resp, err := noRedirectClient().Get(authURL + "&bad=" + oidcstub.BadSignature)
	if err != nil {
		t.Fatalf("GET %s: %v", authURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize redirect: %v", err)
	}
	code := loc.Query().Get("code")

	if _, err := exchanger.Exchange(context.Background(), code, verifier, redirectURI); err == nil {
		t.Fatal("Exchange accepted an ID token with a corrupted signature, want a validation rejection")
	}
}
