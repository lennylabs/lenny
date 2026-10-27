// SPDX-License-Identifier: MIT

package main

import (
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// TestBuildTokenSigningStoresTrustsTheUpstreamIdP_spec_10_2 drives the
// gateway startup step with the §10.3 OIDC keys pointed at an OIDC stub.
// The bearer verifier admits a token the IdP signed, resolves its tenant
// from --tenant-id-claim, and normalizes it to user_bearer, while the
// --bearer-expected-issuer check keeps applying to Lenny-issued tokens
// only. An IdP token for another audience is refused, and a gateway
// whose issuer has no client id, or that names no issuer, trusts no IdP.
//
// spec: 10.2 (Authentication) the Client → Gateway boundary, "`tenant_id`
// extraction", and the `user_bearer` type; 10.3 (Gateway startup
// configuration validation) `auth.oidc.issuerUrl` and `auth.oidc.clientId`
func TestBuildTokenSigningStoresTrustsTheUpstreamIdP_spec_10_2(t *testing.T) {
	idp := oidcstub.New(t)
	master := filepath.Join(t.TempDir(), "master.key")
	const tenantClaim = "https://acme.example/tenant"

	build := func(issuer, clientID string) *gatewayWiring {
		f := tokenSigningFlags(master, "", "https://lenny.acme.com", "lenny")
		claim := tenantClaim
		f.tenantIDClaim = &claim
		f.oidcIssuerURL = &issuer
		f.oidcClientID = &clientID
		w := &gatewayWiring{f: f}
		w.buildTokenSigningStores(nil)
		return w
	}
	idpToken := func(aud string) string {
		return idp.MintToken(oidcstub.MintOptions{
			Subject:  "alice@acme.com",
			Audience: aud,
			Extra:    map[string]any{tenantClaim: "acme", "typ": "ID"},
		})
	}

	w := build(idp.Issuer(), "lenny-gateway")
	claims, err := w.bearerVerifier.Verify(idpToken("lenny-gateway"))
	if err != nil {
		t.Fatalf("an IdP-issued bearer is refused: %v", err)
	}
	if claims.Subject != "alice@acme.com" || claims.TenantID != "acme" || claims.Typ != auth.TokenUserBearer {
		t.Errorf("IdP bearer resolved sub=%q tenant=%q typ=%q, want alice@acme.com acme user_bearer",
			claims.Subject, claims.TenantID, claims.Typ)
	}
	if _, err := w.bearerVerifier.Verify(idpToken("other-client")); err == nil {
		t.Error("an IdP bearer for another audience is admitted")
	}
	if _, err := w.bearerVerifier.Verify(signedClaims(t, w.jwtSigner, "https://lenny.acme.com", "lenny")); err != nil {
		t.Errorf("a key-set token with the expected issuer is refused once the IdP member joins: %v", err)
	}
	if _, err := w.bearerVerifier.Verify(signedClaims(t, w.jwtSigner, "https://other.example", "lenny")); err == nil {
		t.Error("a key-set token from an unexpected issuer is admitted once the IdP member joins")
	}

	for _, c := range []struct{ name, issuer, clientID string }{
		{"issuer without client id", idp.Issuer(), ""},
		{"no issuer", "", "lenny-gateway"},
	} {
		w := build(c.issuer, c.clientID)
		if _, err := w.bearerVerifier.Verify(idpToken("lenny-gateway")); err == nil {
			t.Errorf("%s: an IdP-issued bearer is admitted", c.name)
		}
	}
}
