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
	"github.com/lennylabs/lenny/pkg/gateway/playground"
)

// spec: §10.2 (Authentication), §15.1 (REST API)
// diagnosis: the playground mint answered a pasted user_bearer it cannot
// narrow with something other than the documented refusal: a scope that
// does not parse must return 401 UNAUTHORIZED with details.reason
// subject_scope_invalid, and a scope disjoint from the playground's
// allowed set must return 403 SCOPE_FORBIDDEN with details.activeScope,
// and neither may carry a bearer. A 200 here means the mint issued a
// bearer with an empty scope claim, which every scope gate reads as
// unrestricted; inspect intersectScope and narrowSubjectScope in
// pkg/gateway/playground/token.go.
func TestAPIKeyModeRefusesASubjectScopeItCannotNarrow(t *testing.T) {
	cases := []struct {
		name, scope, wantCode string
		wantStatus            int
		wantDetail            string
		wantValue             string
	}{
		{"raw identity provider scope", "openid profile", "UNAUTHORIZED", http.StatusUnauthorized, "reason", "subject_scope_invalid"},
		{"scope outside the allowed set", "tools:health:read", "SCOPE_FORBIDDEN", http.StatusForbidden, "activeScope", "tools:health:read"},
	}
	signer := devSigner()
	h := newPlayground(t, playground.AuthModeAPIKey)
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pasted, err := signer.Sign(jwt.Claims{
				Subject: "bob", TenantID: "acme", Typ: auth.TokenUserBearer, Scope: tc.scope,
				Audience: []string{"lenny-gateway"}, Expiry: time.Now().Add(time.Hour).Unix(),
			})
			if err != nil {
				t.Fatalf("sign pasted bearer: %v", err)
			}
			resp, raw := postJSON(t, srv.URL+"/v1/playground/token", "{}",
				map[string]string{"Authorization": "Bearer " + pasted})
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, tc.wantStatus, raw)
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
			if env.Error.Code != tc.wantCode {
				t.Errorf("error code = %q, want %q", env.Error.Code, tc.wantCode)
			}
			if got := env.Error.Details[tc.wantDetail]; got != tc.wantValue {
				t.Errorf("details.%s = %v, want %q", tc.wantDetail, got, tc.wantValue)
			}
			if env.BearerToken != "" {
				t.Error("refused mint returned a bearer token")
			}
		})
	}
}
