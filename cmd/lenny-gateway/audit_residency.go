// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// tenantResidencyLookup adapts the tenant store to the §11.7 CMP-058
// auditstore.ResidencyLookup seam. It resolves a target tenant's
// dataResidencyRegion so the platform-tenant audit residency gate can
// route a platform-tenant audit event that references that tenant to the
// tenant's regional platform-Postgres. A tenant that is not found
// resolves to "" so the write falls back to the global platform-Postgres
// (residency rule 2). The store returns soft-deleted rows, so a
// tombstoned tenant's dataResidencyRegion snapshot is still honored while
// the row remains queryable. spec: §11.7 lines 431-432. F-11.7.9.
type tenantResidencyLookup struct {
	tenants tenantstore.Store
}

func (l tenantResidencyLookup) TargetResidencyRegion(ctx context.Context, targetTenantID string) (string, error) {
	t, err := l.tenants.Get(ctx, targetTenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return t.DataResidencyRegion, nil
}
