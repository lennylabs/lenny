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
	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionusage"
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

	rec.RecordUsage(context.Background(), credential.Lease{
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
	rec.RecordUsage(context.Background(), credential.Lease{
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
	rec.RecordUsage(context.Background(), credential.Lease{
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
	rec.RecordUsage(context.Background(), credential.Lease{
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
	enforcer := sessionbudget.New(term, nil, nil)
	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, nil, nil, nil, enforcer)

	lease := credential.Lease{
		LeaseID: "cl-1", SessionID: "s_1", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}
	// First request stays under the 200-token budget: no termination, gate
	// open, and the record path reports not-exhausted.
	if exhausted, _ := rec.RecordUsage(context.Background(), lease, llmproxy.Usage{InputTokens: 80, OutputTokens: 40}); exhausted { // 120
		t.Fatalf("under-budget record must report not exhausted")
	}
	if !enforcer.Allow("s_1") {
		t.Fatalf("session under budget must be allowed")
	}
	if len(term.calls) != 0 {
		t.Fatalf("no termination expected under budget, got %v", term.calls)
	}
	// Second request crosses the budget (cumulative 240 >= 200): the record
	// path reports exhausted and surfaces the enforcer's resolved Outcome (the
	// signal the proxy branches its write path on), and the enforcer's
	// nil-seam path terminates the session and closes its pre-flight gate. The
	// surfaced Outcome is Terminal so the proxy fails the exhausting request
	// closed rather than re-dispatching its own extension.
	exhausted, outcome := rec.RecordUsage(context.Background(), lease, llmproxy.Usage{InputTokens: 90, OutputTokens: 30}) // +120 = 240
	if !exhausted {
		t.Fatalf("over-budget record must report exhausted so the proxy drives its write branch")
	}
	if outcome != llmproxy.OutcomeTerminal {
		t.Fatalf("nil-seam exhaustion must surface OutcomeTerminal (fail closed), got %v", outcome)
	}
	if enforcer.Allow("s_1") {
		t.Fatalf("exhausted session must be denied by the pre-flight gate")
	}
	if len(term.calls) != 1 || term.calls[0] != "s_1" {
		t.Fatalf("budget termination = %v, want [s_1]", term.calls)
	}
}

// grantingReclaimSeam simulates the production in-path §8.6 grant: on the
// exhaustion boundary it applies the raise through the recorder (the
// SessionReclaimer), exactly as leasecontrol.ExtendForBudget's in-path Granted
// path does through the wired reclaimer, then returns Granted. It records how
// many times the enforcer consulted it so a test can assert a request under
// the raised budget does not re-dispatch a second extension.
type grantingReclaimSeam struct {
	rec   *proxyUsageRecorder
	delta int64
	calls int
}

func (s *grantingReclaimSeam) fn(_, _ context.Context, _, sessionID string, _, _ int64) sessionbudget.Outcome {
	s.calls++
	// The in-path grant raises the enforcer budget AND accumulates the delta
	// on the recorder (the SessionReclaimer), so the next RecordUsage passes
	// base + delta to Record.
	s.rec.RaiseBudget(sessionID, s.delta)
	return sessionbudget.Granted
}

// TestProxyUsageRecorderRaiseSurvivesNextRecord_spec_8_6 is the raise-survival
// regression for proposal 0023 findings 2 and 4. After an in-path §8.6 grant
// raises the session budget, a subsequent RecordUsage that would exceed the
// stale base MaxTokenBudget but stays under the raised budget must neither
// re-terminate the session nor re-consult the extension seam. It fails against
// the pre-fix recorder, which read tokenBudget straight from the stale
// sess.DelegationLease.MaxTokenBudget and passed it unchanged to Record, so the
// enforcer clobbered the raise on the next settlement and immediately
// re-exhausted the session. The recorder is wired as its own SessionReclaimer,
// so the grant delta the seam applies is accumulated and added to the base
// budget by RecordUsage.
//
// spec: 8.6 (in-process budget-exhaustion extension and raise-survival), 11.2 (mid-session budget enforcement)
// diagnosis: the §8.6 granted extension delta is not propagated into the budget the recorder passes to Enforcer.Record, so a raised budget is clobbered on the session's next settlement and the session re-exhausts and re-terminates every request (proposal 0023 S3/S4 raise-survival broken).
func TestProxyUsageRecorderRaiseSurvivesNextRecord_spec_8_6(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	// Base budget 200. The session store never learns of a §8.6 grant, so the
	// recorder must add the accumulated delta itself.
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "s_raise", TenantID: "acme", RuntimeRef: "claude-prod", State: session.StateRunning,
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: 200},
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	term := &recordTerminator{}
	enforcer := sessionbudget.New(term, nil, nil)
	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, nil, nil, nil, enforcer)
	// Wire the granting seam that raises by 1000 through the recorder (the
	// reclaimer) on exhaustion, mirroring the production in-path Granted path.
	seam := &grantingReclaimSeam{rec: rec, delta: 1000}
	enforcer.SetExtendOnExhaustion(seam.fn)

	lease := credential.Lease{
		LeaseID: "cl-raise", SessionID: "s_raise", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}

	// First settlement records 250 tokens, crossing the base 200 budget. The
	// seam grants (raising the budget by 1000 to an effective 1200 and clearing
	// the exhausted/deny state through the recorder), so the record path reports
	// Granted and the session is not terminated.
	exhausted, outcome := rec.RecordUsage(ctx, lease, llmproxy.Usage{InputTokens: 150, OutputTokens: 100}) // 250
	if !exhausted {
		t.Fatalf("recording 250 tokens against a 200 budget must cross the exhaustion boundary")
	}
	if outcome != llmproxy.OutcomeGranted {
		t.Fatalf("an in-path grant must surface OutcomeGranted, got %v", outcome)
	}
	if !enforcer.Allow("s_raise") {
		t.Fatalf("a granted-extension session must be admitted by the pre-flight gate")
	}
	if len(term.calls) != 0 {
		t.Fatalf("a granted extension must not terminate the session, got %v", term.calls)
	}

	// Second settlement records another 300 tokens (cumulative 550). This exceeds
	// the stale base budget (200) but stays well under the raised budget (1200).
	// The pre-fix recorder passed the stale 200 to Record, so cumulative 550 >= 200
	// re-exhausted the session and re-consulted the seam. The fixed recorder passes
	// base 200 + accumulated delta 1000 = 1200, so 550 < 1200 does not re-exhaust,
	// the seam is not consulted again, and the session is not terminated.
	callsBefore := seam.calls
	exhausted2, _ := rec.RecordUsage(ctx, lease, llmproxy.Usage{InputTokens: 200, OutputTokens: 100}) // +300 = 550
	if exhausted2 {
		t.Fatalf("a settlement under the raised budget must not re-exhaust the session (the raise must survive the next Record)")
	}
	if seam.calls != callsBefore {
		t.Fatalf("a settlement under the raised budget must not re-consult the extension seam, calls went %d -> %d", callsBefore, seam.calls)
	}
	if !enforcer.Allow("s_raise") {
		t.Fatalf("a session under the raised budget must stay admitted")
	}
	if len(term.calls) != 0 {
		t.Fatalf("no termination expected after a surviving raise, got %v", term.calls)
	}
}

// TestProxyUsageRecorderReclaimerForgetsGrant_spec_8_6 proves the recorder's
// SessionReclaimer clears a session's accumulated grant delta on
// TerminateSession, so a terminated session does not retain a raised budget in
// the recorder's per-session map. It pins the fan-out terminal path
// (TerminateSession) the recorder implements for a detached session whose
// deferred extension outcome is terminal.
//
// spec: 8.6 (SessionReclaimer terminal fan-out), 11.2 (budget accounting cleanup)
// diagnosis: the recorder's SessionReclaimer does not drop a terminated session's accumulated grant delta, leaking a raised budget entry for a torn-down session (proposal 0023 S3/S4 reclaimer cleanup broken).
func TestProxyUsageRecorderReclaimerForgetsGrant_spec_8_6(t *testing.T) {
	sessions := memstore.New()
	term := &recordTerminator{}
	enforcer := sessionbudget.New(term, nil, nil)
	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, nil, nil, nil, enforcer)

	rec.RaiseBudget("s_term", 1000)
	if got := rec.grantedDeltaFor("s_term"); got != 1000 {
		t.Fatalf("RaiseBudget must accumulate the granted delta, got %d", got)
	}
	rec.TerminateSession("s_term")
	if got := rec.grantedDeltaFor("s_term"); got != 0 {
		t.Fatalf("TerminateSession must clear the accumulated grant delta, got %d", got)
	}
	if len(term.calls) != 1 || term.calls[0] != "s_term" {
		t.Fatalf("TerminateSession must terminate the session through the enforcer, got %v", term.calls)
	}

	// The SessionReclaimer methods are nil-safe on the recorder and on an
	// empty session id: the leasecontrol fan-out or the in-path applier must
	// never panic against a recorder that is not wired.
	var nilRec *proxyUsageRecorder
	nilRec.RaiseBudget("s", 1)
	nilRec.TerminateSession("s")
	rec.RaiseBudget("", 1)
	rec.TerminateSession("")
}

// TestProxyUsageRecorderForgetDropsGrant_spec_8_6 proves the recorder's
// forget (wired into the sessionserver BudgetForget closure at session
// settlement) drops a session's accumulated grant delta, so the per-session
// map does not grow without bound as sessions settle. It complements the
// TerminateSession reclaimer path, which clears the same entry on a terminal
// fan-out.
//
// spec: 8.6 (grant-delta accounting cleanup), 11.2 (mid-session budget accounting)
// diagnosis: the recorder's grant-delta map is never cleared on session settlement, leaking a raised-budget entry per settled session (proposal 0023 S3/S4 accounting cleanup broken).
func TestProxyUsageRecorderForgetDropsGrant_spec_8_6(t *testing.T) {
	rec := newProxyUsageRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, nil)
	rec.RaiseBudget("s_forget", 500)
	if got := rec.grantedDeltaFor("s_forget"); got != 500 {
		t.Fatalf("RaiseBudget must accumulate the delta, got %d", got)
	}
	rec.forget("s_forget")
	if got := rec.grantedDeltaFor("s_forget"); got != 0 {
		t.Fatalf("forget must drop the accumulated delta, got %d", got)
	}
	// Nil-safe on a nil recorder and empty session id (the BudgetForget
	// closure may fire before the recorder is wired or for an anonymous
	// session).
	var nilRec *proxyUsageRecorder
	nilRec.forget("s_forget")
	rec.forget("")
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
	enforcer := sessionbudget.New(term, nil, nil)
	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, nil, nil, nil, enforcer)
	rec.RecordUsage(context.Background(), credential.Lease{
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

	rec.RecordUsage(context.Background(), credential.Lease{
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
	rec.RecordUsage(context.Background(), credential.Lease{
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

// spec: §12.4 source (2); §11.2 line 48 — F-12.4.20: the recorder folds
// each proxy-extracted token delta into the in-memory fail-open accumulator
// (both the per-user window and the per-tenant rollup) so a Redis-recovery
// reconcile can restore usage a Redis write dropped during an outage.
func TestProxyUsageRecorderFeedsFailOpenAccumulator_Spec12_4(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "s_1", TenantID: "acme", UserID: "alice", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	quotaCounter := newTestQuotaCounter(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, nil, quotaCounter, fakeQuotaLimits{period: quota.ResetHourly}, nil)
	rec.now = func() time.Time { return now }
	acc := quotafailopen.New()
	rec.setFailOpenAccumulator(acc)

	rec.RecordUsage(context.Background(), credential.Lease{
		LeaseID: "cl-1", SessionID: "s_1", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}, llmproxy.Usage{InputTokens: 100, OutputTokens: 30})

	if got := acc.UserWindow("acme", "alice", quota.ResetHourly, now); got != 130 {
		t.Errorf("accumulator user window = %d, want 130", got)
	}
	if got := acc.TenantRollup("acme", quota.ResetHourly, now); got != 130 {
		t.Errorf("accumulator tenant rollup = %d, want 130", got)
	}
}

// spec: §12.4 source (2) — the rolling period has no single restorable
// window, so the recorder does not feed the accumulator for it (the
// sliding-window counter remains the only recording surface).
func TestProxyUsageRecorderRollingSkipsFailOpenAccumulator_Spec12_4(t *testing.T) {
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
	acc := quotafailopen.New()
	rec.setFailOpenAccumulator(acc)

	rec.RecordUsage(context.Background(), credential.Lease{
		LeaseID: "cl-1", SessionID: "s_1", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}, llmproxy.Usage{InputTokens: 40, OutputTokens: 10})

	if got := acc.Len(); got != 0 {
		t.Errorf("accumulator entries = %d, want 0 (rolling period skipped)", got)
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

	rec.RecordUsage(context.Background(), credential.Lease{
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
	rec.RecordUsage(context.Background(), lease, llmproxy.Usage{InputTokens: 100, OutputTokens: 30})
	rec.RecordUsage(context.Background(), lease, llmproxy.Usage{InputTokens: 50, OutputTokens: 20})

	got, _ := sessUsage.Get(ctx, "acme", "s_1")
	if got.Input != 150 || got.Output != 50 {
		t.Errorf("per-session tokens = %+v, want {Input:150 Output:50}", got)
	}

	// A direct-mode lease never reaches the proxy hot path; the recorder
	// ignores it so a future regression cannot double-count.
	rec.RecordUsage(context.Background(), credential.Lease{
		LeaseID: "cl-2", SessionID: "s_1", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryDirect,
	}, llmproxy.Usage{InputTokens: 999, OutputTokens: 999})
	got, _ = sessUsage.Get(ctx, "acme", "s_1")
	if got.Input != 150 || got.Output != 50 {
		t.Errorf("direct-mode leaked into per-session accumulator: %+v", got)
	}
}

// TestProxyUsageRecorderProxyExtensionWaitTimeoutOverride pins the
// operator override the --proxy-extension-wait-timeout flag feeds into the
// recorder's in-path §8.6 extension deadline. A positive value replaces the
// 5s default; a zero or negative value leaves the default in place so a
// zeroed flag never collapses the wait window to nothing (which would turn
// every elicitation-mode extension into an immediate BUDGET_EXHAUSTED).
// spec: §8.6 line 629; proposal 0023 S5.
func TestProxyUsageRecorderProxyExtensionWaitTimeoutOverride(t *testing.T) {
	rec := newProxyUsageRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, nil)
	if rec.proxyExtensionWaitTimeout != defaultProxyExtensionWaitTimeout {
		t.Fatalf("initial timeout = %v, want default %v", rec.proxyExtensionWaitTimeout, defaultProxyExtensionWaitTimeout)
	}

	rec.setProxyExtensionWaitTimeout(12 * time.Second)
	if rec.proxyExtensionWaitTimeout != 12*time.Second {
		t.Errorf("after override timeout = %v, want 12s", rec.proxyExtensionWaitTimeout)
	}

	// A non-positive flag value (a zeroed --proxy-extension-wait-timeout)
	// must not overwrite the resolved deadline with zero.
	rec.setProxyExtensionWaitTimeout(0)
	if rec.proxyExtensionWaitTimeout != 12*time.Second {
		t.Errorf("zero override clobbered timeout = %v, want 12s preserved", rec.proxyExtensionWaitTimeout)
	}
	rec.setProxyExtensionWaitTimeout(-3 * time.Second)
	if rec.proxyExtensionWaitTimeout != 12*time.Second {
		t.Errorf("negative override clobbered timeout = %v, want 12s preserved", rec.proxyExtensionWaitTimeout)
	}

	// The setter is nil-safe on the recorder.
	var nilRec *proxyUsageRecorder
	nilRec.setProxyExtensionWaitTimeout(5 * time.Second)
}
