// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// signJWTWithExtras builds an HS256 JWT carrying the supplied payload
// map verbatim. The standard Claims.Sign path cannot stamp claims under
// non-default names (the Claims struct tags are fixed), so the
// F-10.2.9 test exercises the alt-claim path by hand-crafting the
// payload.
// spec: §10.2 line 212. F-10.2.9.
func signJWTWithExtras(t *testing.T, secret []byte, kid string, payload map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "HS256", "typ": "JWT"}
	if kid != "" {
		header["kid"] = kid
	}
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." +
		base64.RawURLEncoding.EncodeToString(pb)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

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

// TestBearerHonoursConfigurableTenantClaim asserts the §10.2 line 212
// `auth.tenantIdClaim` Helm value flows through: an OIDC token whose
// tenant identifier lives under a non-default claim name is admitted
// when TenantClaimName matches it, and rejected as TENANT_CLAIM_MISSING
// otherwise. spec: §10.2 line 212. F-10.2.9.
func TestBearerHonoursConfigurableTenantClaim(t *testing.T) {
	secret := []byte("secret")
	signer := jwt.NewHMACSigner("test", secret)
	// Hand-crafted JWT: no `tenant_id` claim, tenant identifier lives
	// under the operator-configured alt claim `acme_tenant`.
	tok := signJWTWithExtras(t, secret, "test", map[string]any{
		"sub":         "alice@acme.com",
		"exp":         time.Now().Add(time.Hour).Unix(),
		"typ":         string(pkgauth.TokenUserBearer),
		"acme_tenant": "acme",
	})

	// With TenantClaimName="acme_tenant" the request resolves to tenant
	// "acme" via Extras.
	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier:        signer,
		MultiTenant:     true,
		TenantClaimName: "acme_tenant",
		Registry:        permissiveRegistry{},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("configurable claim resolves: status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if got.TenantID != "acme" {
		t.Errorf("configurable claim tenant: got %q, want acme", got.TenantID)
	}

	// With the default claim name (`tenant_id`) the alt-named claim is
	// invisible and the request is rejected with TENANT_CLAIM_MISSING.
	inner2, _ := captureHandler()
	h2 := Wrap(inner2, Options{
		Verifier:    signer,
		MultiTenant: true,
		Registry:    permissiveRegistry{},
	})
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req2.Header.Set("Authorization", "Bearer "+tok)
	h2.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Errorf("default claim name + alt-named token: status = %d, want 401", rr2.Code)
	}
}

// TestClaimsClaimStringResolvesAcrossFields covers the typed-field /
// Extras precedence on Claims.ClaimString. spec: §10.2 line 212. F-10.2.9.
func TestClaimsClaimStringResolvesAcrossFields(t *testing.T) {
	secret := []byte("secret")
	signer := jwt.NewHMACSigner("k", secret)
	// Typed field via Sign + parse round-trip.
	tok, err := signer.Sign(jwt.Claims{
		TenantID: "from-typed",
		Subject:  "alice",
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	cl, err := signer.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := cl.ClaimString("tenant_id"); got != "from-typed" {
		t.Errorf("typed precedence: got %q, want from-typed", got)
	}
	if got := cl.ClaimString("sub"); got != "alice" {
		t.Errorf("sub typed: got %q, want alice", got)
	}
	if got := cl.ClaimString("missing"); got != "" {
		t.Errorf("absent claim: got %q, want empty", got)
	}

	// Extras-only claim via hand-crafted payload.
	raw := signJWTWithExtras(t, secret, "k", map[string]any{
		"sub":        "alice@acme.com",
		"exp":        time.Now().Add(time.Hour).Unix(),
		"acme_claim": "acme",
		"num":        42,
	})
	cl2, err := signer.Verify(raw)
	if err != nil {
		t.Fatalf("Verify raw: %v", err)
	}
	if got := cl2.ClaimString("acme_claim"); got != "acme" {
		t.Errorf("extras string: got %q, want acme", got)
	}
	if got := cl2.ClaimString("num"); got != "" {
		t.Errorf("non-string claim: got %q, want empty", got)
	}

	// Nil Extras + unknown claim returns empty (no panic).
	zero := jwt.Claims{}
	if got := zero.ClaimString("anything"); got != "" {
		t.Errorf("nil Extras: got %q, want empty", got)
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

// TestBearerCarriesOriginClaim_spec_27_3 — the §27.3 origin claim minted on a
// playground session-capability JWT must reach handlers as Principal.Origin so
// the session-creation path can detect a /playground/*-originated session and
// apply the §27.6 caps + origin=playground label. A token without the claim
// resolves to an empty Origin. F-27.3.3.
func TestBearerCarriesOriginClaim_spec_27_3(t *testing.T) {
	secret := []byte("secret")
	signer := jwt.NewHMACSigner("test", secret)

	playgroundTok, err := signer.Sign(jwt.Claims{
		TenantID: "acme",
		Subject:  "alice@acme.com",
		Typ:      pkgauth.TokenUserBearer,
		Origin:   "playground",
		Expiry:   time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign playground token: %v", err)
	}
	plainTok, err := signer.Sign(jwt.Claims{
		TenantID: "acme",
		Subject:  "alice@acme.com",
		Typ:      pkgauth.TokenUserBearer,
		Expiry:   time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign plain token: %v", err)
	}

	for _, tc := range []struct {
		name       string
		token      string
		wantOrigin string
	}{
		{"playground origin", playgroundTok, "playground"},
		{"no origin claim", plainTok, ""},
	} {
		inner, got := captureHandler()
		h := Wrap(inner, Options{Verifier: signer, MultiTenant: true, Registry: permissiveRegistry{}})
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("%s: status = %d, want 204; body=%s", tc.name, rr.Code, rr.Body.String())
		}
		if got.Origin != tc.wantOrigin {
			t.Errorf("%s: Principal.Origin = %q, want %q", tc.name, got.Origin, tc.wantOrigin)
		}
	}
}
