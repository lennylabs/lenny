// SPDX-License-Identifier: MIT

// Package tenantstoretest holds tenant-row fixtures shared by the tests
// of every tenantstore.Store implementation and of the gateway's tenant
// registry adapters, so each test runs the same table against its own
// backend.
package tenantstoretest

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// RegistrationCase is one tenant row in a §12.8 lifecycle position and
// the registered reading §10.2 requires of it.
type RegistrationCase struct {
	// Name labels the case in a subtest.
	Name string
	// Registered is the reading tenantstore.Tenant.Registered and every
	// IsRegistered implementation must report for the seeded row.
	Registered bool
	// mutate puts the created row into the case's lifecycle position.
	// nil leaves the row as created.
	mutate func(*tenantstore.Tenant)
	// softDelete tombstones the row through Store.SoftDelete, the
	// §12.8 Phase 6 writer, after mutate.
	softDelete bool
}

// RegistrationCases returns the registered-reading table over every
// §12.8 TenantState and an operator-suspended row: only `deleting` and
// `deleted` are refused. The `deleting` row keeps a zero DeletedAt, so
// it discriminates the State half of the reading from a DeletedAt-only
// check. spec: §10.2 (Authentication: `tenant_id` extraction), §12.8
// (Compliance Interfaces: Tenant deletion lifecycle).
func RegistrationCases() []RegistrationCase {
	return []RegistrationCase{
		{Name: "active", Registered: true, mutate: setState(tenantstore.TenantStateActive)},
		{Name: "empty-state", Registered: true, mutate: setState("")},
		{Name: "disabling", Registered: true, mutate: setState(tenantstore.TenantStateDisabling)},
		{Name: "deleting", Registered: false, mutate: setState(tenantstore.TenantStateDeleting)},
		{Name: "deleted", Registered: false, softDelete: true},
		{Name: "suspended", Registered: true, mutate: func(t *tenantstore.Tenant) {
			t.Suspended = true
			t.SuspendedAt = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		}},
	}
}

func setState(state string) func(*tenantstore.Tenant) {
	return func(t *tenantstore.Tenant) { t.State = state }
}

// Seed creates the case's row under id in store, moves it into the
// case's lifecycle position through the store's own writers, and
// returns the row as the store reads it back.
func (c RegistrationCase) Seed(ctx context.Context, store tenantstore.Store, id string) (tenantstore.Tenant, error) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, tenantstore.Tenant{
		ID: id, State: tenantstore.TenantStateActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return tenantstore.Tenant{}, fmt.Errorf("create tenant %s: %w", id, err)
	}
	if c.mutate != nil {
		if _, err := store.Update(ctx, id, func(t *tenantstore.Tenant) error {
			c.mutate(t)
			return nil
		}); err != nil {
			return tenantstore.Tenant{}, fmt.Errorf("update tenant %s: %w", id, err)
		}
	}
	if c.softDelete {
		if err := store.SoftDelete(ctx, id, now.Add(time.Hour)); err != nil {
			return tenantstore.Tenant{}, fmt.Errorf("soft-delete tenant %s: %w", id, err)
		}
	}
	row, err := store.Get(ctx, id)
	if err != nil {
		return tenantstore.Tenant{}, fmt.Errorf("read tenant %s: %w", id, err)
	}
	return row, nil
}
