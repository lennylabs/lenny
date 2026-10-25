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

// spec: 10.2 (Authentication), 27.3.1 (OIDC Cookie-to-MCP-Bearer Exchange)
//
// TestPlaygroundOIDCMintExpClampedToSessionRecordExpiry_spec_10_2 pins the min
// with the oidc-mode subject, which is the session record: a mint 55 minutes
// into a 3600s session, with a 900s capability token lifetime, returns exp at
// the record's fixed expiry (IssuedAt + oidcSessionTtlSeconds) and a 300s
// expiresInSeconds. Before the clamp the bearer stayed valid for about ten
// minutes after its session record expired, and natural record expiry writes
// no revocation marker that would catch it.
func TestPlaygroundOIDCMintExpClampedToSessionRecordExpiry_spec_10_2(t *testing.T) {
	signer := devSigner()
	store := NewMemorySessionStore()
	oidc := &fakeOIDC{subject: OIDCSubject{UserID: "alice", TenantID: "acme", Scope: "tools:sessions:read"}}
	loginAt := time.Now().UTC().Truncate(time.Second)
	clock := loginAt
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC, OIDCSessionTTL: time.Hour, CapabilityTokenTTL: 900 * time.Second}, Options{
		Signer:   jwtsign.HMAC(signer),
		Verifier: signer,
		Sessions: store,
		OIDC:     oidc,
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
		Now:      func() time.Time { return clock },
	})
	pgSrv := httptest.NewServer(h.PlaygroundRoutes())
	defer pgSrv.Close()
	cookie := completeOIDCLogin(t, h, pgSrv, oidc)

	id, tenant := idTenantForCookie(t, store, cookie)
	rec, err := store.GetSession(context.Background(), tenant, id)
	if err != nil {
		t.Fatalf("GetSession after login: %v", err)
	}
	recordExpiry := rec.IssuedAt.Add(time.Hour).Unix()

	cases := []struct {
		name          string
		elapsed       time.Duration
		wantExp       int64
		wantExpiresIn int64
	}{
		// A mint early in the session is bounded by the lifetime alone.
		{"lifetime bounds", 10 * time.Minute, loginAt.Add(10*time.Minute + 900*time.Second).Unix(), 900},
		// A mint less than the lifetime before record expiry is clamped.
		{"record expiry clamps", 55 * time.Minute, recordExpiry, 300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock = loginAt.Add(tc.elapsed)
			exp, expiresIn := mintCookieExp(t, h, cookie)
			if exp != tc.wantExp {
				t.Errorf("exp = %d, want %d (record expiry %d)", exp, tc.wantExp, recordExpiry)
			}
			if expiresIn != tc.wantExpiresIn {
				t.Errorf("expiresInSeconds = %d, want %d", expiresIn, tc.wantExpiresIn)
			}
			if exp > recordExpiry {
				t.Errorf("bearer exp %d outlives its session record expiry %d", exp, recordExpiry)
			}
		})
	}
}

// mintCookieExp posts an oidc-mode mint with the session cookie and returns
// the minted bearer's exp claim and the response expiresInSeconds.
func mintCookieExp(t *testing.T, h *Handler, cookie string) (int64, int64) {
	t.Helper()
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
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
	exp, _ := decodeJWTPayload(t, body.BearerToken)["exp"].(float64)
	return int64(exp), body.ExpiresInSeconds
}
