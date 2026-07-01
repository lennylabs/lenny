// SPDX-License-Identifier: MIT

package quotabudget

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// UsageReader adapts a Tracker to policy.UsageReader so the gateway can
// wire the §12.4 in-memory budget mode into the existing QuotaEvaluator
// without changing the evaluator: the evaluator resolves the tenant limit
// and calls UsageHierarchical, which returns the in-memory per-replica
// tenant usage (base + local consumption) instead of the Redis counter.
//
// Only the tenant scope carries a value. The per-user and platform-global
// scopes report zero because the §12.4 line 268 in-memory budget mode is
// defined over the per-tenant token budget; QuotaEvaluator therefore gates
// solely on the tenant budget when this reader is wired.
type UsageReader struct {
	tracker *Tracker
}

// NewUsageReader wraps tracker as a policy.UsageReader.
func NewUsageReader(tracker *Tracker) *UsageReader {
	return &UsageReader{tracker: tracker}
}

// UsageHierarchical implements policy.UsageReader. It returns the
// in-memory tenant usage for the fixed-interval window of period
// containing at. A tracker error (slice exhausted with Postgres
// unreachable) propagates so QuotaEvaluator fails closed.
func (r *UsageReader) UsageHierarchical(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time) (quotastore.Scoped, error) {
	used, err := r.tracker.TenantUsed(ctx, tenantID, period, at)
	if err != nil {
		return quotastore.Scoped{}, err
	}
	return quotastore.Scoped{Tenant: used}, nil
}

// SlidingUsageHierarchical implements policy.UsageReader. The in-memory
// budget mode is defined over the fixed-interval windows (the durable
// token_usage_checkpoint has no single restorable rolling-window total),
// so a rolling-period tenant reports zero usage and is not enforced
// through this path. spec: §11.2 (rolling window); §12.4 line 268.
func (r *UsageReader) SlidingUsageHierarchical(ctx context.Context, tenantID, userID string, window, resolution time.Duration, at time.Time) (quotastore.Scoped, error) {
	return quotastore.Scoped{}, nil
}

// Ensure UsageReader satisfies policy.UsageReader at compile time.
var _ policy.UsageReader = (*UsageReader)(nil)
