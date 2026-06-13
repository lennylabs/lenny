//go:build component

// SPDX-License-Identifier: MIT

package poolresume_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	poolpg "github.com/lennylabs/lenny/pkg/gateway/poolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §6.2 lines 166-167
// diagnosis: the Postgres concurrent_max_pod_uptime_seconds column
// (migration 0157) did not round-trip, so the §6.2 concurrent-workspace
// pod-uptime retirement cap an operator sets on the admin API never
// reaches the SandboxTemplate and the slot-claim path can never drain an
// over-uptime pod. The test runs against a real Postgres container with
// the full migration set applied, so it also proves migration 0157.
func TestConcurrentMaxPodUptimeRoundTrip_spec_6_2(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	store := poolpg.New(pg.Pool)
	ctx := context.Background()

	name := "cw-uptime-pool"
	if err := store.Create(ctx, poolstore.Pool{
		Name:                             name,
		RuntimeRef:                       "claude",
		IsolationProfile:                 isolation.ProfileSandboxed,
		ExecutionMode:                    "concurrent",
		ConcurrencyStyle:                 poolstore.ConcurrencyStyleWorkspace,
		MaxConcurrent:                    4,
		AcknowledgeProcessLevelIsolation: true,
		CleanupTimeoutSeconds:            20,
		ConcurrentMaxPodUptimeSeconds:    3600,
		WarmCount:                        1,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ConcurrentMaxPodUptimeSeconds != 3600 {
		t.Fatalf("ConcurrentMaxPodUptimeSeconds = %d, want 3600 (migration 0157 column did not round-trip)", got.ConcurrentMaxPodUptimeSeconds)
	}

	// Update the cap through the pgstore UPDATE path. concurrency_style no
	// longer round-trips (migration 0167 retired the column), so the
	// re-read row carries an empty ConcurrencyStyle that
	// ValidateConcurrentConfig would reject on re-validation; the mutate
	// re-establishes it for the concurrent-mode pool. spec: §5.2
	// (execution modes), §12.6.
	if _, err := store.Update(ctx, name, func(p *poolstore.Pool) error {
		p.ConcurrencyStyle = poolstore.ConcurrencyStyleWorkspace
		p.ConcurrentMaxPodUptimeSeconds = 7200
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = store.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.ConcurrentMaxPodUptimeSeconds != 7200 {
		t.Errorf("after Update ConcurrentMaxPodUptimeSeconds = %d, want 7200", got.ConcurrentMaxPodUptimeSeconds)
	}

	// A pool that never set the cap reads back zero (the column is NULL).
	session := "session-pool"
	if err := store.Create(ctx, poolstore.Pool{
		Name:             session,
		RuntimeRef:       "claude",
		IsolationProfile: isolation.ProfileSandboxed,
		ExecutionMode:    "session",
		WarmCount:        1,
	}); err != nil {
		t.Fatalf("Create session pool: %v", err)
	}
	sp, err := store.Get(ctx, session)
	if err != nil {
		t.Fatalf("Get session pool: %v", err)
	}
	if sp.ConcurrentMaxPodUptimeSeconds != 0 {
		t.Errorf("session pool ConcurrentMaxPodUptimeSeconds = %d, want 0 (NULL column)", sp.ConcurrentMaxPodUptimeSeconds)
	}
}
