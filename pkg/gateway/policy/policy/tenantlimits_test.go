// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// spec: §11.2 line 31 — per-tenant token-quota reset period.

func limitsForTenant(t *testing.T, tenant tenantstore.Tenant, platformPeriod quota.ResetPeriod) TenantLimits {
	t.Helper()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	l := NewTenantStoreLimits(tenants, TenantStoreLimitsOptions{Period: platformPeriod})
	got, err := l.LookupLimits(context.Background(), tenant.ID)
	if err != nil {
		t.Fatalf("LookupLimits: %v", err)
	}
	return got
}

// TestLookupLimits_PerTenantResetPeriod_spec_11_2_31 verifies a tenant
// that names a valid reset period scopes its own token-usage window
// independently of the platform default.
func TestLookupLimits_PerTenantResetPeriod_spec_11_2_31(t *testing.T) {
	got := limitsForTenant(t, tenantstore.Tenant{
		ID: "acme", TokenQuotaPerWindow: 1000, QuotaResetPeriod: "monthly",
	}, quota.ResetHourly)
	if got.Period != quota.ResetMonthly {
		t.Fatalf("period = %q, want monthly (per-tenant override)", got.Period)
	}
}

// TestLookupLimits_EmptyResetPeriodInheritsPlatform_spec_11_2_31
// verifies an unset per-tenant period falls back to the platform-wide
// default.
func TestLookupLimits_EmptyResetPeriodInheritsPlatform_spec_11_2_31(t *testing.T) {
	got := limitsForTenant(t, tenantstore.Tenant{
		ID: "acme", TokenQuotaPerWindow: 1000,
	}, quota.ResetDaily)
	if got.Period != quota.ResetDaily {
		t.Fatalf("period = %q, want daily (platform default)", got.Period)
	}
}

// TestLookupLimits_InvalidResetPeriodInheritsPlatform_spec_11_2_31
// verifies an out-of-enum per-tenant period is ignored in favor of the
// platform default rather than propagating a bad window.
func TestLookupLimits_InvalidResetPeriodInheritsPlatform_spec_11_2_31(t *testing.T) {
	got := limitsForTenant(t, tenantstore.Tenant{
		ID: "acme", TokenQuotaPerWindow: 1000, QuotaResetPeriod: "fortnightly",
	}, quota.ResetHourly)
	if got.Period != quota.ResetHourly {
		t.Fatalf("period = %q, want hourly (invalid override ignored)", got.Period)
	}
}

// TestLookupLimits_DistinctTenantsDistinctPeriods_spec_11_2_31 verifies
// two tenants can run on different reset periods at the same time — the
// gap the §11.2 line 31 "configurable per quota type" wording requires.
func TestLookupLimits_DistinctTenantsDistinctPeriods_spec_11_2_31(t *testing.T) {
	tenants := tenantstore.NewMemory()
	ctx := context.Background()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", TokenQuotaPerWindow: 1, QuotaResetPeriod: "daily"}); err != nil {
		t.Fatalf("seed acme: %v", err)
	}
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "globex", TokenQuotaPerWindow: 1, QuotaResetPeriod: "monthly"}); err != nil {
		t.Fatalf("seed globex: %v", err)
	}
	l := NewTenantStoreLimits(tenants, TenantStoreLimitsOptions{Period: quota.ResetHourly})
	acme, err := l.LookupLimits(ctx, "acme")
	if err != nil {
		t.Fatalf("lookup acme: %v", err)
	}
	globex, err := l.LookupLimits(ctx, "globex")
	if err != nil {
		t.Fatalf("lookup globex: %v", err)
	}
	if acme.Period != quota.ResetDaily || globex.Period != quota.ResetMonthly {
		t.Fatalf("periods = (acme %q, globex %q), want (daily, monthly)", acme.Period, globex.Period)
	}
}
