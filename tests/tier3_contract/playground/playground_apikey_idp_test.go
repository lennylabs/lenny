// SPDX-License-Identifier: MIT

//go:build contract

package playground_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// spec: 27.3 (Authentication) the `playground.authMode=apiKey` bullet, 27.3.1
// (OIDC cookie-to-MCP-bearer exchange) the **Auth by mode** `apiKey` row, 10.2
// (Authentication) "`tenant_id` extraction"
// diagnosis: a refused IdP ID token or Lenny user_bearer means the apiKey
// mint does not accept the credentials the standard Client → Gateway chain
// accepts when it is handed that chain's composed verifier (the key set
// joined by the IdP member under jwt.TenantClaimVerifier). A minted bearer
// without origin "playground" or with another subject or tenant means the
// mint does not carry the pasted credential's identity. An IdP token with
// no tenant claim minted in multi-tenant mode means the §10.2 tenant-claim
// rejection does not apply to IdP tokens.
func TestAPIKeyModeMintsFromIdPTokenAndUserBearer_spec_27_3(t *testing.T) {
	idp := oidcstub.New(t)
	const tenantClaim = "https://acme.example/tenant"
	oidc, err := jwt.NewOIDCVerifier(jwt.OIDCConfig{IssuerURL: idp.Issuer(), Audience: "lenny-gateway"})
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}
	lenny := devSigner()
	composed := jwt.NewMultiVerifier(lenny, jwt.NewTenantClaimVerifier(oidc, tenantClaim))
	cfg := playground.Config{Enabled: true, AuthMode: playground.AuthModeAPIKey, MultiTenant: true, CapabilityTokenTTL: 900 * time.Second}
	h := playground.New(cfg, playground.Options{
		Signer:   jwtsign.HMAC(lenny),
		Verifier: composed,
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
		Sessions: playground.NewMemorySessionStore(),
	})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	userBearer, err := lenny.Sign(jwt.Claims{
		Subject: "bob@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Audience: []string{"lenny-gateway"}, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign user bearer: %v", err)
	}
	idpToken := idp.MintToken(oidcstub.MintOptions{
		Subject: "alice@acme.com", Audience: "lenny-gateway",
		Extra: map[string]any{tenantClaim: "acme"},
	})
	for _, c := range []struct{ name, token, sub string }{
		{"IdP ID token", idpToken, "alice@acme.com"},
		{"Lenny user_bearer", userBearer, "bob@acme.com"},
	} {
		resp, raw := postJSON(t, srv.URL+"/v1/playground/token", "{}", map[string]string{"Authorization": "Bearer " + c.token})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d, want 200; body=%s", c.name, resp.StatusCode, raw)
			continue
		}
		var body struct {
			BearerToken string `json:"bearerToken"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || body.BearerToken == "" {
			t.Errorf("%s: no bearerToken (decode err %v): %s", c.name, err, raw)
			continue
		}
		claims := decodeClaims(t, body.BearerToken)
		if claims["sub"] != c.sub || claims["tenant_id"] != "acme" || claims["origin"] != "playground" {
			t.Errorf("%s: minted bearer sub %v tenant %v origin %v, want %s acme playground",
				c.name, claims["sub"], claims["tenant_id"], claims["origin"], c.sub)
		}
	}

	noTenant := idp.MintToken(oidcstub.MintOptions{Subject: "carol@acme.com", Audience: "lenny-gateway"})
	resp, raw := postJSON(t, srv.URL+"/v1/playground/token", "{}", map[string]string{"Authorization": "Bearer " + noTenant})
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode != http.StatusUnauthorized || env.Error.Code != "TENANT_CLAIM_MISSING" {
		t.Errorf("IdP token without the tenant claim: status %d code %q, want 401 TENANT_CLAIM_MISSING; body=%s",
			resp.StatusCode, env.Error.Code, raw)
	}
}
