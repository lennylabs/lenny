// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// heartbeatEnd is the status the adapter ends a session's stream with when
// the runtime stops answering heartbeats.
var heartbeatEnd = status.Error(codes.DeadlineExceeded, "runtime missed heartbeat ack deadline")

// evictingExecutor is the funnel executor with the stream-eviction seam the
// pod executor has. It records each EvictStream and whether each Release
// arrived on a live context.
type evictingExecutor struct {
	*funnelExecutor

	mu          sync.Mutex
	evicted     []string
	releaseErrs []error
}

func (e *evictingExecutor) EvictStream(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evicted = append(e.evicted, sessionID)
}

func (e *evictingExecutor) Release(ctx context.Context, sessionID string, d executor.Disposition) error {
	e.mu.Lock()
	e.releaseErrs = append(e.releaseErrs, ctx.Err())
	e.mu.Unlock()
	return e.funnelExecutor.Release(ctx, sessionID, d)
}

func (e *evictingExecutor) evictions() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.evicted...)
}

func (e *evictingExecutor) releaseContextErrs() []error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]error(nil), e.releaseErrs...)
}

// getHookStore runs afterGet once, after the first Get returns, and fails
// every Get with getErr when it is set.
type getHookStore struct {
	*hookStore
	getErr   error
	once     sync.Once
	afterGet func()
}

func (g *getHookStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	if g.getErr != nil {
		return sessionstore.Session{}, g.getErr
	}
	row, err := g.hookStore.Get(ctx, tenantID, id)
	if g.afterGet != nil {
		g.once.Do(g.afterGet)
	}
	return row, err
}

// streamFixture is a funnel fixture whose executor has the stream-eviction
// seam.
type streamFixture struct {
	*funnelFixture
	evict *evictingExecutor
}

func newStreamFixture(t *testing.T) *streamFixture {
	t.Helper()
	f := newFunnelFixture(t, funnelOpts{})
	ev := &evictingExecutor{funnelExecutor: f.exec}
	f.srv.executor = ev
	return &streamFixture{funnelFixture: f, evict: ev}
}

// seedAt seeds id in st at coordination_generation rowGen, bound to pod-x,
// with the binding stamped at bindGen.
func (f *streamFixture) seedAt(t *testing.T, id string, st session.State, rowGen, bindGen int64) *podsession.BindResult {
	t.Helper()
	_, bind := f.seed(t, id, st, 0, false)
	if _, err := f.store.Update(context.Background(), "acme", id, func(r *sessionstore.Session) error {
		r.CoordinationGeneration = rowGen
		return nil
	}); err != nil {
		t.Fatalf("set generation of %s: %v", id, err)
	}
	bind.CoordinationGeneration = bindGen
	return bind
}

// leaseSnapshot copies the lease store's holders and acquire count, so a test
// can assert a path wrote no lease.
func (f *streamFixture) leaseSnapshot() (map[string]string, int) {
	out := make(map[string]string, len(f.leases.holders))
	for k, v := range f.leases.holders {
		out[k] = v
	}
	return out, f.leases.acquires
}

func (f *streamFixture) assertLeasesUnchanged(t *testing.T, holders map[string]string, acquires int) {
	t.Helper()
	got, n := f.leaseSnapshot()
	if n != acquires || len(got) != len(holders) {
		t.Fatalf("lease store changed: acquires %d -> %d, holders %v -> %v", acquires, n, holders, got)
	}
	for k, v := range holders {
		if got[k] != v {
			t.Fatalf("lease holder of %s = %q, want %q", k, got[k], v)
		}
	}
}

// spec: 28.5.1 (Gateway-to-pod), 7.3 (Retry and Resume)
// diagnosis: a stream failure on this replica's current binding is reported
// as runtime_crash with the stream's sandbox as the pod assignment, so the
// retryable failure moves a running session to resume_pending and the funnel
// releases the binding with the failed disposition. A failure means the
// report uses another reason, loses the pod identity, or never reaches the
// failure funnel.
func TestStreamFailureReportsRuntimeCrash_spec_28_5_1(t *testing.T) {
	f := newStreamFixture(t)
	f.seedAt(t, "sess-crash", session.StateRunning, 1, 1)

	f.srv.ReportAttachStreamFailure("acme", "sess-crash", "pod-x", heartbeatEnd)

	row := f.state(t, "sess-crash")
	if row.State != session.StateResumePending || row.FailureReason != string(session.FailureRuntimeCrash) {
		t.Fatalf("row = %s/%q, want resume_pending/runtime_crash", row.State, row.FailureReason)
	}
	if row.PodAssignment != "pod-x" {
		t.Errorf("PodAssignment = %q, want the stream's sandbox pod-x", row.PodAssignment)
	}
	if calls := f.exec.callList(); len(calls) != 1 || calls[0] != executor.DispositionFailed {
		t.Errorf("release calls = %v, want one failed release", calls)
	}
	if len(f.evict.evictions()) != 0 {
		t.Errorf("a reported failure evicted without the release: %v", f.evict.evictions())
	}
}

// spec: 28.5.1 (Gateway-to-pod), 7.3 (Retry and Resume)
// diagnosis: the end of a stream whose binding has been replaced, or whose
// session is no longer bound here, is not reported. A failure means a late
// end of a superseded stream fails a session served by a newer binding.
func TestStreamFailureOfSupersededBindingIsNotReported_spec_28_5_1(t *testing.T) {
	for _, tc := range []struct {
		name   string
		unbind bool
	}{
		{"binding names another sandbox", false},
		{"session unbound", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStreamFixture(t)
			f.seedAt(t, "sess-old", session.StateRunning, 1, 1)
			if tc.unbind {
				f.registry.Remove("sess-old")
			}

			f.srv.ReportAttachStreamFailure("acme", "sess-old", "pod-old", heartbeatEnd)

			if st := f.state(t, "sess-old").State; st != session.StateRunning {
				t.Errorf("state = %s, want running", st)
			}
			if calls := f.exec.callList(); len(calls) != 0 {
				t.Errorf("release calls = %v, want none", calls)
			}
			if _, ok := f.registry.Get("sess-old"); ok == tc.unbind {
				t.Errorf("registry entry present = %v, want %v", ok, !tc.unbind)
			}
		})
	}
}

// blockingSealer blocks every Seal until its context ends.
type blockingSealer struct{}

func (blockingSealer) Seal(ctx context.Context, _, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

// spec: 28.5.1 (Gateway-to-pod), 7.3 (Retry and Resume), 7.1 (Normal Flow)
// diagnosis: on a session whose retry policy makes runtime_crash
// non-retryable, the report takes the failed edge, whose terminal pipeline
// seals the workspace. With a seal that never returns, the seal window ends
// on its own real-time bound, and the binding release runs on a report
// context that has not expired. A failure means a hung seal consumed the
// whole report context, so the pod release and the cascade ran on a dead
// context and did nothing.
func TestStreamFailureNonRetryableReleasesOnLiveContext_spec_28_5_1(t *testing.T) {
	f := newStreamFixture(t)
	f.srv.sealer = blockingSealer{}
	f.srv.sealMaxDuration = 100 * time.Millisecond
	f.srv.streamFailureReportTimeout = 3 * time.Second
	f.seedAt(t, "sess-fatal", session.StateRunning, 1, 1)
	if _, err := f.store.Update(context.Background(), "acme", "sess-fatal", func(r *sessionstore.Session) error {
		r.RetryPolicy = &session.RetryPolicy{
			RetryableFailures:    []string{string(session.FailurePodEvicted)},
			NonRetryableFailures: []string{string(session.FailureRuntimeCrash)},
		}
		return nil
	}); err != nil {
		t.Fatalf("set retry policy: %v", err)
	}

	f.srv.ReportAttachStreamFailure("acme", "sess-fatal", "pod-x", heartbeatEnd)

	if st := f.state(t, "sess-fatal").State; st != session.StateFailed {
		t.Fatalf("state = %s, want failed", st)
	}
	calls := f.exec.callList()
	if len(calls) != 1 || calls[0] != executor.DispositionFailed {
		t.Fatalf("release calls = %v, want one failed release", calls)
	}
	if errs := f.evict.releaseContextErrs(); len(errs) != 1 || errs[0] != nil {
		t.Fatalf("release context errors = %v, want the release on a live context", errs)
	}
}

// spec: 10.1.5 (Stale Replica Behavior), 10.1.1 (Stateless Replicas and
// Per-Session Coordination), 28.5.1 (Gateway-to-pod)
// diagnosis: when a peer has advanced the session's coordination_generation
// past this replica's binding, a stream failure is not reported: the binding
// and its stream are evicted, no binder release drains the pod the new
// coordinator serves, and no row or lease is written. A binding stamped at
// the row's generation still reports, and a binding stamped at zero fails
// closed as superseded. A failure means a stale replica fails or drains a
// session that a peer coordinates.
func TestStreamFailureReportFromStaleGenerationEvictsWithoutDrain_spec_10_1_5(t *testing.T) {
	for _, st := range []session.State{
		session.StateRunning, session.StateInputRequired, session.StateSuspended, session.StateResuming,
	} {
		t.Run("stale binding in "+string(st), func(t *testing.T) {
			f := newStreamFixture(t)
			f.seedAt(t, "sess-stale", st, 2, 1)
			before := f.state(t, "sess-stale")
			holders, acquires := f.leaseSnapshot()

			f.srv.ReportAttachStreamFailure("acme", "sess-stale", "pod-x", heartbeatEnd)

			after := f.state(t, "sess-stale")
			if after.State != before.State || after.RetryCount != before.RetryCount ||
				after.CoordinationGeneration != before.CoordinationGeneration {
				t.Errorf("row changed: %s/%d/%d -> %s/%d/%d", before.State, before.RetryCount,
					before.CoordinationGeneration, after.State, after.RetryCount, after.CoordinationGeneration)
			}
			if _, ok := f.registry.Get("sess-stale"); ok {
				t.Error("the stale binding stayed in the registry")
			}
			if ev := f.evict.evictions(); len(ev) != 1 || ev[0] != "sess-stale" {
				t.Errorf("evictions = %v, want one for sess-stale", ev)
			}
			if calls := f.exec.callList(); len(calls) != 0 {
				t.Errorf("binder release calls = %v, want none: the pod is the new coordinator's", calls)
			}
			if n := f.log.count("drain:"); n != 0 {
				t.Errorf("drain stamps = %d, want none", n)
			}
			f.assertLeasesUnchanged(t, holders, acquires)
		})
	}

	t.Run("binding at the row generation reports", func(t *testing.T) {
		f := newStreamFixture(t)
		f.seedAt(t, "sess-own", session.StateRunning, 2, 2)
		f.srv.ReportAttachStreamFailure("acme", "sess-own", "pod-x", heartbeatEnd)
		if st := f.state(t, "sess-own").State; st != session.StateResumePending {
			t.Errorf("state = %s, want resume_pending", st)
		}
		if calls := f.exec.callList(); len(calls) != 1 || calls[0] != executor.DispositionFailed {
			t.Errorf("release calls = %v, want one failed release", calls)
		}
		if ev := f.evict.evictions(); len(ev) != 0 {
			t.Errorf("evictions = %v, want none", ev)
		}
	})

	t.Run("binding stamped at zero is not reported", func(t *testing.T) {
		f := newStreamFixture(t)
		f.seedAt(t, "sess-zero", session.StateRunning, 1, 0)
		f.srv.ReportAttachStreamFailure("acme", "sess-zero", "pod-x", heartbeatEnd)
		if st := f.state(t, "sess-zero").State; st != session.StateRunning {
			t.Errorf("state = %s, want running", st)
		}
		if calls := f.exec.callList(); len(calls) != 0 {
			t.Errorf("release calls = %v, want none", calls)
		}
	})
}

// spec: 10.1.5 (Stale Replica Behavior), 10.1.1 (Stateless Replicas and
// Per-Session Coordination)
// diagnosis: the coordination check leaves alone a binding it does not own
// the decision for. A row this replica moved past the binding's generation
// itself, as a failed resume leaves it in awaiting_client_action, keeps its
// binding. A binding published between the row read and the eviction stays
// and its stream is not evicted. A row read error reports nothing and keeps
// the binding. A failure means the check tears down a binding that a later
// resume or terminal release owns, or a newer binding of the session.
func TestStreamFailureCoordinationCheckKeepsOwnBinding_spec_10_1_5(t *testing.T) {
	t.Run("own generation bump in awaiting_client_action", func(t *testing.T) {
		f := newStreamFixture(t)
		bind := f.seedAt(t, "sess-held", session.StateAwaitingClientAction, 3, 1)
		f.srv.ReportAttachStreamFailure("acme", "sess-held", "pod-x", heartbeatEnd)
		if cur, ok := f.registry.Get("sess-held"); !ok || cur != bind {
			t.Errorf("binding = %+v ok=%v, want the binding kept", cur, ok)
		}
		if calls := f.exec.callList(); len(calls) != 0 {
			t.Errorf("binder calls = %v, want none", calls)
		}
		if ev := f.evict.evictions(); len(ev) != 0 {
			t.Errorf("evictions = %v, want none", ev)
		}
	})

	t.Run("binding replaced after the row read", func(t *testing.T) {
		f := newStreamFixture(t)
		f.seedAt(t, "sess-race", session.StateRunning, 2, 1)
		fresh := &podsession.BindResult{SessionID: "sess-race", TenantID: "acme", SandboxName: "pod-x", CoordinationGeneration: 2}
		f.srv.store = &getHookStore{hookStore: f.store, afterGet: func() { f.registry.Put(fresh) }}

		f.srv.ReportAttachStreamFailure("acme", "sess-race", "pod-x", heartbeatEnd)

		if cur, ok := f.registry.Get("sess-race"); !ok || cur != fresh {
			t.Errorf("binding = %+v ok=%v, want the newer binding kept", cur, ok)
		}
		if ev := f.evict.evictions(); len(ev) != 0 {
			t.Errorf("evictions = %v, want none for the newer binding", ev)
		}
		if calls := f.exec.callList(); len(calls) != 0 {
			t.Errorf("binder calls = %v, want none", calls)
		}
	})

	t.Run("row read error", func(t *testing.T) {
		f := newStreamFixture(t)
		bind := f.seedAt(t, "sess-err", session.StateRunning, 1, 1)
		f.srv.store = &getHookStore{hookStore: f.store, getErr: errors.New("injected read fault")}

		f.srv.ReportAttachStreamFailure("acme", "sess-err", "pod-x", heartbeatEnd)

		if cur, ok := f.registry.Get("sess-err"); !ok || cur != bind {
			t.Errorf("binding = %+v ok=%v, want the binding kept", cur, ok)
		}
		if calls := f.exec.callList(); len(calls) != 0 {
			t.Errorf("binder calls = %v, want none", calls)
		}
		if ev := f.evict.evictions(); len(ev) != 0 {
			t.Errorf("evictions = %v, want none", ev)
		}
	})
}
