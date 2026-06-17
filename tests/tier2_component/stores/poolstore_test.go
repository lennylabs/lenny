//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §5.2 SandboxWarmPool registry, exercising the
// Postgres-backed pkg/gateway/poolstore/pgstore against a real
// container. Covers CRUD, the §5.2 / §5.3 validation, the soft-delete
// lifecycle, and the IncludeDeleted / RuntimeRef list filters.
package stores_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	poolpg "github.com/lennylabs/lenny/pkg/gateway/poolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func poolName(t *testing.T) string {
	t.Helper()
	return "pool-" + newUUID(t)[:8]
}

// spec: 5.2
// diagnosis: the Postgres-backed SandboxWarmPool registry in
// pkg/gateway/poolstore/pgstore did not behave as specified. Create
// and Get must round-trip a pool, Create must reject duplicates and
// the §5.3 standard-isolation guard, Get on a missing name must
// return ErrNotFound, Update must re-validate and advance updated_at,
// the List filters (IncludeDeleted, RuntimeRef) must apply, and the
// soft-delete lifecycle must be idempotent.
func TestPoolStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := poolpg.New(pg.Pool)
	ctx := context.Background()

	t.Run("create and get round-trip", func(t *testing.T) {
		want := poolstore.Pool{
			Name:                   poolName(t),
			RuntimeRef:             "claude",
			IsolationProfile:       isolation.ProfileSandboxed,
			ExecutionMode:          runtimestore.ExecutionModeSession,
			ResourceClass:          "medium",
			WarmCount:              3,
			MaxSessionAgeSeconds:   3600,
			AllowStandardIsolation: false,
		}
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, want.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.RuntimeRef != want.RuntimeRef || got.IsolationProfile != want.IsolationProfile ||
			got.ExecutionMode != want.ExecutionMode || got.ResourceClass != want.ResourceClass ||
			got.WarmCount != want.WarmCount || got.MaxSessionAgeSeconds != want.MaxSessionAgeSeconds ||
			got.AllowStandardIsolation != want.AllowStandardIsolation {
			t.Errorf("scalar mismatch:\n got %+v\nwant %+v", got, want)
		}
		if !got.IsActive() {
			t.Error("freshly created pool reports inactive")
		}
	})

	t.Run("session-mode concurrency and service pools round-trip and re-validate", func(t *testing.T) {
		// §5.2: a concurrent-session pool persists maxConcurrentSessions and
		// the process-level-isolation acknowledgment in the sessionPolicy
		// mirror.
		ws := poolstore.Pool{
			Name:             poolName(t),
			RuntimeRef:       "claude",
			IsolationProfile: isolation.ProfileSandboxed,
			ExecutionMode:    runtimestore.ExecutionModeSession,
			SessionPolicy: &runtimestore.SessionPolicy{
				MaxConcurrentSessions:            8,
				AcknowledgeProcessLevelIsolation: true,
				CleanupTimeoutSeconds:            60,
			},
		}
		if err := store.Create(ctx, ws); err != nil {
			t.Fatalf("Create concurrent-session pool: %v", err)
		}
		got, err := store.Get(ctx, ws.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.SessionPolicy == nil || got.SessionPolicy.MaxConcurrentSessions != 8 ||
			!got.SessionPolicy.AcknowledgeProcessLevelIsolation ||
			got.SessionPolicy.CleanupTimeoutSeconds != 60 {
			t.Errorf("session-policy concurrency fields did not round-trip: %+v", got.SessionPolicy)
		}

		// §5.2: a service-mode pool persists its per-pod maxConcurrent bound.
		st := poolstore.Pool{
			Name:          poolName(t),
			RuntimeRef:    "claude",
			ExecutionMode: runtimestore.ExecutionModeService,
			MaxConcurrent: 4,
		}
		if err := store.Create(ctx, st); err != nil {
			t.Fatalf("Create service pool: %v", err)
		}

		// §5.2: a concurrent-session pool without the acknowledgment is
		// rejected.
		if err := store.Create(ctx, poolstore.Pool{
			Name: poolName(t), ExecutionMode: runtimestore.ExecutionModeSession,
			SessionPolicy: &runtimestore.SessionPolicy{MaxConcurrentSessions: 2},
		}); err == nil || !strings.Contains(err.Error(), "acknowledgeProcessLevelIsolation") {
			t.Errorf("concurrent-session pool without acknowledgment should be rejected, got %v", err)
		}
		// §5.2: a concurrent-session pool that sets recycle.allowCrossTenantReuse
		// is categorically rejected — simultaneous slots have no cross-tenant
		// boundary.
		if err := store.Create(ctx, poolstore.Pool{
			Name: poolName(t), ExecutionMode: runtimestore.ExecutionModeSession,
			IsolationProfile: isolation.ProfileMicrovm,
			SessionPolicy: &runtimestore.SessionPolicy{
				MaxConcurrentSessions:            2,
				AcknowledgeProcessLevelIsolation: true,
				Recycle:                          &runtimestore.RecyclePolicy{AllowCrossTenantReuse: true},
			},
		}); err == nil || !strings.Contains(err.Error(), "allowCrossTenantReuse") {
			t.Errorf("concurrent-session pool with allowCrossTenantReuse should be rejected, got %v", err)
		}
		// §5.2: a session-mode pool that sets the service-only maxConcurrent
		// is rejected rather than silently ignored.
		if err := store.Create(ctx, poolstore.Pool{
			Name: poolName(t), ExecutionMode: runtimestore.ExecutionModeSession,
			MaxConcurrent: 4,
		}); err == nil || !strings.Contains(err.Error(), "maxConcurrent") {
			t.Errorf("session-mode pool with maxConcurrent should be rejected, got %v", err)
		}
	})

	t.Run("duplicate and invalid pools are rejected", func(t *testing.T) {
		p := poolstore.Pool{Name: poolName(t), RuntimeRef: "claude"}
		if err := store.Create(ctx, p); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, p); !errors.Is(err, poolstore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
		if err := store.Create(ctx, poolstore.Pool{Name: "Invalid Name"}); err == nil {
			t.Error("Create with an invalid name should be rejected")
		}
		if err := store.Create(ctx, poolstore.Pool{Name: poolName(t), WarmCount: -1}); err == nil {
			t.Error("Create with a negative warmCount should be rejected")
		}
		// §5.3 security: a standard-isolation pool requires the explicit
		// allowStandardIsolation flag.
		err := store.Create(ctx, poolstore.Pool{
			Name: poolName(t), IsolationProfile: isolation.ProfileStandard,
		})
		if err == nil || !strings.Contains(err.Error(), "allowStandardIsolation") {
			t.Errorf("Create with standard isolation and no flag should be rejected, got %v", err)
		}
		// With the flag set the same pool is admissible.
		if err := store.Create(ctx, poolstore.Pool{
			Name: poolName(t), IsolationProfile: isolation.ProfileStandard,
			AllowStandardIsolation: true,
		}); err != nil {
			t.Errorf("Create standard isolation with the flag: %v", err)
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		if _, err := store.Get(ctx, poolName(t)); !errors.Is(err, poolstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update mutates, advances updated_at, and re-validates", func(t *testing.T) {
		name := poolName(t)
		if err := store.Create(ctx, poolstore.Pool{
			Name: name, RuntimeRef: "claude", WarmCount: 1,
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		before, _ := store.Get(ctx, name)
		updated, err := store.Update(ctx, name, func(p *poolstore.Pool) error {
			p.WarmCount = 5
			p.ResourceClass = "large"
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.WarmCount != 5 || updated.ResourceClass != "large" {
			t.Errorf("Update result not applied: %+v", updated)
		}
		if !updated.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, updated.UpdatedAt)
		}
		// A mutation to a negative warmCount must be rejected.
		if _, err := store.Update(ctx, name, func(p *poolstore.Pool) error {
			p.WarmCount = -1
			return nil
		}); err == nil {
			t.Error("Update to a negative warmCount should be rejected")
		}
		// A mutate error aborts the write.
		sentinel := errors.New("mutate boom")
		if _, err := store.Update(ctx, name, func(*poolstore.Pool) error {
			return sentinel
		}); !errors.Is(err, sentinel) {
			t.Errorf("Update mutate error: got %v, want sentinel", err)
		}
		if _, err := store.Update(ctx, poolName(t), func(*poolstore.Pool) error {
			return nil
		}); !errors.Is(err, poolstore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
	})

	// spec: §15.1 line 797 — the draining_since column persists the pool
	// drain phase across a store round-trip so a restarted gateway still
	// sees the pool as draining. F-15.1.8.
	t.Run("draining_since round-trips", func(t *testing.T) {
		name := poolName(t)
		if err := store.Create(ctx, poolstore.Pool{Name: name, RuntimeRef: "claude"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		fresh, _ := store.Get(ctx, name)
		if fresh.IsDraining() || fresh.Phase() != poolstore.PhaseActive {
			t.Errorf("fresh pool reports draining: %+v", fresh)
		}
		when := time.Now().UTC().Truncate(time.Millisecond)
		if _, err := store.Update(ctx, name, func(p *poolstore.Pool) error {
			p.DrainingSince = when
			return nil
		}); err != nil {
			t.Fatalf("Update drain: %v", err)
		}
		got, err := store.Get(ctx, name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !got.IsDraining() || got.Phase() != poolstore.PhaseDraining {
			t.Errorf("pool not draining after Update: %+v", got)
		}
		if !got.DrainingSince.Equal(when) {
			t.Errorf("DrainingSince = %v, want %v", got.DrainingSince, when)
		}
	})

	t.Run("list filters, ordering, delete, and idempotency", func(t *testing.T) {
		marker := newUUID(t)[:8]
		ids := []string{marker + "-a", marker + "-b", marker + "-c"}
		// a and b warm the "echo" runtime, c warms "claude".
		runtimes := map[string]string{
			ids[0]: "echo", ids[1]: "echo", ids[2]: "claude",
		}
		for _, id := range ids {
			if err := store.Create(ctx, poolstore.Pool{
				Name: id, RuntimeRef: runtimes[id],
			}); err != nil {
				t.Fatalf("Create %s: %v", id, err)
			}
		}
		marked := func(filter poolstore.ListFilter) []poolstore.Pool {
			t.Helper()
			all, err := store.List(ctx, filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var out []poolstore.Pool
			for _, p := range all {
				if strings.HasPrefix(p.Name, marker) {
					out = append(out, p)
				}
			}
			return out
		}
		all := marked(poolstore.ListFilter{})
		if len(all) != 3 || all[0].Name != ids[0] || all[2].Name != ids[2] {
			t.Fatalf("List: not name-ascending or wrong count: %d", len(all))
		}
		// RuntimeRef filter restricts to one runtime.
		byRuntime := marked(poolstore.ListFilter{RuntimeRef: "echo"})
		if len(byRuntime) != 2 || byRuntime[0].Name != ids[0] || byRuntime[1].Name != ids[1] {
			t.Errorf("List RuntimeRef=echo: got %d rows, want [a b]", len(byRuntime))
		}

		if err := store.SoftDelete(ctx, ids[0], time.Now().UTC()); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, ids[0], time.Now().UTC()); err != nil {
			t.Errorf("idempotent SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, poolName(t), time.Now().UTC()); !errors.Is(err, poolstore.ErrNotFound) {
			t.Errorf("SoftDelete missing: got %v, want ErrNotFound", err)
		}
		if n := len(marked(poolstore.ListFilter{})); n != 2 {
			t.Errorf("List default after delete: %d marked, want 2", n)
		}
		if n := len(marked(poolstore.ListFilter{IncludeDeleted: true})); n != 3 {
			t.Errorf("List IncludeDeleted after delete: %d marked, want 3", n)
		}
		// IncludeDeleted composes with the RuntimeRef filter.
		if n := len(marked(poolstore.ListFilter{IncludeDeleted: true, RuntimeRef: "echo"})); n != 2 {
			t.Errorf("List IncludeDeleted+RuntimeRef=echo: %d marked, want 2", n)
		}
		deleted, err := store.Get(ctx, ids[0])
		if err != nil || deleted.IsActive() {
			t.Errorf("Get soft-deleted: %+v err=%v; want inactive", deleted, err)
		}
	})
}
