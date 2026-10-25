//go:build component

// SPDX-License-Identifier: MIT

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

// spec: 13.3 (Credential Flow: Write-before-issue ordering, Token rotation
// and revocation)
// diagnosis: a failure means a revocation that skips the §11.7 lock
// (Store.Revoke, RevokeBySubject, or RevokeCascade) and commits after the
// rotation's step (1a) re-read let the rotation's revoke UPDATE match no
// live row, and the rotation committed a replacement for an explicitly
// revoked token. Check revokePrevious and previousRevokedError in
// pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go.
func TestRotationRefusesSubjectRevokedAfterReRead_spec_13_3(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	store := issuedtokenstore.New(pg.Pool)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tenant := freshTenant(t, ctx, pg)
	subj := "subj-" + tenant
	now := time.Now().UTC()
	if err := store.Record(ctx, issuedtokenstore.IssuedToken{
		JTI: subj, TenantID: tenant, Subject: "alice@acme.com", TokenHash: []byte(subj),
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Record subject: %v", err)
	}

	// The revoker runs the UPDATE Store.Revoke runs, in a tenant-scoped
	// transaction without the §11.7 lock, and holds it uncommitted so the rotation's re-read sees the
	// subject live and its revoke UPDATE blocks on the row lock.
	revoker, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin revoker: %v", err)
	}
	defer func() { _ = revoker.Rollback(context.Background()) }()
	// The lenny_tenant_guard trigger refuses a write whose transaction
	// has not set app.current_tenant; set_config(..., true) is the
	// parameterized form of the SET LOCAL pgtenant.InTx issues.
	if _, err := revoker.Exec(ctx,
		`SELECT set_config('app.current_tenant', $1, true)`, tenant); err != nil {
		t.Fatalf("revoker set tenant: %v", err)
	}
	if _, err := revoker.Exec(ctx,
		`UPDATE issued_tokens SET revoked_at = $3, revoked_reason = 'explicit_revoke'
		 WHERE jti = $1 AND tenant_id = $2`, subj, tenant, now); err != nil {
		t.Fatalf("revoker UPDATE: %v", err)
	}

	at := time.Now().UTC().Truncate(time.Microsecond)
	replacement := issuedtokenstore.IssuedToken{
		JTI: "repl-" + tenant, TenantID: tenant, Subject: "alice@acme.com", TokenHash: []byte("r"),
		Scope: []string{"sessions:read"}, Audiences: []string{"lenny-gateway"},
		IssuedAt: at, ExpiresAt: at.Add(time.Hour),
	}
	type result struct {
		revoked bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		_, revoked, rerr := store.RecordWithRotationAudit(ctx, replacement, subjectSource(subj), subj,
			"rotation_replaced", "token.exchanged", []byte(`{"policy_result":"accepted"}`), at)
		done <- result{revoked: revoked, err: rerr}
	}()

	waitForRevokeUpdateBlocked(t, ctx, pg)
	if err := revoker.Commit(ctx); err != nil {
		t.Fatalf("commit revoker: %v", err)
	}

	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		t.Fatal("rotation did not return after the revoker committed")
	}
	var revokedErr *issuedtokenstore.SourceRevokedError
	if !errors.As(res.err, &revokedErr) || revokedErr.Reason() != "subject_token_revoked" || revokedErr.JTI != subj {
		t.Fatalf("RecordWithRotationAudit err = %v, want subject_token_revoked for %q", res.err, subj)
	}
	if res.revoked {
		t.Error("refused rotation reported a revoke")
	}
	if issued, exchanged := tenantRowCounts(t, ctx, pg, tenant); issued != 1 || exchanged != 0 {
		t.Errorf("issued=%d exchanged=%d, want the subject row only and no token.exchanged row", issued, exchanged)
	}
	var reason string
	if err := pg.Pool.QueryRow(ctx,
		`SELECT revoked_reason FROM issued_tokens WHERE jti = $1`, subj).Scan(&reason); err != nil {
		t.Fatalf("read subject: %v", err)
	}
	if reason != "explicit_revoke" {
		t.Errorf("subject revoked_reason = %q, want the revoker's explicit_revoke", reason)
	}
}

// waitForRevokeUpdateBlocked waits until a backend is blocked on a row
// lock while running the rotation's revoke UPDATE.
func waitForRevokeUpdateBlocked(t *testing.T, ctx context.Context, pg *containers.Postgres) {
	t.Helper()
	for {
		var n int
		if err := pg.Pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity
			 WHERE wait_event_type = 'Lock'
			   AND query LIKE '%revoked_at IS NULL%RETURNING sub%'`).Scan(&n); err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if n > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("rotation never blocked on the revoker's row lock")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
