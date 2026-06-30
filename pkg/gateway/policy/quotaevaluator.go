// SPDX-License-Identifier: MIT

// Package policy holds the gateway's built-in §4.8 policy
// interceptors. These are Go values implementing
// interceptor.Interceptor that the gateway registers on the §4.8
// built-in chain at fixed reserved priorities.
//
// QuotaEvaluator is the §4.8 built-in that enforces the §11.2
// hierarchical token budget on the admission path. AuditSink emits the
// §11.7 `interceptor.rejected` audit row when a chain REJECTs. Both run
// inside the gateway process; neither is an external interceptor.
package policy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// QuotaEvaluatorPriority is the §4.8 built-in priority for
// QuotaEvaluator. The §4.8 built-in interceptor table fixes it at 200
// (below AuthEvaluator at 100, above every external interceptor).
const QuotaEvaluatorPriority int32 = 200

// QuotaEvaluatorName identifies QuotaEvaluator in audit rows and chain
// errors.
const QuotaEvaluatorName = "QuotaEvaluator"

// CodeQuotaExceeded is the §15.1 error code a quota rejection carries.
// The gateway maps it to HTTP 429.
const CodeQuotaExceeded = "QUOTA_EXCEEDED"

// Metadata keys QuotaEvaluator reads from interceptor.Request.Metadata.
// AuthEvaluator (priority 100, PreAuth) populates the authenticated
// identity; QuotaEvaluator (priority 200, PostAuth) reads it. Per §4.8
// the metadata map carries the authenticated identity at every
// priority above the reserved ceiling.
const (
	// MetadataTenantID is the authenticated tenant id.
	MetadataTenantID = "tenant_id"
	// MetadataUserID is the authenticated user id (the OIDC subject).
	MetadataUserID = "user_id"
)

// TenantLimits is the §11.2 hierarchical token budget resolved for one
// tenant: the configured global, tenant, and user per-window limits.
// A non-positive limit at any scope means that scope is unlimited.
type TenantLimits struct {
	// Global is the platform-wide per-window token limit. Zero means
	// the platform sets no global cap.
	Global int64

	// Tenant is the tenant's per-window token limit
	// (`Tenant.TokenQuotaPerWindow`). Zero means the tenant has no
	// token budget configured.
	Tenant int64

	// User is the per-user per-window token limit. Zero means the
	// platform applies the tenant limit to every user without a
	// narrower per-user cap.
	User int64

	// Period is the §11.2 reset period the limits and the usage
	// counter share. The zero value is treated as quota.ResetHourly.
	Period quota.ResetPeriod

	// RollingWindow is the rolling-window length applied when Period is
	// quota.ResetRolling. A non-positive value selects
	// DefaultRollingWindow. It is ignored for the fixed-interval
	// periods. spec: §11.2 ("rolling window").
	RollingWindow time.Duration
}

// TenantLimitLookup resolves the §11.2 hierarchical token budget for a
// tenant. The gateway backs it with the tenant registry
// (pkg/gateway/tenantstore).
type TenantLimitLookup interface {
	// LookupLimits returns the resolved limits for tenantID. A lookup
	// for an unknown tenant returns ErrTenantNotFound so the evaluator
	// can fail closed.
	LookupLimits(ctx context.Context, tenantID string) (TenantLimits, error)
}

// ErrTenantNotFound — the tenant id is not in the registry. A
// fail-closed QuotaEvaluator rejects a request for an unknown tenant.
var ErrTenantNotFound = errors.New("policy: tenant not found")

// UsageReader reads the §11.2 recorded token usage at every scope of
// the hierarchy (global ⊇ tenant ⊇ user) for a (tenant, user) window.
// The gateway backs it with the Redis-backed quotastore.Counter.
type UsageReader interface {
	// UsageHierarchical returns the recorded token totals at the user,
	// tenant, and global scopes for the fixed-interval window of period
	// containing at. A scope with no recorded usage reads as 0.
	UsageHierarchical(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time) (quotastore.Scoped, error)
	// SlidingUsageHierarchical is the rolling-window counterpart, summing
	// resolution-sized buckets across the rolling window ending at at.
	SlidingUsageHierarchical(ctx context.Context, tenantID, userID string, window, resolution time.Duration, at time.Time) (quotastore.Scoped, error)
}

// DefaultRollingWindow is the §11.2 rolling-window length applied when a
// tenant configures the `rolling` reset period but no explicit window
// length is resolved. One hour matches the hourly fixed window so a
// tenant switching to `rolling` keeps a comparable budget horizon. The
// value is operator-tunable (--quota-rolling-window-seconds).
const DefaultRollingWindow = time.Hour

// Compile-time assertion that the Redis counter satisfies UsageReader.
var _ UsageReader = (*quotastore.Counter)(nil)

// QuotaEvaluator is the §4.8 built-in QuotaEvaluator interceptor. It
// enforces the §11.2 hierarchical token budget (global → tenant →
// user) on the admission path: it reads the tenant's configured limits
// and the recorded window usage, runs quota.HierarchicalCheck, and
// returns ActionReject naming the bound scope when a window is
// hard-exceeded.
//
// QuotaEvaluator is built-in (Builtin() == true), registers at the
// reserved priority 200, and is fail-closed: a limit-lookup or
// usage-read error rejects the request rather than admitting it
// unmetered. The gateway registers it on the PostAuth phase chain.
//
// QuotaEvaluator is token-budget-only. The §8.3 contentPolicy.maxInputSize
// byte cap on TaskSpec.input is owned by DelegationPolicyEvaluator at
// PreDelegation (the phase whose payload is the delegation input);
// QuotaEvaluator does not measure input size. The §11.2 user, tenant,
// and global scopes are read from three independent counter windows
// (UsageReader.UsageHierarchical), so each scope threshold fires on its
// own measurement.
type QuotaEvaluator struct {
	limits TenantLimitLookup
	usage  UsageReader
	clock  func() time.Time
}

// NewQuotaEvaluator returns a QuotaEvaluator backed by limits and
// usage. clock overrides time.Now for tests; pass nil in production.
func NewQuotaEvaluator(limits TenantLimitLookup, usage UsageReader, clock func() time.Time) *QuotaEvaluator {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &QuotaEvaluator{limits: limits, usage: usage, clock: clock}
}

// Name implements interceptor.Interceptor.
func (e *QuotaEvaluator) Name() string { return QuotaEvaluatorName }

// Priority implements interceptor.Interceptor. QuotaEvaluator is a
// built-in security-critical interceptor and registers at the reserved
// priority 200.
func (e *QuotaEvaluator) Priority() int32 { return QuotaEvaluatorPriority }

// Builtin implements interceptor.Interceptor. QuotaEvaluator is a
// built-in, so it may register at a priority within the reserved
// ceiling.
func (e *QuotaEvaluator) Builtin() bool { return true }

// FailPolicy implements interceptor.Interceptor. QuotaEvaluator is
// fail-closed: a limit-lookup or usage-read failure is resolved as a
// REJECT so a backing-store outage cannot silently un-enforce the
// budget. Note that the chain's own fail-closed handling only applies
// to an interceptor error; QuotaEvaluator additionally fails closed
// on a fault it can describe by returning an explicit ActionReject.
func (e *QuotaEvaluator) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }

// Timeout implements interceptor.Interceptor. A non-positive value
// selects interceptor.DefaultTimeout.
func (e *QuotaEvaluator) Timeout() time.Duration { return 0 }

// Intercept implements interceptor.Interceptor. It resolves the
// tenant's §11.2 token limits, reads the recorded window usage for the
// (tenant, user) pair, and runs the global → tenant → user
// hierarchical check. A hard-exceeded window returns ActionReject
// naming the bound scope; otherwise the request is admitted with
// ActionAllow (a soft warning is admitted — the billing-event signal
// is emitted elsewhere).
//
// A missing tenant id, an unknown tenant, or a backing-store error
// returns ActionReject so the evaluator fails closed.
func (e *QuotaEvaluator) Intercept(ctx context.Context, req interceptor.Request) (interceptor.Result, error) {
	tenantID := req.Metadata[MetadataTenantID]
	if tenantID == "" {
		tenantID = req.TenantID
	}
	if tenantID == "" {
		return interceptor.Result{
			Action: interceptor.ActionReject,
			Code:   CodeQuotaExceeded,
			Reason: "quota evaluation requires an authenticated tenant; none was present in the request metadata",
		}, nil
	}
	userID := req.Metadata[MetadataUserID]

	limits, err := e.limits.LookupLimits(ctx, tenantID)
	if err != nil {
		if errors.Is(err, ErrTenantNotFound) {
			return interceptor.Result{
				Action: interceptor.ActionReject,
				Code:   CodeQuotaExceeded,
				Reason: fmt.Sprintf("quota evaluation could not resolve limits for tenant %q", tenantID),
			}, nil
		}
		// A backing-store fault — fail closed by surfacing the error so
		// the chain's fail-closed handling rejects the request.
		return interceptor.Result{}, fmt.Errorf("quota limit lookup for tenant %q: %w", tenantID, err)
	}

	period := limits.Period
	if period == "" {
		period = quota.ResetHourly
	}

	h := quota.Hierarchy{
		Global: limits.Global,
		Tenant: limits.Tenant,
		User:   limits.User,
	}
	// With no configured limit at any scope the tenant has no token
	// budget; admit without a counter read.
	if h.Global <= 0 && h.Tenant <= 0 && h.User <= 0 {
		return interceptor.Result{Action: interceptor.ActionAllow}, nil
	}

	now := e.clock()
	// spec: §11.2 — read one independent window total per scope. The
	// §12.4 counter keys the per-user window per (tenant, user); the
	// per-tenant rollup and the platform-wide global rollup accumulate
	// in their own windows (quotastore reserved scope slots), so the
	// global → tenant → user check tests three distinct measurements.
	// The recorder advances all three on each upstream response.
	var used quotastore.Scoped
	if period == quota.ResetRolling {
		// spec: §11.2 ("rolling window") — the rolling reset period is a
		// sliding-window sum, not a fixed bucket; read it via the
		// sliding-window counter at the resolved window length.
		window := limits.RollingWindow
		if window <= 0 {
			window = DefaultRollingWindow
		}
		used, err = e.usage.SlidingUsageHierarchical(ctx, tenantID, userID, window, quotastore.DefaultBucketResolution, now)
	} else {
		used, err = e.usage.UsageHierarchical(ctx, tenantID, userID, period, now)
	}
	if err != nil {
		return interceptor.Result{}, fmt.Errorf("quota usage read for tenant %q user %q: %w", tenantID, userID, err)
	}

	res := quota.HierarchicalCheck(used.Global, used.Tenant, used.User, h)
	if res.State == quota.StateHardExceeded {
		return interceptor.Result{
			Action: interceptor.ActionReject,
			Code:   CodeQuotaExceeded,
			Reason: fmt.Sprintf("the %s token quota is exhausted: %d of %d tokens used in the current %s window",
				res.Scope, res.Used, res.Limit, period),
		}, nil
	}
	return interceptor.Result{Action: interceptor.ActionAllow}, nil
}

// Ensure QuotaEvaluator satisfies the interceptor contract at compile
// time.
var _ interceptor.Interceptor = (*QuotaEvaluator)(nil)
