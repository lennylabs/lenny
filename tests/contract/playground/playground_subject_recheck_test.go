// SPDX-License-Identifier: MIT

//go:build contract

package playground_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

// impersonationRows is a playground.IssuedTokenReader that reports every
// jti as a live platform-admin impersonation row.
type impersonationRows struct{}

func (impersonationRows) Get(_ context.Context, tenantID, jti string) (issuedtokenstore.IssuedToken, error) {
	return issuedtokenstore.IssuedToken{JTI: jti, TenantID: tenantID, Subject: "bob", ActSubject: "alice"}, nil
}

// spec: 10.2 (Playground mint invariants, invariant 1), 15.1 (REST API) the LENNY_PLAYGROUND_BEARER_TYPE_REJECTED catalog row, 13.3 (Credential Flow: Platform-admin impersonation, rule 5)
// diagnosis: a user_bearer whose issued_tokens row is a platform-admin
// impersonation row minted a playground JWT, or its refusal is not the
// 401 LENNY_PLAYGROUND_BEARER_TYPE_REJECTED response with
// details.subjectTyp carrying the subject's typ. The playground mint does
// not apply §13.3 step (1a) to the pasted subject; inspect
// admitSubjectRow in pkg/gateway/mcpfabric/playground/token.go.
func TestAPIKeyModeRejectsImpersonationSubject_spec_10_2(t *testing.T) {
	signer := devSigner()
	pasted, err := signer.Sign(jwt.Claims{
		Subject: "bob", TenantID: "acme", Typ: auth.TokenUserBearer, JWTID: "imp-1",
		Audience: []string{"lenny-gateway"}, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign pasted bearer: %v", err)
	}
	cfg := playground.Config{Enabled: true, AuthMode: playground.AuthModeAPIKey, CapabilityTokenTTL: 900 * time.Second}
	h := playground.New(cfg, playground.Options{
		Signer: jwtsign.HMAC(signer), Verifier: signer, IssuedTokens: impersonationRows{},
	})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	resp, raw := postJSON(t, srv.URL+"/v1/playground/token", "{}",
		map[string]string{"Authorization": "Bearer " + pasted})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", resp.StatusCode, raw)
	}
	var env struct {
		BearerToken string `json:"bearerToken"`
		Error       struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, raw)
	}
	if env.Error.Code != "LENNY_PLAYGROUND_BEARER_TYPE_REJECTED" {
		t.Errorf("error code = %q, want LENNY_PLAYGROUND_BEARER_TYPE_REJECTED", env.Error.Code)
	}
	if got := env.Error.Details["subjectTyp"]; got != string(auth.TokenUserBearer) {
		t.Errorf("details.subjectTyp = %v, want user_bearer", got)
	}
	if env.BearerToken != "" {
		t.Error("refused mint returned a bearer token")
	}
}
