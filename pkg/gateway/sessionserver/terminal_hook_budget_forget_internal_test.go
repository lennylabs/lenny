// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// forgetRecorder wires a recording budgetForget into a funnel fixture and
// records each forget and each executor release, in order, on one log.
type forgetRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *forgetRecorder) add(e string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *forgetRecorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *forgetRecorder) count(e string) int {
	n := 0
	for _, got := range r.list() {
		if got == e {
			n++
		}
	}
	return n
}

func newForgetFixture(t *testing.T) (*funnelFixture, *forgetRecorder) {
	t.Helper()
	f := newFunnelFixture(t, funnelOpts{})
	rec := &forgetRecorder{}
	f.srv.budgetForget = func(id string) { rec.add("forget:" + id) }
	f.exec.onRelease = func(id string) { rec.add("release:" + id) }
	return f, rec
}

// assertForgetOnceAfterRelease fails unless the log holds exactly one forget
// for id and that forget follows the executor release.
func assertForgetOnceAfterRelease(t *testing.T, rec *forgetRecorder, id string) {
	t.Helper()
	if n := rec.count("forget:" + id); n != 1 {
		t.Fatalf("forget calls for %s = %d (log %v), want 1", id, n, rec.list())
	}
	events := rec.list()
	release, forget := -1, -1
	for i, e := range events {
		switch e {
		case "release:" + id:
			if release < 0 {
				release = i
			}
		case "forget:" + id:
			forget = i
		}
	}
	if release < 0 || forget < release {
		t.Fatalf("log %v: want the forget after the executor release", events)
	}
}

// TestHookPathWriterForgetsBudgetStateOnce_spec_11_2 drives a hook-path
// terminal writer that reaches the post-terminal hook with no signal tail, as
// lenny/cancel_session does. The hook body forgets the session's §11.2 budget
// state exactly once, after the pod release. Before the forget moved into the
// hook body it ran only on the funnel path, so this writer left the
// enforcer, recorder, and anomaly-detector entries behind.
//
// spec: §8.6, §11.2, §7.1 (Terminal pipeline context)
func TestHookPathWriterForgetsBudgetStateOnce_spec_11_2(t *testing.T) {
	f, rec := newForgetFixture(t)
	f.seed(t, "s", session.StateRunning, 0, true)
	from, updated, err := f.srv.writeTerminal(context.Background(), "acme", "s", session.StateCancelled, nonTerminalGuard)
	if err != nil {
		t.Fatalf("writeTerminal: %v", err)
	}
	f.srv.AfterTerminalWrite(context.Background(), from, updated, nil)
	assertForgetOnceAfterRelease(t, rec, "s")
}

// TestFunnelWriterForgetsBudgetStateOnce_spec_11_2 drives the funnel through
// OnSessionTerminal, whose hook carries the terminal-signal tail and whose
// afterTerminalRelease runs after it. The forget runs once, from the hook
// body, after the pod release; afterTerminalRelease adds no second forget.
//
// spec: §8.6, §11.2, §7.1 (Terminal pipeline context)
func TestFunnelWriterForgetsBudgetStateOnce_spec_11_2(t *testing.T) {
	f, rec := newForgetFixture(t)
	f.seed(t, "s", session.StateRunning, 0, true)
	from, updated, err := f.srv.writeTerminal(context.Background(), "acme", "s", session.StateCompleted, nonTerminalGuard)
	if err != nil {
		t.Fatalf("writeTerminal: %v", err)
	}
	f.srv.OnSessionTerminal(context.Background(), from, updated)
	if err := f.srv.WaitTerminalWork(context.Background()); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}
	if err := f.srv.WaitTerminalWork(context.Background()); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}
	assertForgetOnceAfterRelease(t, rec, "s")
}

// TestConcurrentTerminalWritersForgetOnce_spec_11_2 races hook-path terminal
// writers on one session. Only the writer whose terminal write applied runs
// the hook, so the session's budget state is forgotten exactly once.
//
// spec: §8.6, §11.2, §7.1 (Terminal pipeline context)
func TestConcurrentTerminalWritersForgetOnce_spec_11_2(t *testing.T) {
	const writers = 8
	for iter := 0; iter < 20; iter++ {
		f, rec := newForgetFixture(t)
		f.seed(t, "s", session.StateRunning, 0, true)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				from, updated, err := f.srv.writeTerminal(context.Background(), "acme", "s", session.StateCancelled, nonTerminalGuard)
				if err != nil {
					return
				}
				f.srv.AfterTerminalWrite(context.Background(), from, updated, nil)
			}()
		}
		close(start)
		wg.Wait()
		if n := rec.count("forget:s"); n != 1 {
			t.Fatalf("iteration %d: forget calls = %d (log %v), want 1", iter, n, rec.list())
		}
	}
}

// TestRefusedTerminalWriteForgetsNothing_spec_11_2 runs a terminal writer
// whose guard refuses an already-terminal row. A write that does not apply
// starts no hook, so it forgets nothing.
//
// spec: §8.6, §11.2
func TestRefusedTerminalWriteForgetsNothing_spec_11_2(t *testing.T) {
	f, rec := newForgetFixture(t)
	f.seed(t, "s", session.StateCompleted, 0, true)
	if _, _, err := f.srv.writeTerminal(context.Background(), "acme", "s", session.StateCancelled, nonTerminalGuard); err == nil {
		t.Fatal("writeTerminal over a completed row applied, want it refused")
	}
	if n := rec.count("forget:s"); n != 0 {
		t.Fatalf("forget calls = %d, want 0 for a refused write", n)
	}
}
