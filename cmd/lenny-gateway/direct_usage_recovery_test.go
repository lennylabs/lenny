// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// recoveryPuller is a directUsagePuller test double for the crash-recovery
// reader. Unlike stubPuller it records the cumulative flag of each pull so a
// test can assert the recovery read requests the session cumulative total
// (cumulative=true), not the steady-state incremental delta.
type recoveryPuller struct {
	mu          sync.Mutex
	reports     map[string]adapterclient.UsageReport
	err         error
	pulls       []string
	cumulatives []bool
}

func (p *recoveryPuller) ReportUsageForLease(_ context.Context, sessionID string, deliveryMode credential.DeliveryMode, cumulative bool) (adapterclient.UsageReport, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pulls = append(p.pulls, sessionID)
	p.cumulatives = append(p.cumulatives, cumulative)
	if deliveryMode == credential.DeliveryProxy {
		return adapterclient.UsageReport{}, adapterclient.ErrUsageReportProxyMode
	}
	if p.err != nil {
		return adapterclient.UsageReport{}, p.err
	}
	return p.reports[sessionID], nil
}

func (p *recoveryPuller) pullCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pulls)
}

// stubSubjectResolver resolves session→user from a fixed map; an absent
// session resolves to "" (the pod-reported total still counts against the
// rollup), matching the SessionStore-backed resolver's missing-row behaviour.
type stubSubjectResolver struct {
	users map[string]string
}

func (r stubSubjectResolver) ResolveUser(_ context.Context, _ /*tenantID*/, sessionID string) string {
	return r.users[sessionID]
}

// stubPeriods resolves every tenant to hourly.
type stubPeriods struct{}

func (stubPeriods) ResolvePeriod(_ context.Context, _ string) (quota.ResetPeriod, error) {
	return quota.ResetHourly, nil
}

// newRecoveryReaderUnderTest builds a directUsageRecoveryReader over a real
// registry, injecting a stub puller through pullerFor so the registry-keyed
// enumeration runs without a live gRPC adapter.
func newRecoveryReaderUnderTest(registry *podsession.Registry, leases directUsageLeaseLookup, subjects directUsageSubjectResolver, puller directUsagePuller) *directUsageRecoveryReader {
	r := newDirectUsageRecoveryReader(registry, leases, subjects, stubPeriods{}, time.Second, nil)
	r.pullerFor = func(*podsession.BindResult) directUsagePuller { return puller }
	return r
}

var recoveryNow = time.Date(2026, 6, 2, 14, 30, 0, 0, time.UTC)

// TestRecoveryReaderPullsCumulativeAndAttributesWindows_spec_11_2_line46
// proves the crash-recovery reader resolves a bound direct-mode session to its
// (tenant, user) window and its per-tenant rollup, pulling the session
// cumulative total with cumulative=true. It would fail against a reader that
// pulled the incremental delta (cumulative=false), which the §11.2 line 46 MAX
// rule cannot use to reconstruct a counter, or that dropped the per-user
// attribution.
//
// spec: 11.2 (line 46 crash-recovery MAX rule; pod-reported cumulative total),
// 4.7 (ReportUsage cumulative read).
// diagnosis: the crash-recovery pod-usage reader under-counted a direct-mode session because it pulled the incremental delta instead of the session cumulative total, so a reconnected replica reconstructed MAX(stale_checkpoint, small_delta) and silently un-recovered lost usage (proposal 0024 S15 cumulative recovery read broken).
func TestRecoveryReaderPullsCumulativeAndAttributesWindows_spec_11_2_line46(t *testing.T) {
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme"})
	leases := stubLeaseLookup{leases: map[string]credential.Lease{
		"s1": directUsage("s1", "acme"),
	}}
	subjects := stubSubjectResolver{users: map[string]string{"s1": "alice@acme.com"}}
	puller := &recoveryPuller{reports: map[string]adapterclient.UsageReport{
		"s1": {InputTokens: 1200, OutputTokens: 800}, // cumulative total 2000
	}}
	reader := newRecoveryReaderUnderTest(registry, leases, subjects, puller)

	ctx := context.Background()
	// The user window and the tenant rollup both carry the session cumulative
	// total.
	if got := reader.UserWindow(ctx, "acme", "alice@acme.com", quota.ResetHourly, recoveryNow); got != 2000 {
		t.Errorf("UserWindow = %d, want 2000", got)
	}
	if got := reader.TenantRollup(ctx, "acme", quota.ResetHourly, recoveryNow); got != 2000 {
		t.Errorf("TenantRollup = %d, want 2000", got)
	}
	// The recovery read must request the cumulative total, not the incremental
	// delta.
	puller.mu.Lock()
	defer puller.mu.Unlock()
	for i, c := range puller.cumulatives {
		if !c {
			t.Errorf("pull %d used cumulative=%v, want true (crash-recovery cumulative read)", i, c)
		}
	}
}

// TestRecoveryReaderPullsEachSessionOncePerPass_spec_11_2_line46 proves the
// reader pulls each bound session exactly once per reconcile pass despite the
// several fold-site calls (UserWindow, TenantRollup, Snapshot) quotacheckpoint
// makes within one pass. This is load-bearing: the §4.7 cumulative read
// advances the adapter watermark, so a second pull in the same pass would
// return zero and the fold would under-count. It would fail against a reader
// that re-pulled on every method call.
//
// spec: 11.2 (line 46), 4.7 (cumulative read advances the watermark).
// diagnosis: the crash-recovery reader pulled a session's watermark-advancing cumulative total more than once per reconcile pass, so the second pull returned zero and the MAX fold under-counted the session (proposal 0024 S15 pull-once-per-pass invariant broken).
func TestRecoveryReaderPullsEachSessionOncePerPass_spec_11_2_line46(t *testing.T) {
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme"})
	leases := stubLeaseLookup{leases: map[string]credential.Lease{
		"s1": directUsage("s1", "acme"),
	}}
	subjects := stubSubjectResolver{users: map[string]string{"s1": "alice@acme.com"}}
	puller := &recoveryPuller{reports: map[string]adapterclient.UsageReport{
		"s1": {InputTokens: 500, OutputTokens: 500},
	}}
	reader := newRecoveryReaderUnderTest(registry, leases, subjects, puller)

	ctx := context.Background()
	// Every fold site the row pass and the no-checkpoint pass touch, all with
	// the same `now`.
	_ = reader.UserWindow(ctx, "acme", "alice@acme.com", quota.ResetHourly, recoveryNow)
	_ = reader.TenantRollup(ctx, "acme", quota.ResetHourly, recoveryNow)
	_ = reader.Snapshot(ctx, recoveryNow)
	_ = reader.UserWindow(ctx, "acme", "alice@acme.com", quota.ResetHourly, recoveryNow)

	if puller.pullCount() != 1 {
		t.Fatalf("session must be pulled exactly once per pass, got %d pulls", puller.pullCount())
	}

	// A later pass with a different `now` re-pulls (a fresh recovery edge).
	_ = reader.UserWindow(ctx, "acme", "alice@acme.com", quota.ResetHourly, recoveryNow.Add(time.Hour))
	if puller.pullCount() != 2 {
		t.Fatalf("a later reconcile pass must re-pull, got %d pulls", puller.pullCount())
	}
}

// TestRecoveryReaderSkipsProxyLease_spec_4_9_1468 proves the reader never folds
// a proxy-mode session's counts into the recovery MAX: proxy-extracted counts
// are already authoritative on the §4.9 path, so folding a pod-reported total
// for a proxy session would double-count. The reader filters by delivery mode
// before pulling, so the proxy session contributes zero.
//
// spec: 4.9 (line 1468 proxy-extracted counts authoritative), 11.2 (line 46).
// diagnosis: the crash-recovery reader double-counted a proxy-mode session by folding a pod-reported total into the MAX, competing with the authoritative §4.9 proxy-extracted count (proposal 0024 S15 proxy-mode filter broken).
func TestRecoveryReaderSkipsProxyLease_spec_4_9_1468(t *testing.T) {
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s_direct", TenantID: "acme"})
	registry.Put(&podsession.BindResult{SessionID: "s_proxy", TenantID: "acme"})
	leases := stubLeaseLookup{leases: map[string]credential.Lease{
		"s_direct": directUsage("s_direct", "acme"),
		"s_proxy": {
			LeaseID: "cl-p", SessionID: "s_proxy", TenantID: "acme",
			Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
		},
	}}
	subjects := stubSubjectResolver{users: map[string]string{
		"s_direct": "alice@acme.com",
		"s_proxy":  "bob@acme.com",
	}}
	puller := &recoveryPuller{reports: map[string]adapterclient.UsageReport{
		"s_direct": {InputTokens: 100, OutputTokens: 100},
	}}
	reader := newRecoveryReaderUnderTest(registry, leases, subjects, puller)

	ctx := context.Background()
	// The proxy session's window carries nothing (it was never pulled).
	if got := reader.UserWindow(ctx, "acme", "bob@acme.com", quota.ResetHourly, recoveryNow); got != 0 {
		t.Errorf("proxy-mode user window = %d, want 0 (never folded)", got)
	}
	// The direct session is folded.
	if got := reader.UserWindow(ctx, "acme", "alice@acme.com", quota.ResetHourly, recoveryNow); got != 200 {
		t.Errorf("direct-mode user window = %d, want 200", got)
	}
	// Only the direct session was pulled.
	puller.mu.Lock()
	defer puller.mu.Unlock()
	if len(puller.pulls) != 1 || puller.pulls[0] != "s_direct" {
		t.Fatalf("only the direct-mode session must be pulled, got %v", puller.pulls)
	}
}

// TestRecoveryReaderErroredPullContributesNothing_spec_11_2_line46 proves a
// transport error, a timeout, or an ErrUsageReportProxyMode misroute
// contributes nothing to the MAX (fail-closed to the other sources; the reader
// never fabricates a total). The stub errors every direct pull, so the window
// carries zero and the reader does not lower a counter.
//
// spec: 11.2 (line 46 fail-closed pod-usage source), 4.7 (ReportUsage).
// diagnosis: an errored or timed-out crash-recovery pull fabricated a token total (or crashed the reader) instead of contributing nothing, so the recovery MAX either over-counted or dropped a real source silently (proposal 0024 S15 fail-closed contract broken).
func TestRecoveryReaderErroredPullContributesNothing_spec_11_2_line46(t *testing.T) {
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s_wedged", TenantID: "acme"})
	leases := stubLeaseLookup{leases: map[string]credential.Lease{
		"s_wedged": directUsage("s_wedged", "acme"),
	}}
	subjects := stubSubjectResolver{users: map[string]string{"s_wedged": "alice@acme.com"}}
	puller := &recoveryPuller{err: errors.New("adapter unreachable")}
	reader := newRecoveryReaderUnderTest(registry, leases, subjects, puller)

	ctx := context.Background()
	if got := reader.UserWindow(ctx, "acme", "alice@acme.com", quota.ResetHourly, recoveryNow); got != 0 {
		t.Errorf("errored pull user window = %d, want 0 (fail-closed, no fabricated total)", got)
	}
	if got := reader.Snapshot(ctx, recoveryNow); len(got) != 0 {
		t.Errorf("errored pull Snapshot = %v, want empty", got)
	}
}

// TestRecoveryReaderMissingSubjectCountsRollupOnly_spec_11_2_line46 proves a
// session whose user cannot be resolved still contributes its cumulative total
// to the per-tenant rollup (the tenant-scope MAX source), just not to any
// per-user window. The reader never invents a subject.
//
// spec: 11.2 (line 46 per-tenant rollup reconstruction).
func TestRecoveryReaderMissingSubjectCountsRollupOnly_spec_11_2_line46(t *testing.T) {
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme"})
	leases := stubLeaseLookup{leases: map[string]credential.Lease{
		"s1": directUsage("s1", "acme"),
	}}
	subjects := stubSubjectResolver{users: map[string]string{}} // no user resolves
	puller := &recoveryPuller{reports: map[string]adapterclient.UsageReport{
		"s1": {InputTokens: 300, OutputTokens: 100},
	}}
	reader := newRecoveryReaderUnderTest(registry, leases, subjects, puller)

	ctx := context.Background()
	if got := reader.TenantRollup(ctx, "acme", quota.ResetHourly, recoveryNow); got != 400 {
		t.Errorf("tenant rollup = %d, want 400 (unresolved subject still counts against rollup)", got)
	}
	// No per-user window was fabricated.
	for _, s := range reader.Snapshot(ctx, recoveryNow) {
		if s.UserID != "" {
			t.Errorf("Snapshot has a per-user window %q for an unresolved subject, want rollup only", s.UserID)
		}
	}
}

// TestNewDirectUsageRecoveryReaderNilOnMissingDeps proves the reader
// constructor returns nil when any seam the pull needs is absent, so
// buildQuotaCheckpoint degrades to the MAX(redis, postgres, failopen) rule with
// a nil PodUsage seam on a minimal gateway.
func TestNewDirectUsageRecoveryReaderNilOnMissingDeps(t *testing.T) {
	registry := podsession.NewRegistry()
	leases := stubLeaseLookup{}
	subjects := stubSubjectResolver{}
	if r := newDirectUsageRecoveryReader(nil, leases, subjects, stubPeriods{}, time.Second, nil); r != nil {
		t.Error("nil registry must yield a nil reader")
	}
	if r := newDirectUsageRecoveryReader(registry, nil, subjects, stubPeriods{}, time.Second, nil); r != nil {
		t.Error("nil lease lookup must yield a nil reader")
	}
	if r := newDirectUsageRecoveryReader(registry, leases, nil, stubPeriods{}, time.Second, nil); r != nil {
		t.Error("nil subject resolver must yield a nil reader")
	}
	if r := newDirectUsageRecoveryReader(registry, leases, subjects, nil, time.Second, nil); r != nil {
		t.Error("nil period resolver must yield a nil reader")
	}
	if r := newDirectUsageRecoveryReader(registry, leases, subjects, stubPeriods{}, time.Second, nil); r == nil {
		t.Error("all deps present must yield a live reader")
	}
}

// TestSessionStoreSubjectResolver_spec_11_2_line46 proves the production
// subject resolver returns a bound session's user id for the per-user recovery
// window and "" for a missing session (which still counts against the
// per-tenant rollup), reusing the same SessionStore Get lookup the proxy
// recorder uses. A nil store resolves to "" so a minimal gateway degrades
// gracefully.
//
// spec: 11.2 (line 46 per-user recovery window attribution).
func TestSessionStoreSubjectResolver_spec_11_2_line46(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, sessionstore.Session{
		ID: "s1", TenantID: "acme", UserID: "alice@acme.com",
		State: session.StateRunning, CreatedAt: t0, UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	r := sessionStoreSubjectResolver{sessions: store}
	if got := r.ResolveUser(ctx, "acme", "s1"); got != "alice@acme.com" {
		t.Errorf("ResolveUser(bound) = %q, want alice@acme.com", got)
	}
	if got := r.ResolveUser(ctx, "acme", "missing"); got != "" {
		t.Errorf("ResolveUser(missing) = %q, want \"\"", got)
	}
	var nilResolver sessionStoreSubjectResolver
	if got := nilResolver.ResolveUser(ctx, "acme", "s1"); got != "" {
		t.Errorf("ResolveUser with nil store = %q, want \"\"", got)
	}
}

var _ quotacheckpoint.PodUsageReader = (*directUsageRecoveryReader)(nil)
