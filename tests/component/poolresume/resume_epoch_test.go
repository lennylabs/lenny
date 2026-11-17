//go:build component

// SPDX-License-Identifier: MIT

// Package poolresume_test is the tier-2 contract for the §4.6.2 item 3
// condition (c) cross-process resume channel on the Postgres-backed
// pkg/gateway/poolstore/pgstore. The gateway's resume-reconciliation
// handler bumps reconciliation_resume_epoch without changing
// pool_config_generation; the PoolScalingController reads the same column
// on its next reconcile tick to clear a stuck pool's denial backoff. The
// test runs against a real Postgres container with the full migration set
// applied, so it also proves migration 0151 round-trips.
package poolresume_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	poolpg "github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §4.6.2 item 3 condition (c)
// diagnosis: the Postgres reconciliation_resume_epoch column (migration
// 0151) did not round-trip, or BumpResumeEpoch failed to advance it
// monotonically, mutated pool_config_generation, or mishandled the
// unknown / soft-deleted pool. The PoolScalingController polls this
// column to honor an operator resume across the gateway↔controller
// process boundary, so a regression silently strands stuck pools.
func TestPoolResumeEpochContract(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	store := poolpg.New(pg.Pool)
	ctx := context.Background()

	name := "resume-pool"
	if err := store.Create(ctx, poolstore.Pool{
		Name:             name,
		RuntimeRef:       "claude",
		IsolationProfile: isolation.ProfileSandboxed,
		ExecutionMode:    runtimestore.ExecutionModeSession,
		WarmCount:        1,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	before, err := store.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if before.ReconciliationResumeEpoch != 0 {
		t.Errorf("fresh pool resume epoch = %d, want 0", before.ReconciliationResumeEpoch)
	}

	e1, err := store.BumpResumeEpoch(ctx, name)
	if err != nil {
		t.Fatalf("BumpResumeEpoch: %v", err)
	}
	e2, err := store.BumpResumeEpoch(ctx, name)
	if err != nil {
		t.Fatalf("BumpResumeEpoch: %v", err)
	}
	if e1 != 1 || e2 != 2 {
		t.Errorf("epochs = %d, %d; want 1, 2", e1, e2)
	}

	after, err := store.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get after bump: %v", err)
	}
	if after.ReconciliationResumeEpoch != 2 {
		t.Errorf("stored resume epoch = %d, want 2", after.ReconciliationResumeEpoch)
	}
	if after.Generation != before.Generation {
		t.Errorf("Generation changed %d -> %d; a resume must not be a config change",
			before.Generation, after.Generation)
	}

	// An admin Update preserves the epoch (the mutate does not touch it)
	// while still bumping the config generation.
	updated, err := store.Update(ctx, name, func(p *poolstore.Pool) error {
		p.WarmCount = 4
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.ReconciliationResumeEpoch != 2 {
		t.Errorf("resume epoch after Update = %d, want 2 (preserved)", updated.ReconciliationResumeEpoch)
	}
	if updated.Generation != before.Generation+1 {
		t.Errorf("Generation after Update = %d, want %d", updated.Generation, before.Generation+1)
	}

	if _, err := store.BumpResumeEpoch(ctx, "missing-pool"); !errors.Is(err, poolstore.ErrNotFound) {
		t.Errorf("BumpResumeEpoch unknown = %v, want ErrNotFound", err)
	}

	if err := store.SoftDelete(ctx, name, time.Now().UTC()); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := store.BumpResumeEpoch(ctx, name); !errors.Is(err, poolstore.ErrNotFound) {
		t.Errorf("BumpResumeEpoch soft-deleted = %v, want ErrNotFound", err)
	}
}
