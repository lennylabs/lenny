//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the lenny-ops durable platform-audit path
// (pkg/ops/opsaudit.Recorder over pkg/gateway/auditstore.Store routed
// through the §12.6 StoreRouter), exercised against a real Postgres
// container. It proves F-25.4.14 (lenny-ops accesses Postgres through the
// StoreRouter) and F-25.4.22 (ops_event.* audit events are committed to
// the durable §11.7 audit_log hash chain under the platform tenant)
// together: a recorder built exactly as cmd/lenny-ops builds it commits
// remediation-lock and self-health events that read back as a verifiable
// per-tenant chain.
package opsaudit_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/ops/opsaudit"
	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func startPG(t *testing.T) *containers.Postgres {
	t.Helper()
	return containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
}

// spec: §11.7 line 435 (ops_event.* route to the platform tenant);
// §25.4 lines 1490-1500 (lenny-ops uses the StoreRouter); §25.4 lines
// 2338-2340 (lock audit events). F-25.4.14, F-25.4.22.
// diagnosis: a failure means lenny-ops does not durably commit ops_event
// rows to the platform-tenant audit chain through the StoreRouter, so
// lock and other operational audit events could be lost.
func TestOpsAuditDurable_CommitsPlatformChain_spec_25_4_22(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()

	// Seed the platform tenant the ops_event.* chain commits under.
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, opsaudit.PlatformTenantID); err != nil {
		t.Fatalf("seed platform tenant: %v", err)
	}

	// Build the router + audit store + recorder exactly as cmd/lenny-ops
	// does (buildStoreRouter -> buildPlatformAuditRecorder).
	router, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: pg.Pool})
	if err != nil {
		t.Fatalf("store router: %v", err)
	}
	store := auditstore.New(router)
	recorder := opsaudit.New(store)
	if !recorder.Durable() {
		t.Fatal("recorder not durable with a wired store")
	}

	at := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	recorder.Record("remediation.lock_acquired", map[string]any{
		"scope":      "pool:default-gvisor",
		"acquiredBy": "alice",
	}, at)
	recorder.Record("remediation.lock_extended", map[string]any{
		"scope": "pool:default-gvisor",
	}, at.Add(time.Second))
	recorder.Record("ops_health_status_changed", map[string]any{
		"current": "degraded",
	}, at.Add(2*time.Second))

	if recorder.FailedAppends() != 0 {
		t.Fatalf("FailedAppends = %d, want 0", recorder.FailedAppends())
	}

	rows, err := store.Rows(ctx, opsaudit.PlatformTenantID)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	wantTypes := []string{"remediation.lock_acquired", "remediation.lock_extended", "ops_health_status_changed"}
	for i, want := range wantTypes {
		if rows[i].EventType != want {
			t.Errorf("row[%d] event_type = %q, want %q", i, rows[i].EventType, want)
		}
		if rows[i].Seq != uint64(i+1) {
			t.Errorf("row[%d] seq = %d, want %d (monotonic per-tenant)", i, rows[i].Seq, i+1)
		}
	}
	var first map[string]any
	if err := json.Unmarshal(rows[0].Payload, &first); err != nil {
		t.Fatalf("row[0] payload not JSON: %v", err)
	}
	if first["scope"] != "pool:default-gvisor" || first["acquiredBy"] != "alice" {
		t.Errorf("row[0] payload = %s, missing fields", rows[0].Payload)
	}

	// The chain verifies: the durable §11.7 hash links hold.
	res, err := store.Verify(ctx, opsaudit.PlatformTenantID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Integrity != audit.ChainVerified {
		t.Errorf("platform audit chain Integrity = %v, want ChainVerified", res.Integrity)
	}
}
