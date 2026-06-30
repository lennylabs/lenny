//go:build component

// SPDX-License-Identifier: MIT

package poolresume_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	poolpg "github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §5.2 (recycle lifecycle, pod retirement)
// diagnosis: the Postgres session_policy JSONB column (migration 0167
// renames task_policy → session_policy) did not round-trip the §5.2
// recycle.maxPodUptimeSeconds retirement cap an operator sets on the admin
// API, so it never reaches the SandboxTemplate and the claim path can never
// drain an over-uptime pod. The test runs against a real Postgres container
// with the full migration set applied, so it also proves the migration
// rename and the dropped concurrent-mode columns.
func TestRecycleMaxPodUptimeRoundTrip_spec_5_2(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	store := poolpg.New(pg.Pool)
	ctx := context.Background()

	name := "recycle-uptime-pool"
	if err := store.Create(ctx, poolstore.Pool{
		Name:             name,
		RuntimeRef:       "claude",
		IsolationProfile: isolation.ProfileSandboxed,
		ExecutionMode:    runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          50,
				MaxPodUptimeSeconds:        3600,
			},
		},
		WarmCount: 1,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SessionPolicy == nil || got.SessionPolicy.Recycle == nil ||
		got.SessionPolicy.Recycle.MaxPodUptimeSeconds != 3600 {
		t.Fatalf("recycle.maxPodUptimeSeconds did not round-trip (session_policy column): %+v", got.SessionPolicy)
	}

	// Update the cap through the pgstore UPDATE path.
	if _, err := store.Update(ctx, name, func(p *poolstore.Pool) error {
		p.SessionPolicy.Recycle.MaxPodUptimeSeconds = 7200
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = store.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.SessionPolicy.Recycle.MaxPodUptimeSeconds != 7200 {
		t.Errorf("after Update recycle.maxPodUptimeSeconds = %d, want 7200", got.SessionPolicy.Recycle.MaxPodUptimeSeconds)
	}

	// A pool that never set a session policy reads back nil (NULL column).
	session := "plain-pool"
	if err := store.Create(ctx, poolstore.Pool{
		Name:             session,
		RuntimeRef:       "claude",
		IsolationProfile: isolation.ProfileSandboxed,
		ExecutionMode:    runtimestore.ExecutionModeSession,
		WarmCount:        1,
	}); err != nil {
		t.Fatalf("Create plain pool: %v", err)
	}
	sp, err := store.Get(ctx, session)
	if err != nil {
		t.Fatalf("Get plain pool: %v", err)
	}
	if sp.SessionPolicy != nil {
		t.Errorf("plain pool sessionPolicy = %+v, want nil (NULL column)", sp.SessionPolicy)
	}
}
