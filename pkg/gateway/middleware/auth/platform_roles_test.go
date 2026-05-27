// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// stubPlatformRoles is a minimal PlatformRoleResolver for tests.
type stubPlatformRoles struct {
	roles []pkgauth.Role
	found bool
	err   error
	calls int
}

func (s *stubPlatformRoles) ResolveRoles(_ context.Context, _, _ string) ([]pkgauth.Role, bool, error) {
	s.calls++
	return s.roles, s.found, s.err
}

// spec: §10.2 line 294 — when the platform-managed mapping returns a
// row, its Roles fully replace the JWT roles claim. F-10.2.3.
func TestPlatformRolesOverrideJWTClaim(t *testing.T) {
	secret := []byte("plat")
	signer := jwt.NewHMACSigner("k", secret)
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Typ:      pkgauth.TokenUserBearer,
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
		Expiry:   time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	resolver := &stubPlatformRoles{
		roles: []pkgauth.Role{pkgauth.RoleUser}, // tenant-admin downgraded the user
		found: true,
	}
	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier:      signer,
		MultiTenant:   true,
		Registry:      permissiveRegistry{},
		PlatformRoles: resolver,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", rr.Code)
	}
	if got.HasRole(pkgauth.RolePlatformAdmin) {
		t.Errorf("platform-admin should have been overridden; got roles=%v", got.Roles)
	}
	if !got.HasRole(pkgauth.RoleUser) {
		t.Errorf("user role override missing; got roles=%v", got.Roles)
	}
	if resolver.calls != 1 {
		t.Errorf("resolver called %d times, want 1", resolver.calls)
	}
}

// A user with no platform-managed row falls through to the OIDC claim.
// F-10.2.3.
func TestPlatformRolesAbsentLeavesJWTClaim(t *testing.T) {
	secret := []byte("plat")
	signer := jwt.NewHMACSigner("k", secret)
	tok, _ := signer.Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Typ:      pkgauth.TokenUserBearer,
		Roles:    []pkgauth.Role{pkgauth.RoleTenantAdmin},
		Expiry:   time.Now().Add(time.Hour).Unix(),
	})
	resolver := &stubPlatformRoles{found: false}
	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier: signer, MultiTenant: true,
		Registry: permissiveRegistry{}, PlatformRoles: resolver,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !got.HasRole(pkgauth.RoleTenantAdmin) {
		t.Errorf("OIDC tenant-admin claim should survive; got roles=%v", got.Roles)
	}
}

// Resolver lookup error fails the request closed with 500 so we never
// silently fall back to the unmodified OIDC claim. F-10.2.3.
func TestPlatformRolesLookupErrorFailsClosed(t *testing.T) {
	secret := []byte("plat")
	signer := jwt.NewHMACSigner("k", secret)
	tok, _ := signer.Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Typ:      pkgauth.TokenUserBearer,
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
		Expiry:   time.Now().Add(time.Hour).Unix(),
	})
	resolver := &stubPlatformRoles{err: errors.New("pg unreachable")}
	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier: signer, MultiTenant: true,
		Registry: permissiveRegistry{}, PlatformRoles: resolver,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", rr.Code)
	}
}

// A row with explicitly empty Roles downgrades the OIDC claim to no
// roles (a tenant-admin can revoke platform-admin by storing []).
// F-10.2.3.
func TestPlatformRolesEmptyRowDowngrades(t *testing.T) {
	secret := []byte("plat")
	signer := jwt.NewHMACSigner("k", secret)
	tok, _ := signer.Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Typ:      pkgauth.TokenUserBearer,
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
		Expiry:   time.Now().Add(time.Hour).Unix(),
	})
	resolver := &stubPlatformRoles{found: true, roles: nil}
	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier: signer, MultiTenant: true,
		Registry: permissiveRegistry{}, PlatformRoles: resolver,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", rr.Code)
	}
	if len(got.Roles) != 0 {
		t.Errorf("expected empty roles, got %v", got.Roles)
	}
}
