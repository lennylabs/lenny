// SPDX-License-Identifier: MIT

package quotacheckpoint

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// AdminReconciler adapts *Service to the admin.QuotaReconciler seam behind
// `POST /v1/admin/quota/reconcile`, mapping the §24.6 reconcile scope and
// result between the admin wire types and this package's types. It is the
// production wiring that turns the 503 QUOTA_RECONCILE_UNAVAILABLE stub
// into a real reconcile once the Postgres checkpoint store is present.
//
// spec: §24.6 line 99; §15.1 line 879.
type AdminReconciler struct {
	Service *Service
}

var _ admin.QuotaReconciler = AdminReconciler{}

// Reconcile implements admin.QuotaReconciler.
func (a AdminReconciler) Reconcile(ctx context.Context, scope admin.QuotaReconcileScope) (admin.QuotaReconcileResult, error) {
	res, err := a.Service.Reconcile(ctx, ReconcileScope{
		AllTenants: scope.AllTenants,
		TenantID:   scope.TenantID,
	})
	if err != nil {
		if errors.Is(err, ErrTenantNotFound) {
			return admin.QuotaReconcileResult{}, admin.ErrQuotaTenantNotFound
		}
		return admin.QuotaReconcileResult{}, err
	}
	out := admin.QuotaReconcileResult{
		TenantsReconciled: res.TenantsReconciled,
		CountersWritten:   res.CountersWritten,
	}
	for _, c := range res.Counters {
		out.Tenants = append(out.Tenants, admin.QuotaTenantReconcileResult{
			TenantID:        c.TenantID,
			CheckpointValue: c.CheckpointValue,
			InMemoryValue:   c.InMemoryValue,
			WrittenValue:    c.WrittenValue,
		})
	}
	return out, nil
}
