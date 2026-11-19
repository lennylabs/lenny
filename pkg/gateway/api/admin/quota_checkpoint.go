// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/quota/quotacheckpoint"
)

// CheckpointReconciler is the reconcile of the Postgres token-usage
// checkpoint service, *quotacheckpoint.Service.
type CheckpointReconciler interface {
	Reconcile(ctx context.Context, scope quotacheckpoint.ReconcileScope) (quotacheckpoint.ReconcileResult, error)
}

// QuotaCheckpointReconciler adapts the checkpoint service to the
// QuotaReconciler behind `POST /v1/admin/quota/reconcile`, mapping the
// reconcile scope and result between the admin wire types and the
// checkpoint service's types. It is the production wiring that turns the
// 503 QUOTA_RECONCILE_UNAVAILABLE answer into a reconcile once the Postgres
// checkpoint store is present. It sits on the admin side so that the quota
// package imports no part of the gateway.
//
// spec: §24.6 (Quota Operations), §15.1 (REST API)
type QuotaCheckpointReconciler struct {
	Service CheckpointReconciler
}

var _ QuotaReconciler = QuotaCheckpointReconciler{}

// Reconcile implements QuotaReconciler.
func (a QuotaCheckpointReconciler) Reconcile(ctx context.Context, scope QuotaReconcileScope) (QuotaReconcileResult, error) {
	res, err := a.Service.Reconcile(ctx, quotacheckpoint.ReconcileScope{
		AllTenants: scope.AllTenants,
		TenantID:   scope.TenantID,
	})
	if err != nil {
		if errors.Is(err, quotacheckpoint.ErrTenantNotFound) {
			return QuotaReconcileResult{}, ErrQuotaTenantNotFound
		}
		return QuotaReconcileResult{}, err
	}
	out := QuotaReconcileResult{
		TenantsReconciled: res.TenantsReconciled,
		CountersWritten:   res.CountersWritten,
	}
	for _, c := range res.Counters {
		out.Tenants = append(out.Tenants, QuotaTenantReconcileResult{
			TenantID:        c.TenantID,
			CheckpointValue: c.CheckpointValue,
			InMemoryValue:   c.InMemoryValue,
			WrittenValue:    c.WrittenValue,
		})
	}
	return out, nil
}
