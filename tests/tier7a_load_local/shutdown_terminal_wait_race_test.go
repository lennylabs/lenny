// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the shutdown wait on the
// terminal claim record.
//
// At SIGTERM the gateway waits until no terminal pipeline and no release of
// a terminal session's binding is in progress, for at most the terminal
// pipeline budget, and only then cancels the session server's lifetime.
// These cases run many post-terminal hooks and terminal binding releases at
// once, open further claims while the wait runs, as the background loops
// do, and then wait and Close in the order the gateway uses, under the race
// detector. They assert that every pipeline whose claim or release opened
// before the wait returned has released its pod exactly once, on a live
// context, before Close runs, and that no goroutine outlives the round.
//
// spec: §10.1.7 (Terminal pipelines at process exit.), §7.1 (Terminal
// pipeline context), §7.2 (Interactive Session Model).
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// shutdownWaitHold returns a random hold of up to max, under a lock so
// concurrent callers share one generator.
type shutdownWaitHold struct {
	mu  sync.Mutex
	rng *rand.Rand
}

func (h *shutdownWaitHold) next(maxHold time.Duration) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return time.Duration(h.rng.Int63n(int64(maxHold)))
}

// shutdownWaitSealer holds each seal for a random time.
type shutdownWaitSealer struct{ hold *shutdownWaitHold }

func (s *shutdownWaitSealer) Seal(ctx context.Context, _, _ string) error {
	select {
	case <-time.After(s.hold.next(30 * time.Millisecond)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// shutdownWaitExecutor holds each release for a random time and counts the
// releases that ran on a live context and those that ran on a cancelled
// one, per session.
type shutdownWaitExecutor struct {
	hold *shutdownWaitHold

	mu        sync.Mutex
	live      map[string]int
	cancelled map[string]int
	closed    bool
	late      []string
}

func (e *shutdownWaitExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, fmt.Errorf("not used")
}

func (e *shutdownWaitExecutor) Close(ctx context.Context, sessionID string) error {
	return e.Release(ctx, sessionID, "")
}

func (e *shutdownWaitExecutor) Release(ctx context.Context, sessionID string, _ executor.Disposition) error {
	time.Sleep(e.hold.next(5 * time.Millisecond))
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		e.late = append(e.late, sessionID)
	}
	if ctx.Err() != nil {
		e.cancelled[sessionID]++
		return ctx.Err()
	}
	e.live[sessionID]++
	return nil
}

// markClosed records that the server's lifetime ended, so any release that
// runs afterwards is reported as late.
func (e *shutdownWaitExecutor) markClosed() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
}

// shutdownWaitFixture is one round's server and its collaborators.
type shutdownWaitFixture struct {
	store    sessionstore.Store
	registry *podsession.Registry
	exec     *shutdownWaitExecutor
	srv      *sessionserver.Server
}

func newShutdownWaitFixture(hold *shutdownWaitHold) *shutdownWaitFixture {
	f := &shutdownWaitFixture{
		store:    memstore.New(),
		registry: podsession.NewRegistry(),
		exec:     &shutdownWaitExecutor{hold: hold, live: map[string]int{}, cancelled: map[string]int{}},
	}
	f.srv = sessionserver.New(f.store, sessionserver.Options{
		Sealer:                     &shutdownWaitSealer{hold: hold},
		Executor:                   f.exec,
		PodRegistry:                f.registry,
		WorkspaceSealMaxDuration:   10 * time.Second,
		StreamFailureReportTimeout: 5 * time.Second,
	})
	return f
}

// seedTerminal writes a completed row and publishes its binding stamped at
// the row's generation, so a terminal binding release releases it.
func (f *shutdownWaitFixture) seedTerminal(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	ctx := context.Background()
	if err := f.store.Create(ctx, sessionstore.Session{ID: id, TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	row, err := f.store.Update(ctx, "acme", id, func(r *sessionstore.Session) error {
		r.State = session.StateCompleted
		r.CoordinationGeneration = 1
		r.TerminatedAt = time.Now()
		return nil
	})
	if err != nil {
		t.Fatalf("terminal write %s: %v", id, err)
	}
	f.registry.Put(&podsession.BindResult{SessionID: id, TenantID: "acme", SandboxName: "pod-" + id, CoordinationGeneration: 1})
	return row
}

// startHook opens the writer claim synchronously, as every terminal writer
// does before its write, and runs the post-terminal hook in a goroutine.
func (f *shutdownWaitFixture) startHook(row sessionstore.Session, wg *sync.WaitGroup) {
	end := f.srv.BeginTerminal(row.ID)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer end()
		f.srv.OnSessionTerminal(context.Background(), session.StateRunning, row)
	}()
}

// diagnosis: many post-terminal hooks and terminal binding releases run when
// shutdown begins, and more open while the wait runs. The wait returns only
// once each has released its pod, exactly once and on a live context, and
// no release runs after Close. A release missing at the wait's return means
// the wait returned with terminal work open; a release on a cancelled
// context or after Close means Close ran before the wait drained it, so the
// slot-accounting lock wait on the server-lifetime context was cut; a
// double release means the claim record let two releases run for one
// session.
//
// spec: §10.1.7 (Terminal pipelines at process exit.), §7.1 (Terminal
// pipeline context), §7.2 (Interactive Session Model)
func TestShutdownWaitDrainsEveryOpenTerminalPipeline_spec_10_1_7(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	baseline := runtime.NumGoroutine()
	for round := 0; round < 10; round++ {
		shutdownWaitRound(t, round, rand.New(rand.NewSource(rng.Int63())))
	}
	waitGoroutines(t, baseline, 5)
}

func shutdownWaitRound(t *testing.T, round int, rng *rand.Rand) {
	t.Helper()
	const before, during = 24, 16
	hold := &shutdownWaitHold{rng: rand.New(rand.NewSource(rng.Int63()))}
	f := newShutdownWaitFixture(hold)
	defer f.srv.Close()

	ids := make([]string, 0, before+during)
	rows := make([]sessionstore.Session, 0, before+during)
	for i := 0; i < before+during; i++ {
		id := fmt.Sprintf("r%d-s%d", round, i)
		ids = append(ids, id)
		rows = append(rows, f.seedTerminal(t, id))
	}
	var wg sync.WaitGroup
	for _, row := range rows[:before] {
		f.openTerminalWork(row, rng.Intn(2) == 0, &wg)
	}
	// A sentinel claim keeps the record non-empty until every mid-wait
	// opening has happened, so each one opens before the wait can return.
	sentinel := f.srv.BeginTerminal(fmt.Sprintf("r%d-sentinel", round))
	waitDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), f.srv.TerminalPipelineBudget())
		defer cancel()
		waitDone <- f.srv.WaitTerminalWork(ctx)
	}()
	for _, row := range rows[before:] {
		time.Sleep(time.Duration(rng.Intn(3000)) * time.Microsecond)
		f.openTerminalWork(row, rng.Intn(2) == 0, &wg)
	}
	sentinel()

	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("round %d: WaitTerminalWork = %v, want nil", round, err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("round %d: WaitTerminalWork did not return; open entries %d", round, f.srv.OpenTerminalWork())
	}
	assertShutdownWaitReleases(t, round, f, ids)
	f.exec.markClosed()
	f.srv.Close()
	wg.Wait()
	if len(f.exec.late) != 0 {
		t.Errorf("round %d: releases after Close: %v", round, f.exec.late)
	}
}

// openTerminalWork starts either a post-terminal hook or a terminal binding
// release for row. Both mark the terminal claim record before returning.
func (f *shutdownWaitFixture) openTerminalWork(row sessionstore.Session, hook bool, wg *sync.WaitGroup) {
	if hook {
		f.startHook(row, wg)
		return
	}
	f.srv.ReleaseTerminalBinding(row)
}

func assertShutdownWaitReleases(t *testing.T, round int, f *shutdownWaitFixture, ids []string) {
	t.Helper()
	f.exec.mu.Lock()
	defer f.exec.mu.Unlock()
	for _, id := range ids {
		if n := f.exec.live[id]; n != 1 {
			t.Errorf("round %d: session %s released %d times on a live context at the wait's return, want 1", round, id, n)
		}
		if n := f.exec.cancelled[id]; n != 0 {
			t.Errorf("round %d: session %s released %d times on a cancelled context", round, id, n)
		}
	}
}
