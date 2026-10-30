// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// spec: 10.2 (Authentication: the Client → Gateway boundary "OIDC/OAuth 2.1
// (MCP-standard protected resource server)" and the `user_bearer` type),
// 10.3 (Gateway startup configuration validation: `auth.oidc.issuerUrl`,
// `auth.oidc.clientId`)
// diagnosis: a failure on the admitted requests means the live
// cmd/lenny-gateway binary outside dev mode refuses an ID token the
// deployer's IdP signed, so the primary client authentication boundary
// accepts only Lenny-signed tokens: check that buildIdPVerifier builds the
// jwt.OIDCVerifier from --oidc-issuer-url and --oidc-client-id and that
// composeBearerVerifier joins it to the bearer verifier. A failure on the
// refused requests means an IdP token for another audience, from another
// issuer, or past its exp reached a handler.
func TestLiveGatewayAuthenticatesUpstreamOIDCBearer_spec_10_2(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)
	idp := oidcstub.New(t)
	foreign := oidcstub.New(t)
	gw := gateway.StartWith(
		t,
		"--no-environment-policy", "allow-all",
		"--dev-mode=false",
		"--oidc-issuer-url="+idp.Issuer(),
		"--oidc-client-id=lenny-gateway",
	)
	base := gw.BaseURL()
	bearer := func(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }
	alice := oidcstub.MintOptions{Subject: "alice@acme.com", TenantID: "default", Audience: "lenny-gateway"}

	got := postureDo(t, http.MethodGet, base+"/v1/admin/me", bearer(idp.MintToken(alice)), "")
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/admin/me with an IdP ID token: status %d code %q, want 200 (%s)\ngateway logs:\n%s",
			got.status, got.code, got.body, gw.Logs())
	}
	var me struct {
		Subject  string `json:"subject"`
		TenantID string `json:"tenantId"`
	}
	if err := json.Unmarshal([]byte(got.body), &me); err != nil || me.Subject != "alice@acme.com" || me.TenantID != "default" {
		t.Errorf("GET /v1/admin/me: subject %q tenantId %q (decode err %v), want alice@acme.com in default", me.Subject, me.TenantID, err)
	}
	// The session-creation route admits the bearer: whatever the handler
	// answers, the auth chain does not refuse it.
	if got := postureDo(t, http.MethodPost, base+"/v1/sessions", bearer(idp.MintToken(alice)), `{"runtime":"echo"}`); got.status == http.StatusUnauthorized {
		t.Errorf("POST /v1/sessions with an IdP ID token: status 401 code %q (%s)", got.code, got.body)
	}

	wrongAud := alice
	wrongAud.Audience = "other-client"
	expired := alice
	expired.Lifetime = -time.Minute
	for _, c := range []struct {
		name  string
		token string
	}{
		{"foreign issuer", foreign.MintToken(alice)},
		{"wrong audience", idp.MintToken(wrongAud)},
		{"expired", idp.MintToken(expired)},
	} {
		if got := postureDo(t, http.MethodGet, base+"/v1/admin/me", bearer(c.token), ""); got.status != http.StatusUnauthorized {
			t.Errorf("%s: status %d code %q, want 401 (%s)", c.name, got.status, got.code, got.body)
		}
	}
}
