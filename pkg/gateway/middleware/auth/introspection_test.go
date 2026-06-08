// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// stubIntrospector is a GroupIntrospector for the §10.6 real-time
// group-check middleware tests.
type stubIntrospector struct {
	enabled bool
	active  bool
	groups  []string
	err     error
	calls   int
	gotTok  string
}

func (s *stubIntrospector) IntrospectGroups(_ context.Context, _, token string) (bool, bool, []string, error) {
	s.calls++
	s.gotTok = token
	return s.enabled, s.active, s.groups, s.err
}

func introspectionTestToken(t *testing.T, signer *jwt.HMACSigner) string {
	t.Helper()
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Typ:      pkgauth.TokenUserBearer,
		Groups:   []string{"jwt-stale-group"},
		Expiry:   time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// spec: §10.6 line 661 — when the tenant enables introspection, the
// provider's real-time group set replaces the JWT groups claim.
func TestIntrospectionReplacesJWTGroups_spec_10_6(t *testing.T) {
	signer := jwt.NewHMACSigner("k", []byte("plat"))
	tok := introspectionTestToken(t, signer)
	intro := &stubIntrospector{enabled: true, active: true, groups: []string{"eng", "oncall"}}
	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier: signer, MultiTenant: true, Registry: permissiveRegistry{},
		GroupIntrospector: intro,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", rr.Code)
	}
	if want := []string{"eng", "oncall"}; !reflect.DeepEqual(got.Groups, want) {
		t.Fatalf("groups = %v, want %v (real-time set must replace the JWT claim)", got.Groups, want)
	}
	if intro.calls != 1 {
		t.Fatalf("introspector called %d times, want 1", intro.calls)
	}
	if intro.gotTok != tok {
		t.Fatalf("introspector received a different token than the bearer presented")
	}
}

// spec: §10.6 line 661 — a tenant that leaves introspection off keeps the
// JWT groups claim untouched.
func TestIntrospectionDisabledKeepsJWTGroups_spec_10_6(t *testing.T) {
	signer := jwt.NewHMACSigner("k", []byte("plat"))
	tok := introspectionTestToken(t, signer)
	intro := &stubIntrospector{enabled: false}
	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier: signer, MultiTenant: true, Registry: permissiveRegistry{},
		GroupIntrospector: intro,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", rr.Code)
	}
	if want := []string{"jwt-stale-group"}; !reflect.DeepEqual(got.Groups, want) {
		t.Fatalf("groups = %v, want %v (JWT claim preserved when introspection is off)", got.Groups, want)
	}
}

// spec: §10.6 line 661 — an inactive-token verdict rejects the bearer
// (401) rather than honoring the stale JWT groups.
func TestIntrospectionInactiveTokenRejects_spec_10_6(t *testing.T) {
	signer := jwt.NewHMACSigner("k", []byte("plat"))
	tok := introspectionTestToken(t, signer)
	intro := &stubIntrospector{enabled: true, active: false}
	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier: signer, MultiTenant: true, Registry: permissiveRegistry{},
		GroupIntrospector: intro,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401 for an inactive token", rr.Code)
	}
}

// spec: §10.6 line 661 — a transport/config failure fails closed with 503
// rather than falling back to the JWT groups, because the operator
// enabled the real-time check for a security reason.
func TestIntrospectionFailsClosedOnError_spec_10_6(t *testing.T) {
	signer := jwt.NewHMACSigner("k", []byte("plat"))
	tok := introspectionTestToken(t, signer)
	intro := &stubIntrospector{enabled: true, err: errors.New("endpoint unreachable")}
	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier: signer, MultiTenant: true, Registry: permissiveRegistry{},
		GroupIntrospector: intro,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503 when introspection is unavailable", rr.Code)
	}
}

// A nil GroupIntrospector leaves the auth path unchanged (the JWT groups
// stand), so deployments that do not wire introspection are unaffected.
func TestNilIntrospectorLeavesGroups_spec_10_6(t *testing.T) {
	signer := jwt.NewHMACSigner("k", []byte("plat"))
	tok := introspectionTestToken(t, signer)
	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier: signer, MultiTenant: true, Registry: permissiveRegistry{},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", rr.Code)
	}
	if want := []string{"jwt-stale-group"}; !reflect.DeepEqual(got.Groups, want) {
		t.Fatalf("groups = %v, want %v", got.Groups, want)
	}
}
