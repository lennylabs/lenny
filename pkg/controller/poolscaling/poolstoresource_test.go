// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// newMemoryStore returns a poolstore.Memory seeded with pools.
func newMemoryStore(t *testing.T, pools ...poolstore.Pool) poolstore.Store {
	t.Helper()
	m := poolstore.NewMemory()
	for _, p := range pools {
		if err := m.Create(context.Background(), p); err != nil {
			t.Fatalf("seed pool %q: %v", p.Name, err)
		}
	}
	return m
}

// spec: §4.6.2 — the PoolStoreSource maps active store rows into the
// SandboxTemplate spec and the bootstrap minWarm/maxWarm.
func TestPoolStoreSourceMapsActivePools(t *testing.T) {
	store := newMemoryStore(t, poolstore.Pool{
		Name:                 "acme-agents",
		RuntimeRef:           "claude-code",
		IsolationProfile:     isolation.ProfileSandboxed,
		ExecutionMode:        runtimestore.ExecutionModeSession,
		ResourceClass:        "medium",
		WarmCount:            3,
		MaxSessionAgeSeconds: 3600,
	})
	src := &poolscaling.PoolStoreSource{Store: store, Namespace: "lenny-agents"}

	configs, err := src.ListPoolConfigs(context.Background())
	if err != nil {
		t.Fatalf("ListPoolConfigs: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("want 1 config, got %d", len(configs))
	}
	c := configs[0]
	if c.Name != "acme-agents" || c.Namespace != "lenny-agents" {
		t.Fatalf("name/namespace = %q/%q", c.Name, c.Namespace)
	}
	if c.Template.RuntimeRef != "claude-code" {
		t.Fatalf("runtimeRef = %q", c.Template.RuntimeRef)
	}
	if c.Template.IsolationProfile != "sandboxed" {
		t.Fatalf("isolationProfile = %q", c.Template.IsolationProfile)
	}
	if c.Template.ResourceClass != "medium" || c.Template.ExecutionMode != "session" {
		t.Fatalf("resourceClass/executionMode = %q/%q", c.Template.ResourceClass, c.Template.ExecutionMode)
	}
	if c.Template.MaxSessionAgeSeconds != 3600 {
		t.Fatalf("maxSessionAgeSeconds = %d", c.Template.MaxSessionAgeSeconds)
	}
	// v1 bootstrap: warmCount is both the minWarm floor and maxWarm.
	if c.MinWarm != 3 || c.MaxWarm != 3 {
		t.Fatalf("minWarm/maxWarm = %d/%d, want 3/3", c.MinWarm, c.MaxWarm)
	}
}

// spec: §4.6.2 — soft-deleted pools are not reconciled; the source
// lists only active rows.
func TestPoolStoreSourceExcludesSoftDeleted(t *testing.T) {
	store := newMemoryStore(t,
		poolstore.Pool{Name: "live", RuntimeRef: "r1", WarmCount: 1},
		poolstore.Pool{Name: "gone", RuntimeRef: "r2", WarmCount: 1},
	)
	if err := store.SoftDelete(context.Background(), "gone", time.Now()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	src := &poolscaling.PoolStoreSource{Store: store, Namespace: "lenny-agents"}

	configs, err := src.ListPoolConfigs(context.Background())
	if err != nil {
		t.Fatalf("ListPoolConfigs: %v", err)
	}
	if len(configs) != 1 || configs[0].Name != "live" {
		t.Fatalf("want only [live], got %+v", configs)
	}
}

// spec: §4.6.2 — the derived CRDs need an agent namespace; an empty
// namespace is a misconfiguration that fails fast rather than
// materializing CRDs in the wrong namespace.
func TestPoolStoreSourceRequiresNamespace(t *testing.T) {
	store := newMemoryStore(t, poolstore.Pool{Name: "p", RuntimeRef: "r", WarmCount: 1})
	src := &poolscaling.PoolStoreSource{Store: store, Namespace: ""}

	if _, err := src.ListPoolConfigs(context.Background()); err == nil {
		t.Fatal("want error for empty namespace, got nil")
	}
}

// TestPoolStoreSourceFoldsTaskPolicyIntoCRD verifies the §5.2 task-mode
// taskPolicy block + the top-level Pool.AllowCrossTenantReuse are folded
// onto SandboxTemplate.spec.taskPolicy so the pool-config validator sees
// the deployer's intent.
//
// spec: §5.2 lines 398-475.
func TestPoolStoreSourceFoldsTaskPolicyIntoCRD(t *testing.T) {
	mt := 2
	store := newMemoryStore(t, poolstore.Pool{
		Name:                  "task-pool",
		RuntimeRef:            "claude-code",
		IsolationProfile:      isolation.ProfileMicrovm,
		ExecutionMode:         runtimestore.ExecutionModeTask,
		AllowCrossTenantReuse: true,
		WarmCount:             2,
		TaskPolicy: &poolstore.TaskPolicy{
			AcknowledgeBestEffortScrub:      true,
			MicrovmScrubMode:                runtimestore.MicrovmScrubInPlace,
			AcknowledgeMicrovmResidualState: true,
			CleanupCommands:                 []string{"pkill jupyter"},
			CleanupTimeoutSeconds:           30,
			OnCleanupFailure:                runtimestore.CleanupFailureFail,
			MaxScrubFailures:                4,
			MaxTasksPerPod:                  50,
			MaxPodUptimeSeconds:             86400,
			MaxTaskRetries:                  &mt,
		},
	})
	src := &poolscaling.PoolStoreSource{Store: store, Namespace: "lenny-agents"}
	configs, err := src.ListPoolConfigs(context.Background())
	if err != nil {
		t.Fatalf("ListPoolConfigs: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("want 1 config, got %d", len(configs))
	}
	tp := configs[0].Template.TaskPolicy
	if tp == nil {
		t.Fatal("CRD TaskPolicy not populated")
	}
	if !tp.AllowCrossTenantReuse {
		t.Error("AllowCrossTenantReuse should mirror the top-level Pool field")
	}
	if !tp.AcknowledgeBestEffortScrub {
		t.Error("AcknowledgeBestEffortScrub did not propagate")
	}
	if tp.MicrovmScrubMode != "in-place" {
		t.Errorf("MicrovmScrubMode = %q", tp.MicrovmScrubMode)
	}
	if !tp.AcknowledgeMicrovmResidualState {
		t.Error("AcknowledgeMicrovmResidualState did not propagate")
	}
	if tp.MaxTasksPerPod != 50 {
		t.Errorf("MaxTasksPerPod = %d", tp.MaxTasksPerPod)
	}
	if tp.MaxScrubFailures == nil || *tp.MaxScrubFailures != 4 {
		t.Errorf("MaxScrubFailures: %#v", tp.MaxScrubFailures)
	}
	if tp.MaxPodUptimeSeconds == nil || *tp.MaxPodUptimeSeconds != 86400 {
		t.Errorf("MaxPodUptimeSeconds: %#v", tp.MaxPodUptimeSeconds)
	}
	if tp.MaxTaskRetries == nil || *tp.MaxTaskRetries != 2 {
		t.Errorf("MaxTaskRetries: %#v", tp.MaxTaskRetries)
	}
	if len(tp.CleanupCommands) != 1 || tp.CleanupCommands[0] != "pkill jupyter" {
		t.Errorf("CleanupCommands: %#v", tp.CleanupCommands)
	}
	if tp.OnCleanupFailure != "fail" {
		t.Errorf("OnCleanupFailure = %q", tp.OnCleanupFailure)
	}
}

// TestPoolStoreSourcePopulatesConcurrentWorkspacePolicy verifies the
// §5.2 concurrent-workspace pool's stored AcknowledgeProcessLevelIsolation
// + CleanupTimeoutSeconds flow into the SandboxTemplate's
// concurrentWorkspacePolicy block so the pool-config validation webhook
// admits the pool.
//
// spec: §5.2 lines 487-494.
func TestPoolStoreSourcePopulatesConcurrentWorkspacePolicy(t *testing.T) {
	store := newMemoryStore(t, poolstore.Pool{
		Name:                             "cw-pool",
		RuntimeRef:                       "claude-code",
		ExecutionMode:                    runtimestore.ExecutionModeConcurrent,
		ConcurrencyStyle:                 poolstore.ConcurrencyStyleWorkspace,
		MaxConcurrent:                    4,
		AcknowledgeProcessLevelIsolation: true,
		CleanupTimeoutSeconds:            60,
		ConcurrentMaxPodUptimeSeconds:    86400,
		WarmCount:                        1,
	})
	src := &poolscaling.PoolStoreSource{Store: store, Namespace: "lenny-agents"}
	configs, err := src.ListPoolConfigs(context.Background())
	if err != nil {
		t.Fatalf("ListPoolConfigs: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("want 1 config, got %d", len(configs))
	}
	cw := configs[0].Template.ConcurrentWorkspacePolicy
	if cw == nil {
		t.Fatal("CRD ConcurrentWorkspacePolicy not populated")
	}
	if !cw.AcknowledgeProcessLevelIsolation {
		t.Error("AcknowledgeProcessLevelIsolation did not propagate")
	}
	if cw.CleanupTimeoutSeconds != 60 {
		t.Errorf("CleanupTimeoutSeconds = %d", cw.CleanupTimeoutSeconds)
	}
	// spec: §6.2 lines 166-167 — the concurrent-workspace pod-uptime
	// retirement cap flows into the CRD so ResolvePool can surface it.
	if cw.MaxPodUptimeSeconds == nil || *cw.MaxPodUptimeSeconds != 86400 {
		t.Errorf("MaxPodUptimeSeconds = %#v, want 86400", cw.MaxPodUptimeSeconds)
	}
}
