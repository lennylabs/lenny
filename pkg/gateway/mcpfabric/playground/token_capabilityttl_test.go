// SPDX-License-Identifier: MIT

package playground

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

// mintExp posts a mint to h, optionally with a subject bearer, and returns
// the minted bearer's exp claim and the response expiresInSeconds.
func mintExp(t *testing.T, h *Handler, subjectToken string) (int64, int64) {
	t.Helper()
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if subjectToken != "" {
		req.Header.Set("Authorization", "Bearer "+subjectToken)
	}
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
	exp, _ := decodeJWTPayload(t, body.BearerToken)["exp"].(float64)
	return int64(exp), body.ExpiresInSeconds
}

// spec: 10.2 (Authentication), 27.3.1 (OIDC Cookie-to-MCP-Bearer Exchange)
//
// TestPlaygroundMintExpAtCapabilityTokenTTLBounds_spec_10_2 pins the
// playground bearer exp to now + auth.capabilityTokenTtlSeconds at both edges
// of the 120s..3600s bound, and the response expiresInSeconds to the same
// lifetime.
func TestPlaygroundMintExpAtCapabilityTokenTTLBounds_spec_10_2(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, ttl := range []time.Duration{jwt.MinCapabilityTokenTTL, jwt.MaxCapabilityTokenTTL} {
		h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme", CapabilityTokenTTL: ttl}, Options{
			Signer:   jwtsign.HMAC(devSigner()),
			Verifier: devSigner(),
			Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
			Now:      func() time.Time { return now },
		})
		exp, expiresIn := mintExp(t, h, "")
		if want := now.Add(ttl).Unix(); exp != want {
			t.Errorf("ttl %s: exp = %d, want now+ttl = %d", ttl, exp, want)
		}
		if want := int64(ttl / time.Second); expiresIn != want {
			t.Errorf("ttl %s: expiresInSeconds = %d, want %d", ttl, expiresIn, want)
		}
	}
}

// spec: 10.2 (Authentication), 27.3.1 (OIDC Cookie-to-MCP-Bearer Exchange)
//
// TestPlaygroundMintExpClampedToSubjectExp_spec_10_2 pins the min with the
// subject token exp: an apiKey-mode subject that expires 300s from now caps a
// 900s capability token lifetime at the subject's exp, and expiresInSeconds
// reports the clamped 300s. Before the clamp the bearer outlived its subject.
func TestPlaygroundMintExpClampedToSubjectExp_spec_10_2(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	signer := devSigner()
	subjectExp := now.Add(300 * time.Second).Unix()
	subjectToken, err := signer.Sign(jwt.Claims{
		Subject:  "alice",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Scope:    "tools:sessions:read",
		Expiry:   subjectExp,
	})
	if err != nil {
		t.Fatalf("sign subject token: %v", err)
	}
	h := New(Config{Enabled: true, AuthMode: AuthModeAPIKey, CapabilityTokenTTL: 900 * time.Second}, Options{
		Signer:   jwtsign.HMAC(signer),
		Verifier: signer,
		Now:      func() time.Time { return now },
	})
	exp, expiresIn := mintExp(t, h, subjectToken)
	if exp != subjectExp {
		t.Errorf("exp = %d, want subject exp %d", exp, subjectExp)
	}
	if expiresIn != 300 {
		t.Errorf("expiresInSeconds = %d, want 300", expiresIn)
	}
}
