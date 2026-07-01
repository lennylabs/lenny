// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// TenantStoreLimits resolves the §11.2 hierarchical token budget from
// the tenant registry. The tenant-scope limit is the tenant row's
// `TokenQuotaPerWindow`; the global scope is a platform-wide default
// the gateway supplies; the per-user scope is a platform-wide
// per-user fraction or an explicit per-user limit.
//
// TenantStoreLimits is the production TenantLimitLookup the gateway
// wires into QuotaEvaluator.
type TenantStoreLimits struct {
	tenants tenantstore.Store

	// globalLimit is the platform-wide per-window token limit applied
	// to every tenant at the global scope. Zero disables the global
	// cap.
	globalLimit int64

	// userLimit is the platform-wide per-window per-user token limit.
	// Zero means no per-user cap (the tenant limit binds every user).
	userLimit int64

	// period is the §11.2 reset period the limits and the usage
	// counter share.
	period quota.ResetPeriod

	// rollingWindow is the §11.2 rolling-window length applied when the
	// resolved period is quota.ResetRolling. A non-positive value lets
	// QuotaEvaluator fall back to DefaultRollingWindow.
	rollingWindow time.Duration
}

// TenantStoreLimitsOptions configures NewTenantStoreLimits.
type TenantStoreLimitsOptions struct {
	// GlobalTokenQuotaPerWindow is the platform-wide global per-window
	// token limit. Zero disables the global cap.
	GlobalTokenQuotaPerWindow int64

	// UserTokenQuotaPerWindow is the platform-wide per-user per-window
	// token limit. Zero disables the per-user cap.
	UserTokenQuotaPerWindow int64

	// Period is the §11.2 reset period. The zero value selects
	// quota.ResetHourly.
	Period quota.ResetPeriod

	// RollingWindow is the §11.2 rolling-window length applied when the
	// resolved period is quota.ResetRolling. A non-positive value lets
	// QuotaEvaluator fall back to DefaultRollingWindow.
	RollingWindow time.Duration
}

// NewTenantStoreLimits returns a TenantLimitLookup backed by the
// tenant registry.
func NewTenantStoreLimits(tenants tenantstore.Store, opts TenantStoreLimitsOptions) *TenantStoreLimits {
	period := opts.Period
	if period == "" {
		period = quota.ResetHourly
	}
	return &TenantStoreLimits{
		tenants:       tenants,
		globalLimit:   opts.GlobalTokenQuotaPerWindow,
		userLimit:     opts.UserTokenQuotaPerWindow,
		period:        period,
		rollingWindow: opts.RollingWindow,
	}
}

// LookupLimits implements TenantLimitLookup. It reads the tenant row's
// `TokenQuotaPerWindow` as the tenant-scope limit and combines it with
// the platform-wide global and per-user limits. A soft-deleted tenant
// is treated as not found so the evaluator fails closed.
func (l *TenantStoreLimits) LookupLimits(ctx context.Context, tenantID string) (TenantLimits, error) {
	tenant, err := l.tenants.Get(ctx, tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return TenantLimits{}, ErrTenantNotFound
	}
	if err != nil {
		return TenantLimits{}, err
	}
	if !tenant.IsActive() {
		return TenantLimits{}, ErrTenantNotFound
	}
	// §11.2 nesting rule: a user limit cannot exceed the tenant's. When
	// the platform per-user cap is wider than the tenant's own limit,
	// clamp it to the tenant limit so the resolved hierarchy is valid.
	userLimit := l.userLimit
	if tenant.TokenQuotaPerWindow > 0 && userLimit > tenant.TokenQuotaPerWindow {
		userLimit = tenant.TokenQuotaPerWindow
	}
	// §11.2 line 31: the reset period is configurable per tenant. A
	// tenant that names a valid period scopes its own token-usage
	// window; an empty or invalid value inherits the platform default.
	period := l.period
	if p := quota.ResetPeriod(tenant.QuotaResetPeriod); p.IsValid() {
		period = p
	}
	return TenantLimits{
		Global:        l.globalLimit,
		Tenant:        tenant.TokenQuotaPerWindow,
		User:          userLimit,
		Period:        period,
		RollingWindow: l.rollingWindow,
	}, nil
}

// Ensure TenantStoreLimits satisfies TenantLimitLookup at compile time.
var _ TenantLimitLookup = (*TenantStoreLimits)(nil)
