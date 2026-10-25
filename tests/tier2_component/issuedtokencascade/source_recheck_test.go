//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §13.3 write-before-issue step (1a) re-read in
// issuedtokenstore.RecordWithAudit and RecordWithRotationAudit against a
// real Postgres container with the production migrations applied.
package issuedtokencascade_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// tenantRowCounts returns the tenant's issued_tokens and token.exchanged
// audit_log row counts.
func tenantRowCounts(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) (issued, exchanged int) {
	t.Helper()
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(*) FROM issued_tokens WHERE tenant_id = $1`, tenant).Scan(&issued); err != nil {
		t.Fatalf("count issued_tokens: %v", err)
	}
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND event_type = 'token.exchanged'`, tenant).Scan(&exchanged); err != nil {
		t.Fatalf("count audit_log: %v", err)
	}
	return issued, exchanged
}

// spec: 13.3 (Credential Flow), 8.2 (Delegation Mechanism)
// diagnosis: a failure means the issuance transaction did not re-read the
// subject or actor token's issued_tokens.revoked_at under the §11.7 lock
// before writing, so a token revoked after the Token Service's pre-check
// still produced a child or a replacement. Check lockAndRecheck in
// pkg/gateway/storage/issuedtokenstore/recheck.go and that both
// RecordWithAudit and RecordWithRotationAudit call it before any INSERT.
func TestIssuanceReReadsSourceRevocationUnderLock_spec_13_3(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	store := issuedtokenstore.New(pg.Pool)
	ctx := context.Background()

	seed := func(t *testing.T, tenant, jti string, revoked bool) {
		t.Helper()
		now := time.Now().UTC()
		if err := store.Record(ctx, issuedtokenstore.IssuedToken{
			JTI: jti, TenantID: tenant, Subject: "alice@acme.com", TokenHash: []byte(jti),
			IssuedAt: now, ExpiresAt: now.Add(time.Hour),
		}); err != nil {
			t.Fatalf("Record %s: %v", jti, err)
		}
		if revoked {
			if err := store.Revoke(ctx, tenant, jti, "explicit_revoke", now); err != nil {
				t.Fatalf("Revoke %s: %v", jti, err)
			}
		}
	}
	child := func(tenant string) issuedtokenstore.IssuedToken {
		at := time.Now().UTC().Truncate(time.Microsecond)
		return issuedtokenstore.IssuedToken{
			JTI: "child-" + tenant, TenantID: tenant, Subject: "alice@acme.com", TokenHash: []byte("c"),
			Scope: []string{"sessions:read"}, Audiences: []string{"lenny-gateway"},
			IssuedAt: at, ExpiresAt: at.Add(time.Hour),
		}
	}
	sources := []issuedtokenstore.SourceToken{
		{Role: issuedtokenstore.SourceSubjectToken, JTI: "subj"},
		{Role: issuedtokenstore.SourceActorToken, JTI: "actor"},
	}

	cases := []struct {
		name          string
		subjRevoked   bool
		actorRevoked  bool
		seedSubject   bool
		wantReason    string
		wantCommitted bool
	}{
		{name: "revoked subject", seedSubject: true, subjRevoked: true, wantReason: "subject_token_revoked"},
		{name: "revoked actor", seedSubject: true, actorRevoked: true, wantReason: "actor_token_revoked"},
		{name: "both revoked reports the subject", seedSubject: true, subjRevoked: true, actorRevoked: true, wantReason: "subject_token_revoked"},
		{name: "live sources commit", seedSubject: true, wantCommitted: true},
		{name: "subject with no row passes", seedSubject: false, wantCommitted: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := freshTenant(t, ctx, pg)
			if tc.seedSubject {
				seed(t, tenant, "subj", tc.subjRevoked)
			}
			seed(t, tenant, "actor", tc.actorRevoked)
			issuedBefore, _ := tenantRowCounts(t, ctx, pg, tenant)

			_, err := store.RecordWithAudit(ctx, child(tenant), sources,
				"token.exchanged", []byte(`{"policy_result":"accepted"}`), time.Now().UTC())
			issuedAfter, exchanged := tenantRowCounts(t, ctx, pg, tenant)
			if tc.wantCommitted {
				if err != nil {
					t.Fatalf("RecordWithAudit: %v", err)
				}
				if issuedAfter != issuedBefore+1 || exchanged != 1 {
					t.Fatalf("committed issued=%d exchanged=%d, want one new row each", issuedAfter-issuedBefore, exchanged)
				}
				return
			}
			var revokedErr *issuedtokenstore.SourceRevokedError
			if !errors.As(err, &revokedErr) {
				t.Fatalf("RecordWithAudit err = %v, want *SourceRevokedError", err)
			}
			if revokedErr.Reason() != tc.wantReason {
				t.Errorf("reason = %q, want %q", revokedErr.Reason(), tc.wantReason)
			}
			if revokedErr.RevokedAt.IsZero() {
				t.Error("SourceRevokedError carries no revocation instant")
			}
			if issuedAfter != issuedBefore || exchanged != 0 {
				t.Errorf("rejected issuance committed issued=%d exchanged=%d, want none", issuedAfter-issuedBefore, exchanged)
			}
		})
	}

	t.Run("rotation of a revoked subject commits nothing", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		seed(t, tenant, "subj", true)
		_, revoked, err := store.RecordWithRotationAudit(ctx, child(tenant), subjectSource("subj"), "subj",
			"rotation_replaced", "token.exchanged", []byte(`{"policy_result":"accepted"}`), time.Now().UTC())
		var revokedErr *issuedtokenstore.SourceRevokedError
		if !errors.As(err, &revokedErr) || revokedErr.Role != issuedtokenstore.SourceSubjectToken {
			t.Fatalf("RecordWithRotationAudit err = %v, want subject *SourceRevokedError", err)
		}
		if revoked {
			t.Error("refused rotation reported a revoke")
		}
		if issued, exchanged := tenantRowCounts(t, ctx, pg, tenant); issued != 1 || exchanged != 0 {
			t.Errorf("issued=%d exchanged=%d, want the seeded row only and no audit row", issued, exchanged)
		}
	})
}
