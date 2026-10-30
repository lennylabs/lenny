// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §7.1 — seal-and-export invariant: bounded exponential
// backoff (5s/2×/60s cap), total window maxWorkspaceSealDurationSeconds
// (300s), then failed/workspace_seal_timeout + workspaceSealFailed audit
// event + lenny_workspace_seal_duration_seconds{outcome} histogram.

// flakySealer fails its first failUntil seal attempts, then succeeds.
// A negative failUntil fails every attempt.
type flakySealer struct {
	failUntil int
	calls     int
	err       error
}

func (f *flakySealer) Seal(context.Context, string, string) error {
	f.calls++
	if f.failUntil < 0 || f.calls <= f.failUntil {
		if f.err != nil {
			return f.err
		}
		return errors.New("minio: connection refused")
	}
	return nil
}

// sealMetricRec records each (pool, outcome, seconds) observation.
type sealMetricRec struct {
	obs []struct {
		pool, outcome string
		seconds       float64
	}
}

func (r *sealMetricRec) observe(pool, outcome string, seconds float64) {
	r.obs = append(r.obs, struct {
		pool, outcome string
		seconds       float64
	}{pool, outcome, seconds})
}

// sealBoundGeneration is the coordination generation the seal tests stamp on
// their bindings. No row in these tests reaches it, so the hook's
// stale-binding step leaves the binding for the seal step to find.
const sealBoundGeneration = 1 << 30

// sealBoundRegistry returns a pod registry holding a binding for each
// session, so the seal step has a binding to mark closing and seal.
func sealBoundRegistry(sessionIDs ...string) *podsession.Registry {
	reg := podsession.NewRegistry()
	for _, id := range sessionIDs {
		reg.Put(&podsession.BindResult{SessionID: id, TenantID: "acme", CoordinationGeneration: sealBoundGeneration})
	}
	return reg
}

// advancingClock returns a clock func and a sealSleep func that advances
// the clock by the slept duration, so the bounded-backoff loop terminates
// deterministically without real delays.
func advancingClock(start time.Time) (func() time.Time, func(context.Context, time.Duration) bool, *[]time.Duration) {
	now := start
	slept := []time.Duration{}
	clock := func() time.Time { return now }
	sleep := func(_ context.Context, d time.Duration) bool {
		slept = append(slept, d)
		now = now.Add(d)
		return true
	}
	return clock, sleep, &slept
}

func TestSealWorkspaceRetriesThenSucceeds_spec_7_1_3(t *testing.T) {
	clock, sleep, slept := advancingClock(time.Unix(0, 0).UTC())
	metric := &sealMetricRec{}
	sealer := &flakySealer{failUntil: 2} // fail twice, succeed on the 3rd
	srv := New(memstore.New(), Options{
		DevTenantHeader:              true,
		PodRegistry:                  sealBoundRegistry("s1"),
		Sealer:                       sealer,
		SealSleep:                    sleep,
		ObserveWorkspaceSealDuration: metric.observe,
		Clock:                        clock,
	})

	sess := sessionstore.Session{ID: "s1", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted}
	if err := srv.sealWorkspace(context.Background(), sess); err != nil {
		t.Fatalf("sealWorkspace returned error after eventual success: %v", err)
	}
	if sealer.calls != 3 {
		t.Errorf("seal attempts = %d, want 3 (2 failures + 1 success)", sealer.calls)
	}
	if len(*slept) != 2 {
		t.Errorf("backoff sleeps = %d, want 2", len(*slept))
	}
	// spec: §7.1 — initial 5s, factor 2×.
	if len(*slept) == 2 && ((*slept)[0] != 5*time.Second || (*slept)[1] != 10*time.Second) {
		t.Errorf("backoff sequence = %v, want [5s 10s]", *slept)
	}
	if len(metric.obs) != 1 || metric.obs[0].outcome != sealOutcomeSuccess {
		t.Errorf("seal metric = %+v, want one success observation", metric.obs)
	}
	if metric.obs[0].pool != "claude-code" {
		t.Errorf("seal metric pool = %q, want claude-code", metric.obs[0].pool)
	}
}

func TestSealWorkspaceBackoffBoundedByWindow_spec_7_1_3(t *testing.T) {
	clock, sleep, slept := advancingClock(time.Unix(0, 0).UTC())
	metric := &sealMetricRec{}
	sealer := &flakySealer{failUntil: -1} // never succeeds
	srv := New(memstore.New(), Options{
		DevTenantHeader:              true,
		PodRegistry:                  sealBoundRegistry("s1"),
		Sealer:                       sealer,
		SealSleep:                    sleep,
		ObserveWorkspaceSealDuration: metric.observe,
		Clock:                        clock,
	})

	sess := sessionstore.Session{ID: "s1", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted}
	err := srv.sealWorkspace(context.Background(), sess)
	if err == nil {
		t.Fatal("sealWorkspace must return the last error when the window is exhausted")
	}
	// Each per-attempt backoff is capped at 60s; the total never exceeds
	// the 300s default window.
	var total time.Duration
	for _, d := range *slept {
		if d > sealBackoffCap {
			t.Errorf("backoff %v exceeds the 60s per-attempt cap", d)
		}
		total += d
	}
	if total > DefaultWorkspaceSealMaxDuration {
		t.Errorf("total backoff %v exceeds the %v window", total, DefaultWorkspaceSealMaxDuration)
	}
	if sealer.calls < 2 {
		t.Errorf("seal attempts = %d, want the seal to be retried before giving up", sealer.calls)
	}
	if len(metric.obs) != 1 || metric.obs[0].outcome != sealOutcomeTimeout {
		t.Errorf("seal metric = %+v, want one timeout observation", metric.obs)
	}
}

// TestSealWorkspacePermanentErrorReturnsImmediately_spec_7_1_permanent
// pins the §7.1 permanent-error arm: a seal that fails with a
// gRPC status the adapter reports as permanent (the §4.4
// workspace-size probe returns FailedPrecondition) ends the retry window
// on its first observation. Exactly one Seal call, zero backoff sleeps,
// an outcome="permanent" observation, and no outcome="timeout"
// observation (so WorkspaceSealStuck stays silent on a tenant
// configuration condition). The pre-fix loop retried every error for the
// full 300s window, so it would sleep and observe outcome="timeout".
func TestSealWorkspacePermanentErrorReturnsImmediately_spec_7_1_permanent(t *testing.T) {
	permanentCodes := []codes.Code{
		codes.FailedPrecondition,
		codes.InvalidArgument,
		codes.Unimplemented,
	}
	for _, code := range permanentCodes {
		t.Run(code.String(), func(t *testing.T) {
			clock, sleep, slept := advancingClock(time.Unix(0, 0).UTC())
			metric := &sealMetricRec{}
			sealer := &flakySealer{failUntil: -1, err: status.Error(code, "workspace exceeds workspaceSizeLimitBytes")}
			srv := New(memstore.New(), Options{
				DevTenantHeader:              true,
				PodRegistry:                  sealBoundRegistry("s1"),
				Sealer:                       sealer,
				SealSleep:                    sleep,
				ObserveWorkspaceSealDuration: metric.observe,
				Clock:                        clock,
			})

			sess := sessionstore.Session{ID: "s1", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted}
			err := srv.sealWorkspace(context.Background(), sess)
			if err == nil {
				t.Fatal("sealWorkspace must return the permanent seal error")
			}
			if sealer.calls != 1 {
				t.Errorf("seal attempts = %d, want 1 (permanent error is not retried)", sealer.calls)
			}
			if len(*slept) != 0 {
				t.Errorf("backoff sleeps = %d, want 0 (permanent error returns without sleeping)", len(*slept))
			}
			if len(metric.obs) != 1 || metric.obs[0].outcome != sealOutcomePermanent {
				t.Fatalf("seal metric = %+v, want one permanent observation", metric.obs)
			}
			for _, o := range metric.obs {
				if o.outcome == sealOutcomeTimeout {
					t.Errorf("observed outcome=timeout on a permanent error; WorkspaceSealStuck must not fire")
				}
			}
			if metric.obs[0].pool != "claude-code" {
				t.Errorf("seal metric pool = %q, want claude-code", metric.obs[0].pool)
			}
		})
	}
}

// TestRecordSessionCompletedPermanentSealStaysTimeoutClass_spec_7_1_permanent
// confirms the §7.1 terminal state is unchanged by the permanent
// arm: the session is still relabelled failed/workspace_seal_timeout even
// though the seal returned immediately on a permanent error, so the §7.1
// failureClass enum is untouched.
func TestRecordSessionCompletedPermanentSealStaysTimeoutClass_spec_7_1_permanent(t *testing.T) {
	metric := &sealMetricRec{}
	store := memstore.New()
	srv := New(store, Options{
		DevTenantHeader:              true,
		PodRegistry:                  sealBoundRegistry("s_perm"),
		Sealer:                       &flakySealer{failUntil: -1, err: status.Error(codes.FailedPrecondition, "workspace exceeds workspaceSizeLimitBytes")},
		ObserveWorkspaceSealDuration: metric.observe,
		Clock:                        func() time.Time { return time.Unix(0, 0).UTC() },
	})
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s_perm", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	completed, _ := store.Get(context.Background(), "acme", "s_perm")

	srv.recordSessionCompleted(context.Background(), session.StateRunning, completed)

	row, _ := store.Get(context.Background(), "acme", "s_perm")
	if row.State != session.StateFailed {
		t.Errorf("state after permanent seal error = %q, want failed", row.State)
	}
	if row.FailureClass != session.FailureClassWorkspaceSealTimeout {
		t.Errorf("failureClass = %q, want workspace_seal_timeout (enum unchanged)", row.FailureClass)
	}
	if len(metric.obs) != 1 || metric.obs[0].outcome != sealOutcomePermanent {
		t.Errorf("seal metric = %+v, want one permanent observation", metric.obs)
	}
}

func TestRecordSessionCompletedFailsOnSealTimeout_spec_7_1_3(t *testing.T) {
	clock, sleep, _ := advancingClock(time.Unix(0, 0).UTC())
	metric := &sealMetricRec{}
	audit := &captureLifecycleAudit{}
	store := memstore.New()
	srv := New(store, Options{
		DevTenantHeader:              true,
		PodRegistry:                  sealBoundRegistry("s_seal"),
		Sealer:                       &flakySealer{failUntil: -1, err: errors.New("minio: 503 slow down")},
		SealSleep:                    sleep,
		ObserveWorkspaceSealDuration: metric.observe,
		LifecycleAuditSink:           audit,
		Clock:                        clock,
	})

	// The session has already transitioned to completed (the handler does
	// this before recordSessionCompleted runs).
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s_seal", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	completed, _ := store.Get(context.Background(), "acme", "s_seal")

	srv.recordSessionCompleted(context.Background(), session.StateRunning, completed)

	// spec: §7.1 — the row is re-labeled failed/workspace_seal_timeout.
	row, _ := store.Get(context.Background(), "acme", "s_seal")
	if row.State != session.StateFailed {
		t.Errorf("state after seal timeout = %q, want failed", row.State)
	}
	if row.FailureClass != session.FailureClassWorkspaceSealTimeout {
		t.Errorf("failureClass = %q, want workspace_seal_timeout", row.FailureClass)
	}
	// spec: §7.1 — workspaceSealFailed audit event records the last error.
	var sealFailed *SessionLifecycleEvent
	for i := range audit.events {
		if audit.events[i].EventType == auditWorkspaceSealFailed {
			sealFailed = &audit.events[i]
		}
	}
	if sealFailed == nil {
		t.Fatalf("no workspaceSealFailed audit event emitted; got %+v", audit.events)
	}
	if sealFailed.Detail != "minio: 503 slow down" {
		t.Errorf("audit detail = %q, want the last MinIO error", sealFailed.Detail)
	}
	if sealFailed.FailureClass != string(session.FailureClassWorkspaceSealTimeout) {
		t.Errorf("audit failureClass = %q, want workspace_seal_timeout", sealFailed.FailureClass)
	}
	// The timeout histogram was observed.
	if len(metric.obs) != 1 || metric.obs[0].outcome != sealOutcomeTimeout {
		t.Errorf("seal metric = %+v, want one timeout observation", metric.obs)
	}
}

// TestRecordSessionCompletedCleanSealStaysCompleted_spec_7_1_3 confirms a
// successful seal leaves the terminal state untouched and observes the
// success histogram with no failure transition.
func TestRecordSessionCompletedCleanSealStaysCompleted_spec_7_1_3(t *testing.T) {
	metric := &sealMetricRec{}
	store := memstore.New()
	srv := New(store, Options{
		DevTenantHeader:              true,
		PodRegistry:                  sealBoundRegistry("s_ok"),
		Sealer:                       &flakySealer{failUntil: 0}, // succeeds first try
		ObserveWorkspaceSealDuration: metric.observe,
		Clock:                        func() time.Time { return time.Unix(0, 0).UTC() },
	})
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s_ok", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	completed, _ := store.Get(context.Background(), "acme", "s_ok")

	srv.recordSessionCompleted(context.Background(), session.StateRunning, completed)

	row, _ := store.Get(context.Background(), "acme", "s_ok")
	if row.State != session.StateCompleted {
		t.Errorf("state after clean seal = %q, want completed (unchanged)", row.State)
	}
	if len(metric.obs) != 1 || metric.obs[0].outcome != sealOutcomeSuccess {
		t.Errorf("seal metric = %+v, want one success observation", metric.obs)
	}
}

// errTransientExport is an export failure the seal retries.
var errTransientExport = errors.New("minio: connection refused")

// sealDispositionExecutor records each release's disposition and each
// stream eviction. It satisfies the streamEvicter seam the seal start uses.
type sealDispositionExecutor struct {
	mu           sync.Mutex
	dispositions []executor.Disposition
	evicted      []string
}

func (e *sealDispositionExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (e *sealDispositionExecutor) Close(ctx context.Context, sessionID string) error {
	return e.Release(ctx, sessionID, "")
}

func (e *sealDispositionExecutor) Release(_ context.Context, _ string, d executor.Disposition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dispositions = append(e.dispositions, d)
	return nil
}

func (e *sealDispositionExecutor) EvictStream(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evicted = append(e.evicted, sessionID)
}

func (e *sealDispositionExecutor) snapshot() ([]executor.Disposition, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]executor.Disposition(nil), e.dispositions...), append([]string(nil), e.evicted...)
}

// scriptedSealer returns errs[i] on call i+1 and the last entry on every
// later call; an empty script succeeds. Before returning on call removeOn
// it removes the session's binding from reg. On every call it records
// whether the binding was marked closing and the executor had evicted the
// stream, which the seal start does before the first attempt.
type scriptedSealer struct {
	errs     []error
	reg      *podsession.Registry
	removeOn int
	exec     *sealDispositionExecutor
	calls    int
	unmarked int
}

func (s *scriptedSealer) Seal(_ context.Context, _, sessionID string) error {
	s.calls++
	if s.reg != nil {
		bind, ok := s.reg.Get(sessionID)
		_, evicted := s.exec.snapshot()
		if !ok || !bind.Closing() || len(evicted) == 0 {
			s.unmarked++
		}
		if s.calls == s.removeOn {
			s.reg.Remove(sessionID)
		}
	}
	if len(s.errs) == 0 {
		return nil
	}
	if s.calls <= len(s.errs) {
		return s.errs[s.calls-1]
	}
	return s.errs[len(s.errs)-1]
}

// relabelFailingStore fails every Update, standing in for a relabel whose
// store write errors.
type relabelFailingStore struct{ sessionstore.Store }

func (relabelFailingStore) Update(context.Context, string, string, func(*sessionstore.Session) error) (sessionstore.Session, error) {
	return sessionstore.Session{}, errors.New("postgres: connection reset")
}

// sealOutcomeCase is one row of the seal outcome table.
type sealOutcomeCase struct {
	name string
	// bound registers a binding for the session; nilRegistry wires no
	// registry at all.
	bound, nilRegistry bool
	errs               []error
	removeOn           int
	failRelabel        bool
	wantCalls          int
	wantOutcome        string
	wantState          session.State
	wantDisposition    executor.Disposition
}

// TestSealOutcomeTable_spec_7_1 pins the seal outcomes the §7.1
// Seal-and-export invariant states, through the post-terminal hook. A
// binding removed after the seal begins ends the window on its first
// observation as permanent and relabels the session failed, and the hook
// then reports failed on its one status_change and one session_complete. A
// session never bound on this replica, and a server with no registry, skip
// the Sealer. A session row absent on the primary is permanent, while an
// adapter gRPC NotFound is retried. A failed seal releases with the failed
// disposition even when the relabel's store write errors, which is the
// assertion that tells the hook's own failure flag apart from the
// relabel's returned row. Before the fix, a removed binding retried for the
// whole window (or read as a success through the Checkpointer's no-binding
// mapping), ErrNotFound was retried, and a failed relabel released with the
// clean disposition.
//
// spec: 7.1 (Normal Flow)
func TestSealOutcomeTable_spec_7_1(t *testing.T) {
	cases := []sealOutcomeCase{
		{
			name: "binding removed after the seal begins", bound: true,
			errs: []error{errTransientExport}, removeOn: 1,
			wantCalls: 1, wantOutcome: sealOutcomePermanent, wantState: session.StateFailed,
			wantDisposition: executor.DispositionFailed,
		},
		{
			name:      "never-bound session skips the Sealer",
			wantCalls: 0, wantOutcome: sealOutcomeSuccess, wantState: session.StateCompleted,
			wantDisposition: executor.DispositionCompleted,
		},
		{
			name: "nil registry behaves as never-bound", nilRegistry: true,
			wantCalls: 0, wantOutcome: sealOutcomeSuccess, wantState: session.StateCompleted,
			wantDisposition: executor.DispositionCompleted,
		},
		{
			name: "row absent on the primary is permanent", bound: true,
			errs:      []error{fmt.Errorf("checkpointer: record snapshot: %w", sessionstore.ErrNotFound)},
			wantCalls: 1, wantOutcome: sealOutcomePermanent, wantState: session.StateFailed,
			wantDisposition: executor.DispositionFailed,
		},
		{
			name: "adapter gRPC NotFound is retried", bound: true,
			errs:      []error{status.Error(codes.NotFound, "no such session"), nil},
			wantCalls: 2, wantOutcome: sealOutcomeSuccess, wantState: session.StateCompleted,
			wantDisposition: executor.DispositionCompleted,
		},
		{
			name: "failed relabel still releases with the failed disposition", bound: true,
			errs: []error{status.Error(codes.FailedPrecondition, "workspace too large")}, failRelabel: true,
			wantCalls: 1, wantOutcome: sealOutcomePermanent, wantState: session.StateCompleted,
			wantDisposition: executor.DispositionFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runSealOutcomeCase(t, tc) })
	}
}

func runSealOutcomeCase(t *testing.T, tc sealOutcomeCase) {
	t.Helper()
	clock, sleep, _ := advancingClock(time.Unix(0, 0).UTC())
	metric := &sealMetricRec{}
	inner := memstore.New()
	var store sessionstore.Store = inner
	if tc.failRelabel {
		store = relabelFailingStore{Store: inner}
	}
	exec := &sealDispositionExecutor{}
	var reg *podsession.Registry
	if !tc.nilRegistry {
		reg = podsession.NewRegistry()
		if tc.bound {
			reg = sealBoundRegistry("s")
		}
	}
	sealer := &scriptedSealer{errs: tc.errs, reg: reg, removeOn: tc.removeOn, exec: exec}
	bus := sessionevents.NewBus(64)
	srv := New(store, Options{
		DevTenantHeader: true, Sealer: sealer, PodRegistry: reg, Executor: exec, Events: bus,
		SealSleep: sleep, ObserveWorkspaceSealDuration: metric.observe, Clock: clock,
	})
	t.Cleanup(srv.Close)
	row := sessionstore.Session{ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateCompleted}
	if err := inner.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv.AfterTerminalWrite(context.Background(), session.StateRunning, row, nil)

	if sealer.calls != tc.wantCalls {
		t.Errorf("Seal calls = %d, want %d", sealer.calls, tc.wantCalls)
	}
	if sealer.unmarked != 0 {
		t.Errorf("%d Seal call(s) ran before the seal start marked the binding closing and evicted its stream", sealer.unmarked)
	}
	if len(metric.obs) != 1 || metric.obs[0].outcome != tc.wantOutcome {
		t.Errorf("seal metric = %+v, want one %s observation", metric.obs, tc.wantOutcome)
	}
	got, err := inner.Get(context.Background(), "acme", "s")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != tc.wantState {
		t.Errorf("state after the hook = %q, want %q", got.State, tc.wantState)
	}
	disps, _ := exec.snapshot()
	if len(disps) != 1 || disps[0] != tc.wantDisposition {
		t.Errorf("release dispositions = %v, want [%s]", disps, tc.wantDisposition)
	}
	assertTerminalEventsReport(t, bus, tc.wantState)
}

// assertTerminalEventsReport fails unless the hook emitted exactly one
// status_change and one session_complete, each reporting want.
func assertTerminalEventsReport(t *testing.T, bus *sessionevents.Bus, want session.State) {
	t.Helper()
	counts := map[string]int{}
	for _, ev := range bus.History("s", 0) {
		if ev.Type != "status_change" && ev.Type != "session_complete" {
			continue
		}
		counts[ev.Type]++
		if !containsState(ev.Data, want) {
			t.Errorf("%s = %s, want it to report %s", ev.Type, ev.Data, want)
		}
	}
	if counts["status_change"] != 1 || counts["session_complete"] != 1 {
		t.Errorf("terminal events = %v, want one status_change and one session_complete", counts)
	}
}

// TestSealBindingRemovalAtRandomPointsOfTheRetryLoop_spec_7_1 removes the
// session's binding at a random point of the seal retry loop, either inside
// a Seal attempt or during a backoff wait, and asserts that the attempt that
// first observes the removal ends the window as permanent with no further
// attempt or wait. The seed is logged so a failure replays.
//
// spec: 7.1 (Normal Flow)
func TestSealBindingRemovalAtRandomPointsOfTheRetryLoop_spec_7_1(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 200; i++ {
		k := 1 + rng.Intn(5)
		duringWait := rng.Intn(2) == 1
		runRandomBindingRemoval(t, i, k, duringWait)
	}
}

func runRandomBindingRemoval(t *testing.T, iter, k int, duringWait bool) {
	t.Helper()
	reg := sealBoundRegistry("s")
	exec := &sealDispositionExecutor{}
	sealer := &scriptedSealer{errs: []error{errTransientExport}, reg: reg, exec: exec}
	if !duringWait {
		sealer.removeOn = k
	}
	clock, advance, slept := advancingClock(time.Unix(0, 0).UTC())
	sleep := func(ctx context.Context, d time.Duration) bool {
		if duringWait && len(*slept)+1 == k {
			reg.Remove("s")
		}
		return advance(ctx, d)
	}
	metric := &sealMetricRec{}
	srv := New(memstore.New(), Options{
		DevTenantHeader: true, Sealer: sealer, PodRegistry: reg, Executor: exec,
		SealSleep: sleep, ObserveWorkspaceSealDuration: metric.observe, Clock: clock,
	})
	defer srv.Close()
	sess := sessionstore.Session{ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateCompleted}

	if err := srv.sealWorkspace(context.Background(), sess); err == nil {
		t.Fatalf("iteration %d (k=%d wait=%v): sealWorkspace succeeded though the binding was removed", iter, k, duringWait)
	}
	wantCalls, wantSleeps := k, k-1
	if duringWait {
		wantCalls, wantSleeps = k+1, k
	}
	if sealer.calls != wantCalls || len(*slept) != wantSleeps {
		t.Fatalf("iteration %d (k=%d wait=%v): calls %d sleeps %d, want %d and %d",
			iter, k, duringWait, sealer.calls, len(*slept), wantCalls, wantSleeps)
	}
	if len(metric.obs) != 1 || metric.obs[0].outcome != sealOutcomePermanent {
		t.Fatalf("iteration %d (k=%d wait=%v): seal metric = %+v, want one permanent observation",
			iter, k, duringWait, metric.obs)
	}
}
