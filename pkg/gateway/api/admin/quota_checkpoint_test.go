// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/quota/quotacheckpoint"
)

// fakeCheckpointReconciler answers a reconcile with a fixed result, or
// ErrTenantNotFound for a tenant it does not know, and records the scope
// it was asked for.
type fakeCheckpointReconciler struct {
	known string
	res   quotacheckpoint.ReconcileResult
	err   error
	got   []quotacheckpoint.ReconcileScope
}

func (f *fakeCheckpointReconciler) Reconcile(_ context.Context, scope quotacheckpoint.ReconcileScope) (quotacheckpoint.ReconcileResult, error) {
	f.got = append(f.got, scope)
	if f.err != nil {
		return quotacheckpoint.ReconcileResult{}, f.err
	}
	if scope.TenantID != "" && scope.TenantID != f.known {
		return quotacheckpoint.ReconcileResult{}, quotacheckpoint.ErrTenantNotFound
	}
	return f.res, nil
}

// TestQuotaCheckpointReconcilerMapsScopeResultAndErrors checks that the
// adapter passes the reconcile scope through, maps an unknown tenant to
// ErrQuotaTenantNotFound, which the handler answers with 404, passes any
// other failure through, and maps each restored window's MAX-rule inputs
// into the admin wire result.
//
// spec: §24.6 (Quota Operations), §15.1 (REST API)
func TestQuotaCheckpointReconcilerMapsScopeResultAndErrors(t *testing.T) {
	t.Parallel()
	svc := &fakeCheckpointReconciler{
		known: "acme",
		res: quotacheckpoint.ReconcileResult{
			TenantsReconciled: 1,
			CountersWritten:   1,
			Counters: []quotacheckpoint.CounterResult{{
				TenantID: "acme", Scope: quotacheckpoint.ScopeUser, SubjectID: "alice@acme.com", Period: "hourly",
				CheckpointValue: 400, InMemoryValue: 50, WrittenValue: 400,
			}},
		},
	}
	adapter := admin.QuotaCheckpointReconciler{Service: svc}

	if _, err := adapter.Reconcile(context.Background(), admin.QuotaReconcileScope{TenantID: "globex"}); !errors.Is(err, admin.ErrQuotaTenantNotFound) {
		t.Fatalf("unknown tenant err = %v, want admin.ErrQuotaTenantNotFound", err)
	}

	out, err := adapter.Reconcile(context.Background(), admin.QuotaReconcileScope{AllTenants: true})
	if err != nil {
		t.Fatalf("Reconcile(all): %v", err)
	}
	if out.CountersWritten != 1 || out.TenantsReconciled != 1 || len(out.Tenants) != 1 {
		t.Fatalf("admin result = %+v, want one tenant with one counter written", out)
	}
	if got := out.Tenants[0]; got.TenantID != "acme" || got.CheckpointValue != 400 || got.InMemoryValue != 50 || got.WrittenValue != 400 {
		t.Errorf("admin detail = %+v, want acme/400/50/400", got)
	}
	if want := (quotacheckpoint.ReconcileScope{AllTenants: true}); svc.got[1] != want {
		t.Errorf("scope passed = %+v, want %+v", svc.got[1], want)
	}

	boom := errors.New("checkpoint store down")
	failing := admin.QuotaCheckpointReconciler{Service: &fakeCheckpointReconciler{err: boom}}
	if _, err := failing.Reconcile(context.Background(), admin.QuotaReconcileScope{TenantID: "acme"}); !errors.Is(err, boom) || errors.Is(err, admin.ErrQuotaTenantNotFound) {
		t.Errorf("store failure err = %v, want the store's error passed through", err)
	}
}
