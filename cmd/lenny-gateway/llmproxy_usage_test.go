// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionusage"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// fakeQuotaLimits is a policy.TenantLimitLookup test double returning a
// fixed reset period for every tenant.
type fakeQuotaLimits struct {
	period        quota.ResetPeriod
	rollingWindow time.Duration
}

func (f fakeQuotaLimits) LookupLimits(_ context.Context, _ string) (policy.TenantLimits, error) {
	return policy.TenantLimits{Period: f.period, RollingWindow: f.rollingWindow}, nil
}

// TestProxyUsageRecorderRecordsProxyMode_Spec4_9_1468 confirms a
// proxy-mode lease's authoritative counts land in the usagestore with
// the lease's tenant and the session's runtime.
// spec: spec/04_system-components.md line 1468.
func TestProxyUsageRecorderRecordsProxyMode_Spec4_9_1468(t *testing.T) {
	var sessions sessionstore.Store = memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: "s_1", TenantID: "acme", RuntimeRef: "claude-prod", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	usage := usagestore.NewMemory()
	rec := newProxyUsageRecorder(usage, sessions, nil, nil, nil, nil)
	if rec == nil {
		t.Fatal("newProxyUsageRecorder returned nil with a usage store set")
	}

	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-1", SessionID: "s_1", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}, llmproxy.Usage{InputTokens: 100, OutputTokens: 30})

	report, err := usage.Aggregate(context.Background(), "acme", nil)
	if err != nil {
		t.Fatalf("usage.Aggregate: %v", err)
	}
	if report.TotalTokens.Input != 100 || report.TotalTokens.Output != 30 {
		t.Errorf("tokens not recorded: got %+v want input=100 output=30", report.TotalTokens)
	}
	if len(report.ByRuntime) != 1 || report.ByRuntime[0].Runtime != "claude-prod" {
		t.Errorf("runtime rollup absent: %+v", report.ByRuntime)
	}
}

// TestProxyUsageRecorderIgnoresDirectMode_Spec4_9_1468 confirms a
// direct-mode lease's counts are not double-counted by the proxy
// recorder (the §4.9 LLM proxy never sees direct-mode traffic; the
// defensive check guards against future regressions).
// spec: spec/04_system-components.md line 1468.
func TestProxyUsageRecorderIgnoresDirectMode_Spec4_9_1468(t *testing.T) {
	usage := usagestore.NewMemory()
	rec := newProxyUsageRecorder(usage, memstore.New(), nil, nil, nil, nil)
	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-2", SessionID: "s_2", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryDirect,
	}, llmproxy.Usage{InputTokens: 99, OutputTokens: 1})
	report, _ := usage.Aggregate(context.Background(), "acme", nil)
	if report.TotalTokens.Input != 0 || report.TotalTokens.Output != 0 {
		t.Errorf("direct-mode counts leaked into usagestore: %+v", report.TotalTokens)
	}
}

// TestProxyUsageRecorderDropsTenantlessLease_Spec4_9_1468 confirms a
// lease without a tenant attribution is dropped rather than producing
// a tenant-empty usage series.
// spec: spec/04_system-components.md line 1468.
func TestProxyUsageRecorderDropsTenantlessLease_Spec4_9_1468(t *testing.T) {
	usage := usagestore.NewMemory()
	rec := newProxyUsageRecorder(usage, memstore.New(), nil, nil, nil, nil)
	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-3", SessionID: "s_3", TenantID: "",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}, llmproxy.Usage{InputTokens: 5, OutputTokens: 5})
	report, _ := usage.Aggregate(context.Background(), "", nil)
	if report.TotalSessions != 0 || report.TotalTokens.Input != 0 {
		t.Errorf("tenantless lease leaked into the usagestore: %+v", report)
	}
}

// TestProxyUsageRecorderSessionMissOmitsRuntime_Spec4_9_1468 confirms
// the recorder still records tenant-scoped counts when the session
// lookup misses (the byTenant rollup must keep reporting).
// spec: spec/04_system-components.md line 1468.
func TestProxyUsageRecorderSessionMissOmitsRuntime_Spec4_9_1468(t *testing.T) {
	usage := usagestore.NewMemory()
	rec := newProxyUsageRecorder(usage, memstore.New(), nil, nil, nil, nil)
	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-4", SessionID: "missing", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}, llmproxy.Usage{InputTokens: 7, OutputTokens: 3})
	report, _ := usage.Aggregate(context.Background(), "acme", nil)
	if report.TotalTokens.Input != 7 || report.TotalTokens.Output != 3 {
		t.Errorf("tokens not recorded on session miss: %+v", report.TotalTokens)
	}
	if len(report.ByRuntime) != 0 {
		t.Errorf("byRuntime should be empty on session miss: %+v", report.ByRuntime)
	}
}

// TestProxyUsageRecorderNilUsageReturnsNil confirms the recorder skips
// wiring when no usagestore is configured.
func TestProxyUsageRecorderNilUsageReturnsNil(t *testing.T) {
	if rec := newProxyUsageRecorder(nil, memstore.New(), nil, nil, nil, nil); rec != nil {
		t.Errorf("newProxyUsageRecorder(nil, _) = %v, want nil", rec)
	}
}

// recordTerminator captures sessionbudget.Terminator calls.
type recordTerminator struct{ calls []string }

func (r *recordTerminator) TerminateSession(sessionID, _ string) {
	r.calls = append(r.calls, sessionID)
}

// TestProxyUsageRecorderEnforcesSessionBudget_Spec11_2 proves F-11.2.21:
// the recorder feeds the §11.2 mid-session enforcer each request's
// proxy-recorded tokens against the session's §8.2 lease token budget,
// and the enforcer terminates the session and closes its pre-flight gate
// once the cumulative usage exhausts the budget.
func TestProxyUsageRecorderEnforcesSessionBudget_Spec11_2(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "s_1", TenantID: "acme", RuntimeRef: "claude-prod", State: session.StateRunning,
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: 200},
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	term := &recordTerminator{}
	enforcer := sessionbudget.New(term, nil)
	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, nil, nil, nil, enforcer)

	lease := credential.Lease{
		LeaseID: "cl-1", SessionID: "s_1", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}
	// First request stays under the 200-token budget: no termination, gate
	// open.
	rec.RecordUsage(lease, llmproxy.Usage{InputTokens: 80, OutputTokens: 40}) // 120
	if !enforcer.Allow("s_1") {
		t.Fatalf("session under budget must be allowed")
	}
	if len(term.calls) != 0 {
		t.Fatalf("no termination expected under budget, got %v", term.calls)
	}
	// Second request crosses the budget (cumulative 240 >= 200): the
	// enforcer terminates the session and the gate closes.
	rec.RecordUsage(lease, llmproxy.Usage{InputTokens: 90, OutputTokens: 30}) // +120 = 240
	if enforcer.Allow("s_1") {
		t.Fatalf("exhausted session must be denied by the pre-flight gate")
	}
	if len(term.calls) != 1 || term.calls[0] != "s_1" {
		t.Fatalf("budget termination = %v, want [s_1]", term.calls)
	}
}

// TestProxyUsageRecorderNoBudgetNoEnforcement_Spec11_2 confirms a session
// without a lease token budget is never terminated for budget reasons (an
// unbounded session is the §8.2 default).
func TestProxyUsageRecorderNoBudgetNoEnforcement_Spec11_2(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "s_1", TenantID: "acme", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	term := &recordTerminator{}
	enforcer := sessionbudget.New(term, nil)
	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, nil, nil, nil, enforcer)
	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-1", SessionID: "s_1", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}, llmproxy.Usage{InputTokens: 10_000, OutputTokens: 10_000})
	if !enforcer.Allow("s_1") {
		t.Fatalf("an unbounded session must stay allowed regardless of usage")
	}
	if len(term.calls) != 0 {
		t.Fatalf("no termination for an unbounded session, got %v", term.calls)
	}
}

func newTestQuotaCounter(t *testing.T) *quotastore.Counter {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	return quotastore.New(cl)
}

// TestProxyUsageRecorderAdvancesQuotaCounter_Spec11_2 proves F-11.2.2:
// proxy-extracted counts advance the §11.2 hierarchical token counter at
// all three scopes (per-user, per-tenant rollup, global rollup) so
// QuotaEvaluator no longer reads a perpetual zero.
func TestProxyUsageRecorderAdvancesQuotaCounter_Spec11_2(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "s_1", TenantID: "acme", UserID: "alice", RuntimeRef: "claude-prod", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	quotaCounter := newTestQuotaCounter(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, nil, quotaCounter, fakeQuotaLimits{period: quota.ResetHourly}, nil)
	rec.now = func() time.Time { return now }

	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-1", SessionID: "s_1", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}, llmproxy.Usage{InputTokens: 100, OutputTokens: 30})

	got, err := quotaCounter.UsageHierarchical(ctx, "acme", "alice", quota.ResetHourly, now)
	if err != nil {
		t.Fatalf("UsageHierarchical: %v", err)
	}
	if got.User != 130 || got.Tenant != 130 || got.Global != 130 {
		t.Errorf("hierarchical counters = %+v, want all 130", got)
	}
	// A second user under the same tenant lifts the tenant and global
	// rollups but not the first user's window — proving the scopes are
	// independent windows, not one collapsed counter.
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "s_2", TenantID: "acme", UserID: "bob", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("sessions.Create bob: %v", err)
	}
	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-2", SessionID: "s_2", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}, llmproxy.Usage{InputTokens: 20, OutputTokens: 0})

	alice, _ := quotaCounter.UsageHierarchical(ctx, "acme", "alice", quota.ResetHourly, now)
	if alice.User != 130 {
		t.Errorf("alice per-user window = %d, want 130 (unchanged by bob)", alice.User)
	}
	if alice.Tenant != 150 || alice.Global != 150 {
		t.Errorf("tenant/global rollups = (%d,%d), want 150 each", alice.Tenant, alice.Global)
	}
}

// TestProxyUsageRecorderRollingWritesSlidingWindow_Spec11_2 proves
// F-11.2.3: a tenant on the rolling reset period has its usage recorded
// through the sliding-window counter (the fixed-window store errors for
// ResetRolling), readable via SlidingUsageHierarchical.
func TestProxyUsageRecorderRollingWritesSlidingWindow_Spec11_2(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "s_1", TenantID: "acme", UserID: "alice", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	quotaCounter := newTestQuotaCounter(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, nil, quotaCounter,
		fakeQuotaLimits{period: quota.ResetRolling, rollingWindow: time.Hour}, nil)
	rec.now = func() time.Time { return now }

	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-1", SessionID: "s_1", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}, llmproxy.Usage{InputTokens: 40, OutputTokens: 10})

	got, err := quotaCounter.SlidingUsageHierarchical(ctx, "acme", "alice", time.Hour, quotastore.DefaultBucketResolution, now)
	if err != nil {
		t.Fatalf("SlidingUsageHierarchical: %v", err)
	}
	if got.User != 50 || got.Tenant != 50 || got.Global != 50 {
		t.Errorf("rolling hierarchical counters = %+v, want all 50", got)
	}
	// The fixed-window read must be empty — nothing was written there.
	fixed, err := quotaCounter.UsageHierarchical(ctx, "acme", "alice", quota.ResetHourly, now)
	if err != nil {
		t.Fatalf("UsageHierarchical: %v", err)
	}
	if fixed.User != 0 {
		t.Errorf("fixed-window user counter = %d, want 0 (rolling write must not land in a fixed bucket)", fixed.User)
	}
}

// TestProxyUsageRecorderRecordsPerSession_spec_8_8_897 confirms the
// recorder folds a proxy-mode lease's authoritative counts into the §8.8
// per-session accumulator (the source the TaskResult usage / treeUsage
// rollups read), keyed by the lease's session, and ignores a direct-mode
// lease.
// spec: §8.8 lines 897-917; §4.9 line 1468. F-8.8.3.
func TestProxyUsageRecorderRecordsPerSession_spec_8_8_897(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "s_1", TenantID: "acme", RuntimeRef: "claude-prod", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	sessUsage := sessionusage.NewMemory()
	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, sessUsage, nil, nil, nil)

	lease := credential.Lease{
		LeaseID: "cl-1", SessionID: "s_1", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}
	rec.RecordUsage(lease, llmproxy.Usage{InputTokens: 100, OutputTokens: 30})
	rec.RecordUsage(lease, llmproxy.Usage{InputTokens: 50, OutputTokens: 20})

	got, _ := sessUsage.Get(ctx, "acme", "s_1")
	if got.Input != 150 || got.Output != 50 {
		t.Errorf("per-session tokens = %+v, want {Input:150 Output:50}", got)
	}

	// A direct-mode lease never reaches the proxy hot path; the recorder
	// ignores it so a future regression cannot double-count.
	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-2", SessionID: "s_1", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryDirect,
	}, llmproxy.Usage{InputTokens: 999, OutputTokens: 999})
	got, _ = sessUsage.Get(ctx, "acme", "s_1")
	if got.Input != 150 || got.Output != 50 {
		t.Errorf("direct-mode leaked into per-session accumulator: %+v", got)
	}
}
