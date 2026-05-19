// SPDX-License-Identifier: MIT

package auth

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// spec: §10.2 RBAC + role-claim propagation.

func TestPrincipalHasRole(t *testing.T) {
	p := Principal{Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin, pkgauth.RoleUser}}
	if !p.HasRole(pkgauth.RolePlatformAdmin) {
		t.Error("HasRole(platform-admin) = false, want true")
	}
	if !p.HasRole(pkgauth.RoleUser) {
		t.Error("HasRole(user) = false, want true")
	}
	if p.HasRole(pkgauth.RoleTenantAdmin) {
		t.Error("HasRole(tenant-admin) = true, want false")
	}
	if (Principal{}).HasRole(pkgauth.RolePlatformAdmin) {
		t.Error("zero Principal HasRole = true, want false")
	}
}

func TestParseRolesHeader(t *testing.T) {
	cases := []struct {
		in   string
		want []pkgauth.Role
	}{
		{"", nil},
		{"  ", nil},
		{"platform-admin", []pkgauth.Role{pkgauth.RolePlatformAdmin}},
		{"platform-admin,user", []pkgauth.Role{pkgauth.RolePlatformAdmin, pkgauth.RoleUser}},
		{" platform-admin , user ", []pkgauth.Role{pkgauth.RolePlatformAdmin, pkgauth.RoleUser}},
		{"platform-admin,,user", []pkgauth.Role{pkgauth.RolePlatformAdmin, pkgauth.RoleUser}},
		{"unknown,user", []pkgauth.Role{pkgauth.RoleUser}},
		{"unknown,bogus", nil},
	}
	for _, c := range cases {
		got := parseRolesHeader(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseRolesHeader(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseGroupsHeader(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"security-engineers", []string{"security-engineers"}},
		{"security-engineers,platform-team", []string{"security-engineers", "platform-team"}},
		{" security-engineers , platform-team ", []string{"security-engineers", "platform-team"}},
		{"a,,b", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := parseGroupsHeader(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseGroupsHeader(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

type permissiveRegistry struct{}

func (permissiveRegistry) IsRegistered(string) (bool, error) { return true, nil }

func captureHandler() (http.Handler, *Principal) {
	captured := &Principal{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := FromContext(r.Context()); ok {
			*captured = p
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return h, captured
}

func TestDevHeadersPropagatesRolesOnlyWhenAllowDevRolesIsSet(t *testing.T) {
	// AllowDevRoles=true: X-Lenny-Roles is honoured (dev mode).
	{
		inner, got := captureHandler()
		h := Wrap(inner, Options{
			MultiTenant:     true,
			AllowDevHeaders: true,
			AllowDevRoles:   true,
			Registry:        permissiveRegistry{},
		})

		req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		req.Header.Set("X-Lenny-Roles", "platform-admin, user")

		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("dev-mode request: status = %d, want 204", rr.Code)
		}
		if !got.HasRole(pkgauth.RolePlatformAdmin) || !got.HasRole(pkgauth.RoleUser) {
			t.Errorf("dev-mode roles: got %v", got.Roles)
		}
	}

	// AllowDevRoles=false (default): X-Lenny-Roles is dropped so a
	// caller cannot self-claim platform-admin even when dev headers
	// are otherwise on.
	{
		inner, got := captureHandler()
		h := Wrap(inner, Options{
			MultiTenant:     true,
			AllowDevHeaders: true,
			AllowDevRoles:   false,
			Registry:        permissiveRegistry{},
		})

		req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		req.Header.Set("X-Lenny-Roles", "platform-admin")

		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("hardened request: status = %d, want 204", rr.Code)
		}
		if got.HasRole(pkgauth.RolePlatformAdmin) {
			t.Errorf("X-Lenny-Roles must be dropped when AllowDevRoles is false; got %v", got.Roles)
		}
	}
}

func TestBearerPropagatesRoles(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Expiry:   time.Now().Add(time.Hour).Unix(),
		Typ:      pkgauth.TokenUserBearer,
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier:    signer,
		MultiTenant: true,
		Registry:    permissiveRegistry{},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("Bearer request: status = %d, want 204; body = %s", rr.Code, rr.Body.String())
	}
	if got.TenantID != "acme" {
		t.Errorf("Bearer tenant: got %q, want acme", got.TenantID)
	}
	if !got.HasRole(pkgauth.RolePlatformAdmin) {
		t.Errorf("Bearer roles missing platform-admin: got %v", got.Roles)
	}
}

// TestBearerAcceptsTokenFromSecondaryVerifier exercises the §17.4
// Embedded Mode bearer path: the gateway runs with a jwt.MultiVerifier
// whose primary member is the Token Service signer and whose secondary
// member is the trusted embedded OIDC key. A token signed by the
// secondary key, which the primary cannot validate, is still accepted.
func TestBearerAcceptsTokenFromSecondaryVerifier(t *testing.T) {
	tokenService := jwt.NewHMACSigner("token-service", []byte("token-service-secret"))
	embedded := jwt.NewHMACSigner("embedded-oidc", []byte("embedded-oidc-secret"))

	tok, err := embedded.Sign(jwt.Claims{
		Subject:  "alice@dev.local",
		TenantID: "default",
		Audience: []string{"dev.local"},
		Issuer:   "https://lenny.dev.local",
		Expiry:   time.Now().Add(time.Hour).Unix(),
		Typ:      pkgauth.TokenUserBearer,
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier:    jwt.NewMultiVerifier(tokenService, embedded),
		MultiTenant: true,
		Registry:    permissiveRegistry{},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("embedded-OIDC bearer: status = %d, want 204; body = %s", rr.Code, rr.Body.String())
	}
	if got.Subject != "alice@dev.local" {
		t.Errorf("subject = %q, want alice@dev.local", got.Subject)
	}
	if !got.HasRole(pkgauth.RolePlatformAdmin) {
		t.Errorf("roles missing platform-admin: got %v", got.Roles)
	}
}

// TestBearerRejectsTokenSignedByNoVerifier confirms the MultiVerifier
// bearer path still rejects a token neither member can validate.
func TestBearerRejectsTokenSignedByNoVerifier(t *testing.T) {
	tokenService := jwt.NewHMACSigner("token-service", []byte("token-service-secret"))
	embedded := jwt.NewHMACSigner("embedded-oidc", []byte("embedded-oidc-secret"))
	forger := jwt.NewHMACSigner("forger", []byte("forger-secret"))

	tok, err := forger.Sign(jwt.Claims{
		Subject:  "mallory@evil.example",
		TenantID: "default",
		Expiry:   time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier:    jwt.NewMultiVerifier(tokenService, embedded),
		MultiTenant: true,
		Registry:    permissiveRegistry{},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("forged bearer: status = %d, want 401; body = %s", rr.Code, rr.Body.String())
	}
}

func TestBearerPropagatesGroups(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Expiry:   time.Now().Add(time.Hour).Unix(),
		Typ:      pkgauth.TokenUserBearer,
		Groups:   []string{"security-engineers", "platform-team"},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier:    signer,
		MultiTenant: true,
		Registry:    permissiveRegistry{},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("Bearer request: status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if len(got.Groups) != 2 || got.Groups[0] != "security-engineers" || got.Groups[1] != "platform-team" {
		t.Errorf("Bearer groups: got %v, want [security-engineers platform-team]", got.Groups)
	}
}

func TestDevHeadersPropagatesGroupsOnlyWhenAllowDevRolesIsSet(t *testing.T) {
	// AllowDevRoles=true: X-Lenny-Groups is honoured.
	{
		inner, got := captureHandler()
		h := Wrap(inner, Options{
			MultiTenant:     true,
			AllowDevHeaders: true,
			AllowDevRoles:   true,
			Registry:        permissiveRegistry{},
		})
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-Groups", "security-engineers, platform-team")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("dev-mode request: status = %d", rr.Code)
		}
		if len(got.Groups) != 2 {
			t.Errorf("dev-mode groups: got %v", got.Groups)
		}
	}

	// AllowDevRoles=false: X-Lenny-Groups is dropped so a caller cannot
	// self-claim environment membership.
	{
		inner, got := captureHandler()
		h := Wrap(inner, Options{
			MultiTenant:     true,
			AllowDevHeaders: true,
			AllowDevRoles:   false,
			Registry:        permissiveRegistry{},
		})
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-Groups", "security-engineers")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("hardened request: status = %d", rr.Code)
		}
		if len(got.Groups) != 0 {
			t.Errorf("X-Lenny-Groups must be dropped when AllowDevRoles is false; got %v", got.Groups)
		}
	}
}
