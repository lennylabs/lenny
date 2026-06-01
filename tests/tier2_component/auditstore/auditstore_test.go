//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.7 Postgres audit chain
// (pkg/gateway/auditstore), exercised against a real Postgres
// container. Covers append + per-tenant sequence + prev_hash
// chaining, verification, tamper detection via the chain link,
// cross-tenant isolation, and the Get / ErrNotFound path.
package auditstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func startPG(t *testing.T) *containers.Postgres {
	t.Helper()
	return containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
}

func seedTenant(t *testing.T, ctx context.Context, pg *containers.Postgres, id string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, id); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
}

// spec: 11.7
// diagnosis: the Postgres audit chain in pkg/gateway/auditstore did
// not behave as specified. Append must build a verifiable per-tenant
// chain with monotonic sequence numbers and prev_hash links, Verify
// must detect a tampered row, chains must be isolated per tenant, and
// Get must return ErrNotFound for an absent sequence number.
func TestAuditStoreContract(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()

	t.Run("append builds a verifiable per-tenant chain", func(t *testing.T) {
		seedTenant(t, ctx, pg, "acme")
		var appended []audit.Row
		for i, et := range []string{"admin.tenant.created", "admin.runtime.created", "admin.user.created"} {
			payload := json.RawMessage(`{"i":` + string(rune('0'+i)) + `}`)
			row, err := store.Append(ctx, "acme", et, payload, time.Now())
			if err != nil {
				t.Fatalf("Append %s: %v", et, err)
			}
			if row.Seq != uint64(i+1) {
				t.Errorf("Append %s seq = %d, want %d", et, row.Seq, i+1)
			}
			appended = append(appended, row)
		}
		if appended[0].PrevHash != audit.GenesisPrevHash {
			t.Errorf("genesis prev_hash = %q, want the sentinel", appended[0].PrevHash)
		}
		if appended[1].PrevHash != audit.LinkHash(appended[0]) {
			t.Error("row 2 prev_hash does not link to row 1")
		}

		rows, err := store.Rows(ctx, "acme")
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("Rows returned %d, want 3", len(rows))
		}
		if rows[0].EventType != "admin.tenant.created" || rows[2].Seq != 3 {
			t.Errorf("rows not in sequence order: %+v", rows)
		}
		res, err := store.Verify(ctx, "acme")
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if res.Integrity != audit.ChainVerified {
			t.Errorf("Verify = %q (%s), want verified", res.Integrity, res.Detail)
		}
	})

	t.Run("verify detects a tampered row via the chain link", func(t *testing.T) {
		seedTenant(t, ctx, pg, "globex")
		for _, et := range []string{"e1", "e2", "e3"} {
			if _, err := store.Append(ctx, "globex", et, json.RawMessage(`{"v":1}`), time.Now()); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		// Tamper row 2's payload in place. The append-only triggers
		// permit an UPDATE only under erasure mode, so the tamper
		// transaction sets both the tenant context and the erasure
		// guard — and the tampered row still breaks the chain at row 3.
		tx, err := pg.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tamper tx: %v", err)
		}
		_, _ = tx.Exec(ctx, "SELECT set_config('app.current_tenant', 'globex', true)")
		_, _ = tx.Exec(ctx, "SELECT set_config('lenny.erasure_mode', 'true', true)")
		if _, err := tx.Exec(ctx,
			`UPDATE audit_log SET payload = '{"v":999}'::jsonb
			 WHERE tenant_id = 'globex' AND sequence_number = 2`); err != nil {
			t.Fatalf("tamper UPDATE: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit tamper tx: %v", err)
		}
		res, err := store.Verify(ctx, "globex")
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if res.Integrity != audit.ChainBroken {
			t.Errorf("Verify of a tampered chain = %q, want broken", res.Integrity)
		}
	})

	t.Run("chains are isolated per tenant", func(t *testing.T) {
		seedTenant(t, ctx, pg, "initech")
		if _, err := store.Append(ctx, "initech", "e1", json.RawMessage(`{}`), time.Now()); err != nil {
			t.Fatalf("Append: %v", err)
		}
		// A tenant with no audit rows verifies as an empty chain.
		seedTenant(t, ctx, pg, "umbrella")
		rows, err := store.Rows(ctx, "umbrella")
		if err != nil || len(rows) != 0 {
			t.Errorf("Rows for an unused tenant = %d rows (err %v), want 0", len(rows), err)
		}
		res, err := store.Verify(ctx, "umbrella")
		if err != nil || res.Integrity != audit.ChainVerified {
			t.Errorf("Verify of an empty chain = %q (err %v), want verified", res.Integrity, err)
		}
	})

	t.Run("get returns a row or ErrNotFound", func(t *testing.T) {
		seedTenant(t, ctx, pg, "hooli")
		if _, err := store.Append(ctx, "hooli", "only", json.RawMessage(`{"x":1}`), time.Now()); err != nil {
			t.Fatalf("Append: %v", err)
		}
		got, err := store.Get(ctx, "hooli", 1)
		if err != nil {
			t.Fatalf("Get(1): %v", err)
		}
		if got.EventType != "only" || got.Seq != 1 {
			t.Errorf("Get(1) = %+v", got)
		}
		if _, err := store.Get(ctx, "hooli", 99); !errors.Is(err, auditstore.ErrNotFound) {
			t.Errorf("Get(99): got %v, want ErrNotFound", err)
		}
	})
}
