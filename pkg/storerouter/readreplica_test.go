// SPDX-License-Identifier: MIT

package storerouter_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/storerouter"
)

// spec: §12.3 line 146 — "Read-heavy queries (session status, task tree,
// audit reads, usage reports) should be routed to read replicas." When a
// ReadPostgres pool is configured and the audit log lives on the primary,
// AuditReadShard resolves to the replica while AuditShard (the write path)
// stays on the primary. F-12.3.16 / F-17.9.13.
func TestAuditReadShardRoutesToReplica_spec_12_3_146(t *testing.T) {
	primary := fakePool(t)
	replica := fakePool(t)
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{
		Postgres:     primary,
		ReadPostgres: replica,
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	read, err := r.AuditReadShard(context.Background(), "acme")
	if err != nil {
		t.Fatalf("AuditReadShard: %v", err)
	}
	if read != replica {
		t.Errorf("AuditReadShard routed to the wrong pool: want the replica")
	}
	write, err := r.AuditShard(context.Background(), "acme")
	if err != nil {
		t.Fatalf("AuditShard: %v", err)
	}
	if write != primary {
		t.Errorf("AuditShard must stay on the primary write pool")
	}
}

// spec: §12.3 line 146 — with no read replica configured every read shares
// the primary pool, so a single-instance deployment is unchanged.
func TestAuditReadShardFallsBackToPrimary_spec_12_3_146(t *testing.T) {
	primary := fakePool(t)
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: primary})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	read, err := r.AuditReadShard(context.Background(), "acme")
	if err != nil {
		t.Fatalf("AuditReadShard: %v", err)
	}
	if read != primary {
		t.Errorf("AuditReadShard with no replica must resolve to the primary")
	}
}

// spec: §12.3 line 146 / line 103 — a separate billing/audit instance has no
// reader split in v1: audit reads stay on that instance even when a primary
// read replica is configured, because the replica replicates the primary,
// not the separate billing/audit instance.
func TestAuditReadShardSeparateInstanceIgnoresReplica_spec_12_3_146(t *testing.T) {
	primary := fakePool(t)
	replica := fakePool(t)
	auditInstance := fakePool(t)
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{
		Postgres:             primary,
		ReadPostgres:         replica,
		BillingAuditPostgres: auditInstance,
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	read, err := r.AuditReadShard(context.Background(), "acme")
	if err != nil {
		t.Fatalf("AuditReadShard: %v", err)
	}
	if read != auditInstance {
		t.Errorf("AuditReadShard must resolve to the separate billing/audit instance, not the primary replica")
	}
}

// spec: §12.3 line 146 — an empty tenant id is rejected on the read path,
// matching the write-path AuditShard contract.
func TestAuditReadShardRejectsEmptyTenant_spec_12_3_146(t *testing.T) {
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: fakePool(t)})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	if _, err := r.AuditReadShard(context.Background(), ""); err == nil {
		t.Error("AuditReadShard with empty tenant: expected error")
	}
}
