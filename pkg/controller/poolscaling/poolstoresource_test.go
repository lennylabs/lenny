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
	store := newMemoryStore(
		t,
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

// TestPoolStoreSourceFoldsScrubProfileIntoCRD verifies the store recycle
// scrub control (microvmScrubMode → scrubProfile, plus the residual-state
// acknowledgment) is folded onto SandboxTemplate.spec.sessionPolicy.recycle
// so the pool-config validator's fail-closed in-place gate sees the
// deployer's intent. The remaining recycle and concurrency knobs reach the
// gateway through the poolstore directly and are not carried on the CRD.
//
// spec: §5.2 (recycle lifecycle, Kata scrub variant).
func TestPoolStoreSourceFoldsScrubProfileIntoCRD(t *testing.T) {
	// The CRD mapping reads the scrub control from the §5.2 sessionPolicy
	// recycle block: scrubProfile (MicrovmScrubMode) and the in-place
	// residual-state acknowledgment.
	store := newMemoryStore(t, poolstore.Pool{
		Name:             "recycle-pool",
		RuntimeRef:       "claude-code",
		IsolationProfile: isolation.ProfileMicrovm,
		ExecutionMode:    runtimestore.ExecutionModeSession,
		WarmCount:        2,
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                         true,
				AcknowledgeBestEffortScrub:      true,
				MaxSessionsPerPod:               50,
				AllowCrossTenantReuse:           true,
				ScrubProfile:                    runtimestore.MicrovmScrubInPlace,
				AcknowledgeMicrovmResidualState: true,
			},
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
	sp := configs[0].Template.SessionPolicy
	if sp == nil || sp.Recycle == nil {
		t.Fatal("CRD SessionPolicy.Recycle not populated")
	}
	if sp.Recycle.ScrubProfile != "in-place" {
		t.Errorf("ScrubProfile = %q, want in-place", sp.Recycle.ScrubProfile)
	}
	if !sp.Recycle.AcknowledgeMicrovmResidualState {
		t.Error("AcknowledgeMicrovmResidualState did not propagate")
	}
}

// TestPoolStoreSourceMapsRestartScrubMode verifies the store `restart`
// scrub mode maps onto the CRD `vm-restart` scrub profile.
//
// spec: §5.2 (Kata/microvm scrub variant).
func TestPoolStoreSourceMapsRestartScrubMode(t *testing.T) {
	store := newMemoryStore(t, poolstore.Pool{
		Name:             "restart-pool",
		RuntimeRef:       "claude-code",
		IsolationProfile: isolation.ProfileMicrovm,
		ExecutionMode:    runtimestore.ExecutionModeSession,
		WarmCount:        1,
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          10,
				AllowCrossTenantReuse:      true,
				ScrubProfile:               runtimestore.MicrovmScrubRestart,
			},
		},
	})
	src := &poolscaling.PoolStoreSource{Store: store, Namespace: "lenny-agents"}
	configs, err := src.ListPoolConfigs(context.Background())
	if err != nil {
		t.Fatalf("ListPoolConfigs: %v", err)
	}
	sp := configs[0].Template.SessionPolicy
	if sp == nil || sp.Recycle == nil {
		t.Fatal("CRD SessionPolicy.Recycle not populated")
	}
	if sp.Recycle.ScrubProfile != "vm-restart" {
		t.Errorf("ScrubProfile = %q, want vm-restart", sp.Recycle.ScrubProfile)
	}
}

// TestPoolStoreSourceLeavesSessionPolicyNilWithoutScrubControl verifies a
// pool with no recycle scrub control leaves the CRD on its default
// one-session-per-pod configuration (no SessionPolicy block).
//
// spec: §5.2 (sessionPolicy default).
func TestPoolStoreSourceLeavesSessionPolicyNilWithoutScrubControl(t *testing.T) {
	store := newMemoryStore(t, poolstore.Pool{
		Name:          "plain-pool",
		RuntimeRef:    "claude-code",
		ExecutionMode: runtimestore.ExecutionModeSession,
		WarmCount:     1,
	})
	src := &poolscaling.PoolStoreSource{Store: store, Namespace: "lenny-agents"}
	configs, err := src.ListPoolConfigs(context.Background())
	if err != nil {
		t.Fatalf("ListPoolConfigs: %v", err)
	}
	if sp := configs[0].Template.SessionPolicy; sp != nil {
		t.Errorf("SessionPolicy = %#v, want nil for a pool with no scrub control", sp)
	}
}
