// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// gate is a blocking point a fake can wait on until the test releases it.
type gate struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func newGate() *gate { return &gate{entered: make(chan struct{}), release: make(chan struct{})} }

// wait signals entry once and blocks until release or ctx ends.
func (g *gate) wait(ctx context.Context) {
	g.once.Do(func() { close(g.entered) })
	select {
	case <-g.release:
	case <-ctx.Done():
	}
}

// TestRunnerSLAAndCancellation_spec_12_8 pins that the SLA deadline only
// raises the alert, never stops a job, and that a context ended inside a
// phase makes the runner write nothing: the job stays in its persisted
// phase with no failure counted, and a RunPending on a live context then
// finishes it.
// spec: 12.8 (Erasure SLA enforcement), 12.8 (Erasure job phase field (crash recovery))
func TestRunnerSLAAndCancellation_spec_12_8(t *testing.T) {
	t.Run("past deadline completes", func(t *testing.T) {
		h := newRunHarness(t)
		j := h.start("acme", "alice", nil)
		if _, err := h.jobs.Update(context.Background(), j.ID, func(w *erasurejob.Job) error {
			w.Deadline = time.Hour
			w.StartedAt = h.clock().Add(-3 * time.Hour)
			return nil
		}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if err := h.runner.RunPending(context.Background()); err != nil {
			t.Fatalf("RunPending: %v", err)
		}
		if got := h.job(j.ID); got.Phase != erasurejob.PhaseCompleted {
			t.Errorf("overdue job phase = %s, want completed", got.Phase)
		}
	})
	t.Run("cancelled inside a phase", func(t *testing.T) {
		h := newRunHarness(t)
		ctx, cancel := context.WithCancel(context.Background())
		h.deleteGate = func(context.Context, string) { cancel() }
		h.set(func() { h.deleteErr = context.Canceled })
		j := h.start("acme", "alice", nil)
		if err := h.runner.Run(ctx, j.ID); err == nil {
			t.Fatal("Run on an ended context returned nil")
		}
		got := h.job(j.ID)
		if got.Phase != erasurejob.PhaseStoreDeleting {
			t.Errorf("phase = %s, want store_deleting kept for the next leader", got.Phase)
		}
		if f := h.failureCount(); len(f) != 0 {
			t.Errorf("failures = %v, want none on an ended context", f)
		}
		h.deleteGate = nil
		h.set(func() { h.deleteErr = nil })
		if err := h.runner.RunPending(context.Background()); err != nil {
			t.Fatalf("RunPending: %v", err)
		}
		if got := h.job(j.ID); got.Phase != erasurejob.PhaseCompleted {
			t.Errorf("phase after resume = %s, want completed", got.Phase)
		}
	})
}

// TestRunnerAdmission_spec_12_8 pins RunPending's admission rules: a job
// already running on this runner is not started twice by an overlapping
// call, at most the limit of runs proceed at once, and at most one run per
// tenant, so a tenant blocked behind its salt lock does not keep another
// tenant's job from running even when its own second job is listed first.
// spec: 12.8 (Erasure job phase field (crash recovery)), 12.8 (erasure_salt key management)
func TestRunnerAdmission_spec_12_8(t *testing.T) {
	t.Run("overlapping call skips the running job", func(t *testing.T) {
		h := newRunHarness(t)
		h.runner.WithMaxConcurrentJobs(2)
		g := newGate()
		h.deleteGate = func(ctx context.Context, tenant string) {
			if tenant == "acme" {
				g.wait(ctx)
			}
		}
		acme := h.start("acme", "alice", nil)
		first := make(chan error, 1)
		go func() { first <- h.runner.RunPending(context.Background()) }()
		<-g.entered
		globex := h.start("globex", "alice", nil)
		if err := h.runner.RunPending(context.Background()); err != nil {
			t.Fatalf("second RunPending: %v", err)
		}
		if n := h.count("delete:acme"); n != 1 {
			t.Errorf("acme DeleteByUser ran %d times, want 1 (no second run)", n)
		}
		if got := h.job(globex.ID); got.Phase != erasurejob.PhaseCompleted {
			t.Errorf("globex phase = %s, want completed", got.Phase)
		}
		close(g.release)
		if err := <-first; err != nil {
			t.Fatalf("first RunPending: %v", err)
		}
		if got := h.job(acme.ID); got.Phase != erasurejob.PhaseCompleted {
			t.Errorf("acme phase = %s, want completed", got.Phase)
		}
	})
	t.Run("limit holds a third tenant", func(t *testing.T) {
		h := newRunHarness(t)
		h.runner.WithMaxConcurrentJobs(2)
		gates := map[string]*gate{"acme": newGate(), "globex": newGate()}
		h.deleteGate = func(ctx context.Context, tenant string) {
			if g, ok := gates[tenant]; ok {
				g.wait(ctx)
			}
		}
		h.start("acme", "alice", nil)
		h.start("globex", "alice", nil)
		first := make(chan error, 1)
		go func() { first <- h.runner.RunPending(context.Background()) }()
		<-gates["acme"].entered
		<-gates["globex"].entered
		initech := h.start("initech", "alice", nil)
		if err := h.runner.RunPending(context.Background()); err != nil {
			t.Fatalf("RunPending at the limit: %v", err)
		}
		if n := h.count("drain:initech"); n != 0 {
			t.Errorf("initech started %d runs at the limit, want 0", n)
		}
		close(gates["acme"].release)
		waitFor(t, "initech to run once a slot freed", func() bool {
			if err := h.runner.RunPending(context.Background()); err != nil {
				t.Fatalf("RunPending after a slot freed: %v", err)
			}
			return h.job(initech.ID).Phase == erasurejob.PhaseCompleted
		})
		if got := h.job(initech.ID); got.Phase != erasurejob.PhaseCompleted {
			t.Errorf("initech phase = %s, want completed once a slot freed", got.Phase)
		}
		close(gates["globex"].release)
		<-first
	})
	t.Run("salt-lock backlog does not hold another tenant", func(t *testing.T) {
		h := newRunHarness(t)
		h.runner.WithMaxConcurrentJobs(2)
		g := newGate()
		h.pseudoGate = func(ctx context.Context, tenant string) {
			if tenant == "acme" {
				g.wait(ctx)
			}
		}
		h.start("acme", "alice", nil)
		first := make(chan error, 1)
		go func() { first <- h.runner.RunPending(context.Background()) }()
		<-g.entered
		acme2 := h.start("acme", "bob", nil)
		globex := h.start("globex", "alice", nil)
		// The second acme job is listed before the globex job.
		for id, at := range map[string]time.Time{acme2.ID: h.clock().Add(-time.Minute), globex.ID: h.clock()} {
			at := at
			if _, err := h.jobs.Update(context.Background(), id, func(w *erasurejob.Job) error { w.StartedAt = at; return nil }); err != nil {
				t.Fatalf("Update: %v", err)
			}
		}
		if err := h.runner.RunPending(context.Background()); err != nil {
			t.Fatalf("RunPending: %v", err)
		}
		if n := h.count("drain:acme"); n != 1 {
			t.Errorf("acme started %d runs, want 1 (the second acme job waits)", n)
		}
		if got := h.job(globex.ID); got.Phase != erasurejob.PhaseCompleted {
			t.Errorf("globex phase = %s, want completed while acme is blocked", got.Phase)
		}
		close(g.release)
		<-first
	})
}

// TestRunnerGuardRefusesStaleWrites_spec_12_8 pins the job-write guard: two
// runners over one store run one job with their steps interleaved by
// blocking fakes. Each transition appears once, the two receipts are
// byte-identical, the failure counter increments at most once, a stale
// runner's failure leaves a completed job completed, a failure (alone or
// followed by a restart) between the receipt and Complete leaves the job
// failed or initiated with the restriction set, and a runner that read
// initiated before a failure and a restart writes nothing.
// spec: 12.8 (Erasure job phase field (crash recovery)), 12.8 (Processing restriction during erasure (GDPR Article 18))
func TestRunnerGuardRefusesStaleWrites_spec_12_8(t *testing.T) {
	ctx := context.Background()
	t.Run("stale advance after another runner completed", func(t *testing.T) {
		h := newRunHarness(t)
		g := newGate()
		staleRunner := h.newRunner()
		j := h.start("acme", "alice", nil)
		h.drainGate = func(ctx context.Context, _ string) { g.wait(ctx) }
		stale := make(chan error, 1)
		go func() { stale <- staleRunner.Run(ctx, j.ID) }()
		<-g.entered
		h.drainGate = nil
		fresh := h.newRunner()
		if err := fresh.Run(ctx, j.ID); err != nil {
			t.Fatalf("fresh Run: %v", err)
		}
		close(g.release)
		if err := <-stale; !errors.Is(err, erasurejob.ErrJobMoved) {
			t.Errorf("stale Run = %v, want ErrJobMoved", err)
		}
		got := h.job(j.ID)
		seen := map[erasurejob.Phase]int{}
		for _, p := range phases(got) {
			seen[p]++
		}
		for p, n := range seen {
			if n != 1 {
				t.Errorf("phase %s appears %d times in PhaseLog %v", p, n, phases(got))
			}
		}
		if got.Phase != erasurejob.PhaseCompleted {
			t.Errorf("phase = %s, want completed", got.Phase)
		}
	})
	t.Run("two receipts are byte-identical", func(t *testing.T) {
		h := newRunHarness(t)
		g := newGate()
		j := h.start("acme", "alice", nil)
		h.receiptGate = func(ctx context.Context, _ erasurejob.Job) { g.wait(ctx) }
		first := h.newRunner()
		done := make(chan error, 1)
		go func() { done <- first.Run(ctx, j.ID) }()
		<-g.entered
		h.receiptGate = nil
		second := h.newRunner()
		if err := second.Run(ctx, j.ID); err != nil {
			t.Fatalf("second Run: %v", err)
		}
		close(g.release)
		if err := <-done; !errors.Is(err, erasurejob.ErrJobMoved) {
			t.Errorf("first Run = %v, want ErrJobMoved from Complete", err)
		}
		rs := h.receiptList()
		if len(rs) != 2 || string(rs[0]) != string(rs[1]) {
			t.Errorf("receipts = %d; want 2 byte-identical", len(rs))
		}
	})
	t.Run("stale failure leaves completed job completed", func(t *testing.T) {
		h := newRunHarness(t)
		g := newGate()
		j := h.start("acme", "alice", nil)
		h.moveTo(j.ID, erasurejob.PhaseStoreDeleting, nil)
		h.deleteGate = func(ctx context.Context, _ string) { g.wait(ctx) }
		h.set(func() { h.deleteErr = errBoom })
		staleRunner := h.newRunner()
		stale := make(chan error, 1)
		go func() { stale <- staleRunner.Run(ctx, j.ID) }()
		<-g.entered
		h.deleteGate = nil
		h.set(func() { h.deleteErr = nil })
		if err := h.newRunner().Run(ctx, j.ID); err != nil {
			t.Fatalf("fresh Run: %v", err)
		}
		h.set(func() { h.deleteErr = errBoom })
		close(g.release)
		if err := <-stale; !errors.Is(err, erasurejob.ErrJobMoved) {
			t.Errorf("stale Run = %v, want ErrJobMoved", err)
		}
		if got := h.job(j.ID); got.Phase != erasurejob.PhaseCompleted {
			t.Errorf("phase = %s, want completed", got.Phase)
		}
		if f := h.failureCount(); len(f) != 0 {
			t.Errorf("failures = %v, want none", f)
		}
	})
	for _, restart := range []bool{false, true} {
		name := "failure between receipt and Complete"
		if restart {
			name += " then restart"
		}
		t.Run(name, func(t *testing.T) {
			h := newRunHarness(t)
			g := newGate()
			j := h.start("acme", "alice", nil)
			h.receiptGate = func(ctx context.Context, _ erasurejob.Job) { g.wait(ctx) }
			done := make(chan error, 1)
			go func() { done <- h.runner.Run(ctx, j.ID) }()
			<-g.entered
			if _, err := h.jobs.Update(ctx, j.ID, func(w *erasurejob.Job) error {
				w.Phase = erasurejob.PhaseFailed
				w.PhaseLog = append(w.PhaseLog, erasurejob.PhaseTransition{Phase: erasurejob.PhaseFailed, At: h.clock()})
				return nil
			}); err != nil {
				t.Fatalf("fail: %v", err)
			}
			want := erasurejob.PhaseFailed
			if restart {
				if _, err := h.jobs.Restart(ctx, j.ID); err != nil {
					t.Fatalf("Restart: %v", err)
				}
				want = erasurejob.PhaseInitiated
			}
			close(g.release)
			if err := <-done; !errors.Is(err, erasurejob.ErrJobMoved) {
				t.Errorf("Run = %v, want ErrJobMoved", err)
			}
			if got := h.job(j.ID); got.Phase != want {
				t.Errorf("phase = %s, want %s", got.Phase, want)
			}
			if r, _ := h.restricted("acme", "alice"); !r {
				t.Error("restriction cleared by a stale Complete")
			}
		})
	}
	t.Run("initiated read across a failure and a restart writes nothing", func(t *testing.T) {
		h := newRunHarness(t)
		g := newGate()
		j := h.start("acme", "alice", nil)
		h.drainGate = func(ctx context.Context, _ string) { g.wait(ctx) }
		done := make(chan error, 1)
		go func() { done <- h.runner.Run(ctx, j.ID) }()
		<-g.entered
		if _, err := h.jobs.Update(ctx, j.ID, func(w *erasurejob.Job) error {
			w.Phase = erasurejob.PhaseFailed
			w.PhaseLog = append(w.PhaseLog, erasurejob.PhaseTransition{Phase: erasurejob.PhaseFailed, At: h.clock()})
			return nil
		}); err != nil {
			t.Fatalf("fail: %v", err)
		}
		if _, err := h.jobs.Restart(ctx, j.ID); err != nil {
			t.Fatalf("Restart: %v", err)
		}
		before := h.job(j.ID)
		close(g.release)
		if err := <-done; !errors.Is(err, erasurejob.ErrJobMoved) {
			t.Errorf("Run = %v, want ErrJobMoved", err)
		}
		after := h.job(j.ID)
		if after.Phase != erasurejob.PhaseInitiated || len(after.PhaseLog) != len(before.PhaseLog) {
			t.Errorf("job after the stale run = %s %v, want unchanged initiated %v", after.Phase, phases(after), phases(before))
		}
		if n := h.count("delete:acme"); n != 0 {
			t.Errorf("DeleteByUser ran %d times, want 0", n)
		}
	})
}

// TestRunPendingCrossTenantRead_spec_12_3 pins the cross_tenant_read event
// of the runner's all-tenants read: exactly one event per RunPending, with
// the runner endpoint, no caller, and the listed row count, including an
// empty read; an emit error starts no run; a nil function emits nothing.
// spec: 12.3 (Postgres HA Requirements), 12.8 (Erasure job phase field (crash recovery))
func TestRunPendingCrossTenantRead_spec_12_3(t *testing.T) {
	type event struct {
		endpoint, caller string
		rows             int
	}
	ctx := context.Background()
	h := newRunHarness(t)
	var events []event
	var emitErr error
	h.runner.WithCrossTenantReadAudit(func(_ context.Context, endpoint, caller string, rows int) error {
		events = append(events, event{endpoint, caller, rows})
		return emitErr
	})
	if err := h.runner.RunPending(ctx); err != nil {
		t.Fatalf("empty RunPending: %v", err)
	}
	h.start("acme", "alice", nil)
	h.start("globex", "alice", nil)
	emitErr = errBoom
	if err := h.runner.RunPending(ctx); !errors.Is(err, errBoom) {
		t.Fatalf("RunPending with a failing emit = %v, want the emit error", err)
	}
	if n := h.count("drain:acme") + h.count("drain:globex"); n != 0 {
		t.Errorf("runs started after a failed emit: %d, want 0", n)
	}
	want := []event{
		{erasurejob.CrossTenantReadEndpointRunner, "", 0},
		{erasurejob.CrossTenantReadEndpointRunner, "", 2},
	}
	if len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Errorf("events = %+v, want %+v", events, want)
	}
	h.runner.WithCrossTenantReadAudit(nil)
	if err := h.runner.RunPending(ctx); err != nil {
		t.Fatalf("RunPending with no recorder: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("a nil recorder emitted: %+v", events)
	}
}

// TestRunnerPollRunsPendingJobs_spec_12_8 pins Poll: it runs a pending job
// on its first tick, runs a job recorded later on a following tick, and
// returns once its context ends.
// spec: 12.8 (Erasure job phase field (crash recovery)), 12.5 (Artifact Store)
func TestRunnerPollRunsPendingJobs_spec_12_8(t *testing.T) {
	h := newRunHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	first := h.start("acme", "alice", nil)
	done := make(chan struct{})
	go func() {
		h.runner.Poll(ctx, 5*time.Millisecond)
		close(done)
	}()
	waitFor(t, "the first job to complete", func() bool { return h.job(first.ID).Phase == erasurejob.PhaseCompleted })
	second := h.start("globex", "alice", nil)
	waitFor(t, "a later job to complete", func() bool { return h.job(second.ID).Phase == erasurejob.PhaseCompleted })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Poll did not return after its context ended")
	}
}
