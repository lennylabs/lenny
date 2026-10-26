// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// spec: 27.3 (Authentication) the `playground.authMode=apiKey` bullet, 27.3.1
// (OIDC cookie-to-MCP-bearer exchange) the **Auth by mode** `apiKey` row,
// 10.2 (Authentication) the Client → Gateway boundary
// diagnosis: a 401 UNAUTHORIZED for the pasted IdP ID token means the live
// cmd/lenny-gateway binary verifies the apiKey-mode bearer with the Lenny
// key set alone: check that buildHTTPSurface passes w.bearerVerifier, the
// verifier composeBearerVerifier builds with the IdP member, as
// playground.Options.Verifier. A 200 for the foreign issuer's or the wrong
// audience's token means the playground admits a credential the standard
// auth chain refuses.
func TestLivePlaygroundAPIKeyMintAdmitsIdPToken_spec_27_3(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	idp := oidcstub.New(t)
	foreign := oidcstub.New(t)
	gw := gateway.StartWith(
		t,
		"--no-environment-policy", "allow-all",
		"--dev-mode=false",
		"--oidc-issuer-url="+idp.Issuer(),
		"--oidc-client-id=lenny-gateway",
		"--playground-enabled",
		"--playground-auth-mode=apiKey",
	)
	mint := func(tok string) postureResponse {
		return postureDo(t, http.MethodPost, gw.BaseURL()+"/v1/playground/token",
			map[string]string{"Authorization": "Bearer " + tok}, "{}")
	}
	alice := oidcstub.MintOptions{Subject: "alice@acme.com", TenantID: "default", Audience: "lenny-gateway"}

	got := mint(idp.MintToken(alice))
	if got.status != http.StatusOK {
		t.Fatalf("apiKey mint with an IdP ID token: status %d code %q (%s)\ngateway logs:\n%s",
			got.status, got.code, got.body, gw.Logs())
	}
	var body struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil || body.BearerToken == "" {
		t.Errorf("apiKey mint with an IdP ID token returned no bearerToken (decode err %v): %s", err, got.body)
	}

	wrongAud := alice
	wrongAud.Audience = "other-client"
	for _, c := range []struct{ name, token string }{
		{"foreign issuer", foreign.MintToken(alice)},
		{"wrong audience", idp.MintToken(wrongAud)},
	} {
		if got := mint(c.token); got.status != http.StatusUnauthorized {
			t.Errorf("%s: status %d code %q, want 401 (%s)", c.name, got.status, got.code, got.body)
		}
	}
}
