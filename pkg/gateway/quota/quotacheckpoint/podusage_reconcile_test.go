// SPDX-License-Identifier: MIT

package quotacheckpoint_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/quota"
)

// podKey identifies a (tenant, subject, period) window in the fake pod-usage
// reader; an empty subject addresses the per-tenant rollup.
type podKey struct {
	tenant, subject string
	period          quota.ResetPeriod
}

// fakePodUsage is an in-memory quotacheckpoint.PodUsageReader. It returns the
// pod-reported cumulative total for each window from a fixed map, mirroring a
// live crash-recovery pull without a real adapter.
type fakePodUsage struct {
	windows map[podKey]int64
}

func newFakePodUsage() *fakePodUsage {
	return &fakePodUsage{windows: map[podKey]int64{}}
}

// setUser records a per-user pod-reported cumulative total. It also folds the
// same tokens into the per-tenant rollup, matching how a live reader
// aggregates each session into both scopes.
func (p *fakePodUsage) setUser(tenant, user string, period quota.ResetPeriod, tokens int64) {
	p.windows[podKey{tenant, user, period}] += tokens
	p.windows[podKey{tenant, "", period}] += tokens
}

func (p *fakePodUsage) UserWindow(_ context.Context, tenantID, userID string, period quota.ResetPeriod, _ time.Time) int64 {
	return p.windows[podKey{tenantID, userID, period}]
}

func (p *fakePodUsage) TenantRollup(_ context.Context, tenantID string, period quota.ResetPeriod, _ time.Time) int64 {
	return p.windows[podKey{tenantID, "", period}]
}

func (p *fakePodUsage) Snapshot(_ context.Context, _ time.Time) []quotacheckpoint.PodUsageSample {
	var out []quotacheckpoint.PodUsageSample
	for k, tokens := range p.windows {
		if tokens <= 0 {
			continue
		}
		out = append(out, quotacheckpoint.PodUsageSample{
			TenantID: k.tenant,
			UserID:   k.subject,
			Period:   k.period,
			Tokens:   tokens,
		})
	}
	return out
}

var _ quotacheckpoint.PodUsageReader = (*fakePodUsage)(nil)

// spec: 11.2 (line 46 crash-recovery MAX rule; pod-reported cumulative
// total), 4.7 (ReportUsage cumulative read) — when a checkpoint row exists and
// the pod-reported cumulative total for that window is the highest of the
// sources, the reconcile restores it via MAX(redis_current,
// postgres_checkpoint, in_memory_failopen, pod_reported_cumulative), so a
// direct-mode session whose Redis usage was lost is not under-counted.
func TestReconcileFoldsPodReportedCumulativeIntoMax_spec_11_2_line46(t *testing.T) {
	t.Parallel()
	hourly := label(t, quota.ResetHourly)
	store := newFakeStore()
	_ = store.Write(context.Background(), []quotacheckpoint.Row{
		{TenantID: "acme", Scope: quotacheckpoint.ScopeUser, SubjectID: "alice@acme.com", Period: "hourly", WindowLabel: hourly, TokenTotal: 500},
		{TenantID: "acme", Scope: quotacheckpoint.ScopeTenant, Period: "hourly", WindowLabel: hourly, TokenTotal: 900},
	})
	cnt := newFakeCounter()
	cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}] = 100 // Redis restarted, lost most
	cnt.m[counterKey{"acme", "", quota.ResetHourly}] = 0

	pod := newFakePodUsage()
	pod.setUser("acme", "alice@acme.com", quota.ResetHourly, 2000) // pod cumulative total is highest

	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, PodUsage: pod, Now: clock}
	res, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{AllTenants: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.CountersWritten != 2 {
		t.Fatalf("CountersWritten = %d, want 2", res.CountersWritten)
	}
	// User: MAX(100 redis, 500 checkpoint, 2000 pod) = 2000.
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 2000 {
		t.Errorf("user counter = %d, want 2000 (pod-reported cumulative wins)", got)
	}
	// Tenant rollup: MAX(0 redis, 900 checkpoint, 2000 pod) = 2000.
	if got := cnt.m[counterKey{"acme", "", quota.ResetHourly}]; got != 2000 {
		t.Errorf("tenant rollup = %d, want 2000 (pod-reported cumulative wins)", got)
	}
	// The CounterResult surfaces the pod-reported input for operator visibility.
	var sawUserPod bool
	for _, c := range res.Counters {
		if c.Scope == quotacheckpoint.ScopeUser && c.SubjectID == "alice@acme.com" {
			if c.PodUsageValue != 2000 {
				t.Errorf("user CounterResult.PodUsageValue = %d, want 2000", c.PodUsageValue)
			}
			sawUserPod = true
		}
	}
	if !sawUserPod {
		t.Error("no user CounterResult reported")
	}
}

// spec: 11.2 (line 46) — the pod-reported cumulative total and the fail-open
// accumulator are both MAX-rule sources. When both carry a value the reconcile
// takes the larger; each source can win independently.
func TestReconcilePodUsageAndFailOpenBothFoldIntoMax_spec_11_2_line46(t *testing.T) {
	t.Parallel()
	hourly := label(t, quota.ResetHourly)
	store := newFakeStore()
	_ = store.Write(context.Background(), []quotacheckpoint.Row{
		{TenantID: "acme", Scope: quotacheckpoint.ScopeUser, SubjectID: "alice@acme.com", Period: "hourly", WindowLabel: hourly, TokenTotal: 300},
	})
	cnt := newFakeCounter()

	acc := quotafailopen.New()
	acc.Record("acme", "alice@acme.com", quota.ResetHourly, fixedNow, 800) // fail-open highest

	pod := newFakePodUsage()
	pod.setUser("acme", "alice@acme.com", quota.ResetHourly, 500)

	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, FailOpen: acc, PodUsage: pod, Now: clock}
	if _, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{AllTenants: true}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// MAX(0 redis, 300 checkpoint, 800 failopen, 500 pod) = 800.
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 800 {
		t.Errorf("user counter = %d, want 800 (failopen wins over pod)", got)
	}
}

// spec: 11.2 (line 46) — a window that opened entirely during the outage has
// no checkpoint row and no fail-open entry, so only the pod-reported source
// carries it. The no-checkpoint pass restores it directly from the pod source.
func TestReconcilePodUsageOnlyWindow_spec_11_2_line46(t *testing.T) {
	t.Parallel()
	store := newFakeStore() // no checkpoint rows
	cnt := newFakeCounter() // Redis empty after restart

	pod := newFakePodUsage()
	pod.setUser("acme", "alice@acme.com", quota.ResetHourly, 1200)

	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, PodUsage: pod, Now: clock}
	res, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{AllTenants: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.CountersWritten != 2 {
		t.Fatalf("CountersWritten = %d, want 2 (user + rollup from pod source)", res.CountersWritten)
	}
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 1200 {
		t.Errorf("user counter = %d, want 1200 (restored from pod source)", got)
	}
	if got := cnt.m[counterKey{"acme", "", quota.ResetHourly}]; got != 1200 {
		t.Errorf("tenant rollup = %d, want 1200 (restored from pod source)", got)
	}
}

// spec: 11.2 (line 46) — a fail-open-only window and a pod-usage-only window
// are both restored by the no-checkpoint pass, and a window carried by both
// sources is restored once from the MAX of the two, not double-counted.
func TestReconcileNoCheckpointUnionOfFailOpenAndPod_spec_11_2_line46(t *testing.T) {
	t.Parallel()
	store := newFakeStore() // no checkpoint rows
	cnt := newFakeCounter()

	acc := quotafailopen.New()
	acc.Record("acme", "alice@acme.com", quota.ResetHourly, fixedNow, 400) // fail-open + pod on same window

	pod := newFakePodUsage()
	pod.setUser("acme", "alice@acme.com", quota.ResetHourly, 900) // pod higher on the shared window
	pod.setUser("acme", "bob@acme.com", quota.ResetHourly, 600)   // pod-only window

	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, FailOpen: acc, PodUsage: pod, Now: clock}
	res, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{AllTenants: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// alice user (shared), bob user (pod-only), tenant rollup (union) = 3 windows.
	if res.CountersWritten != 3 {
		t.Fatalf("CountersWritten = %d, want 3 (alice + bob + rollup, no double restore)", res.CountersWritten)
	}
	// Shared window: MAX(400 failopen, 900 pod) = 900, restored once.
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 900 {
		t.Errorf("alice counter = %d, want 900 (MAX of failopen 400 and pod 900)", got)
	}
	// Pod-only window.
	if got := cnt.m[counterKey{"acme", "bob@acme.com", quota.ResetHourly}]; got != 600 {
		t.Errorf("bob counter = %d, want 600 (pod-only)", got)
	}
	// Tenant rollup: alice 400+900 folded → pod carries 900+600=1500 rollup;
	// failopen carries 400 rollup. MAX = 1500.
	if got := cnt.m[counterKey{"acme", "", quota.ResetHourly}]; got != 1500 {
		t.Errorf("tenant rollup = %d, want 1500", got)
	}
}

// spec: 11.2 (line 46) — a nil PodUsage reader preserves the exact prior
// behaviour: the MAX omits the pod-reported source and reduces to
// MAX(redis_current, postgres_checkpoint). This is the behaviour-preserving
// guard that lets a minimal gateway run without a pod-usage source. This test
// would fail if the fold ran unconditionally against a nil reader.
func TestReconcileNilPodUsageIsBehaviorPreserving_spec_11_2_line46(t *testing.T) {
	t.Parallel()
	hourly := label(t, quota.ResetHourly)
	store := newFakeStore()
	_ = store.Write(context.Background(), []quotacheckpoint.Row{
		{TenantID: "acme", Scope: quotacheckpoint.ScopeUser, SubjectID: "alice@acme.com", Period: "hourly", WindowLabel: hourly, TokenTotal: 500},
	})
	cnt := newFakeCounter()
	cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}] = 100
	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, Now: clock} // PodUsage nil
	res, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{AllTenants: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.CountersWritten != 1 {
		t.Fatalf("CountersWritten = %d, want 1", res.CountersWritten)
	}
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 500 {
		t.Errorf("user counter = %d, want 500 (MAX redis/checkpoint, no pod source)", got)
	}
	for _, c := range res.Counters {
		if c.PodUsageValue != 0 {
			t.Errorf("PodUsageValue = %d, want 0 with nil reader", c.PodUsageValue)
		}
	}
}

// spec: 11.2 (line 46) — a session that contributes no pod-reported total (a
// proxy-mode, missing, errored, or timed-out session, which the gateway reader
// drops so it never fabricates a total) leaves the MAX at the other sources.
// Here the pod source reports zero for the window, so a stale-checkpoint MAX
// must still win and the counter is not lowered.
func TestReconcileZeroPodReportedTotalDoesNotLowerCounter_spec_11_2_line46(t *testing.T) {
	t.Parallel()
	hourly := label(t, quota.ResetHourly)
	store := newFakeStore()
	_ = store.Write(context.Background(), []quotacheckpoint.Row{
		{TenantID: "acme", Scope: quotacheckpoint.ScopeUser, SubjectID: "alice@acme.com", Period: "hourly", WindowLabel: hourly, TokenTotal: 700},
	})
	cnt := newFakeCounter()
	cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}] = 200

	pod := newFakePodUsage() // reports zero for every window (no contribution)

	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, PodUsage: pod, Now: clock}
	res, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{AllTenants: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// MAX(200 redis, 700 checkpoint, 0 pod) = 700 — the zero pod total never
	// lowers the counter.
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 700 {
		t.Errorf("user counter = %d, want 700 (zero pod total does not lower the MAX)", got)
	}
	for _, c := range res.Counters {
		if c.PodUsageValue != 0 {
			t.Errorf("PodUsageValue = %d, want 0 (no contribution)", c.PodUsageValue)
		}
	}
}

// spec: 24.6 — a per-tenant reconcile folds only the named tenant's
// pod-reported totals, leaving other tenants' pod-usage windows untouched.
func TestReconcilePodUsageScopedPerTenant_spec_24_6(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	cnt := newFakeCounter()
	pod := newFakePodUsage()
	pod.setUser("acme", "alice@acme.com", quota.ResetHourly, 400)
	pod.setUser("globex", "carol@globex.com", quota.ResetHourly, 600)

	tenants := quotacheckpoint.TenantExistsFunc(func(_ context.Context, _ string) (bool, error) { return true, nil })
	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, PodUsage: pod, Tenants: tenants, Now: clock}

	if _, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{TenantID: "acme"}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 400 {
		t.Errorf("acme user counter = %d, want 400", got)
	}
	if got := cnt.m[counterKey{"globex", "carol@globex.com", quota.ResetHourly}]; got != 0 {
		t.Errorf("globex user counter = %d, want 0 (out of scope)", got)
	}
}
