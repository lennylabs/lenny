// SPDX-License-Identifier: MIT

package quotacheckpoint_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/quotafailopen"
	"github.com/lennylabs/lenny/pkg/quota"
)

// spec: §12.4 source (2); §11.2 line 48 — when a checkpoint row exists and
// the in-memory fail-open accumulator also carries usage for that window,
// the reconcile restores MAX(redis_current, postgres_checkpoint,
// in_memory_failopen). Here the accumulator's value is the highest of the
// three, so it wins.
func TestReconcileFoldsFailOpenIntoMax_spec_12_4(t *testing.T) {
	t.Parallel()
	hourly := label(t, quota.ResetHourly)
	store := newFakeStore()
	_ = store.Write(context.Background(), []quotacheckpoint.Row{
		{TenantID: "acme", Scope: quotacheckpoint.ScopeUser, SubjectID: "alice@acme.com", Period: "hourly", WindowLabel: hourly, TokenTotal: 500},
		{TenantID: "acme", Scope: quotacheckpoint.ScopeTenant, Period: "hourly", WindowLabel: hourly, TokenTotal: 900},
	})
	cnt := newFakeCounter()
	cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}] = 100 // Redis restarted, lost most
	cnt.m[counterKey{"acme", "", quota.ResetHourly}] = 0                 // Redis empty

	// The accumulator captured outage usage the Redis write dropped: a
	// cumulative total higher than the pre-outage checkpoint.
	acc := quotafailopen.New()
	acc.Record("acme", "alice@acme.com", quota.ResetHourly, fixedNow, 1500) // user + rollup

	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, FailOpen: acc, Now: clock}
	res, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{AllTenants: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.CountersWritten != 2 {
		t.Fatalf("CountersWritten = %d, want 2", res.CountersWritten)
	}
	// User: MAX(100 redis, 500 checkpoint, 1500 failopen) = 1500.
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 1500 {
		t.Errorf("user counter = %d, want 1500 (failopen wins)", got)
	}
	// Tenant rollup: MAX(0 redis, 900 checkpoint, 1500 failopen) = 1500.
	if got := cnt.m[counterKey{"acme", "", quota.ResetHourly}]; got != 1500 {
		t.Errorf("tenant rollup = %d, want 1500 (failopen wins)", got)
	}
	// The CounterResult surfaces the fail-open input for operator visibility.
	var sawUserFailOpen bool
	for _, c := range res.Counters {
		if c.Scope == quotacheckpoint.ScopeUser && c.SubjectID == "alice@acme.com" {
			if c.FailOpenValue != 1500 {
				t.Errorf("user CounterResult.FailOpenValue = %d, want 1500", c.FailOpenValue)
			}
			sawUserFailOpen = true
		}
	}
	if !sawUserFailOpen {
		t.Error("no user CounterResult reported")
	}
}

// spec: §12.4 source (2) — a window that opened entirely during the outage
// has no checkpoint row, so the row-based reconcile never sees it. The
// fail-open-only pass restores it directly from the accumulator
// (MAX(redis_current, in_memory_failopen)).
func TestReconcileFailOpenOnlyWindow_spec_12_4(t *testing.T) {
	t.Parallel()
	store := newFakeStore() // no checkpoint rows at all
	cnt := newFakeCounter() // Redis empty after restart

	acc := quotafailopen.New()
	acc.Record("acme", "alice@acme.com", quota.ResetHourly, fixedNow, 700)

	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, FailOpen: acc, Now: clock}
	res, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{AllTenants: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// Two windows restored from the accumulator alone: the user window and
	// its tenant rollup.
	if res.CountersWritten != 2 {
		t.Fatalf("CountersWritten = %d, want 2 (user + rollup from accumulator)", res.CountersWritten)
	}
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 700 {
		t.Errorf("user counter = %d, want 700 (restored from accumulator)", got)
	}
	if got := cnt.m[counterKey{"acme", "", quota.ResetHourly}]; got != 700 {
		t.Errorf("tenant rollup = %d, want 700 (restored from accumulator)", got)
	}
}

// A checkpoint-backed window is not restored twice: the row pass handles it
// (folding in the accumulator) and the fail-open-only pass skips it.
func TestReconcileNoDoubleRestore_spec_12_4(t *testing.T) {
	t.Parallel()
	hourly := label(t, quota.ResetHourly)
	store := newFakeStore()
	_ = store.Write(context.Background(), []quotacheckpoint.Row{
		{TenantID: "acme", Scope: quotacheckpoint.ScopeUser, SubjectID: "alice@acme.com", Period: "hourly", WindowLabel: hourly, TokenTotal: 200},
		{TenantID: "acme", Scope: quotacheckpoint.ScopeTenant, Period: "hourly", WindowLabel: hourly, TokenTotal: 200},
	})
	cnt := newFakeCounter()
	acc := quotafailopen.New()
	acc.Record("acme", "alice@acme.com", quota.ResetHourly, fixedNow, 300)

	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, FailOpen: acc, Now: clock}
	res, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{AllTenants: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// Exactly two counters (user + rollup), each restored once.
	if res.CountersWritten != 2 {
		t.Fatalf("CountersWritten = %d, want 2 (no double restore)", res.CountersWritten)
	}
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 300 {
		t.Errorf("user counter = %d, want 300 (MAX of checkpoint 200 and failopen 300)", got)
	}
}

// spec: §24.6 — a per-tenant reconcile restores only the named tenant's
// fail-open-only windows, leaving other tenants' accumulator entries alone.
func TestReconcileFailOpenScopedPerTenant_spec_24_6(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	cnt := newFakeCounter()
	acc := quotafailopen.New()
	acc.Record("acme", "alice@acme.com", quota.ResetHourly, fixedNow, 400)
	acc.Record("globex", "carol@globex.com", quota.ResetHourly, fixedNow, 600)

	tenants := quotacheckpoint.TenantExistsFunc(func(_ context.Context, _ string) (bool, error) { return true, nil })
	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, FailOpen: acc, Tenants: tenants, Now: clock}

	if _, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{TenantID: "acme"}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// acme restored from the accumulator.
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 400 {
		t.Errorf("acme user counter = %d, want 400", got)
	}
	// globex untouched (different tenant scope).
	if got := cnt.m[counterKey{"globex", "carol@globex.com", quota.ResetHourly}]; got != 0 {
		t.Errorf("globex user counter = %d, want 0 (out of scope)", got)
	}
}

// A nil FailOpen accumulator reduces the rule to MAX(redis_current,
// postgres_checkpoint) — the pre-F-12.4.20 behaviour.
func TestReconcileNilFailOpenIsNoOp_spec_11_2(t *testing.T) {
	t.Parallel()
	hourly := label(t, quota.ResetHourly)
	store := newFakeStore()
	_ = store.Write(context.Background(), []quotacheckpoint.Row{
		{TenantID: "acme", Scope: quotacheckpoint.ScopeUser, SubjectID: "alice@acme.com", Period: "hourly", WindowLabel: hourly, TokenTotal: 500},
	})
	cnt := newFakeCounter()
	cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}] = 100
	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, Now: clock} // FailOpen nil
	res, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{AllTenants: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.CountersWritten != 1 {
		t.Fatalf("CountersWritten = %d, want 1", res.CountersWritten)
	}
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 500 {
		t.Errorf("user counter = %d, want 500 (MAX redis/checkpoint, no failopen)", got)
	}
}
