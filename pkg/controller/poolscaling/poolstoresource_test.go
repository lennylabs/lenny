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
