// SPDX-License-Identifier: MIT

package main

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// idpFlags returns the Token Service IdP flags with the given issuer,
// client id, and tenant claim, and the package defaults for the rest.
func idpFlags(issuer, clientID, tenantClaim string) *tokenServiceFlags {
	refresh, minRefetch, timeout := jwt.DefaultOIDCKeyRefreshInterval, jwt.DefaultOIDCMinRefetchInterval, jwt.DefaultOIDCFetchTimeout
	return &tokenServiceFlags{
		oidcIssuerURL:              &issuer,
		oidcClientID:               &clientID,
		tenantIDClaim:              &tenantClaim,
		oidcJWKSRefreshInterval:    &refresh,
		oidcJWKSMinRefetchInterval: &minRefetch,
		oidcFetchTimeout:           &timeout,
	}
}

// TestBuildCallerVerifierAdmitsTheUpstreamIdP_spec_13_3 builds the caller
// verifier from the IdP flags pointed at an OIDC stub. It admits an ID
// token the IdP signed for the configured client, resolves the tenant from
// --tenant-id-claim, normalizes the token to user_bearer, and keeps
// admitting a Lenny-issued token. An IdP token for another audience is
// refused, and an issuer without a client id or no issuer at all leaves
// the key set as the only member.
//
// spec: 13.3 (Credential Flow) "Client authentication on /v1/oauth/token",
// 10.2 (Authentication) "`tenant_id` extraction"
func TestBuildCallerVerifierAdmitsTheUpstreamIdP_spec_13_3(t *testing.T) {
	idp := oidcstub.New(t)
	const tenantClaim = "https://acme.example/tenant"
	lenny := jwt.NewHMACSigner("lenny-1", []byte("lenny-test-key"))
	lennyToken, err := lenny.Sign(jwt.Claims{
		Subject: "bob@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Audience: []string{"lenny-gateway"}, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign Lenny token: %v", err)
	}
	idpToken := func(aud string) string {
		return idp.MintToken(oidcstub.MintOptions{
			Subject: "alice@acme.com", Audience: aud,
			Extra: map[string]any{tenantClaim: "acme"},
		})
	}

	v, err := buildCallerVerifier(idpFlags(idp.Issuer(), "lenny-gateway", tenantClaim), lenny)
	if err != nil {
		t.Fatalf("buildCallerVerifier: %v", err)
	}
	claims, err := v.Verify(idpToken("lenny-gateway"))
	if err != nil {
		t.Fatalf("an IdP caller token is refused: %v", err)
	}
	if claims.Subject != "alice@acme.com" || claims.TenantID != "acme" || claims.Typ != auth.TokenUserBearer {
		t.Errorf("IdP caller resolved sub=%q tenant=%q typ=%q, want alice@acme.com acme user_bearer",
			claims.Subject, claims.TenantID, claims.Typ)
	}
	if _, err := v.Verify(lennyToken); err != nil {
		t.Errorf("a Lenny-issued caller token is refused beside the IdP member: %v", err)
	}
	if _, err := v.Verify(idpToken("other-client")); err == nil {
		t.Error("an IdP caller token for another audience is admitted")
	}

	for _, c := range []struct{ name, issuer, clientID string }{
		{"issuer without client id", idp.Issuer(), ""},
		{"no issuer", "", "lenny-gateway"},
	} {
		v, err := buildCallerVerifier(idpFlags(c.issuer, c.clientID, tenantClaim), lenny)
		if err != nil {
			t.Fatalf("%s: buildCallerVerifier: %v", c.name, err)
		}
		if _, err := v.Verify(idpToken("lenny-gateway")); err == nil {
			t.Errorf("%s: an IdP caller token is admitted", c.name)
		}
		if _, err := v.Verify(lennyToken); err != nil {
			t.Errorf("%s: a Lenny-issued caller token is refused: %v", c.name, err)
		}
	}

	if _, err := buildCallerVerifier(idpFlags("not a url", "lenny-gateway", tenantClaim), lenny); err == nil {
		t.Error("buildCallerVerifier accepts an issuer that is not an absolute URL")
	}
}
