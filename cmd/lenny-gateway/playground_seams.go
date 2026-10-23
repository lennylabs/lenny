// SPDX-License-Identifier: MIT

package main

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// playgroundTenantRegistry adapts a tenantstore.Store into the
// playground.TenantRegistry that the §27.3.1 OIDC callback and apiKey
// mint admission and the §27.2 layer-4 dev-mode Ready-gate consult. It
// applies the same registration reading as bearerTenantRegistry
// (tenantRegistered), so a `deleting` or `deleted` tenant, `default`
// included, is not registered and a store error refuses.
// spec: §10.2, §27.3.1, §12.8.
type playgroundTenantRegistry struct {
	store tenantstore.Store
}

func (r playgroundTenantRegistry) IsRegistered(tenantID string) (bool, error) {
	return tenantRegistered(context.Background(), r.store, tenantID)
}
