// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// deadlockMetrics records the §16.1 deadlock resolutions the detector emits.
type deadlockMetrics struct {
	mu          sync.Mutex
	detected    int
	pools       []string
	resolutions []string
	durations   []float64
}

func (m *deadlockMetrics) IncDelegationDeadlockDetected(pool string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.detected++
	m.pools = append(m.pools, pool)
}

func (m *deadlockMetrics) ObserveDelegationDeadlockResolution(pool, resolution string, seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pools = append(m.pools, pool)
	m.resolutions = append(m.resolutions, resolution)
	m.durations = append(m.durations, seconds)
}

// mutatorProbeStore wraps a session store. Update fails with updateErr when
// it is set; otherwise it runs beforeMutate under the store's row lock,
// immediately before the caller's mutator.
type mutatorProbeStore struct {
	sessionstore.Store
	updateErr    error
	beforeMutate func()
}

func (p *mutatorProbeStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if p.updateErr != nil {
		return sessionstore.Session{}, p.updateErr
	}
	return p.Store.Update(ctx, tenantID, id, func(s *sessionstore.Session) error {
		if p.beforeMutate != nil {
			p.beforeMutate()
		}
		return mutate(s)
	})
}

// deadlockFailWiring builds the wiring deadlockFail reads: the session store,
// a session server for the terminal claim and pipeline, and the live
// input-wait registry.
func deadlockFailWiring(t *testing.T, store sessionstore.Store, waits *inputwait.Registry) *gatewayWiring {
	t.Helper()
	w := &gatewayWiring{}
	w.sessions = store
	w.inputWaits = waits
	w.sessionSrv = sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	t.Cleanup(w.sessionSrv.Close)
	return w
}

// rowState reads one acme row's state and failure reason.
func rowState(t *testing.T, store sessionstore.Store, id string) (session.State, string) {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row.State, row.FailureReason
}

// deadlockNonTerminalStates are the non-terminal states a deepest blocked
// task's row can hold when the detector fails it.
var deadlockNonTerminalStates = []session.State{
	session.StateRunning,
	session.StateInputRequired,
	session.StateResumePending,
	session.StateResuming,
	session.StateAwaitingClientAction,
	session.StateSuspended,
	session.StateStarting,
	session.StateReady,
	session.StateFinalizing,
}

// TestDeadlockFailRefusesAnsweredWaitInEveryState_spec_8_8 pins the locked
// re-check of the live wait: a task whose request_input wait has left the
// registry is not failed, whatever non-terminal state its row holds. The
// write returns false and leaves the row unchanged. Against the writer that
// checked only for a terminal row, every case here was failed.
//
// spec: §8.8 (Subtree deadlock detection)
func TestDeadlockFailRefusesAnsweredWaitInEveryState_spec_8_8(t *testing.T) {
	for _, st := range deadlockNonTerminalStates {
		t.Run(string(st), func(t *testing.T) {
			store := memstore.New()
			waits := inputwait.NewRegistry()
			seedInState(t, store, "c", st)
			if _, err := waits.Register("c", "r1", nil); err != nil {
				t.Fatalf("register: %v", err)
			}
			if err := waits.Resolve("c", "r1", "answer"); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			w := deadlockFailWiring(t, store, waits)
			if w.deadlockFail(context.Background(), "acme", "c") {
				t.Fatal("deadlockFail = true for an answered wait, want false")
			}
			if got, reason := rowState(t, store, "c"); got != st || reason != "" {
				t.Errorf("row = %q/%q, want %q unchanged", got, reason, st)
			}
		})
	}
}

// TestDeadlockFailRefusesTerminalRowAndUnwiredRegistry_spec_8_8 covers the
// other refusals: a terminal row is never rewritten even with a pending
// wait, and with no input-wait registry wired no wait can be pending, so
// the write fails closed to no write.
//
// spec: §8.8 (Subtree deadlock detection), §7.2 (Interactive Session Model)
func TestDeadlockFailRefusesTerminalRowAndUnwiredRegistry_spec_8_8(t *testing.T) {
	t.Run("terminal row", func(t *testing.T) {
		store := memstore.New()
		waits := inputwait.NewRegistry()
		seedInState(t, store, "c", session.StateCompleted)
		if _, err := waits.Register("c", "r1", nil); err != nil {
			t.Fatalf("register: %v", err)
		}
		if deadlockFailWiring(t, store, waits).deadlockFail(context.Background(), "acme", "c") {
			t.Fatal("deadlockFail = true over a terminal row, want false")
		}
		if got, _ := rowState(t, store, "c"); got != session.StateCompleted {
			t.Errorf("row = %q, want completed unchanged", got)
		}
	})
	t.Run("no registry", func(t *testing.T) {
		store := memstore.New()
		seedInState(t, store, "c", session.StateRunning)
		if deadlockFailWiring(t, store, nil).deadlockFail(context.Background(), "acme", "c") {
			t.Fatal("deadlockFail = true with no registry wired, want false")
		}
		if got, _ := rowState(t, store, "c"); got != session.StateRunning {
			t.Errorf("row = %q, want running unchanged", got)
		}
	})
}

// TestDeadlockFailStoreErrorReturnsFalseAndLogs_spec_8_8 covers a store
// error on the write: deadlockFail logs it and returns false, so the
// detector records no resolution for the action.
//
// spec: §8.8 (Subtree deadlock detection), §16.1
func TestDeadlockFailStoreErrorReturnsFalseAndLogs_spec_8_8(t *testing.T) {
	mem := memstore.New()
	seedInState(t, mem, "c", session.StateRunning)
	waits := inputwait.NewRegistry()
	if _, err := waits.Register("c", "r1", nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	store := &mutatorProbeStore{Store: mem, updateErr: errors.New("connection reset")}
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	if deadlockFailWiring(t, store, waits).deadlockFail(context.Background(), "acme", "c") {
		t.Fatal("deadlockFail = true on a store error, want false")
	}
	if out := buf.String(); !strings.Contains(out, "deadlock-timeout fail c") || !strings.Contains(out, "connection reset") {
		t.Errorf("log = %q, want the deadlock-timeout fail line naming the session and the error", out)
	}
}

// deadlockScenario is a live P awaits C subtree in which C, a running row,
// holds a pending request_input wait. Its detector runs the production
// deadlockFail through the given wrapper.
type deadlockScenario struct {
	store   *mutatorProbeStore
	waits   *inputwait.Registry
	wiring  *gatewayWiring
	metrics *deadlockMetrics
	det     *deadlock.Detector
}

func newDeadlockScenario(t *testing.T, wrap func(w *gatewayWiring) deadlock.FailFunc) *deadlockScenario {
	t.Helper()
	mem := memstore.New()
	seedInState(t, mem, "p", session.StateRunning)
	seedInState(t, mem, "c", session.StateRunning)
	if _, err := mem.Update(context.Background(), "acme", "p", func(s *sessionstore.Session) error { s.PoolRef = "pool-root"; return nil }); err != nil {
		t.Fatalf("set root pool: %v", err)
	}
	sc := &deadlockScenario{store: &mutatorProbeStore{Store: mem}, waits: inputwait.NewRegistry(), metrics: &deadlockMetrics{}}
	sc.waits.SetClock(func() time.Time { return deadlockBase })
	if _, err := sc.waits.Register("c", "r1", nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	tracker := deadlock.NewAwaitTracker()
	t.Cleanup(tracker.Begin("acme", "p", []string{"c"}))
	sc.wiring = deadlockFailWiring(t, sc.store, sc.waits)
	sc.det = deadlock.NewDetector(deadlock.NewManager(120*time.Second, sc.metrics), tracker, sc.waits, deadlockNodeLookup(mem), wrap(sc.wiring))
	return sc
}

var deadlockBase = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

// TestDeadlockTimeoutFailsEdgeServedWaitOnRunningRow_spec_8_8 drives the
// detector over a wait served on the gateway-edge endpoint, which leaves the
// blocked task's row running. At willTimeoutAt the task is failed with
// DEADLOCK_TIMEOUT and the `timeout` resolution is recorded once, measured
// from the first detection, and the detection and the resolution carry the
// root's pool. A state gate on input_required would leave this task live.
//
// spec: §8.8 (Subtree deadlock detection), §16.1 (Metrics)
func TestDeadlockTimeoutFailsEdgeServedWaitOnRunningRow_spec_8_8(t *testing.T) {
	sc := newDeadlockScenario(t, func(w *gatewayWiring) deadlock.FailFunc { return w.deadlockFail })
	ctx := context.Background()
	sc.det.RunOnce(ctx, deadlockBase)
	sc.det.RunOnce(ctx, deadlockBase.Add(125*time.Second))
	got, reason := rowState(t, sc.store, "c")
	if got != session.StateFailed || reason != string(session.FailureDeadlockTimeout) {
		t.Fatalf("row c = %q/%q, want failed/%s", got, reason, session.FailureDeadlockTimeout)
	}
	if p, _ := rowState(t, sc.store, "p"); p != session.StateRunning {
		t.Errorf("row p = %q, want the subtree root left running", p)
	}
	if len(sc.metrics.resolutions) != 1 || sc.metrics.resolutions[0] != "timeout" || sc.metrics.durations[0] != 125 {
		t.Errorf("resolutions = %v durations = %v, want [timeout] [125]", sc.metrics.resolutions, sc.metrics.durations)
	}
	// The detection and the resolution carry the root's PoolRef, which the
	// production node lookup reads from the row (§16.1).
	if want := []string{"pool-root", "pool-root"}; !reflect.DeepEqual(sc.metrics.pools, want) {
		t.Errorf("pool labels = %v, want %v", sc.metrics.pools, want)
	}
}

// TestDeadlockTimeoutSkipsWaitAnsweredAfterSnapshot_spec_8_8 answers the
// task's wait after the timed-out sweep's snapshot and before the fail
// write. The write is refused, the row stays running, and no resolution of
// any label is recorded. Against the pre-fix detector, the task was failed
// and `timeout` was recorded before the write.
//
// spec: §8.8 (Subtree deadlock detection), §16.1
func TestDeadlockTimeoutSkipsWaitAnsweredAfterSnapshot_spec_8_8(t *testing.T) {
	sc := newDeadlockScenario(t, func(w *gatewayWiring) deadlock.FailFunc {
		return func(ctx context.Context, tenantID, id string) bool {
			_ = w.inputWaits.Resolve(id, "r1", "answer")
			return w.deadlockFail(ctx, tenantID, id)
		}
	})
	ctx := context.Background()
	sc.det.RunOnce(ctx, deadlockBase)
	sc.det.RunOnce(ctx, deadlockBase.Add(120*time.Second))
	if got, _ := rowState(t, sc.store, "c"); got != session.StateRunning {
		t.Fatalf("row c = %q, want running: its wait was answered before the write", got)
	}
	if len(sc.metrics.resolutions) != 0 {
		t.Errorf("resolutions = %v, want none for a refused write", sc.metrics.resolutions)
	}
}

// TestDeadlockClearanceResolutionCarriesRootPool_spec_16_1 clears the
// detected subtree before its timeout over the production node lookup and
// the live registry: an answer to the blocked task's wait records
// `client_input`, a cancelled blocked child records `child_cancel`, and a
// child that leaves its wait without either records no resolution. Each
// recorded resolution carries the root's PoolRef and the
// detection-to-clearing latency.
//
// spec: §16.1 (Metrics), §8.8 (Subtree deadlock detection)
func TestDeadlockClearanceResolutionCarriesRootPool_spec_16_1(t *testing.T) {
	cases := []struct {
		name  string
		clear func(t *testing.T, sc *deadlockScenario)
		want  []string
	}{
		{name: "answered", want: []string{deadlock.ResolutionClientInput}, clear: func(t *testing.T, sc *deadlockScenario) {
			if err := sc.waits.Resolve("c", "r1", "yes"); err != nil {
				t.Fatalf("resolve: %v", err)
			}
		}},
		{name: "child cancelled", want: []string{deadlock.ResolutionChildCancel}, clear: func(t *testing.T, sc *deadlockScenario) {
			sc.waits.Cancel("c", "r1")
			if _, err := sc.store.Store.Update(context.Background(), "acme", "c", func(s *sessionstore.Session) error {
				s.State = session.StateCancelled
				return nil
			}); err != nil {
				t.Fatalf("cancel c: %v", err)
			}
		}},
		{name: "wait left unanswered", clear: func(_ *testing.T, sc *deadlockScenario) {
			sc.waits.Cancel("c", "r1")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := newDeadlockScenario(t, func(w *gatewayWiring) deadlock.FailFunc { return w.deadlockFail })
			ctx := context.Background()
			sc.det.RunOnce(ctx, deadlockBase)
			tc.clear(t, sc)
			sc.det.RunOnce(ctx, deadlockBase.Add(30*time.Second))
			if !reflect.DeepEqual(sc.metrics.resolutions, tc.want) {
				t.Fatalf("resolutions = %v, want %v", sc.metrics.resolutions, tc.want)
			}
			wantPools := []string{"pool-root"}
			if len(tc.want) == 1 {
				wantPools = append(wantPools, "pool-root")
				if sc.metrics.durations[0] != 30 {
					t.Errorf("duration = %v, want 30", sc.metrics.durations[0])
				}
			}
			if !reflect.DeepEqual(sc.metrics.pools, wantPools) {
				t.Errorf("pool labels = %v, want %v", sc.metrics.pools, wantPools)
			}
			if _, ok := sc.det.Manager().Event("p"); ok {
				t.Error("Event(p) still active after the subtree cleared")
			}
		})
	}
}

// TestDeadlockTimeoutStoreErrorRedetects_spec_8_8 covers a store error on
// the timed-out sweep's write: no resolution is recorded, the tracked entry
// is deleted, and the next sweep re-detects the still-deadlocked subtree
// with a fresh detectedAt.
//
// spec: §8.8 (Subtree deadlock detection), §16.1
func TestDeadlockTimeoutStoreErrorRedetects_spec_8_8(t *testing.T) {
	sc := newDeadlockScenario(t, func(w *gatewayWiring) deadlock.FailFunc { return w.deadlockFail })
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	ctx := context.Background()
	sc.det.RunOnce(ctx, deadlockBase)
	sc.store.updateErr = errors.New("connection reset")
	timedOut := deadlockBase.Add(120 * time.Second)
	sc.det.RunOnce(ctx, timedOut)
	if len(sc.metrics.resolutions) != 0 {
		t.Fatalf("resolutions = %v after a store error, want none", sc.metrics.resolutions)
	}
	if _, ok := sc.det.Manager().Event("p"); ok {
		t.Fatal("Event(p) still active after the timed-out sweep, want the entry deleted")
	}
	if !strings.Contains(buf.String(), "connection reset") {
		t.Errorf("log = %q, want the store error logged", buf.String())
	}
	sc.store.updateErr = nil
	redetect := timedOut.Add(10 * time.Second)
	sc.det.RunOnce(ctx, redetect)
	ev, ok := sc.det.Manager().Event("p")
	if !ok || !ev.DetectedAt.Equal(redetect) {
		t.Errorf("re-detected event = %+v ok=%v, want DetectedAt %v", ev, ok, redetect)
	}
}

// TestDeadlockFailNeverFailsWaitResolvedBeforeMutator_spec_8_8 races the
// wait's Resolve against the fail write on many seeded interleavings. The
// invariant: when the wait had left the registry before the mutator ran,
// the write never applies, and an applied write always leaves the row
// failed with DEADLOCK_TIMEOUT. The seed is logged for replay.
//
// spec: §8.8 (Subtree deadlock detection)
func TestDeadlockFailNeverFailsWaitResolvedBeforeMutator_spec_8_8(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed=%d", seed)
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 200; i++ {
		mem := memstore.New()
		seedInState(t, mem, "c", session.StateRunning)
		waits := inputwait.NewRegistry()
		if _, err := waits.Register("c", "r1", nil); err != nil {
			t.Fatalf("register: %v", err)
		}
		// Only the first Update is the deadlock write; the terminal pipeline
		// that follows an applied write updates the row again.
		var resolved, resolvedAtMutate, sawMutate atomic.Bool
		store := &mutatorProbeStore{Store: mem, beforeMutate: func() {
			if sawMutate.CompareAndSwap(false, true) {
				resolvedAtMutate.Store(resolved.Load())
			}
		}}
		w := deadlockFailWiring(t, store, waits)
		resolveDelay := time.Duration(rng.Intn(50)) * time.Microsecond
		failDelay := time.Duration(rng.Intn(50)) * time.Microsecond
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			time.Sleep(resolveDelay)
			_ = waits.Resolve("c", "r1", "answer")
			resolved.Store(true)
		}()
		var applied bool
		go func() {
			defer wg.Done()
			time.Sleep(failDelay)
			applied = w.deadlockFail(context.Background(), "acme", "c")
		}()
		wg.Wait()
		if applied && resolvedAtMutate.Load() {
			t.Fatalf("seed=%d iteration %d: the write applied although the wait left the registry before the mutator ran", seed, i)
		}
		got, reason := rowState(t, mem, "c")
		if applied != (got == session.StateFailed) {
			t.Fatalf("seed=%d iteration %d: applied=%v but row = %q", seed, i, applied, got)
		}
		if applied && reason != string(session.FailureDeadlockTimeout) {
			t.Fatalf("seed=%d iteration %d: reason = %q, want %s", seed, i, reason, session.FailureDeadlockTimeout)
		}
	}
}
