// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §10.2 platform-managed role mapping on
// lenny-ops (F-25.4.33). A real cmd/lenny-ops process outside dev mode
// authenticates Lenny-issued tokens through the §10.2 key set over a real
// Postgres, and must authorize on the stored role assignment in the users
// table the gateway reads, so a tenant-admin's downgrade takes effect on
// the next request rather than at token expiry.
package tier9_security_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
)

// spec: 10.2 (Authentication) "Role assignment", 25.4 (The lenny-ops Service: Authentication, revocation set rule 5)
// diagnosis: a downgraded user admitted on lenny-ops means lenny-ops
// authorizes on the token's roles claim and does not consult the
// platform-managed users row the gateway reads, so a removed
// platform-admin role stays usable on the operability surface until the
// token expires. A refusal of the user before the downgrade, or after
// the assignment is removed, means lenny-ops applies a row that records
// no assignment. A refusal after Postgres stops means the role lookup
// fails the request, which §25.4 revocation set rule 5 forbids on
// lenny-ops during a Postgres outage.
func TestOpsAppliesPlatformRoleDowngrade_spec_10_2(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)
	refs := map[string]string{kms.AliasJWTSigning: "local-key-1"}
	_, signer := stackkey.Signer(t, stackkey.MasterKeyFile(t), refs)
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	ops := opsprocess.StartOpts(t, opsprocess.Options{
		Args: []string{"--postgres-dsn=" + pg.DSN, "--kms-provider=local", "--kms-alias=" + kms.AliasJWTSigning + "=local-key-1"},
		Env:  []string{"LENNY_DEV_MODE=false"},
	})
	users := userpg.New(pg.Pool)
	ctx := context.Background()

	alice := signForgery(t, signer, roleClaims("alice@acme.com", "jti-alice"))
	bob := signForgery(t, signer, roleClaims("bob@acme.com", "jti-bob"))

	// Both principals hold platform-admin through the claim, and no row
	// records an assignment for either.
	ops.WaitAccepted(t, alice, 30*time.Second)
	ops.WaitAccepted(t, bob, 30*time.Second)

	// A tenant-admin downgrades alice to user. The next request is
	// refused by the §25.4 role gate, while bob is unaffected.
	if err := users.Create(ctx, userstore.User{
		TenantID: auth.DefaultTenantID, Subject: "alice@acme.com",
		Roles: []auth.Role{auth.RoleUser}, RoleAssigned: true,
		RoleAssignedBy: "carol@acme.com", RoleAssignedAt: time.Now(),
	}); err != nil {
		t.Fatalf("assign alice user: %v", err)
	}
	if got := ops.Me(t, alice); got.Status != http.StatusForbidden || got.Code != opsprocess.CodeForbidden {
		t.Fatalf("downgraded alice: %d %s, want 403 %s", got.Status, got.Body, opsprocess.CodeForbidden)
	}
	if got := ops.Me(t, bob); got.Status != http.StatusOK {
		t.Fatalf("bob: %d %s, want 200", got.Status, got.Body)
	}

	// Removing the assignment returns alice to her claimed role.
	if _, err := users.Update(ctx, auth.DefaultTenantID, "alice@acme.com", func(u *userstore.User) error {
		u.RoleAssigned = false
		u.Roles = nil
		return nil
	}); err != nil {
		t.Fatalf("remove alice assignment: %v", err)
	}
	if got := ops.Me(t, alice); got.Status != http.StatusOK {
		t.Fatalf("alice after the assignment is removed: %d %s, want 200", got.Status, got.Body)
	}

	// With Postgres gone lenny-ops keeps accepting a non-revoked token
	// (§25.4 revocation set rule 5), so the failed role lookup leaves the
	// claim in force rather than refusing the request.
	pg.Stop(t)
	if got := ops.Me(t, bob); got.Status != http.StatusOK {
		t.Fatalf("bob with Postgres stopped: %d %s, want 200 on the claim (§25.4 rule 5)", got.Status, got.Body)
	}
}

// roleClaims returns platform-admin default-tenant claims for subject.
func roleClaims(subject, jti string) jwt.Claims {
	c := forgeryClaims(jti)
	c.Subject = subject
	return c
}
