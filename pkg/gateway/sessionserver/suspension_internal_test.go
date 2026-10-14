// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// ----- fakes -----

// fakeSealer answers SealSession and records seals and unseals.
type fakeSealer struct {
	mu      sync.Mutex
	sealed  bool
	idleAge time.Duration
	err     error
	seals   []int
	unseals int
	order   *[]string
}

func (f *fakeSealer) SealSession(_ context.Context, _ string, _ int64, idleSeconds int) (bool, time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seals = append(f.seals, idleSeconds)
	if f.order != nil {
		*f.order = append(*f.order, "seal")
	}
	return f.sealed, f.idleAge, f.err
}

func (f *fakeSealer) UnsealSession(context.Context, string, int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unseals++
	if f.order != nil {
		*f.order = append(*f.order, "unseal")
	}
	return nil
}

func (f *fakeSealer) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seals), f.unseals
}

// fakeSuspensionCheckpointer answers CheckpointWithTrigger and runs before,
// when set, inside the checkpoint.
type fakeSuspensionCheckpointer struct {
	mu     sync.Mutex
	err    error
	calls  int
	before func()
	order  *[]string
}

func (f *fakeSuspensionCheckpointer) CheckpointWithTrigger(context.Context, string, string, checkpoint.Trigger) error {
	f.mu.Lock()
	f.calls++
	if f.order != nil {
		*f.order = append(*f.order, "checkpoint")
	}
	before, err := f.before, f.err
	f.mu.Unlock()
	if before != nil {
		before()
	}
	return err
}

func (f *fakeSuspensionCheckpointer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// releasingExecutor records each Release.
type releasingExecutor struct {
	mu       sync.Mutex
	released []string
	order    *[]string
}

func (*releasingExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (*releasingExecutor) Close(context.Context, string) error { return nil }

func (e *releasingExecutor) Release(_ context.Context, sessionID string, _ executor.Disposition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.released = append(e.released, sessionID)
	if e.order != nil {
		*e.order = append(*e.order, "release")
	}
	return nil
}

func (e *releasingExecutor) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.released)
}

// timeStamper records Stamp (as the zero time) and StampAt calls.
type timeStamper struct {
	mu    sync.Mutex
	at    []time.Time
	stamp int
}

func (s *timeStamper) Stamp(string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stamp++
}

func (s *timeStamper) StampAt(_, _ string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.at = append(s.at, at)
}

// suspensionRig is a server wired for the suspension paths.
type suspensionRig struct {
	srv       *Server
	store     sessionstore.Store
	registry  *podsession.Registry
	sealer    *fakeSealer
	cp        *fakeSuspensionCheckpointer
	exec      *releasingExecutor
	stamper   *timeStamper
	coord     *sessioninbox.Coordinator
	now       time.Time
	released  int
	cpFailed  int
	order     []string
	metricsMu sync.Mutex
}

const rigGeneration = 3

func newSuspensionRig(t *testing.T, withBinder bool) *suspensionRig {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	r := &suspensionRig{
		store:    memstore.New(),
		registry: podsession.NewRegistry(),
		stamper:  &timeStamper{},
		now:      time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC),
		coord: sessioninbox.NewCoordinator(sessioninbox.Config{
			Inbox: sessioninbox.NewMemoryInbox(10),
			DLQ:   sessioninbox.NewDLQ(rc, 10),
		}),
	}
	r.sealer = &fakeSealer{sealed: true, order: &r.order}
	r.cp = &fakeSuspensionCheckpointer{order: &r.order}
	r.exec = &releasingExecutor{order: &r.order}
	opts := Options{
		Executor:               r.exec,
		PodRegistry:            r.registry,
		SuspensionCheckpointer: r.cp,
		ActivityStamper:        r.stamper,
		Messaging:              r.coord,
		Clock:                  func() time.Time { return r.now },
		IncSessionPodReleasedDuringSuspension: func(string, string) {
			r.metricsMu.Lock()
			r.released++
			r.metricsMu.Unlock()
		},
		IncSessionSuspensionCheckpointFailed: func(string, string) {
			r.metricsMu.Lock()
			r.cpFailed++
			r.metricsMu.Unlock()
		},
	}
	if withBinder {
		opts.PodBinder = &podsession.Binder{}
	}
	r.srv = New(r.store, opts)
	r.srv.sealerFor = func(*podsession.BindResult) sessionSealer { return r.sealer }
	t.Cleanup(r.srv.Close)
	return r
}

// seedRunning seeds a running row on pod-1 at the rig's generation and
// registers its binding.
func (r *suspensionRig) seedRunning(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	row := sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1",
		PoolRef: "pool-a", CoordinationGeneration: rigGeneration,
		CreatedAt: r.now.Add(-time.Hour), UpdatedAt: r.now.Add(-time.Hour),
		LastAgentActivityAt: r.now.Add(-time.Hour),
	}
	if err := r.store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	r.registry.Put(&podsession.BindResult{SessionID: id, TenantID: "acme", CoordinationGeneration: rigGeneration})
	stored, _ := r.store.Get(context.Background(), "acme", id)
	return stored
}

// seedSuspended seeds an interrupt suspension on pod-1 and registers its
// binding.
func (r *suspensionRig) seedSuspended(t *testing.T, id string, since time.Time) sessionstore.Session {
	t.Helper()
	row := sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateSuspended, PodAssignment: "pod-1",
		PoolRef: "pool-a", CoordinationGeneration: rigGeneration,
		SuspendedAt: since, SuspendedReason: sessionstore.SuspendedReasonInterruptAcknowledged,
		CreatedAt: since, UpdatedAt: since,
	}
	if err := r.store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	r.registry.Put(&podsession.BindResult{SessionID: id, TenantID: "acme", CoordinationGeneration: rigGeneration})
	stored, _ := r.store.Get(context.Background(), "acme", id)
	return stored
}

// wait blocks until the session's suspension entry is gone.
func (r *suspensionRig) wait(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for r.srv.suspensions.has(id) {
		if time.Now().After(deadline) {
			t.Fatalf("suspension of %s did not finish", id)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (r *suspensionRig) get(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	row, err := r.store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row
}

func (r *suspensionRig) metrics() (released, cpFailed int) {
	r.metricsMu.Lock()
	defer r.metricsMu.Unlock()
	return r.released, r.cpFailed
}

// ----- classifyIdleWrite -----

// TestClassifyIdleWrite_spec_6_2 pins the outcome of each combination of
// generation, state, pod, and activity the idle write can see.
//
// spec: 6.2 (maxClientIdleSeconds clock behavior across states), 10.1.1
// (Stateless Replicas and Per-Session Coordination)
func TestClassifyIdleWrite_spec_6_2(t *testing.T) {
	at := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	listed := sessionstore.Session{State: session.StateRunning, PodAssignment: "pod-1", LastAgentActivityAt: at, CoordinationGeneration: 3}
	with := func(f func(*sessionstore.Session)) sessionstore.Session {
		r := listed
		f(&r)
		return r
	}
	for _, tc := range []struct {
		name string
		seen sessionstore.Session
		want idleWriteOutcome
	}{
		{"unchanged_running_applies", listed, idleWriteApply},
		{"later_generation_running_is_stale", with(func(r *sessionstore.Session) { r.CoordinationGeneration = 4 }), idleWriteStale},
		{"later_generation_podless_idle_is_stale", with(func(r *sessionstore.Session) {
			r.CoordinationGeneration = 4
			r.State = session.StateSuspended
			r.SuspendedReason = sessionstore.SuspendedReasonIdle
			r.PodAssignment = ""
		}), idleWriteStale},
		{"own_landed_write", with(func(r *sessionstore.Session) {
			r.State = session.StateSuspended
			r.SuspendedReason = sessionstore.SuspendedReasonIdle
			r.PodAssignment = ""
		}), idleWriteLanded},
		{"activity_moved_lifts", with(func(r *sessionstore.Session) { r.LastAgentActivityAt = at.Add(time.Second) }), idleWriteLift},
		{"interrupted_on_pod_lifts", with(func(r *sessionstore.Session) {
			r.State = session.StateSuspended
			r.SuspendedReason = sessionstore.SuspendedReasonInterruptAcknowledged
		}), idleWriteLift},
		{"input_required_on_pod_lifts", with(func(r *sessionstore.Session) { r.State = session.StateInputRequired }), idleWriteLift},
		{"running_on_another_pod_stops", with(func(r *sessionstore.Session) { r.PodAssignment = "pod-2" }), idleWriteStop},
		{"resume_pending_podless_stops", with(func(r *sessionstore.Session) {
			r.State = session.StateResumePending
			r.PodAssignment = ""
		}), idleWriteStop},
		{"terminal_podless_stops", with(func(r *sessionstore.Session) {
			r.State = session.StateCompleted
			r.PodAssignment = ""
		}), idleWriteStop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyIdleWrite(tc.seen, listed, 3); got != tc.want {
				t.Fatalf("classifyIdleWrite = %d, want %d", got, tc.want)
			}
		})
	}
}

// ----- SuspendIdle -----

// TestSuspendIdleSealAnswerMovesTheClock_spec_6_2 pins that a seal that does
// not take leaves the row running with no checkpoint and no write, and moves
// the gateway idle clock: not sealed with an idle age sets it to now minus
// the age, and a refusal or an error sets it to now; the seal receives the
// idle seconds the sweep passed.
//
// spec: 6.2 (maxClientIdleSeconds clock behavior across states)
func TestSuspendIdleSealAnswerMovesTheClock_spec_6_2(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sealed    bool
		idleAge   time.Duration
		err       error
		wantAt    bool
		wantStamp bool
	}{
		{name: "not_sealed", idleAge: 30 * time.Second, wantAt: true},
		{name: "activity_untapped", err: status.Error(codes.FailedPrecondition, "activity_untapped: no tap"), wantStamp: true},
		{name: "rpc_error", err: errors.New("adapter unreachable"), wantStamp: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newSuspensionRig(t, false)
			row := r.seedRunning(t, "s1")
			r.sealer.sealed, r.sealer.idleAge, r.sealer.err = tc.sealed, tc.idleAge, tc.err
			r.srv.SuspendIdle(context.Background(), row, 900)
			r.wait(t, "s1")
			if got := r.get(t, "s1"); got.State != session.StateRunning || got.PodAssignment != "pod-1" {
				t.Fatalf("row = %q on %q, want running on pod-1", got.State, got.PodAssignment)
			}
			if r.cp.count() != 0 || r.exec.count() != 0 {
				t.Fatalf("checkpoints %d releases %d, want none", r.cp.count(), r.exec.count())
			}
			if seals, unseals := r.sealer.counts(); seals != 1 || unseals != 0 || r.sealer.seals[0] != 900 {
				t.Fatalf("seals %v unseals %d, want one seal of 900 and no unseal", r.sealer.seals, unseals)
			}
			if tc.wantAt && (len(r.stamper.at) != 1 || !r.stamper.at[0].Equal(r.now.Add(-tc.idleAge))) {
				t.Errorf("StampAt = %v, want [now - %s]", r.stamper.at, tc.idleAge)
			}
			if tc.wantStamp && (r.stamper.stamp != 1 || len(r.stamper.at) != 0) {
				t.Errorf("Stamp %d StampAt %v, want one Stamp", r.stamper.stamp, r.stamper.at)
			}
		})
	}
}

// TestSuspendIdleCompletes_spec_6_2 pins the full idle suspension: seal,
// checkpoint, one suspended write with reason Idle and no pod, then the
// executor release and one release event, with no unseal; with an inbox
// entry present at the write the row ends resume_pending.
//
// spec: 6.2 (Pod State Machine), 16.1 (Metrics), 7.2 (Message delivery
// routing — seven paths, path 6)
func TestSuspendIdleCompletes_spec_6_2(t *testing.T) {
	t.Run("no_held_message", func(t *testing.T) {
		r := newSuspensionRig(t, true)
		row := r.seedRunning(t, "s1")
		r.srv.SuspendIdle(context.Background(), row, 900)
		r.wait(t, "s1")
		got := r.get(t, "s1")
		if got.State != session.StateSuspended || got.SuspendedReason != sessionstore.SuspendedReasonIdle || got.PodAssignment != "" {
			t.Fatalf("row = %q/%q on %q, want an Idle suspension with no pod", got.State, got.SuspendedReason, got.PodAssignment)
		}
		if !got.SuspendedAt.Equal(r.now) {
			t.Errorf("SuspendedAt = %v, want %v", got.SuspendedAt, r.now)
		}
		want := []string{"seal", "checkpoint", "release"}
		if len(r.order) != len(want) {
			t.Fatalf("order = %v, want %v", r.order, want)
		}
		for i := range want {
			if r.order[i] != want[i] {
				t.Fatalf("order = %v, want %v", r.order, want)
			}
		}
		if released, failed := r.metrics(); released != 1 || failed != 0 {
			t.Errorf("released events %d checkpoint failures %d, want 1 and 0", released, failed)
		}
	})
	t.Run("inbox_entry_resumes", func(t *testing.T) {
		r := newSuspensionRig(t, true)
		row := r.seedRunning(t, "s1")
		if _, err := r.coord.EnqueueInbox(context.Background(), "acme", "s1", sessioninbox.Message{MessageID: "m1", Payload: []byte(`{}`), EnqueuedAt: r.now}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		r.srv.SuspendIdle(context.Background(), row, 900)
		r.wait(t, "s1")
		if got := r.get(t, "s1"); got.State != session.StateResumePending {
			t.Fatalf("state = %q, want resume_pending (a message was held at the write)", got.State)
		}
	})
}

// TestSuspendIdleCheckpointFailureUnseals_spec_6_2 pins that a checkpoint
// failure unseals once, leaves the row running with its pod, counts one
// checkpoint failure, and suppresses the next attempt for 60 seconds.
//
// spec: 6.2 (Pod State Machine), 4.4.3 (Checkpoint Quiescence, Timeout, and
// Abort Handling), 16.1 (Metrics)
func TestSuspendIdleCheckpointFailureUnseals_spec_6_2(t *testing.T) {
	r := newSuspensionRig(t, false)
	row := r.seedRunning(t, "s1")
	r.cp.err = errors.New("object store down")
	r.srv.SuspendIdle(context.Background(), row, 900)
	r.wait(t, "s1")
	if got := r.get(t, "s1"); got.State != session.StateRunning || got.PodAssignment != "pod-1" {
		t.Fatalf("row = %q on %q, want running on pod-1", got.State, got.PodAssignment)
	}
	if seals, unseals := r.sealer.counts(); seals != 1 || unseals != 1 {
		t.Fatalf("seals %d unseals %d, want 1 each", seals, unseals)
	}
	if _, failed := r.metrics(); failed != 1 {
		t.Errorf("checkpoint failures = %d, want 1", failed)
	}
	r.now = r.now.Add(59 * time.Second)
	r.srv.SuspendIdle(context.Background(), row, 900)
	r.wait(t, "s1")
	if seals, _ := r.sealer.counts(); seals != 1 {
		t.Fatalf("seals = %d inside the retry interval, want 1", seals)
	}
	r.now = r.now.Add(time.Second)
	r.cp.err = nil
	r.srv.SuspendIdle(context.Background(), row, 900)
	r.wait(t, "s1")
	if seals, _ := r.sealer.counts(); seals != 2 {
		t.Fatalf("seals = %d after the retry interval, want 2", seals)
	}
}

// TestSuspendIdleGuardOutcomes_spec_6_2 pins the write's guard: activity that
// moved during the checkpoint unseals once and writes nothing; a generation
// advance makes no unseal and no release; and an open terminal claim starts
// nothing.
//
// spec: 6.2 (maxClientIdleSeconds clock behavior across states), 10.1.1
// (Stateless Replicas and Per-Session Coordination)
func TestSuspendIdleGuardOutcomes_spec_6_2(t *testing.T) {
	t.Run("activity_moved_lifts", func(t *testing.T) {
		r := newSuspensionRig(t, false)
		row := r.seedRunning(t, "s1")
		r.cp.before = func() {
			_, _ = r.store.Update(context.Background(), "acme", "s1", func(s *sessionstore.Session) error {
				s.LastAgentActivityAt = r.now
				return nil
			})
		}
		r.srv.SuspendIdle(context.Background(), row, 900)
		r.wait(t, "s1")
		if got := r.get(t, "s1"); got.State != session.StateRunning {
			t.Fatalf("state = %q, want running", got.State)
		}
		if _, unseals := r.sealer.counts(); unseals != 1 || r.exec.count() != 0 {
			t.Fatalf("unseals %d releases %d, want 1 and 0", unseals, r.exec.count())
		}
	})
	t.Run("generation_advanced_is_stale", func(t *testing.T) {
		r := newSuspensionRig(t, false)
		row := r.seedRunning(t, "s1")
		r.cp.before = func() {
			_, _ = r.store.Update(context.Background(), "acme", "s1", func(s *sessionstore.Session) error {
				s.CoordinationGeneration = rigGeneration + 1
				return nil
			})
		}
		r.srv.SuspendIdle(context.Background(), row, 900)
		r.wait(t, "s1")
		if got := r.get(t, "s1"); got.State != session.StateRunning {
			t.Fatalf("state = %q, want running", got.State)
		}
		if _, unseals := r.sealer.counts(); unseals != 0 || r.exec.count() != 0 {
			t.Fatalf("unseals %d releases %d, want none", unseals, r.exec.count())
		}
	})
	t.Run("terminal_claim_starts_nothing", func(t *testing.T) {
		r := newSuspensionRig(t, false)
		row := r.seedRunning(t, "s1")
		end := r.srv.BeginTerminal("s1")
		defer end()
		r.srv.SuspendIdle(context.Background(), row, 900)
		r.wait(t, "s1")
		if seals, _ := r.sealer.counts(); seals != 0 || r.cp.count() != 0 {
			t.Fatalf("seals %d checkpoints %d under a terminal claim, want none", seals, r.cp.count())
		}
	})
	t.Run("no_binding_writes_nothing", func(t *testing.T) {
		r := newSuspensionRig(t, false)
		row := r.seedRunning(t, "s1")
		r.registry.Remove("s1")
		r.srv.SuspendIdle(context.Background(), row, 900)
		if seals, _ := r.sealer.counts(); seals != 0 || r.srv.suspensions.has("s1") {
			t.Fatalf("seals %d, want none without a binding", seals)
		}
	})
}

// ----- ReleaseSuspendedPod -----

// TestReleaseSuspendedPod_spec_6_2 pins the interrupt suspension's release: a
// checkpoint, the clear of the pod, the executor release, and one release
// event, with no seal call; a checkpoint failure keeps the pod and counts one
// failure; a row that moved during the checkpoint releases nothing; and a DLQ
// entry accepted after SuspendedAt moves the row to resume_pending.
//
// spec: 6.2 (Pod State Machine), 4.4.3 (Checkpoint Quiescence, Timeout, and
// Abort Handling), 16.1 (Metrics), 7.2 (Message delivery routing — seven
// paths, path 6)
func TestReleaseSuspendedPod_spec_6_2(t *testing.T) {
	t.Run("releases", func(t *testing.T) {
		r := newSuspensionRig(t, true)
		row := r.seedSuspended(t, "s1", r.now.Add(-time.Hour))
		r.srv.ReleaseSuspendedPod(context.Background(), row)
		r.wait(t, "s1")
		got := r.get(t, "s1")
		if got.State != session.StateSuspended || got.PodAssignment != "" {
			t.Fatalf("row = %q on %q, want podless suspended", got.State, got.PodAssignment)
		}
		if r.exec.count() != 1 {
			t.Fatalf("releases = %d, want 1", r.exec.count())
		}
		if seals, _ := r.sealer.counts(); seals != 0 {
			t.Errorf("seals = %d, want none for an interrupt suspension", seals)
		}
		if released, _ := r.metrics(); released != 1 {
			t.Errorf("release events = %d, want 1", released)
		}
	})
	t.Run("checkpoint_failure_keeps_pod", func(t *testing.T) {
		r := newSuspensionRig(t, true)
		row := r.seedSuspended(t, "s1", r.now.Add(-time.Hour))
		r.cp.err = errors.New("checkpoint failed")
		r.srv.ReleaseSuspendedPod(context.Background(), row)
		r.wait(t, "s1")
		if got := r.get(t, "s1"); got.PodAssignment != "pod-1" || r.exec.count() != 0 {
			t.Fatalf("pod %q releases %d, want pod-1 kept", got.PodAssignment, r.exec.count())
		}
		if released, failed := r.metrics(); released != 0 || failed != 1 {
			t.Errorf("released %d failed %d, want 0 and 1", released, failed)
		}
		if !r.srv.suspensionRetries.waiting("s1", r.now) {
			t.Error("no retry time recorded after the checkpoint failure")
		}
	})
	t.Run("row_moved_during_checkpoint", func(t *testing.T) {
		r := newSuspensionRig(t, true)
		row := r.seedSuspended(t, "s1", r.now.Add(-time.Hour))
		r.cp.before = func() {
			_, _ = r.store.Update(context.Background(), "acme", "s1", func(s *sessionstore.Session) error {
				s.State = session.StateRunning
				return nil
			})
		}
		r.srv.ReleaseSuspendedPod(context.Background(), row)
		r.wait(t, "s1")
		if got := r.get(t, "s1"); got.State != session.StateRunning || got.PodAssignment != "pod-1" || r.exec.count() != 0 {
			t.Fatalf("row %q on %q releases %d, want running on pod-1 and nothing released", got.State, got.PodAssignment, r.exec.count())
		}
	})
	t.Run("held_after_suspension_resumes", func(t *testing.T) {
		r := newSuspensionRig(t, true)
		since := r.now.Add(-time.Hour)
		row := r.seedSuspended(t, "s1", since)
		if _, err := r.coord.EnqueueDLQ(context.Background(), "acme", "s1", sessioninbox.Message{MessageID: "m1", Payload: []byte(`{}`), EnqueuedAt: r.now}, 0); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		r.srv.ReleaseSuspendedPod(context.Background(), row)
		r.wait(t, "s1")
		if got := r.get(t, "s1"); got.State != session.StateResumePending || got.PodAssignment != "" {
			t.Fatalf("row = %q on %q, want podless resume_pending", got.State, got.PodAssignment)
		}
	})
	t.Run("no_binding_starts_nothing", func(t *testing.T) {
		r := newSuspensionRig(t, true)
		row := r.seedSuspended(t, "s1", r.now.Add(-time.Hour))
		r.registry.Remove("s1")
		r.srv.ReleaseSuspendedPod(context.Background(), row)
		if r.srv.suspensions.has("s1") || r.cp.count() != 0 {
			t.Fatal("a release started for a session with no local binding")
		}
	})
}

// ----- retry record -----

// TestRetryRecordPrunesOnInsert_spec_6_2 pins that an insertion removes every
// entry whose 60 seconds have passed and keeps the rest, and that a lookup
// removes nothing.
//
// spec: 6.2 (Pod State Machine)
func TestRetryRecordPrunesOnInsert_spec_6_2(t *testing.T) {
	var r retryRecord
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	r.recordFailure("old", t0)
	r.recordFailure("recent", t0.Add(30*time.Second))
	if r.waiting("old", t0.Add(61*time.Second)) || r.size() != 2 {
		t.Fatalf("lookup changed the record: size %d", r.size())
	}
	r.recordFailure("new", t0.Add(61*time.Second))
	if r.size() != 2 || r.waiting("old", t0.Add(61*time.Second)) || !r.waiting("recent", t0.Add(61*time.Second)) {
		t.Fatalf("after the insertion: size %d, want old pruned and recent kept", r.size())
	}
}
