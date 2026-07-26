// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// spec: §27.3.1 line 93 — "On each successful POST /v1/playground/token
// mint, the gateway rewrites the session record in place with the new
// bearer jti/exp". CurrentExp must equal the minted bearer's exp so the
// §27.3.1 revokedMarkerTTL bounds the marker to the live bearer's
// remaining lifetime rather than falling through to the conservative
// BearerTTL+skew default.
func TestMintStampsCurrentExpOnSessionRecord_spec_27_3_1_93(t *testing.T) {
	signer := devSigner()
	store := NewMemorySessionStore()
	oidc := &fakeOIDC{subject: OIDCSubject{
		UserID:   "alice",
		TenantID: "acme",
		Scope:    "tools:sessions:read",
	}}
	const bearerTTL = 900 * time.Second
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC, OIDCSessionTTL: time.Hour, BearerTTL: bearerTTL}, Options{
		Signer:   signer,
		Sessions: store,
		OIDC:     oidc,
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
	})

	pgSrv := httptest.NewServer(h.PlaygroundRoutes())
	defer pgSrv.Close()
	tokenSrv := httptest.NewServer(h.TokenRoutes())
	defer tokenSrv.Close()

	cookie := completeOIDCLogin(t, h, pgSrv, oidc)

	// Mint a bearer and capture the full token so its exp claim can be
	// compared against the record's stamped CurrentExp.
	req, _ := http.NewRequest(http.MethodPost, tokenSrv.URL+"/v1/playground/token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mint status = %d, want 200", resp.StatusCode)
	}
	var body tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode mint: %v", err)
	}
	claims := decodeJWTPayload(t, body.BearerToken)
	bearerExp, ok := claims["exp"].(float64)
	if !ok || bearerExp == 0 {
		t.Fatalf("minted JWT carried no exp claim: %v", claims["exp"])
	}

	id, tenant := idTenantForCookie(t, store, cookie)
	rec, err := store.GetSession(context.Background(), tenant, id)
	if err != nil {
		t.Fatalf("GetSession after mint: %v", err)
	}
	if rec.CurrentExp == 0 {
		t.Fatal("CurrentExp was not stamped on the session record at mint, so the §27.3.1 revokedMarkerTTL falls back to the conservative BearerTTL+skew default")
	}
	if rec.CurrentExp != int64(bearerExp) {
		t.Fatalf("record CurrentExp = %d, want minted bearer exp %d", rec.CurrentExp, int64(bearerExp))
	}
}
