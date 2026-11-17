// SPDX-License-Identifier: MIT

//go:build contract

package rest_auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// oidcTenantClaim is a non-default auth.tenantIdClaim, as an IdP that
// stamps tenant identity under a namespaced claim requires.
const oidcTenantClaim = "https://acme.example/tenant"

// newOIDCServer serves POST /v1/sessions behind the auth middleware with
// the bearer verifier the gateway composes for an upstream IdP: the
// discovered-JWKS verifier wrapped to read the tenant from
// oidcTenantClaim, in multi-tenant mode with acme registered.
func newOIDCServer(t *testing.T, idp *oidcstub.Stub) *httptest.Server {
	t.Helper()
	v, err := jwt.NewOIDCVerifier(jwt.OIDCConfig{IssuerURL: idp.Issuer(), Audience: "lenny-gateway"})
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	ts := httptest.NewServer(authmw.Wrap(srv.Handler(), authmw.Options{
		Verifier:    jwt.NewTenantClaimVerifier(v, oidcTenantClaim),
		MultiTenant: true,
		Registry:    &fakeRegistry{registered: map[string]bool{"acme": true}},
		RequireAuth: true,
	}))
	t.Cleanup(ts.Close)
	return ts
}

// spec: 10.2 (Authentication: the Client → Gateway boundary "OIDC/OAuth 2.1
// (MCP-standard protected resource server)", "`tenant_id` extraction")
// diagnosis: a failure on the accepted row means the gateway auth chain
// cannot authenticate an ID token the deployer's IdP signed: discovery,
// the JWK Set fetch, the RS256 check, or the string-aud decoding in
// jwt.OIDCVerifier is broken, or the tenant was not read from the
// configured claim. A failure on a refused row means an IdP token with
// the wrong audience, issuer, lifetime, or tenant claim reached the
// handler, or the tenant-claim rejection codes regressed for IdP tokens.
func TestOIDCBearerAuthenticatesThroughDiscoveredJWKS_spec_10_2(t *testing.T) {
	idp := oidcstub.New(t)
	foreign := oidcstub.New(t)
	ts := newOIDCServer(t, idp)

	mint := func(s *oidcstub.Stub, mutate func(*oidcstub.MintOptions)) string {
		opts := oidcstub.MintOptions{
			Subject: "alice@acme.com",
			Extra:   map[string]any{oidcTenantClaim: "acme"},
		}
		if mutate != nil {
			mutate(&opts)
		}
		return s.MintToken(opts)
	}

	resp, body := do(t, ts, map[string]string{"Authorization": "Bearer " + mint(idp, nil)},
		map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("IdP ID token: status %d (body %v), want 201", resp.StatusCode, body)
	}
	if body["tenantId"] != "acme" {
		t.Errorf("IdP ID token: tenantId %v, want acme from %s", body["tenantId"], oidcTenantClaim)
	}

	for _, c := range []struct {
		name   string
		token  string
		status int
		code   string
	}{
		{"foreign issuer", mint(foreign, nil), http.StatusUnauthorized, ""},
		{"wrong audience", mint(idp, func(o *oidcstub.MintOptions) { o.Audience = "other-client" }), http.StatusUnauthorized, ""},
		{"expired", mint(idp, func(o *oidcstub.MintOptions) { o.Lifetime = -time.Minute }), http.StatusUnauthorized, ""},
		{"tenant claim absent", mint(idp, func(o *oidcstub.MintOptions) { o.Extra = nil; o.TenantID = "acme" }), http.StatusUnauthorized, "TENANT_CLAIM_MISSING"},
		{"tenant unregistered", mint(idp, func(o *oidcstub.MintOptions) { o.Extra = map[string]any{oidcTenantClaim: "globex"} }), http.StatusForbidden, "TENANT_NOT_FOUND"},
	} {
		resp, body := do(t, ts, map[string]string{"Authorization": "Bearer " + c.token},
			map[string]any{"runtimeRef": "claude-code"})
		if resp.StatusCode != c.status {
			t.Errorf("%s: status %d (body %v), want %d", c.name, resp.StatusCode, body, c.status)
			continue
		}
		if c.code != "" {
			envelope, _ := body["error"].(map[string]any)
			if envelope["code"] != c.code {
				t.Errorf("%s: error code %v, want %s", c.name, envelope["code"], c.code)
			}
		}
	}
}
