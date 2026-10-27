// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// credentialCapture records the credential on the request the middleware
// admits.
func credentialCapture() (http.Handler, *Credential, *bool) {
	var got Credential
	var ok bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = CredentialFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}), &got, &ok
}

// spec: 29.3 (Interactive message send) Forwarding rule 2, 13.3 (Credential Flow)
func TestMiddlewareRecordsTheVerifiedBearer_spec_29_3(t *testing.T) {
	secret := []byte("secret")
	tok := signJWTWithExtras(t, secret, "test", map[string]any{
		"sub":       "alice@acme.com",
		"exp":       time.Now().Add(time.Hour).Unix(),
		"typ":       string(pkgauth.TokenUserBearer),
		"tenant_id": "acme",
	})
	inner, got, ok := credentialCapture()
	h := Wrap(inner, Options{Verifier: jwt.NewTenantClaimVerifier(jwt.NewHMACSigner("test", secret), "")})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent || !*ok || got.Authorization != "Bearer "+tok || len(got.DevHeaders) != 0 {
		t.Fatalf("status %d, credential %v (recorded=%v), want the verified Authorization header", rr.Code, got.Authorization != "", *ok)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 2, 10.2 (Authentication)
func TestMiddlewareRecordsTheClientSentDevHeaders_spec_29_3(t *testing.T) {
	inner, got, ok := credentialCapture()
	h := Wrap(inner, Options{AllowDevHeaders: true, DevHeaderNamesTenant: true, AllowDevRoles: true})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("X-Lenny-User-ID", "alice")
	req.Header.Set("X-Lenny-Roles", "tenant-admin")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !*ok || got.DevHeaders.Get("X-Lenny-User-ID") != "alice" || got.DevHeaders.Get("X-Lenny-Roles") != "tenant-admin" {
		t.Fatalf("credential = %+v (recorded=%v), want the client's dev headers", got, *ok)
	}
	// The middleware echoes the resolved tenant into X-Lenny-Tenant-ID; the
	// recorded credential keeps the client's request, which named none.
	if v := got.DevHeaders.Get("X-Lenny-Tenant-ID"); v != "" {
		t.Errorf("recorded X-Lenny-Tenant-ID = %q, want the client's absent header", v)
	}
	if got.Authorization != "" {
		t.Errorf("recorded Authorization = %q, want none", got.Authorization)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 3
func TestNoCredentialIsRecordedWithoutOne_spec_29_3(t *testing.T) {
	inner, _, ok := credentialCapture()
	Wrap(inner, Options{}).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
	if *ok {
		t.Error("a request admitted without a credential recorded one")
	}
	if _, found := CredentialFrom(WithCredential(context.Background(), Credential{})); found {
		t.Error("an empty credential reads as present")
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 3
func TestPresentsCredential_spec_29_3(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"Authorization", true},
		{"X-Lenny-Tenant-ID", true},
		{"X-Lenny-User-ID", true},
		{"X-Lenny-Roles", true},
		{"X-Lenny-Groups", true},
		{"X-Lenny-Calling-Session", false},
		{"Content-Type", false},
	}
	for _, c := range cases {
		h := http.Header{}
		h.Set(c.header, "v")
		if got := PresentsCredential(h); got != c.want {
			t.Errorf("PresentsCredential(%s) = %v, want %v", c.header, got, c.want)
		}
	}
}
