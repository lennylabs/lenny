//go:build integration

// SPDX-License-Identifier: MIT

package tier4_integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// spec: 13.3 (Credential Flow) "Client authentication on /v1/oauth/token", 10.2 (Authentication) "`tenant_id` extraction"
// diagnosis: a 401 invalid_client for the IdP-signed caller means the
// cmd/lenny-token-service binary authenticates the /v1/oauth/token caller
// with the Lenny key set alone: check that buildCallerVerifier builds the
// jwt.OIDCVerifier from --oidc-issuer-url and --oidc-client-id and that
// buildServer passes it as Options.CallerVerifier. A 200 for the foreign
// issuer's caller means the IdP member does not check the issuer. A 200
// for the IdP token as the subject_token means the IdP member leaked onto
// the subject verifier, which §13.3 does not name.
func TestLiveTokenServiceAdmitsIdPCallerToken_spec_13_3(t *testing.T) {
	t.Parallel()
	tokensvc.SkipUnlessAvailable(t)
	idp := oidcstub.New(t)
	foreign := oidcstub.New(t)
	refs := map[string]string{kms.AliasJWTSigning: "local-key-1"}
	_, signer := stackkey.Signer(t, stackkey.MasterKeyFile(t), refs)
	ts := tokensvc.StartOpts(t, tokensvc.Options{
		Args: append(
			append([]string{"--kms-provider=local"}, aliasArgs(refs)...),
			"--oidc-issuer-url="+idp.Issuer(),
			"--oidc-client-id=lenny-gateway",
		),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	subject, err := signer.Sign(ctx, rotationCallerClaims("idp-subject"))
	if err != nil {
		t.Fatalf("sign subject: %v", err)
	}
	dave := oidcstub.MintOptions{Subject: "dave@acme.com", TenantID: auth.DefaultTenantID, Audience: "lenny-gateway"}

	code, body := oauthExchange(t, ts.BaseURL(), idp.MintToken(dave), subject, "")
	tok, _ := body["access_token"].(string)
	if code != http.StatusOK || tok == "" {
		t.Fatalf("IdP caller: status %d body %v, want 200 with an access_token\n%s", code, body, ts.Logs())
	}

	if code, body := oauthExchange(t, ts.BaseURL(), foreign.MintToken(dave), subject, ""); code != http.StatusUnauthorized || body["error"] != "invalid_client" {
		t.Errorf("foreign issuer caller: status %d body %v, want 401 invalid_client", code, body)
	}
	lennyCaller, err := signer.Sign(ctx, rotationCallerClaims("idp-lenny-caller"))
	if err != nil {
		t.Fatalf("sign caller: %v", err)
	}
	if code, body := oauthExchange(t, ts.BaseURL(), lennyCaller, idp.MintToken(dave), ""); code != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("IdP token as subject_token: status %d body %v, want 400 invalid_grant", code, body)
	}
}
