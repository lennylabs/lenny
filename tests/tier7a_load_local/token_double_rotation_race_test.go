// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a atomicity coverage for two concurrent rotations of one subject
// token. Both rotations sign outside the lock and then race into the
// write-before-issue transaction. The §11.7 lock serializes them, and the
// step (1a) re-read makes the second one observe the first one's
// revoked_at stamp, so exactly one replacement commits.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering, Token
// rotation and revocation), §11.7.
package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// seedRotationTenant registers a tenant, provisions its §11.7 audit
// sequence, and records one live subject token.
func seedRotationTenant(t *testing.T, ctx context.Context, pg *containers.Postgres, store *issuedtokenstore.Store) (tenant, subjectJTI string) {
	t.Helper()
	tenant = "t-" + uuid.NewString()[:8]
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, tenant, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx,
		"CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(tenant)+" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision audit sequence: %v", err)
	}
	subjectJTI = "subj-" + tenant
	now := time.Now().UTC()
	if err := store.Record(ctx, issuedtokenstore.IssuedToken{
		JTI: subjectJTI, TenantID: tenant, Subject: "alice@acme.com", TokenHash: []byte(subjectJTI),
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("record subject: %v", err)
	}
	return tenant, subjectJTI
}

// rotate runs one rotation of subjectJTI to newJTI the way the Token
// Service calls the store, after a random delay that varies the
// interleaving.
func rotate(ctx context.Context, store *issuedtokenstore.Store, tenant, subjectJTI, newJTI string, delay time.Duration) error {
	time.Sleep(delay)
	at := time.Now().UTC()
	_, _, err := store.RecordWithRotationAudit(ctx, issuedtokenstore.IssuedToken{
		JTI: newJTI, TenantID: tenant, Subject: "alice@acme.com", TokenHash: []byte(newJTI),
		Scope: []string{"sessions:read"}, Audiences: []string{"lenny-gateway"},
		IssuedAt: at, ExpiresAt: at.Add(15 * time.Minute),
	}, []issuedtokenstore.SourceToken{{Role: issuedtokenstore.SourceSubjectToken, JTI: subjectJTI}},
		subjectJTI, "rotation_replaced", "token.exchanged", []byte(`{"policy_result":"accepted"}`), at)
	return err
}

// spec: 13.3 (Credential Flow), 8.2 (Delegation Mechanism), 11.7 (Audit Logging)
// diagnosis: a failure means two concurrent rotations of one subject both
// committed a replacement, or neither did, or the loser failed with
// something other than subject_token_revoked. Two live replacements mean
// the step (1a) re-read did not run under the §11.7 lock before the
// INSERT. Check lockAndRecheck in
// pkg/gateway/storage/issuedtokenstore/recheck.go and its call order in
// RecordWithRotationAudit.
func TestConcurrentDoubleRotationCommitsExactlyOne_spec_13_3(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	store := issuedtokenstore.New(pg.Pool)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const runs = 40
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for run := 0; run < runs; run++ {
		tenant, subj := seedRotationTenant(t, ctx, pg, store)
		newJTIs := [2]string{fmt.Sprintf("r%d-a", run), fmt.Sprintf("r%d-b", run)}
		delays := [2]time.Duration{
			time.Duration(rng.Intn(2000)) * time.Microsecond,
			time.Duration(rng.Intn(2000)) * time.Microsecond,
		}
		var (
			wg   sync.WaitGroup
			errs [2]error
		)
		for i := range newJTIs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				errs[i] = rotate(ctx, store, tenant, subj, newJTIs[i], delays[i])
			}(i)
		}
		wg.Wait()
		assertOneRotationWon(t, ctx, pg, store, run, tenant, subj, newJTIs, errs)
	}
}

// assertOneRotationWon checks the double-rotation invariant for one run:
// exactly one rotation committed, the other failed subject_token_revoked
// and left no row, the subject is revoked, and the tenant's chain holds
// one token.exchanged row.
func assertOneRotationWon(t *testing.T, ctx context.Context, pg *containers.Postgres, store *issuedtokenstore.Store,
	run int, tenant, subj string, newJTIs [2]string, errs [2]error,
) {
	t.Helper()
	winners := 0
	for i, err := range errs {
		if err == nil {
			winners++
			if got, gerr := store.Get(ctx, tenant, newJTIs[i]); gerr != nil || got.Revoked() {
				t.Fatalf("run %d: winner %s not live: err=%v", run, newJTIs[i], gerr)
			}
			continue
		}
		var revokedErr *issuedtokenstore.SourceRevokedError
		if !errors.As(err, &revokedErr) || revokedErr.Reason() != "subject_token_revoked" {
			t.Fatalf("run %d: loser err = %v, want subject_token_revoked", run, err)
		}
		if _, gerr := store.Get(ctx, tenant, newJTIs[i]); !errors.Is(gerr, issuedtokenstore.ErrNotFound) {
			t.Fatalf("run %d: loser %s left a row: err=%v", run, newJTIs[i], gerr)
		}
	}
	if winners != 1 {
		t.Fatalf("run %d: %d rotations committed, want exactly 1 (errs %v)", run, winners, errs)
	}
	if got, err := store.Get(ctx, tenant, subj); err != nil || !got.Revoked() {
		t.Fatalf("run %d: subject not revoked after rotation: err=%v", run, err)
	}
	var exchanged int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND event_type = 'token.exchanged'`, tenant).Scan(&exchanged); err != nil {
		t.Fatalf("run %d: count audit rows: %v", run, err)
	}
	if exchanged != 1 {
		t.Fatalf("run %d: token.exchanged rows = %d, want 1", run, exchanged)
	}
}
