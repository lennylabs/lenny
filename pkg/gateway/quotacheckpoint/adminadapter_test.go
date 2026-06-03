// SPDX-License-Identifier: MIT

package quotacheckpoint_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/quota"
)

// spec: §24.6 line 99 — AdminReconciler maps a per-tenant reconcile of an
// unknown tenant to admin.ErrQuotaTenantNotFound (the handler's 404), and
// maps a successful reconcile into the admin wire result with the MAX-rule
// inputs.
func TestAdminReconcilerMapping_spec_24_6(t *testing.T) {
	t.Parallel()
	hourly := label(t, quota.ResetHourly)
	store := newFakeStore()
	_ = store.Write(context.Background(), []quotacheckpoint.Row{
		{TenantID: "acme", Scope: quotacheckpoint.ScopeUser, SubjectID: "alice@acme.com", Period: "hourly", WindowLabel: hourly, TokenTotal: 400},
	})
	cnt := newFakeCounter()
	cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}] = 50
	svc := &quotacheckpoint.Service{
		Store: store, Reader: cnt, Restorer: cnt, Now: clock,
		Tenants: quotacheckpoint.TenantExistsFunc(func(_ context.Context, id string) (bool, error) {
			return id == "acme", nil
		}),
	}
	adapter := quotacheckpoint.AdminReconciler{Service: svc}

	if _, err := adapter.Reconcile(context.Background(), admin.QuotaReconcileScope{TenantID: "ghost"}); !errors.Is(err, admin.ErrQuotaTenantNotFound) {
		t.Fatalf("unknown tenant err = %v, want admin.ErrQuotaTenantNotFound", err)
	}

	out, err := adapter.Reconcile(context.Background(), admin.QuotaReconcileScope{AllTenants: true})
	if err != nil {
		t.Fatalf("Reconcile(all): %v", err)
	}
	if out.CountersWritten != 1 || out.TenantsReconciled != 1 {
		t.Fatalf("admin result = %d counters / %d tenants, want 1 / 1", out.CountersWritten, out.TenantsReconciled)
	}
	if len(out.Tenants) != 1 {
		t.Fatalf("admin result tenants = %d, want 1", len(out.Tenants))
	}
	got := out.Tenants[0]
	if got.TenantID != "acme" || got.CheckpointValue != 400 || got.InMemoryValue != 50 || got.WrittenValue != 400 {
		t.Errorf("admin detail = %+v, want acme/400/50/400", got)
	}
}

// AdminReconciler satisfies the admin.QuotaReconciler seam at compile time.
func TestAdminReconcilerSatisfiesSeam(t *testing.T) {
	t.Parallel()
	var _ admin.QuotaReconciler = quotacheckpoint.AdminReconciler{}
}
