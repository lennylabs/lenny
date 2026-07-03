//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component tests pinning the §13.3 write-before-issue path across
// all three issued-token-store audit-binding methods under Path A, the S6
// nextval sequencing model. RecordWithAudit, RecordWithRotationAudit, and
// RevokeWithAudit each route their real-tenant token.exchanged/token.revoked
// audit INSERT through auditstore.AppendInTx -> sealAndInsert inside the same
// pgtenant.InTx that inserts (or stamps) the issued_tokens row, so each must
// commit and mint/rotate/revoke rather than taking the fail-closed
// writeIssueStoreError path. The per-tenant audit_seq_<40hex> nextval resolves
// on the issued-token store's own pool (issuedtokenstore.New(w.pgPool), the
// §12.3 primary in production), so these tests provision that sequence on the
// store's pool and prove the sealed row's sequence_number is drawn by nextval.
//
// The pre-fix RecordWithAudit atomicity case is covered in
// write_before_issue_test.go; this file adds the rotation and revoke methods
// and the sequence-source assertion. The fail-closed regression when the
// sequence is absent lives in write_before_issue_failclosed_test.go.
//
// spec: §13.3, §11.7, §10.2. F-11.2.10.
package auditstore_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	auditcatalog "github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// seedTenantForWriteBeforeIssue registers a tenant and provisions its
// per-tenant audit sequence on the store's own pool, the two preconditions
// the §13.3 write-before-issue nextval path needs. It mirrors what
// provisionTenantSequences does at tenant-creation time in production.
func seedTenantForWriteBeforeIssue(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, tenant); err != nil {
		t.Fatalf("seed tenant %q: %v", tenant, err)
	}
	provisionAuditSequence(t, ctx, pg, tenant)
}

// auditSeqLastValue reads the current last_value of the tenant's audit
// sequence directly, so a test can prove the sealed row's sequence_number
// came from nextval on that sequence object rather than from a dense tail
// ordinal. A dense-ordinal write would never advance the sequence, so the
// sequence would stay at its untouched initial state.
func auditSeqLastValue(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) (last int64, called bool) {
	t.Helper()
	name := seqname.AuditSequenceName(tenant)
	if err := pg.Pool.QueryRow(ctx,
		`SELECT last_value, is_called FROM `+name).Scan(&last, &called); err != nil {
		t.Fatalf("read audit sequence %q state: %v", name, err)
	}
	return last, called
}

// spec: §13.3 line 597 / §11.7 / §10.2 — RecordWithRotationAudit binds the
// new issued_tokens INSERT, the token.exchanged audit row, and the prior
// token's revoked_at stamp in one Postgres transaction. Under Path A the
// audit row's sequence_number is drawn by nextval on the tenant's own
// audit_seq_<40hex> sequence, which resolves on the store's own pool.
// The commit must produce a linked, verifiable chain and advance the
// sequence, and it must revoke the prior token atomically with the mint.
// diagnosis: a failure means the rotation write-before-issue transaction
// did not commit under the nextval model, so an atomic token rotation
// either rejects (nextval on a nonexistent relation) or does not draw the
// authoritative sequence_number from the per-tenant sequence, breaking the
// §13.3 mint-with-revoke atomicity on the primary pool.
func TestRotationWriteBeforeIssueCommitsUnderNextval(t *testing.T) {
	t.Parallel()
	pg := startPGForWriteBeforeIssue(t)
	ctx := context.Background()
	const tenant = "acme-rot"
	seedTenantForWriteBeforeIssue(t, ctx, pg, tenant)

	store := issuedtokenstore.New(pg.Pool)
	chain := auditstore.New(pg.Router(t))
	now := time.Now().UTC().Truncate(time.Microsecond)

	// Mint the predecessor token that the rotation will revoke.
	prev := issuedtokenstore.IssuedToken{
		JTI: "jti-prev", TenantID: tenant, Subject: "alice@acme.com",
		TokenHash: []byte("prev-hash"), Scope: []string{"sessions:read"},
		Audience: "lenny-gateway", IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.Record(ctx, prev); err != nil {
		t.Fatalf("Record predecessor: %v", err)
	}

	next := issuedtokenstore.IssuedToken{
		JTI: "jti-next", TenantID: tenant, Subject: "alice@acme.com",
		TokenHash: []byte("next-hash"), Scope: []string{"sessions:read"},
		Audience: "lenny-gateway", IssuedAt: now.Add(time.Minute), ExpiresAt: now.Add(time.Hour),
	}
	payload := json.RawMessage(`{"policy_result":"accepted","jti":"jti-next"}`)
	revokedSub, revoked, err := store.RecordWithRotationAudit(ctx, next, "jti-prev",
		"rotation_replaced", string(auditcatalog.EventTokenExchanged), payload, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("RecordWithRotationAudit: %v", err)
	}
	if !revoked {
		t.Errorf("rotation did not revoke the live predecessor")
	}
	if revokedSub != "alice@acme.com" {
		t.Errorf("revokedSub=%q, want alice@acme.com", revokedSub)
	}

	// The new token committed and the predecessor is durably revoked: the
	// mint and the revoke shared one COMMIT.
	gotNext, err := store.Get(ctx, tenant, "jti-next")
	if err != nil {
		t.Fatalf("Get minted token: %v", err)
	}
	if gotNext.Revoked() {
		t.Errorf("newly-minted token must not be revoked")
	}
	gotPrev, err := store.Get(ctx, tenant, "jti-prev")
	if err != nil {
		t.Fatalf("Get predecessor: %v", err)
	}
	if !gotPrev.Revoked() {
		t.Errorf("predecessor must be revoked after rotation")
	}

	// The token.exchanged audit row committed and its sequence_number was
	// drawn by nextval on the tenant's own audit sequence.
	rows, err := chain.Rows(ctx, tenant)
	if err != nil {
		t.Fatalf("chain.Rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit chain rows=%d, want 1", len(rows))
	}
	if rows[0].EventType != string(auditcatalog.EventTokenExchanged) {
		t.Errorf("audit event_type=%q, want %q", rows[0].EventType, auditcatalog.EventTokenExchanged)
	}
	if rows[0].Seq != 1 {
		t.Errorf("audit row seq=%d, want 1 (first nextval on a fresh sequence)", rows[0].Seq)
	}
	// The sequence advanced: last_value=1, is_called=true is the state after
	// exactly one nextval draw. A dense tail ordinal would leave the sequence
	// untouched at its initial (last_value=1, is_called=false) state.
	if last, called := auditSeqLastValue(t, ctx, pg, tenant); last != 1 || !called {
		t.Errorf("audit sequence state after one draw = (last=%d, called=%v), want (1, true) "+
			"(the sealed sequence_number must be drawn by nextval on the store pool's own sequence)", last, called)
	}
}

// spec: §13.3 line 597 / §11.7 / §10.2 — RevokeWithAudit binds the
// revoked_at stamp and the token.revoked audit row in one transaction, and
// under Path A draws the audit row's sequence_number by nextval on the
// tenant's own audit sequence on the store's own pool. The revoke must
// commit and the sequence must advance.
// diagnosis: a failure means the revoke write-before-issue transaction did
// not commit under the nextval model, so the gateway's admin-rotation
// durable revoke either rejects on nextval of a nonexistent relation or does
// not draw the authoritative sequence_number, breaking the §16.7 mandatory
// rotation audit on the primary pool.
func TestRevokeWriteBeforeIssueCommitsUnderNextval(t *testing.T) {
	t.Parallel()
	pg := startPGForWriteBeforeIssue(t)
	ctx := context.Background()
	const tenant = "acme-rev"
	seedTenantForWriteBeforeIssue(t, ctx, pg, tenant)

	store := issuedtokenstore.New(pg.Pool)
	chain := auditstore.New(pg.Router(t))
	now := time.Now().UTC().Truncate(time.Microsecond)

	tok := issuedtokenstore.IssuedToken{
		JTI: "jti-rev", TenantID: tenant, Subject: "lenny-admin",
		TokenHash: []byte("rev-hash"), IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.Record(ctx, tok); err != nil {
		t.Fatalf("Record: %v", err)
	}

	payload := json.RawMessage(`{"revocation_reason":"rotation_replaced","propagation_mode":"eventbus"}`)
	row, err := store.RevokeWithAudit(ctx, tenant, "jti-rev", "rotation_replaced",
		string(auditcatalog.EventTokenRevoked), payload, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("RevokeWithAudit: %v", err)
	}
	if row.EventType != string(auditcatalog.EventTokenRevoked) {
		t.Errorf("returned audit row event_type=%q, want %q", row.EventType, auditcatalog.EventTokenRevoked)
	}

	got, err := store.Get(ctx, tenant, "jti-rev")
	if err != nil {
		t.Fatalf("Get after revoke: %v", err)
	}
	if !got.Revoked() {
		t.Errorf("token not durably revoked after RevokeWithAudit commit")
	}

	rows, err := chain.Rows(ctx, tenant)
	if err != nil {
		t.Fatalf("chain.Rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit chain rows=%d, want 1", len(rows))
	}
	if rows[0].Seq != 1 {
		t.Errorf("audit row seq=%d, want 1 (first nextval)", rows[0].Seq)
	}
	if rows[0].PrevHash != audit.GenesisPrevHash {
		t.Errorf("first row prev_hash=%q, want genesis sentinel", rows[0].PrevHash)
	}
	if last, called := auditSeqLastValue(t, ctx, pg, tenant); last != 1 || !called {
		t.Errorf("audit sequence state after one draw = (last=%d, called=%v), want (1, true)", last, called)
	}
}
