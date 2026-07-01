//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component tests for the two issued-token-store surfaces the
// gateway's in-process admin-credential rotation path (C7 of proposal
// 0021) consumes: RevokeWithAudit (a reason-parameterized durable revoke
// that binds the revoked_at stamp and the §16.7 token.revoked audit row in
// one transaction) and WithSubjectLock (a per-subject session-scoped
// advisory lock that serializes the non-atomic rotation read-modify-write).
package stores_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	auditcatalog "github.com/lennylabs/lenny/pkg/observability/audit"
)

// spec: §13.3 line 597 (gateway-mediated admin-credential rotation
// ordering), §16.7 line 673 (token.revoked revocation_reason).
// diagnosis: RevokeWithAudit must stamp revoked_at/revoked_reason on the
// named jti and write the token.revoked audit row in the SAME transaction.
// A failure means the durable revoke and its audit trail are not bound in
// one COMMIT, so the gateway's admin-rotation path could durably revoke a
// token with no audit row (violating §16.7's mandatory rotation audit) or
// emit an audit row for a revocation the durable store never applied.
// This is the corrected outcome the pre-fix best-effort `Revoke` (which
// emits no audit row at all, admintoken.go:351 `_ = p.issued.Revoke(...)`)
// cannot produce.
func TestRevokeWithAuditBindsRevocationAndAuditRow(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := issuedtokenstore.New(pg.Pool)
	chain := auditstore.New(pg.Router(t))
	ctx := context.Background()

	tenant := freshTenant(t, ctx, pg)
	jti := "jti-" + newUUID(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Record(ctx, issuedtokenstore.IssuedToken{
		JTI: jti, TenantID: tenant, Subject: "lenny-admin",
		TokenHash: []byte{0xa1}, IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	revokedAt := now.Add(time.Minute)
	payload, _ := json.Marshal(map[string]any{
		"revoked_jti":       jti,
		"revoked_sub":       "lenny-admin",
		"revocation_reason": "rotation_replaced",
		"propagation_mode":  "eventbus",
		"timestamp":         revokedAt,
	})
	row, err := store.RevokeWithAudit(ctx, tenant, jti, "rotation_replaced",
		string(auditcatalog.EventTokenRevoked), json.RawMessage(payload), revokedAt)
	if err != nil {
		t.Fatalf("RevokeWithAudit: %v", err)
	}
	if row.EventType != string(auditcatalog.EventTokenRevoked) {
		t.Errorf("returned audit row event_type=%q, want %q", row.EventType, auditcatalog.EventTokenRevoked)
	}

	// Durable stamp landed with the parameterized reason.
	got, err := store.Get(ctx, tenant, jti)
	if err != nil {
		t.Fatalf("Get after RevokeWithAudit: %v", err)
	}
	if !got.Revoked() {
		t.Fatal("token not durably revoked after RevokeWithAudit")
	}
	if got.RevokedReason != "rotation_replaced" {
		t.Errorf("revoked_reason=%q, want rotation_replaced", got.RevokedReason)
	}
	if !got.RevokedAt.Equal(revokedAt) {
		t.Errorf("revoked_at=%v, want %v", got.RevokedAt, revokedAt)
	}

	// The token.revoked audit row committed in the same transaction.
	rows, err := chain.Rows(ctx, tenant)
	if err != nil {
		t.Fatalf("chain.Rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit chain rows=%d, want 1 (revoke + audit bound in one tx)", len(rows))
	}
	if rows[0].EventType != string(auditcatalog.EventTokenRevoked) {
		t.Errorf("audit row event_type=%q, want %q", rows[0].EventType, auditcatalog.EventTokenRevoked)
	}
	var decoded struct {
		Reason string `json:"revocation_reason"`
	}
	if err := json.Unmarshal(rows[0].Payload, &decoded); err != nil {
		t.Fatalf("decode audit payload: %v", err)
	}
	if decoded.Reason != "rotation_replaced" {
		t.Errorf("audit payload revocation_reason=%q, want rotation_replaced", decoded.Reason)
	}
}

// spec: §13.3 line 597, §16.7 line 673.
// diagnosis: a RevokeWithAudit retry against a token the durable store
// already revoked must return ErrNotFound and NOT write a second audit
// row. A failure means a retried admin rotation double-emits a
// token.revoked row (or re-stamps revoked_at), so the audit chain no
// longer reflects the actual revocation history. The AND revoked_at IS
// NULL predicate is what makes the durable revoke idempotent under retry.
func TestRevokeWithAuditIsIdempotentOnAlreadyRevoked(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := issuedtokenstore.New(pg.Pool)
	chain := auditstore.New(pg.Router(t))
	ctx := context.Background()

	tenant := freshTenant(t, ctx, pg)
	jti := "jti-" + newUUID(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Record(ctx, issuedtokenstore.IssuedToken{
		JTI: jti, TenantID: tenant, Subject: "lenny-admin",
		TokenHash: []byte{0xa2}, IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	payload := json.RawMessage(`{"revocation_reason":"rotation_replaced"}`)
	if _, err := store.RevokeWithAudit(ctx, tenant, jti, "rotation_replaced",
		string(auditcatalog.EventTokenRevoked), payload, now.Add(time.Minute)); err != nil {
		t.Fatalf("first RevokeWithAudit: %v", err)
	}

	// A retry (or a reclaimer sweep after the in-handler revoke already
	// committed) must be a no-op, not a second audit row.
	_, err := store.RevokeWithAudit(ctx, tenant, jti, "rotation_replaced",
		string(auditcatalog.EventTokenRevoked), payload, now.Add(2*time.Minute))
	if !errors.Is(err, issuedtokenstore.ErrNotFound) {
		t.Errorf("retry RevokeWithAudit: got %v, want ErrNotFound", err)
	}

	rows, err := chain.Rows(ctx, tenant)
	if err != nil {
		t.Fatalf("chain.Rows: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("audit chain rows=%d after retry, want 1 (retry must not double-emit)", len(rows))
	}
	// The reason from the first (winning) revoke is retained.
	got, err := store.Get(ctx, tenant, jti)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.RevokedAt.Equal(now.Add(time.Minute)) {
		t.Errorf("revoked_at=%v, want the first revoke instant %v (retry must not re-stamp)",
			got.RevokedAt, now.Add(time.Minute))
	}
}

// spec: §13.3 line 597, §16.7 line 673.
// diagnosis: RevokeWithAudit on a jti that does not exist must return
// ErrNotFound and write no audit row. A failure means the gateway could
// emit a token.revoked audit row for a token that was never issued.
func TestRevokeWithAuditMissingTokenIsErrNotFound(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := issuedtokenstore.New(pg.Pool)
	chain := auditstore.New(pg.Router(t))
	ctx := context.Background()

	tenant := freshTenant(t, ctx, pg)
	_, err := store.RevokeWithAudit(ctx, tenant, "jti-absent", "rotation_replaced",
		string(auditcatalog.EventTokenRevoked), json.RawMessage(`{}`), time.Now().UTC())
	if !errors.Is(err, issuedtokenstore.ErrNotFound) {
		t.Errorf("RevokeWithAudit(absent): got %v, want ErrNotFound", err)
	}
	rows, err := chain.Rows(ctx, tenant)
	if err != nil {
		t.Fatalf("chain.Rows: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("audit chain rows=%d, want 0 (no row for a missing token)", len(rows))
	}
}

// spec: §13.3 line 605 (advisory-locked concurrent-rotation discipline),
// §13.3 line 597.
// diagnosis: WithSubjectLock must provide mutual exclusion: while one
// caller holds the lock for a subject, a second caller for the same
// subject must block until the first releases. A failure means the
// per-subject session-scoped advisory lock does not serialize the
// gateway's non-atomic rotation read-modify-write, so two concurrent
// rotations could interleave the Secret patch and the store transactions
// and drop a successor jti (the exact leak C7 adds the lock to close).
// The critical section here counts overlapping entries; a working lock
// keeps the max observed concurrency at 1.
func TestWithSubjectLockSerializesSameSubject(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := issuedtokenstore.New(pg.Pool)
	tenant := freshTenant(t, context.Background(), pg)

	const subject = "lenny-admin"
	const workers = 8

	var (
		mu        sync.Mutex
		inside    int
		maxInside int
	)
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := store.WithSubjectLock(ctx, tenant, subject, func(context.Context) error {
				mu.Lock()
				inside++
				if inside > maxInside {
					maxInside = inside
				}
				mu.Unlock()

				// Hold the critical section briefly so an unsynchronized
				// implementation would show overlapping entries.
				time.Sleep(20 * time.Millisecond)

				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("WithSubjectLock worker: %v", err)
	}
	if maxInside != 1 {
		t.Errorf("max concurrent critical-section entries = %d, want 1 (advisory lock must serialize the subject)", maxInside)
	}
}

// spec: §13.3 line 605.
// diagnosis: WithSubjectLock must NOT serialize distinct subjects — the
// lock key is per-subject (hashtext("admintoken:"+subject)), so two
// different subjects rotate concurrently. A failure means the lock key is
// subject-independent, needlessly serializing every admin rotation across
// subjects. Two distinct subjects must be able to hold the lock at once.
func TestWithSubjectLockAllowsDistinctSubjects(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := issuedtokenstore.New(pg.Pool)
	tenant := freshTenant(t, context.Background(), pg)

	// subjA enters and holds; subjB must be able to enter while subjA holds.
	aEntered := make(chan struct{})
	bEntered := make(chan struct{})
	releaseA := make(chan struct{})
	done := make(chan error, 2)

	go func() {
		done <- store.WithSubjectLock(context.Background(), tenant, "subject-a", func(context.Context) error {
			close(aEntered)
			<-releaseA
			return nil
		})
	}()

	select {
	case <-aEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("subject-a never entered its critical section")
	}

	go func() {
		done <- store.WithSubjectLock(context.Background(), tenant, "subject-b", func(context.Context) error {
			close(bEntered)
			return nil
		})
	}()

	// subject-b must enter while subject-a still holds its lock. A
	// subject-independent key would block b behind a.
	select {
	case <-bEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("subject-b blocked behind subject-a: lock key is not per-subject")
	}
	close(releaseA)

	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Errorf("WithSubjectLock: %v", err)
		}
	}
}
