// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// seededPlatformRoles returns a resolver over an in-memory user registry
// holding the default-tenant rows the role-gate tests read: alice
// downgraded to user, dave given an empty assignment, carol assigned
// tenant-admin, and erin with a row that records no assignment.
func seededPlatformRoles(t *testing.T) userstore.PlatformRoles {
	t.Helper()
	s := userstore.NewMemory()
	rows := []userstore.User{
		{Subject: "alice@acme.com", Roles: []auth.Role{auth.RoleUser}, RoleAssigned: true},
		{Subject: "dave@acme.com", RoleAssigned: true},
		{Subject: "carol@acme.com", Roles: []auth.Role{auth.RoleTenantAdmin}, RoleAssigned: true},
		{Subject: "erin@acme.com", Roles: []auth.Role{auth.RoleUser}},
	}
	for _, u := range rows {
		u.TenantID = auth.DefaultTenantID
		if err := s.Create(context.Background(), u); err != nil {
			t.Fatalf("seed %s: %v", u.Subject, err)
		}
	}
	return userstore.PlatformRoles{Store: s}
}

// opsLocksStatus sends GET /v1/admin/remediation-locks with bearer to srv
// and returns the status and the error envelope's details.reason.
func opsLocksStatus(t *testing.T, srv http.Handler, bearer string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/remediation-locks", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	reason, _ := body.Error.Details["reason"].(string)
	return rec.Code, reason
}

// claimsFor returns platform-admin default-tenant claims for subject.
func claimsFor(subject string) jwt.Claims {
	c := opsClaims("jti-"+subject, "https://lenny.acme.com", "")
	c.Subject = subject
	return c
}

// TestOpsRoleGateAppliesStoredAssignment_spec_10_2 is the F-25.4.33
// regression. Every token claims platform-admin. lenny-ops must authorize
// on the stored platform-managed assignment where one exists, as the
// gateway does: a downgraded user and an empty assignment are refused by
// the §25.4 role gate, a tenant-admin assignment is admitted, and a
// missing row or a row with no assignment leaves the claim in force. The
// assignment applies to a Lenny-issued token and to a dev trust-hook
// token alike.
//
// spec: 10.2 (Authentication) "Role assignment", 25.4 (The lenny-ops Service: Authentication)
func TestOpsRoleGateAppliesStoredAssignment_spec_10_2(t *testing.T) {
	keySet, signer := testKeySet(t, 0x31)
	const secret = "role-gate-dev-trust"
	keyFile := writeHMACKeyFile(t, secret)
	cases := []struct {
		subject string
		want    int
	}{
		{subject: "alice@acme.com", want: http.StatusForbidden},
		{subject: "dave@acme.com", want: http.StatusForbidden},
		{subject: "carol@acme.com", want: http.StatusOK},
		{subject: "erin@acme.com", want: http.StatusOK},
		{subject: "bob@acme.com", want: http.StatusOK},
	}
	postures := []struct {
		name    string
		devMode bool
		keyFile string
		mint    func(t *testing.T, c jwt.Claims) string
	}{
		{name: "Lenny-issued token", mint: func(t *testing.T, c jwt.Claims) string { return mintKeySetToken(t, signer, c) }},
		{name: "dev trust-hook token", devMode: true, keyFile: keyFile, mint: func(t *testing.T, c jwt.Claims) string { return mintDevToken(t, secret, c) }},
	}
	for _, p := range postures {
		t.Run(p.name, func(t *testing.T) {
			in := authInputsFor(keySet, p.keyFile, "", p.devMode, false)
			in.platformRoles = seededPlatformRoles(t)
			in.revocations.loaded.Store(true)
			cfg, err := buildAuthConfig(in)
			if err != nil {
				t.Fatalf("buildAuthConfig: %v", err)
			}
			srv := opsserver.New(opsserver.Options{Locks: coordination.NewMemStore(), Auth: cfg})
			for _, tc := range cases {
				t.Run(tc.subject, func(t *testing.T) {
					if got, reason := opsLocksStatus(t, srv, p.mint(t, claimsFor(tc.subject))); got != tc.want {
						t.Fatalf("status = %d (reason %q), want %d", got, reason, tc.want)
					}
				})
			}
		})
	}
}

// TestOpsPlatformRoles_FailsClosedOnLookupError_spec_10_2 pins the wiring
// opsPlatformRoles builds: no resolver without Postgres, so the claim
// stays authoritative, and over a Postgres it cannot reach, a refusal of
// a platform-admin token with the gateway's 500
// platform_role_lookup_failed response rather than a fallback to the
// claim.
//
// spec: 10.2 (Authentication) "Role assignment", 25.4 (The lenny-ops Service: Authentication)
func TestOpsPlatformRoles_FailsClosedOnLookupError_spec_10_2(t *testing.T) {
	if r := opsPlatformRoles(nil); r != nil {
		t.Fatalf("opsPlatformRoles(nil) = %T, want nil", r)
	}
	pool, err := pgxpool.New(context.Background(), "postgres://lenny@127.0.0.1:1/lenny?connect_timeout=2")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	resolver := opsPlatformRoles(pool)
	if resolver == nil {
		t.Fatal("opsPlatformRoles(pool) = nil, want the user-store resolver")
	}

	keySet, signer := testKeySet(t, 0x31)
	in := authInputsFor(keySet, "", "", false, false)
	in.platformRoles = resolver
	in.revocations.loaded.Store(true)
	cfg, err := buildAuthConfig(in)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	if cfg.Options.PlatformRoles == nil {
		t.Fatal("buildAuthConfig dropped the platform role resolver")
	}
	srv := opsserver.New(opsserver.Options{Locks: coordination.NewMemStore(), Auth: cfg})
	got, reason := opsLocksStatus(t, srv, mintKeySetToken(t, signer, claimsFor("alice@acme.com")))
	if got != http.StatusInternalServerError || reason != "platform_role_lookup_failed" {
		t.Fatalf("status = %d reason %q, want 500 platform_role_lookup_failed", got, reason)
	}
}
