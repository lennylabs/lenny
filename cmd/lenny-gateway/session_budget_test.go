// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// TestBudgetTerminatorExpiresRunningSession_spec_7_1 proves F-11.2.21: an
// over-budget session is force-transitioned to the §7.1 `expired`
// terminal with the §8.8 expired:budget reason and the terminal pipeline
// runs.
func TestBudgetTerminatorExpiresRunningSession_spec_7_1(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	if err := store.Create(ctx, sessionstore.Session{
		ID: "s_1", TenantID: "acme", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	var terminalSess sessionstore.Session
	var terminalFromState session.State
	term := &budgetSessionTerminator{
		store: store,
		onTerminal: func(_ context.Context, fromState session.State, s sessionstore.Session) {
			terminalSess = s
			terminalFromState = fromState
		},
	}
	term.terminate(ctx, "s_1", sessionbudget.ReasonBudgetExhausted)

	got, err := store.GetByID(ctx, "s_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != session.StateExpired {
		t.Fatalf("state = %q, want expired", got.State)
	}
	if got.FailureReason != sessionbudget.ReasonBudgetExhausted {
		t.Fatalf("failure reason = %q, want %q", got.FailureReason, sessionbudget.ReasonBudgetExhausted)
	}
	if terminalSess.ID != "s_1" {
		t.Fatalf("onTerminal not invoked with the expired session, got %q", terminalSess.ID)
	}
	// spec: §4.6 — a budget terminate runs against a running session, so the
	// pre-terminal state forwarded is running; the terminal pod-release path
	// must keep it on the §6.2 executor recycle path, not the by-name reclaim.
	if terminalFromState != session.StateRunning {
		t.Fatalf("onTerminal fromState = %q, want running", terminalFromState)
	}
}

// An already-terminal session is a no-op: no state change, no terminal
// pipeline re-run (idempotent against a concurrent natural transition).
func TestBudgetTerminatorIdempotentOnTerminal_spec_11_2(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	if err := store.Create(ctx, sessionstore.Session{
		ID: "s_1", TenantID: "acme", State: session.StateCompleted,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	onTerminalCalls := 0
	term := &budgetSessionTerminator{
		store:      store,
		onTerminal: func(context.Context, session.State, sessionstore.Session) { onTerminalCalls++ },
	}
	term.terminate(ctx, "s_1", sessionbudget.ReasonBudgetExhausted)

	got, _ := store.GetByID(ctx, "s_1")
	if got.State != session.StateCompleted {
		t.Fatalf("a terminal session must not be re-transitioned, got %q", got.State)
	}
	if onTerminalCalls != 0 {
		t.Fatalf("onTerminal must not fire for an already-terminal session, got %d", onTerminalCalls)
	}
}

// An unknown session id is a no-op (best-effort): no panic, no terminal
// pipeline.
func TestBudgetTerminatorUnknownSession_spec_11_2(t *testing.T) {
	ctx := context.Background()
	called := false
	term := &budgetSessionTerminator{
		store:      memstore.New(),
		onTerminal: func(context.Context, session.State, sessionstore.Session) { called = true },
	}
	term.terminate(ctx, "missing", sessionbudget.ReasonBudgetExhausted)
	if called {
		t.Fatalf("onTerminal must not fire for an unknown session")
	}
}

// gatedLookupStore blocks GetByID until gate is closed, so a test observes
// TerminateSession's state before its goroutine reaches the lookup.
type gatedLookupStore struct {
	sessionstore.Store
	gate chan struct{}
}

func (g *gatedLookupStore) GetByID(ctx context.Context, id string) (sessionstore.Session, error) {
	<-g.gate
	return g.Store.GetByID(ctx, id)
}

// claimEvents records, in order, the terminal claim's open and end and the
// steps of the terminal pipeline a test observes.
type claimEvents struct {
	mu     sync.Mutex
	events []string
}

func (c *claimEvents) add(e string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *claimEvents) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...)
}

// recordingClaim returns a beginTerminal that records "open" and, when the
// returned end runs, "end", and a channel closed by that end.
func recordingClaim(ev *claimEvents) (func(string) func(), <-chan struct{}) {
	ended := make(chan struct{})
	return func(string) func() {
		ev.add("open")
		var once sync.Once
		return func() {
			once.Do(func() {
				ev.add("end")
				close(ended)
			})
		}
	}, ended
}

// deadlineSealer records the deadline of the context its Seal receives.
type deadlineSealer struct {
	ev       *claimEvents
	mu       sync.Mutex
	deadline time.Time
	hasDL    bool
}

func (d *deadlineSealer) Seal(ctx context.Context, _, _ string) error {
	d.mu.Lock()
	d.deadline, d.hasDL = ctx.Deadline()
	d.mu.Unlock()
	d.ev.add("seal")
	return nil
}

// TestBudgetTerminatorOpensTheClaimBeforeItsGoroutine_spec_11_2 asserts
// that the §11.2 budget termination opens its terminal claim synchronously:
// the claim is open when TerminateSession returns, before its goroutine
// reaches the session lookup, so a shutdown that races the goroutine sees
// it. The claim stays open through the terminal pipeline and closes after
// it. Against a terminator that opens the claim inside its goroutine, the
// claim is absent while the lookup is blocked.
//
// spec: 11.2 (Budgets and Quotas), 7.1 (Normal Flow), 7.1 (Terminal pipeline
// context)
func TestBudgetTerminatorOpensTheClaimBeforeItsGoroutine_spec_11_2(t *testing.T) {
	ctx := context.Background()
	inner := memstore.New()
	if err := inner.Create(ctx, sessionstore.Session{ID: "s_1", TenantID: "acme", State: session.StateRunning}); err != nil {
		t.Fatalf("create: %v", err)
	}
	store := &gatedLookupStore{Store: inner, gate: make(chan struct{})}
	ev := &claimEvents{}
	begin, ended := recordingClaim(ev)
	term := &budgetSessionTerminator{
		store:         store,
		beginTerminal: begin,
		onTerminal: func(context.Context, session.State, sessionstore.Session) {
			ev.add("hook")
		},
	}
	term.TerminateSession("s_1", sessionbudget.ReasonBudgetExhausted)
	if got := ev.list(); len(got) != 1 || got[0] != "open" {
		t.Fatalf("events when TerminateSession returned = %v, want [open]: the claim must open before the goroutine runs", got)
	}
	close(store.gate)
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the terminal claim did not close after the terminal pipeline")
	}
	if got := ev.list(); len(got) != 3 || got[1] != "hook" || got[2] != "end" {
		t.Fatalf("events = %v, want [open hook end]", got)
	}
}

// TestBudgetTerminatorClosesTheClaimOnEveryExit_spec_11_2 asserts that the
// claim TerminateSession opens closes when the goroutine ends without a
// terminal write: an unknown session and an already-terminal session.
//
// spec: 11.2 (Budgets and Quotas), 7.2 (Interactive Session Model)
func TestBudgetTerminatorClosesTheClaimOnEveryExit_spec_11_2(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed *sessionstore.Session
	}{
		{"unknown session", nil},
		{"already terminal", &sessionstore.Session{ID: "s_1", TenantID: "acme", State: session.StateCompleted}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			if tc.seed != nil {
				if err := store.Create(context.Background(), *tc.seed); err != nil {
					t.Fatalf("create: %v", err)
				}
			}
			ev := &claimEvents{}
			begin, ended := recordingClaim(ev)
			term := &budgetSessionTerminator{
				store:         store,
				beginTerminal: begin,
				onTerminal: func(context.Context, session.State, sessionstore.Session) {
					ev.add("hook")
				},
			}
			term.TerminateSession("s_1", sessionbudget.ReasonBudgetExhausted)
			select {
			case <-ended:
			case <-time.After(5 * time.Second):
				t.Fatal("the terminal claim did not close")
			}
			if got := ev.list(); len(got) != 2 || got[0] != "open" || got[1] != "end" {
				t.Fatalf("events = %v, want [open end] with no hook", got)
			}
		})
	}
}

// TestBudgetTerminatorHookOutlastsTheTerminateTimeout_spec_7_1 asserts that
// the terminal pipeline a budget termination runs is bounded by the §7.1
// terminal pipeline budget rather than by budgetTerminateTimeout: over a
// real session server, the seal observes a deadline later than
// budgetTerminateTimeout from the hook entry, so a seal that outlasts the
// terminate deadline is not cut by it. The claim closes after the seal.
//
// spec: 11.2 (Budgets and Quotas), 7.1 (Normal Flow), 7.1 (Terminal pipeline
// context)
func TestBudgetTerminatorHookOutlastsTheTerminateTimeout_spec_7_1(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	if err := store.Create(ctx, sessionstore.Session{ID: "s_1", TenantID: "acme", State: session.StateRunning}); err != nil {
		t.Fatalf("create: %v", err)
	}
	ev := &claimEvents{}
	sealer := &deadlineSealer{ev: ev}
	srv := sessionserver.New(store, sessionserver.Options{
		Sealer:                     sealer,
		PodRegistry:                sealBoundTestRegistry("s_1"),
		WorkspaceSealMaxDuration:   300 * time.Second,
		StreamFailureReportTimeout: 30 * time.Second,
	})
	defer srv.Close()
	begin, ended := recordingClaim(ev)
	term := &budgetSessionTerminator{store: store, beginTerminal: begin, onTerminal: srv.OnSessionTerminal}
	entered := time.Now()
	term.TerminateSession("s_1", sessionbudget.ReasonBudgetExhausted)
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the terminal claim did not close")
	}
	// spec: §7.1 (Terminal pipeline context) — OnSessionTerminal runs the
	// pipeline detached under the session server's own claim, which
	// outlives the terminator's; wait for the hook's claim.
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := srv.WaitTerminalWork(wctx); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}
	sealer.mu.Lock()
	deadline, hasDL := sealer.deadline, sealer.hasDL
	sealer.mu.Unlock()
	if !hasDL {
		t.Fatal("the seal context carries no deadline; the terminal pipeline must be bounded")
	}
	if !deadline.After(entered.Add(budgetTerminateTimeout)) {
		t.Errorf("seal deadline %v after entry, want later than budgetTerminateTimeout (%v): the hook must be re-bounded by the terminal pipeline budget",
			deadline.Sub(entered), budgetTerminateTimeout)
	}
	if limit := entered.Add(srv.TerminalPipelineBudget() + time.Second); deadline.After(limit) {
		t.Errorf("seal deadline %v after entry exceeds the terminal pipeline budget %v", deadline.Sub(entered), srv.TerminalPipelineBudget())
	}
	// The terminator's claim ends when OnSessionTerminal returns, which
	// races the detached seal, so only the set is fixed.
	if got := ev.list(); len(got) != 3 || got[0] != "open" || !slices.Contains(got, "seal") || !slices.Contains(got, "end") {
		t.Errorf("events = %v, want open followed by seal and end in either order", got)
	}
}

// sealBoundTestRegistry returns a pod registry holding a binding for each
// session, so the post-terminal hook's seal step has a binding to seal. The
// binding's generation is above any row these tests write, so the hook's
// stale-binding step leaves it in place. spec: §7.1 (Seal-and-export
// invariant).
func sealBoundTestRegistry(sessionIDs ...string) *podsession.Registry {
	reg := podsession.NewRegistry()
	for _, id := range sessionIDs {
		reg.Put(&podsession.BindResult{SessionID: id, TenantID: "acme", CoordinationGeneration: 1 << 30})
	}
	return reg
}
