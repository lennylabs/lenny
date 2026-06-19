// SPDX-License-Identifier: MIT

package orphansession_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/orphansession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// ----- fakes -----

type fakeTenants []string

func (f fakeTenants) ListTenants(context.Context) ([]string, error) { return []string(f), nil }

type fakeMirror struct {
	rows     map[string]orphansession.MirrorPod // keyed by podID
	lag      map[string]float64                 // keyed by pool
	lagCalls map[string]int
	getErr   error
	lagErr   error
}

func (f *fakeMirror) GetByPodID(_ context.Context, podID string) (orphansession.MirrorPod, bool, error) {
	if f.getErr != nil {
		return orphansession.MirrorPod{}, false, f.getErr
	}
	p, ok := f.rows[podID]
	return p, ok, nil
}

func (f *fakeMirror) MirrorLagSeconds(_ context.Context, pool string) (float64, error) {
	if f.lagCalls == nil {
		f.lagCalls = map[string]int{}
	}
	f.lagCalls[pool]++
	if f.lagErr != nil {
		return 0, f.lagErr
	}
	return f.lag[pool], nil
}

type fbResult struct {
	phase string
	found bool
}

type fakeFallback struct {
	res   map[string]fbResult // keyed by podID
	err   error
	calls int
}

func (f *fakeFallback) PodPhase(_ context.Context, _, podID, _ string) (string, bool, error) {
	f.calls++
	if f.err != nil {
		return "", false, f.err
	}
	r := f.res[podID]
	return r.phase, r.found, nil
}

type fakeTerminal struct{ called []string }

func (f *fakeTerminal) OnSessionTerminal(_ context.Context, _ session.State, s sessionstore.Session) {
	f.called = append(f.called, s.ID)
}

type fakeMetrics struct {
	reconciliations int
	lag             map[string]float64
}

func (f *fakeMetrics) IncOrphanSessionReconciliation() { f.reconciliations++ }

func (f *fakeMetrics) SetAgentPodStateMirrorLag(pool string, s float64) {
	if f.lag == nil {
		f.lag = map[string]float64{}
	}
	f.lag[pool] = s
}

// ----- helpers -----

func mkSession(t *testing.T, store *memstore.Store, tenant, id string, state session.State, pod, pool string) {
	t.Helper()
	err := store.Create(context.Background(), sessionstore.Session{
		ID:            id,
		TenantID:      tenant,
		State:         state,
		PodAssignment: pod,
		PoolRef:       pool,
		RootSessionID: id,
	})
	if err != nil {
		t.Fatalf("create session %s: %v", id, err)
	}
}

func getState(t *testing.T, store *memstore.Store, tenant, id string) sessionstore.Session {
	t.Helper()
	s, err := store.Get(context.Background(), tenant, id)
	if err != nil {
		t.Fatalf("get session %s: %v", id, err)
	}
	return s
}

// ----- tests -----

// spec: §10.1 line 51 — a non-terminal session whose mirror pod is
// `terminated` is forced to failed/orphan_pod_terminated, the counter
// increments, the per-pool gauge publishes, and the terminal pipeline
// runs.
func TestTick_orphanViaFreshMirror_spec10_1_line51(t *testing.T) {
	store := memstore.New()
	mkSession(t, store, "acme", "s1", session.StateRunning, "pod-1", "pool-a")
	mirror := &fakeMirror{
		rows: map[string]orphansession.MirrorPod{"pod-1": {PoolID: "pool-a", Phase: "terminated"}},
		lag:  map[string]float64{"pool-a": 3},
	}
	term := &fakeTerminal{}
	met := &fakeMetrics{}
	r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{
		Terminal: term, Metrics: met,
	})

	n, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("failed count = %d, want 1", n)
	}
	got := getState(t, store, "acme", "s1")
	if got.State != session.StateFailed {
		t.Errorf("state = %q, want failed", got.State)
	}
	if got.FailureReason != string(session.FailureOrphanPodTerminated) {
		t.Errorf("reason = %q, want orphan_pod_terminated", got.FailureReason)
	}
	if got.FailureClass != session.FailureClassRuntime {
		t.Errorf("class = %q, want runtime_failure", got.FailureClass)
	}
	if met.reconciliations != 1 {
		t.Errorf("reconciliations = %d, want 1", met.reconciliations)
	}
	if met.lag["pool-a"] != 3 {
		t.Errorf("gauge pool-a = %v, want 3", met.lag["pool-a"])
	}
	if len(term.called) != 1 || term.called[0] != "s1" {
		t.Errorf("terminal hook calls = %v, want [s1]", term.called)
	}
}

// spec: §10.1 line 51 — a live pod (non-terminated mirror phase) is left
// alone.
func TestTick_livePodLeftAlone_spec10_1_line51(t *testing.T) {
	store := memstore.New()
	mkSession(t, store, "acme", "s1", session.StateRunning, "pod-1", "pool-a")
	mirror := &fakeMirror{
		rows: map[string]orphansession.MirrorPod{"pod-1": {PoolID: "pool-a", Phase: "attached"}},
	}
	met := &fakeMetrics{}
	r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{Metrics: met})

	n, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Fatalf("failed count = %d, want 0", n)
	}
	if getState(t, store, "acme", "s1").State != session.StateRunning {
		t.Errorf("state changed, want running")
	}
	if met.reconciliations != 0 {
		t.Errorf("reconciliations = %d, want 0", met.reconciliations)
	}
}

// spec: §10.1 line 51 — "Sessions in `suspended` state with no pod
// binding … are excluded — there is no pod to cross-reference."
func TestTick_suspendedNoPodExcluded_spec10_1_line51(t *testing.T) {
	store := memstore.New()
	// suspended with NO pod binding — excluded.
	mkSession(t, store, "acme", "podless", session.StateSuspended, "", "pool-a")
	// suspended WITH a pod whose mirror is terminated — included.
	mkSession(t, store, "acme", "withpod", session.StateSuspended, "pod-2", "pool-a")
	mirror := &fakeMirror{
		rows: map[string]orphansession.MirrorPod{"pod-2": {PoolID: "pool-a", Phase: "terminated"}},
	}
	r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{})

	n, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("failed count = %d, want 1 (only the with-pod suspension)", n)
	}
	if getState(t, store, "acme", "podless").State != session.StateSuspended {
		t.Errorf("podless suspension was transitioned, want left suspended")
	}
	if getState(t, store, "acme", "withpod").State != session.StateFailed {
		t.Errorf("with-pod suspension not failed")
	}
}

// spec: §15.1 / §10.1 line 51 — terminal sessions and non-eligible
// states (resume_pending, created) are skipped even when they carry a
// pod binding pointing at a terminated mirror row.
func TestTick_terminalAndNonEligibleSkipped(t *testing.T) {
	store := memstore.New()
	mkSession(t, store, "acme", "done", session.StateCompleted, "pod-1", "pool-a")
	mkSession(t, store, "acme", "rp", session.StateResumePending, "pod-2", "pool-a")
	mkSession(t, store, "acme", "created", session.StateCreated, "pod-3", "pool-a")
	mirror := &fakeMirror{rows: map[string]orphansession.MirrorPod{
		"pod-1": {PoolID: "pool-a", Phase: "terminated"},
		"pod-2": {PoolID: "pool-a", Phase: "terminated"},
		"pod-3": {PoolID: "pool-a", Phase: "terminated"},
	}}
	r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{})

	n, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Fatalf("failed count = %d, want 0", n)
	}
	if getState(t, store, "acme", "rp").State != session.StateResumePending {
		t.Errorf("resume_pending was transitioned")
	}
}

// spec: §10.1 line 51 — "When mirror staleness exceeds 60s, the orphan
// reconciler falls back to direct Kubernetes API queries." A stale pool
// routes straight to the fallback, ignoring the (potentially wrong)
// mirror row.
func TestTick_staleMirrorFallsBackToKube_spec10_1_line51(t *testing.T) {
	store := memstore.New()
	mkSession(t, store, "acme", "s1", session.StateRunning, "pod-1", "pool-a")
	// Mirror says the pod is still attached, but the pool's mirror is
	// stale (lag 120s > 60s), so the reconciler must not trust it.
	mirror := &fakeMirror{
		rows: map[string]orphansession.MirrorPod{"pod-1": {PoolID: "pool-a", Phase: "attached"}},
		lag:  map[string]float64{"pool-a": 120},
	}
	fb := &fakeFallback{res: map[string]fbResult{"pod-1": {phase: "terminated", found: true}}}
	met := &fakeMetrics{}
	r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{
		Fallback: fb, Metrics: met,
	})

	n, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("failed count = %d, want 1", n)
	}
	if fb.calls != 1 {
		t.Errorf("fallback calls = %d, want 1", fb.calls)
	}
	if mirror.lagCalls["pool-a"] != 1 {
		t.Errorf("lag reads = %d, want 1", mirror.lagCalls["pool-a"])
	}
	if met.lag["pool-a"] != 120 {
		t.Errorf("stale-lag gauge = %v, want 120", met.lag["pool-a"])
	}
}

// spec: §10.1 line 51 — a stale mirror with a live pod (fallback reports
// the pod still running) is left alone.
func TestTick_staleMirrorFallbackAlive(t *testing.T) {
	store := memstore.New()
	mkSession(t, store, "acme", "s1", session.StateRunning, "pod-1", "pool-a")
	mirror := &fakeMirror{
		rows: map[string]orphansession.MirrorPod{"pod-1": {PoolID: "pool-a", Phase: "terminated"}},
		lag:  map[string]float64{"pool-a": 120},
	}
	fb := &fakeFallback{res: map[string]fbResult{"pod-1": {phase: "attached", found: true}}}
	r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{Fallback: fb})

	n, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Fatalf("failed count = %d, want 0 (fallback says alive)", n)
	}
}

// spec: §10.1 line 51 — a missing mirror row resolves through the
// fallback; a deleted Sandbox (found=false) is itself a terminal signal.
func TestTick_mirrorMissFallbackPodGone_spec10_1_line51(t *testing.T) {
	store := memstore.New()
	mkSession(t, store, "acme", "s1", session.StateFinalizing, "pod-gone", "pool-a")
	mirror := &fakeMirror{rows: map[string]orphansession.MirrorPod{}} // no row for pod-gone
	fb := &fakeFallback{res: map[string]fbResult{"pod-gone": {phase: "", found: false}}}
	r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{Fallback: fb})

	n, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("failed count = %d, want 1", n)
	}
	if getState(t, store, "acme", "s1").State != session.StateFailed {
		t.Errorf("session not failed after fallback reported the pod gone")
	}
}

// A missing mirror row with no fallback wired is resolved conservatively
// (no transition) — the reconciler never forces a failure it cannot
// substantiate.
func TestTick_mirrorMissNoFallbackConservative(t *testing.T) {
	store := memstore.New()
	mkSession(t, store, "acme", "s1", session.StateRunning, "pod-x", "pool-a")
	mirror := &fakeMirror{rows: map[string]orphansession.MirrorPod{}}
	r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{})

	n, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Fatalf("failed count = %d, want 0", n)
	}
	if getState(t, store, "acme", "s1").State != session.StateRunning {
		t.Errorf("conservative skip violated — session transitioned")
	}
}

// A fallback read error must not force a false transition; the session
// is skipped and the sweep continues.
func TestTick_fallbackErrorSkips(t *testing.T) {
	store := memstore.New()
	mkSession(t, store, "acme", "s1", session.StateRunning, "pod-1", "pool-a")
	mirror := &fakeMirror{
		rows: map[string]orphansession.MirrorPod{"pod-1": {PoolID: "pool-a", Phase: "attached"}},
		lag:  map[string]float64{"pool-a": 120}, // stale → fallback
	}
	fb := &fakeFallback{err: errors.New("apiserver unreachable")}
	met := &fakeMetrics{}
	r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{Fallback: fb, Metrics: met})

	n, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick returned error, want nil (transient read errors are skipped): %v", err)
	}
	if n != 0 {
		t.Fatalf("failed count = %d, want 0", n)
	}
	if met.reconciliations != 0 {
		t.Errorf("reconciliations = %d, want 0 on read error", met.reconciliations)
	}
	if getState(t, store, "acme", "s1").State != session.StateRunning {
		t.Errorf("session transitioned on a transient fallback error")
	}
}

// A mirror read error skips the affected session without aborting the
// whole sweep.
func TestTick_mirrorErrorSkips(t *testing.T) {
	store := memstore.New()
	mkSession(t, store, "acme", "s1", session.StateRunning, "pod-1", "pool-a")
	mirror := &fakeMirror{getErr: errors.New("postgres down")}
	r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{})

	n, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Fatalf("failed count = %d, want 0", n)
	}
}

// spec: §10.1 line 51 — the reconciler runs across every tenant.
func TestTick_multiTenant(t *testing.T) {
	store := memstore.New()
	mkSession(t, store, "acme", "a1", session.StateRunning, "pod-a", "pool-a")
	mkSession(t, store, "globex", "g1", session.StateInputRequired, "pod-g", "pool-g")
	mirror := &fakeMirror{rows: map[string]orphansession.MirrorPod{
		"pod-a": {PoolID: "pool-a", Phase: "terminated"},
		"pod-g": {PoolID: "pool-g", Phase: "terminated"},
	}}
	met := &fakeMetrics{}
	r := orphansession.New(store, fakeTenants{"acme", "globex"}, mirror, orphansession.Options{Metrics: met})

	n, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 2 {
		t.Fatalf("failed count = %d, want 2", n)
	}
	if met.reconciliations != 2 {
		t.Errorf("reconciliations = %d, want 2", met.reconciliations)
	}
}

// The per-pool lag gauge is published once per pool per Tick even when
// the pool carries several eligible sessions (memoized lag read).
func TestTick_perPoolLagMemoized(t *testing.T) {
	store := memstore.New()
	mkSession(t, store, "acme", "s1", session.StateRunning, "pod-1", "pool-a")
	mkSession(t, store, "acme", "s2", session.StateRunning, "pod-2", "pool-a")
	mirror := &fakeMirror{
		rows: map[string]orphansession.MirrorPod{
			"pod-1": {PoolID: "pool-a", Phase: "attached"},
			"pod-2": {PoolID: "pool-a", Phase: "attached"},
		},
		lag: map[string]float64{"pool-a": 5},
	}
	r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{Metrics: &fakeMetrics{}})

	if _, err := r.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if mirror.lagCalls["pool-a"] != 1 {
		t.Errorf("lag reads for pool-a = %d, want 1 (memoized)", mirror.lagCalls["pool-a"])
	}
}

// A second Tick after an orphan was failed does not re-count it: the
// row is now terminal and no longer eligible. This is the idempotency
// guard that lets the reconciler run on every replica.
func TestTick_idempotentSecondPass(t *testing.T) {
	store := memstore.New()
	mkSession(t, store, "acme", "s1", session.StateRunning, "pod-1", "pool-a")
	mirror := &fakeMirror{rows: map[string]orphansession.MirrorPod{
		"pod-1": {PoolID: "pool-a", Phase: "terminated"},
	}}
	met := &fakeMetrics{}
	r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{Metrics: met})

	if n, _ := r.Tick(context.Background()); n != 1 {
		t.Fatalf("first Tick failed count = %d, want 1", n)
	}
	if n, _ := r.Tick(context.Background()); n != 0 {
		t.Fatalf("second Tick failed count = %d, want 0 (already terminal)", n)
	}
	if met.reconciliations != 1 {
		t.Errorf("reconciliations = %d, want 1 across both passes", met.reconciliations)
	}
}

// New applies the §10.1 defaults for interval and stale threshold.
func TestNew_defaults(t *testing.T) {
	r := orphansession.New(memstore.New(), fakeTenants{}, &fakeMirror{}, orphansession.Options{})
	if r == nil {
		t.Fatal("New returned nil")
	}
	if orphansession.DefaultInterval != 60*time.Second {
		t.Errorf("DefaultInterval = %v, want 60s", orphansession.DefaultInterval)
	}
	if orphansession.DefaultStaleMirrorThreshold != 60*time.Second {
		t.Errorf("DefaultStaleMirrorThreshold = %v, want 60s", orphansession.DefaultStaleMirrorThreshold)
	}
}
