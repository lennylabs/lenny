//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §15.1 usage / metering accumulator, exercising
// the Postgres-backed pkg/gateway/usagestore/pgstore against a real
// container. Covers the Record/Aggregate round-trip, the per-tenant
// and per-runtime rollups, the empty-runtime exclusion, the
// tenant-filter scoping, and cross-tenant isolation.
package stores_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
	usagepg "github.com/lennylabs/lenny/pkg/gateway/usagestore/pgstore"
)

// spec: 15.1
// diagnosis: the Postgres-backed usage accumulator in
// pkg/gateway/usagestore/pgstore did not behave as specified. Record
// must persist a usage event and Aggregate must reproduce the
// usagestore.Memory rollup — the grand totals, the tenant-id-sorted
// per-tenant rollup, and the runtime-sorted per-runtime rollup that
// excludes events with no runtime. A non-empty tenant filter must
// scope the report to one tenant, and an intruder tenant's Aggregate
// must never see another tenant's events.
func TestUsageStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := usagepg.New(pg.Pool)
	ctx := context.Background()

	t.Run("record then aggregate round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Record(ctx, usagestore.Record{
			TenantID: tenant, Runtime: "echo", Sessions: 1,
			Tokens: usagestore.Tokens{Input: 100, Output: 50}, PodMinutes: 2.5,
		}); err != nil {
			t.Fatalf("Record echo: %v", err)
		}
		if err := store.Record(ctx, usagestore.Record{
			TenantID: tenant, Runtime: "claude", Sessions: 1,
			Tokens: usagestore.Tokens{Input: 200, Output: 80}, PodMinutes: 3.0,
		}); err != nil {
			t.Fatalf("Record claude: %v", err)
		}
		rep, err := store.Aggregate(ctx, tenant)
		if err != nil {
			t.Fatalf("Aggregate: %v", err)
		}
		if rep.TotalSessions != 2 {
			t.Errorf("TotalSessions: got %d, want 2", rep.TotalSessions)
		}
		if rep.TotalTokens.Input != 300 || rep.TotalTokens.Output != 130 {
			t.Errorf("TotalTokens: got %+v, want {300 130}", rep.TotalTokens)
		}
		if rep.TotalPodMinutes != 5.5 {
			t.Errorf("TotalPodMinutes: got %v, want 5.5", rep.TotalPodMinutes)
		}
		if len(rep.ByTenant) != 1 || rep.ByTenant[0].TenantID != tenant ||
			rep.ByTenant[0].Sessions != 2 {
			t.Errorf("ByTenant: got %+v, want one row for %q with 2 sessions", rep.ByTenant, tenant)
		}
		if rep.ByTenant[0].Tokens.Input != 300 || rep.ByTenant[0].Tokens.Output != 130 {
			t.Errorf("ByTenant tokens: got %+v, want {300 130}", rep.ByTenant[0].Tokens)
		}
		if len(rep.ByRuntime) != 2 {
			t.Fatalf("ByRuntime: got %d rows, want 2 (echo + claude)", len(rep.ByRuntime))
		}
		// The per-runtime rollup is runtime-ascending: claude before echo.
		if rep.ByRuntime[0].Runtime != "claude" || rep.ByRuntime[1].Runtime != "echo" {
			t.Errorf("ByRuntime order: got %q,%q, want claude,echo",
				rep.ByRuntime[0].Runtime, rep.ByRuntime[1].Runtime)
		}
		if rep.ByRuntime[0].Tokens.Input != 200 || rep.ByRuntime[1].Tokens.Input != 100 {
			t.Errorf("ByRuntime token split wrong: %+v", rep.ByRuntime)
		}
	})

	t.Run("aggregate accumulates repeated runtime events", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		for i := 0; i < 3; i++ {
			if err := store.Record(ctx, usagestore.Record{
				TenantID: tenant, Runtime: "echo", Sessions: 1,
				Tokens: usagestore.Tokens{Input: 10, Output: 4},
			}); err != nil {
				t.Fatalf("Record %d: %v", i, err)
			}
		}
		rep, err := store.Aggregate(ctx, tenant)
		if err != nil {
			t.Fatalf("Aggregate: %v", err)
		}
		if rep.TotalSessions != 3 {
			t.Errorf("TotalSessions: got %d, want 3", rep.TotalSessions)
		}
		if len(rep.ByRuntime) != 1 || rep.ByRuntime[0].Sessions != 3 ||
			rep.ByRuntime[0].Tokens.Input != 30 || rep.ByRuntime[0].Tokens.Output != 12 {
			t.Errorf("ByRuntime accumulation: got %+v, want one echo row {3,30,12}", rep.ByRuntime)
		}
	})

	t.Run("an event with no runtime is excluded from the runtime rollup", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		// A runtime-less event still counts toward the tenant totals.
		if err := store.Record(ctx, usagestore.Record{
			TenantID: tenant, Sessions: 4,
			Tokens: usagestore.Tokens{Input: 7, Output: 2},
		}); err != nil {
			t.Fatalf("Record runtime-less: %v", err)
		}
		if err := store.Record(ctx, usagestore.Record{
			TenantID: tenant, Runtime: "echo", Sessions: 1,
		}); err != nil {
			t.Fatalf("Record echo: %v", err)
		}
		rep, err := store.Aggregate(ctx, tenant)
		if err != nil {
			t.Fatalf("Aggregate: %v", err)
		}
		if rep.TotalSessions != 5 {
			t.Errorf("TotalSessions: got %d, want 5 (runtime-less event counts)", rep.TotalSessions)
		}
		if len(rep.ByTenant) != 1 || rep.ByTenant[0].Sessions != 5 {
			t.Errorf("ByTenant: got %+v, want one row with 5 sessions", rep.ByTenant)
		}
		if len(rep.ByRuntime) != 1 || rep.ByRuntime[0].Runtime != "echo" {
			t.Errorf("ByRuntime: got %+v, want only the echo row (runtime-less excluded)", rep.ByRuntime)
		}
	})

	t.Run("aggregate of a tenant with no events is empty", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		rep, err := store.Aggregate(ctx, tenant)
		if err != nil {
			t.Fatalf("Aggregate: %v", err)
		}
		if rep.TotalSessions != 0 || rep.TotalTokens.Input != 0 ||
			rep.TotalTokens.Output != 0 || rep.TotalPodMinutes != 0 {
			t.Errorf("empty tenant totals not zero: %+v", rep)
		}
		if len(rep.ByTenant) != 0 || len(rep.ByRuntime) != 0 {
			t.Errorf("empty tenant rollups not empty: ByTenant=%+v ByRuntime=%+v",
				rep.ByTenant, rep.ByRuntime)
		}
	})

	t.Run("the tenant filter scopes the report to one tenant", func(t *testing.T) {
		tenantA := freshTenant(t, ctx, pg)
		tenantB := freshTenant(t, ctx, pg)
		if err := store.Record(ctx, usagestore.Record{
			TenantID: tenantA, Runtime: "echo", Sessions: 5,
			Tokens: usagestore.Tokens{Input: 500, Output: 250},
		}); err != nil {
			t.Fatalf("Record tenant A: %v", err)
		}
		if err := store.Record(ctx, usagestore.Record{
			TenantID: tenantB, Runtime: "claude", Sessions: 3,
			Tokens: usagestore.Tokens{Input: 300, Output: 150},
		}); err != nil {
			t.Fatalf("Record tenant B: %v", err)
		}
		rep, err := store.Aggregate(ctx, tenantA)
		if err != nil {
			t.Fatalf("Aggregate(tenantA): %v", err)
		}
		if rep.TotalSessions != 5 || rep.TotalTokens.Input != 500 {
			t.Errorf("filtered totals leaked tenant B: got %+v", rep)
		}
		if len(rep.ByTenant) != 1 || rep.ByTenant[0].TenantID != tenantA {
			t.Errorf("filtered ByTenant: got %+v, want only %q", rep.ByTenant, tenantA)
		}
		if len(rep.ByRuntime) != 1 || rep.ByRuntime[0].Runtime != "echo" {
			t.Errorf("filtered ByRuntime: got %+v, want only echo (tenant B's claude excluded)",
				rep.ByRuntime)
		}
	})

	t.Run("a tenant's aggregate cannot see another tenant's events", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		if err := store.Record(ctx, usagestore.Record{
			TenantID: owner, Runtime: "echo", Sessions: 9,
			Tokens: usagestore.Tokens{Input: 900, Output: 450}, PodMinutes: 12.0,
		}); err != nil {
			t.Fatalf("Record owner: %v", err)
		}
		// The intruder has recorded nothing; its scoped report must be
		// empty, never carrying the owner's row.
		rep, err := store.Aggregate(ctx, intruder)
		if err != nil {
			t.Fatalf("Aggregate(intruder): %v", err)
		}
		if rep.TotalSessions != 0 || rep.TotalTokens.Input != 0 ||
			rep.TotalTokens.Output != 0 || rep.TotalPodMinutes != 0 {
			t.Errorf("intruder saw the owner's totals: %+v", rep)
		}
		if len(rep.ByTenant) != 0 {
			t.Errorf("intruder ByTenant must be empty, got %+v", rep.ByTenant)
		}
		if len(rep.ByRuntime) != 0 {
			t.Errorf("intruder ByRuntime must be empty, got %+v", rep.ByRuntime)
		}
	})
}
