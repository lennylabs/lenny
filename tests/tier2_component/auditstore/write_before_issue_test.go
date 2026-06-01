//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component test for the §13.3 write-before-issue Postgres
// transaction: pkg/gateway/issuedtokenstore.Store.RecordWithAudit must
// commit the issued_tokens INSERT and the audit_log INSERT atomically
// under the §11.7 per-tenant advisory lock. spec: §13.3 line 589 /
// F-4.3.7 / F-4.3.8.
package auditstore_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	auditcatalog "github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func startPGForWriteBeforeIssue(t *testing.T) *containers.Postgres {
	t.Helper()
	return containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
}

// spec: §13.3 line 589 / F-4.3.7 — RecordWithAudit binds the
// issued_tokens INSERT and the token.exchanged audit_log INSERT in one
// Postgres transaction. After a successful call the audit chain
// contains one new row and the issued_tokens table contains the new
// jti.
func TestRecordWithAuditWritesBothRowsAtomically(t *testing.T) {
	t.Parallel()
	pg := startPGForWriteBeforeIssue(t)
	ctx := context.Background()
	_, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, "acme")
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	store := issuedtokenstore.New(pg.Pool)
	chain := auditstore.New(pg.Router(t))

	now := time.Now().UTC()
	tok := issuedtokenstore.IssuedToken{
		JTI:       "jti-1",
		TenantID:  "acme",
		Subject:   "alice@acme.com",
		TokenHash: []byte("hash-1"),
		Scope:     []string{"sessions:read"},
		Audience:  "lenny-gateway",
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
	}
	payload, _ := json.Marshal(map[string]any{
		"caller_sub":    "alice@acme.com",
		"subject_sub":   "alice@acme.com",
		"jti":           tok.JTI,
		"policy_result": "accepted",
	})
	row, err := store.RecordWithAudit(ctx, tok, string(auditcatalog.EventTokenExchanged),
		json.RawMessage(payload), now)
	if err != nil {
		t.Fatalf("RecordWithAudit: %v", err)
	}
	if row.Seq != 1 {
		t.Errorf("audit row seq=%d, want 1", row.Seq)
	}
	if row.EventType != string(auditcatalog.EventTokenExchanged) {
		t.Errorf("audit event_type=%q, want %q", row.EventType, auditcatalog.EventTokenExchanged)
	}

	// Issued-tokens row is present.
	got, err := store.Get(ctx, "acme", tok.JTI)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.JTI != tok.JTI {
		t.Errorf("issued token JTI=%q, want %q", got.JTI, tok.JTI)
	}

	// Audit chain contains the row.
	rows, err := chain.Rows(ctx, "acme")
	if err != nil {
		t.Fatalf("chain.Rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit chain rows=%d, want 1", len(rows))
	}
	if rows[0].EventType != string(auditcatalog.EventTokenExchanged) {
		t.Errorf("audit row event_type=%q, want %q", rows[0].EventType, auditcatalog.EventTokenExchanged)
	}
	if rows[0].PrevHash != audit.GenesisPrevHash {
		t.Errorf("first row prev_hash=%q, want genesis sentinel", rows[0].PrevHash)
	}
}

// spec: §13.3 line 589 / F-4.3.7 — repeated calls produce a
// sequenced audit chain: each new audit row links to its predecessor
// and the issued_tokens primary key prevents duplicate jti.
func TestRecordWithAuditChainsAcrossExchanges(t *testing.T) {
	t.Parallel()
	pg := startPGForWriteBeforeIssue(t)
	ctx := context.Background()
	_, _ = pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, "globex")
	store := issuedtokenstore.New(pg.Pool)
	chain := auditstore.New(pg.Router(t))

	now := time.Now().UTC()
	for i, jti := range []string{"jti-a", "jti-b", "jti-c"} {
		tok := issuedtokenstore.IssuedToken{
			JTI: jti, TenantID: "globex", Subject: "bob@globex.com",
			TokenHash: []byte(jti), Scope: []string{"sessions:read"},
			Audience: "lenny-gateway", IssuedAt: now.Add(time.Duration(i) * time.Second),
			ExpiresAt: now.Add(time.Hour),
		}
		payload, _ := json.Marshal(map[string]any{
			"jti":           jti,
			"policy_result": "accepted",
		})
		row, err := store.RecordWithAudit(ctx, tok,
			string(auditcatalog.EventTokenExchanged), json.RawMessage(payload),
			now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("RecordWithAudit %s: %v", jti, err)
		}
		if row.Seq != uint64(i+1) {
			t.Errorf("row %s seq=%d, want %d", jti, row.Seq, i+1)
		}
	}
	verify, err := chain.Verify(ctx, "globex")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verify.Integrity != audit.ChainVerified {
		t.Errorf("Verify integrity=%v, want %v", verify.Integrity, audit.ChainVerified)
	}
}

// spec: §13.3 line 589 / F-4.3.7 — a duplicate JTI on a retry rolls
// the entire transaction back: the audit chain length does not grow.
func TestRecordWithAuditRollsBackOnDuplicateJTI(t *testing.T) {
	t.Parallel()
	pg := startPGForWriteBeforeIssue(t)
	ctx := context.Background()
	_, _ = pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, "initech")
	store := issuedtokenstore.New(pg.Pool)
	chain := auditstore.New(pg.Router(t))

	now := time.Now().UTC()
	tok := issuedtokenstore.IssuedToken{
		JTI: "jti-dup", TenantID: "initech", Subject: "carol@initech.com",
		TokenHash: []byte("h"), Scope: []string{"x"},
		Audience: "lenny-gateway", IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	payload := json.RawMessage(`{"policy_result":"accepted"}`)
	if _, err := store.RecordWithAudit(ctx, tok,
		string(auditcatalog.EventTokenExchanged), payload, now); err != nil {
		t.Fatalf("first RecordWithAudit: %v", err)
	}
	// Second call with same jti must return ErrAlreadyExists and NOT
	// write a second audit row.
	_, err := store.RecordWithAudit(ctx, tok,
		string(auditcatalog.EventTokenExchanged), payload, now)
	if err == nil {
		t.Fatalf("second RecordWithAudit accepted; want ErrAlreadyExists")
	}
	rows, _ := chain.Rows(ctx, "initech")
	if len(rows) != 1 {
		t.Errorf("audit chain rows=%d after dup, want 1 (write-before-issue must roll back)", len(rows))
	}
}
