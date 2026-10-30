// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// countingKMSProbe records every T4 availability probe it is asked to run.
type countingKMSProbe struct {
	mu     sync.Mutex
	probed []string
}

func (p *countingKMSProbe) ProbeAvailability(_ context.Context, tenantID, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.probed = append(p.probed, tenantID)
	return nil
}

func (p *countingKMSProbe) LastProbeSuccess(string) (time.Time, bool) { return time.Time{}, false }

func (p *countingKMSProbe) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.probed...)
}

// seedLifecycleTenants creates a tenant in `deleting`, a tenant
// tombstoned by SoftDelete, and an active twin, each T4 with a display
// name the seed will differ from.
func seedLifecycleTenants(t *testing.T, tenants *tenantstore.Memory) {
	t.Helper()
	ctx := context.Background()
	for _, id := range []string{"acme-deleting", "acme-deleted", "acme-active"} {
		if err := tenants.Create(ctx, tenantstore.Tenant{
			ID: id, DisplayName: "stored", WorkspaceTier: tenantstore.WorkspaceTierT4,
			State: tenantstore.TenantStateActive,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tenants.Update(ctx, "acme-deleting", func(row *tenantstore.Tenant) error {
		row.State = tenantstore.TenantStateDeleting
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := tenants.SoftDelete(ctx, "acme-deleted", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
}

// TestUpsertTenantsRefusesDeletingAndDeletedTenants_spec_17_6 runs the
// bootstrap tenant seed once without and once with --force-update over a
// `deleting` tenant, a tombstone, and an active twin, each named with a
// changed displayName. Both runs report the deleting and tombstoned
// entries as SEED_VALIDATION errors and leave their State, DeletedAt,
// and Version unchanged, with no KMS probe and no sequence provisioning;
// the closed DDL pool turns any provisioning attempt into a
// SEED_STORE_ERROR, which the active twin shows. The force-update run
// updates the active twin. The force-update run over the tombstone
// fails against a seed that clears DeletedAt to revive the tenant.
// spec: 17.6 (Packaging and Installation), 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 15.1
func TestUpsertTenantsRefusesDeletingAndDeletedTenants_spec_17_6(t *testing.T) {
	for _, run := range []struct {
		name string
		opts bootstrapOptions
	}{
		{"without force-update", bootstrapOptions{}},
		{"with force-update", bootstrapOptions{forceUpdate: true}},
		{"dry run with force-update", bootstrapOptions{forceUpdate: true, dryRun: true}},
	} {
		t.Run(run.name, func(t *testing.T) {
			ctx := context.Background()
			tenants := tenantstore.NewMemory()
			seedLifecycleTenants(t, tenants)
			before := map[string]tenantstore.Tenant{}
			for _, id := range []string{"acme-deleting", "acme-deleted"} {
				row, err := tenants.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				before[id] = row
			}

			pool := lazyPool(t, "postgres://ddl@127.0.0.1:1/lenny")
			pool.Close()
			probe := &countingKMSProbe{}
			r := NewRouter(tenants, Options{BillingAuditDDLPool: pool, PrimaryDDLPool: pool}).WithKMSProbe(probe)
			req := httptest.NewRequest("POST", "/v1/admin/bootstrap", nil)
			seed := []TenantPayload{
				{ID: "acme-deleting", DisplayName: "seeded", WorkspaceTier: tenantstore.WorkspaceTierT4},
				{ID: "acme-deleted", DisplayName: "seeded", WorkspaceTier: tenantstore.WorkspaceTierT4},
				{ID: "acme-active", DisplayName: "seeded", WorkspaceTier: tenantstore.WorkspaceTierT4},
			}
			section := r.upsertTenants(req, seed, run.opts)

			errsByID := map[string]BootstrapError{}
			for _, e := range section.Errors {
				errsByID[e.ID] = e
			}
			for id, wantState := range map[string]string{
				"acme-deleting": tenantstore.TenantStateDeleting,
				"acme-deleted":  tenantstore.TenantStateDeleted,
			} {
				e, ok := errsByID[id]
				if !ok {
					t.Fatalf("%s: no error entry; section=%+v", id, section)
				}
				if e.Code != seedValidationCode {
					t.Errorf("%s: code = %q, want %q (a store error means the seed provisioned sequences)", id, e.Code, seedValidationCode)
				}
				if !strings.Contains(e.Message, id) || !strings.Contains(e.Message, wantState) {
					t.Errorf("%s: message %q must name the tenant and its state %s", id, e.Message, wantState)
				}
				after, err := tenants.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				b := before[id]
				if after.State != b.State || !after.DeletedAt.Equal(b.DeletedAt) || after.Version != b.Version || after.DisplayName != b.DisplayName {
					t.Errorf("%s changed: before state=%q deletedAt=%v version=%d name=%q, after state=%q deletedAt=%v version=%d name=%q",
						id, b.State, b.DeletedAt, b.Version, b.DisplayName, after.State, after.DeletedAt, after.Version, after.DisplayName)
				}
			}
			for _, id := range probe.calls() {
				if id != "acme-active" {
					t.Errorf("KMS probe ran for refused tenant %s", id)
				}
			}

			twin, err := tenants.Get(ctx, "acme-active")
			if err != nil {
				t.Fatal(err)
			}
			wantName := "stored"
			if run.opts.forceUpdate && !run.opts.dryRun {
				wantName = "seeded"
			}
			if twin.DisplayName != wantName {
				t.Errorf("active twin displayName = %q, want %q", twin.DisplayName, wantName)
			}
			if !run.opts.dryRun {
				e, ok := errsByID["acme-active"]
				if !ok || e.Code != seedStoreErrorCode {
					t.Errorf("active twin: entry %+v, want the %s a provisioning attempt on the closed pool raises", e, seedStoreErrorCode)
				}
			}
		})
	}
}

// TestUpsertTenantsRefusesReservedPlatformID_spec_10_2 pins the
// bootstrap half of the platform reservation: a seed entry named
// `platform` is an error and creates no row, and `platform-eu` is
// created.
// spec: 10.2 (Authentication: `tenant_id` extraction), 17.6 (Packaging and Installation)
func TestUpsertTenantsRefusesReservedPlatformID_spec_10_2(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	r := NewRouter(tenants, Options{})
	req := httptest.NewRequest("POST", "/v1/admin/bootstrap", nil)
	section := r.upsertTenants(req, []TenantPayload{{ID: "platform"}, {ID: "platform-eu"}}, bootstrapOptions{})

	if len(section.Errors) != 1 || section.Errors[0].ID != "platform" || section.Errors[0].Code != seedValidationCode {
		t.Fatalf("errors = %+v, want one SEED_VALIDATION entry for platform", section.Errors)
	}
	if section.CreatedCount != 1 {
		t.Errorf("createdCount = %d, want 1 (platform-eu)", section.CreatedCount)
	}
	if _, err := tenants.Get(ctx, "platform"); err == nil {
		t.Error("the seed created a platform tenant row")
	}
	if _, err := tenants.Get(ctx, "platform-eu"); err != nil {
		t.Errorf("platform-eu not created: %v", err)
	}
}
