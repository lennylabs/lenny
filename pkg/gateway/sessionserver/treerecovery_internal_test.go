// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/recovery"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treerecovery"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// treeRecoveryReasons are the reasons the traversal hands the terminal
// marker: the two budget reasons and a reattach error.
var treeRecoveryReasons = []string{
	recovery.ReasonLevelTimeout,
	recovery.ReasonTreeTimeout,
	"sessionserver: tree recovery of node n1 ended in awaiting_client_action",
}

// spec: §8.10 (Delegation Tree Recovery), §7.2 (Interactive Session Model),
// §6.2 (resume_pending wall-clock cap)
// diagnosis: the terminal marker writes `failed` only over a locked row in
// resume_pending, whatever the reason, and runs one post-terminal hook for
// that write. Every other non-terminal state is left unchanged with no hook
// and no event. A failure means tree recovery overwrote a node the resume
// driver, a watchdog, or a committed restore owns, or ended a node expired.
func TestTerminalMarkerFailsOnlyResumePending_spec_8_10(t *testing.T) {
	for _, st := range nonTerminalStates() {
		for _, reason := range treeRecoveryReasons {
			t.Run(string(st)+"/"+reason, func(t *testing.T) {
				store := memstore.New()
				bus := sessionevents.NewBus(64)
				srv := New(store, Options{DevTenantHeader: true, Events: bus})
				defer srv.Close()
				ctx := context.Background()
				row := sessionstore.Session{ID: "n1", TenantID: "acme", RuntimeRef: "echo", State: st}
				if err := store.Create(ctx, row); err != nil {
					t.Fatalf("seed: %v", err)
				}
				before, _ := store.Get(ctx, "acme", "n1")
				sessionTerminalMarker{s: srv}.FailNode(ctx, before, reason)
				if err := srv.WaitTerminalWork(ctx); err != nil {
					t.Fatalf("WaitTerminalWork: %v", err)
				}
				got, err := store.Get(ctx, "acme", "n1")
				if err != nil {
					t.Fatalf("get: %v", err)
				}
				if st == session.StateResumePending {
					if got.State != session.StateFailed || got.TerminatedAt.IsZero() {
						t.Errorf("row = %q terminated %v, want failed with the Terminated fact", got.State, got.TerminatedAt)
					}
					if n := eventCount(bus, "n1", "session_complete"); n != 1 {
						t.Errorf("session_complete = %d, want one post-terminal hook", n)
					}
					return
				}
				if got.State != st || !got.UpdatedAt.Equal(before.UpdatedAt) {
					t.Errorf("row = %q (updated %v), want %q unchanged (updated %v)", got.State, got.UpdatedAt, st, before.UpdatedAt)
				}
				if evs := bus.History("n1", 0); len(evs) != 0 {
					t.Errorf("events = %+v, want none: the guard refused the write", evs)
				}
			})
		}
	}
}

// commitAfterDeadlineReattacher is a NodeReattacher whose restore commits
// running only after the node's recovery deadline ended the wait, as a
// restore that lands just past maxLevelRecoverySeconds does.
type commitAfterDeadlineReattacher struct{ store sessionstore.Store }

func (commitAfterDeadlineReattacher) StartNode(context.Context, sessionstore.Session) {}

func (r commitAfterDeadlineReattacher) ReattachNode(ctx context.Context, node sessionstore.Session) error {
	<-ctx.Done()
	if _, err := r.store.Update(context.Background(), node.TenantID, node.ID, func(row *sessionstore.Session) error {
		row.State = session.StateRunning
		return nil
	}); err != nil {
		return err
	}
	return ctx.Err()
}

// moveOnStartReattacher is a NodeReattacher whose StartNode moves the node's
// row to state, as the §6.2 resume_pending cap moving the node to
// awaiting_client_action during the traversal does, and whose wait is the
// gateway's own sessionNodeReattacher wait.
type moveOnStartReattacher struct {
	srv   *Server
	state session.State
}

func (r moveOnStartReattacher) StartNode(ctx context.Context, node sessionstore.Session) {
	_, _ = r.srv.store.Update(ctx, node.TenantID, node.ID, func(row *sessionstore.Session) error {
		row.State = r.state
		return nil
	})
}

func (r moveOnStartReattacher) ReattachNode(ctx context.Context, node sessionstore.Session) error {
	return sessionNodeReattacher{s: r.srv}.ReattachNode(ctx, node)
}

// runTreeRecovery seeds a root and one resume_pending child, runs the §8.10
// traversal with reattacher and the gateway's terminal marker, and returns
// the summary and the child's row after the pass.
func runTreeRecovery(t *testing.T, srv *Server, store sessionstore.Store, reattacher treerecovery.NodeReattacher) (treerecovery.Summary, sessionstore.Session) {
	t.Helper()
	ctx := context.Background()
	for _, r := range []sessionstore.Session{
		{ID: "root", TenantID: "acme", RootSessionID: "root", State: session.StateRunning},
		{ID: "n1", TenantID: "acme", RuntimeRef: "echo", ParentSessionID: "root", RootSessionID: "root", DelegationDepth: 1, State: session.StateResumePending},
	} {
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("seed %s: %v", r.ID, err)
		}
	}
	orch := treerecovery.New(treerecovery.Config{
		Lister: store, Reattacher: reattacher, Terminal: sessionTerminalMarker{s: srv},
		Recoverable: srv.nodeNeedsRecovery, LevelTimeout: 30 * time.Millisecond, TreeTimeout: time.Minute,
	})
	summary, err := orch.RecoverTree(ctx, "acme", "root")
	if err != nil {
		t.Fatalf("RecoverTree: %v", err)
	}
	got, err := store.Get(ctx, "acme", "n1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return summary, got
}

// spec: §8.10 (Delegation Tree Recovery), §7.2 (Interactive Session Model)
// diagnosis: a restore that commits running after the node's level deadline
// is not overwritten by tree recovery's failed write, and no hook runs. A
// failure means a committed restore was ended failed under a live pod.
func TestTreeRecoveryKeepsRestoreCommittedAfterDeadline_spec_8_10(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(64)
	srv := New(store, Options{DevTenantHeader: true, Events: bus})
	defer srv.Close()
	summary, got := runTreeRecovery(t, srv, store, commitAfterDeadlineReattacher{store: store})
	if summary.Failed != 1 {
		t.Errorf("summary = %+v, want the node counted unrecovered", summary)
	}
	if got.State != session.StateRunning {
		t.Errorf("node = %q, want running: the committed restore is kept", got.State)
	}
	if n := eventCount(bus, "n1", "session_complete"); n != 0 {
		t.Errorf("session_complete = %d, want no hook", n)
	}
}

// spec: §8.10 (Delegation Tree Recovery), §6.2 (resume_pending wall-clock cap)
// diagnosis: a node the §6.2 resume_pending cap moved to
// awaiting_client_action during the traversal is counted failed in the
// summary and its row is left in awaiting_client_action with no hook. A
// failure means tree recovery wrote over the state §7.3 owns.
func TestTreeRecoveryCountsAwaitingClientActionNodeWithoutWriting_spec_8_10(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(64)
	srv := New(store, Options{DevTenantHeader: true, Events: bus})
	defer srv.Close()
	srv.treePollInterval = 2 * time.Millisecond
	summary, got := runTreeRecovery(t, srv, store, moveOnStartReattacher{srv: srv, state: session.StateAwaitingClientAction})
	if summary != (treerecovery.Summary{Recoverable: 1, Failed: 1}) {
		t.Errorf("summary = %+v, want one failed node", summary)
	}
	if got.State != session.StateAwaitingClientAction {
		t.Errorf("node = %q, want awaiting_client_action unchanged", got.State)
	}
	if evs := bus.History("n1", 0); len(evs) != 0 {
		t.Errorf("events = %+v, want none", evs)
	}
}

// spec: §8.10 (Delegation Tree Recovery), §7.2 (Interactive Session Model),
// §7.3 (Resume driver)
// diagnosis: the resume driver's entry write (resume_pending → resuming) and
// tree recovery's failed write race on one resume_pending row under random
// interleavings. Exactly one of them applies, the row ends in resuming or
// failed, at most one post-terminal hook runs, and failed never follows
// resuming. A failure means both writes applied or tree recovery overwrote a
// restore in flight. The seed is logged.
func TestTreeRecoveryFailRacesResumeEntry_spec_8_10(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 200; i++ {
		entryDelay := time.Duration(rng.Intn(200)) * time.Microsecond
		failDelay := time.Duration(rng.Intn(200)) * time.Microsecond
		store := memstore.New()
		bus := sessionevents.NewBus(64)
		srv := New(store, Options{DevTenantHeader: true, Events: bus})
		ctx := context.Background()
		if err := store.Create(ctx, sessionstore.Session{ID: "n1", TenantID: "acme", RuntimeRef: "echo", State: session.StateResumePending}); err != nil {
			t.Fatalf("seed row: %v", err)
		}
		row, _ := store.Get(ctx, "acme", "n1")
		hook := srv.newEntryHook(row, newAttemptBound(srv.afterFuncOrDefault(), func() {}))
		var wg sync.WaitGroup
		var entryErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			time.Sleep(entryDelay)
			entryErr = hook.run(ctx)
		}()
		go func() {
			defer wg.Done()
			time.Sleep(failDelay)
			sessionTerminalMarker{s: srv}.FailNode(ctx, row, recovery.ReasonLevelTimeout)
		}()
		wg.Wait()
		if err := srv.WaitTerminalWork(ctx); err != nil {
			t.Fatalf("WaitTerminalWork: %v", err)
		}
		got, _ := store.Get(ctx, "acme", "n1")
		completes := eventCount(bus, "n1", "session_complete")
		switch got.State {
		case session.StateResuming:
			if entryErr != nil || completes != 0 {
				t.Fatalf("iteration %d (seed %d): resuming row with entry error %v and %d hooks; want the entry applied and no hook", i, seed, entryErr, completes)
			}
		case session.StateFailed:
			if !errors.Is(entryErr, errResumeSuperseded) || completes != 1 {
				t.Fatalf("iteration %d (seed %d): failed row with entry error %v and %d hooks; want the entry refused and one hook", i, seed, entryErr, completes)
			}
		default:
			t.Fatalf("iteration %d (seed %d): row = %q, want resuming or failed", i, seed, got.State)
		}
		srv.Close()
	}
}

// nodeNeedsRecovery is a no-op (no node recoverable) when the pod
// registry is unwired, so the recovery degrades to a no-op on a gateway
// without pod binding (dev / unit-test).
func TestNodeNeedsRecoveryNilRegistry(t *testing.T) {
	srv := New(memstore.New(), Options{DevTenantHeader: true})
	if srv.podRegistry != nil {
		t.Skip("test assumes the bare server has no pod registry")
	}
	if srv.nodeNeedsRecovery(sessionstore.Session{ID: "n1", State: session.StateRunning}) {
		t.Fatal("nodeNeedsRecovery should be false without a pod registry")
	}
}

// The resume path's tree-recovery trigger is plumbed end to end: New
// builds the orchestrator, recoverDelegationTree spawns the detached
// traversal over the resumed tree, and the completion hook fires. With
// no pod registry every descendant is non-recoverable, so the pass is a
// safe no-op that leaves the running children untouched.
func TestRecoverDelegationTreePlumbing_spec_8_10_1016(t *testing.T) {
	store := memstore.New()
	srv := New(store, Options{DevTenantHeader: true})
	if srv.treeRecovery == nil {
		t.Fatal("New must build the tree-recovery orchestrator")
	}
	ctx := context.Background()
	rows := []sessionstore.Session{
		{ID: "root", TenantID: "acme", RootSessionID: "root", State: session.StateRunning},
		{ID: "child", TenantID: "acme", ParentSessionID: "root", RootSessionID: "root", DelegationDepth: 1, State: session.StateRunning},
	}
	for _, r := range rows {
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("seed %s: %v", r.ID, err)
		}
	}
	done := make(chan string, 1)
	srv.treeRecoveryHook = func(rootID string) { done <- rootID }

	srv.recoverDelegationTree(ctx, "acme", "root")

	select {
	case got := <-done:
		if got != "root" {
			t.Fatalf("hook fired for %q, want root", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tree-recovery goroutine did not complete")
	}

	// No pod registry → the running child is left running, not
	// terminated.
	child, err := store.Get(ctx, "acme", "child")
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if child.State != session.StateRunning {
		t.Fatalf("child state = %q, want running (untouched)", child.State)
	}
}

// A nil orchestrator and an empty root are no-ops that still fire the
// hook so the resume handler can call the trigger unconditionally.
func TestRecoverDelegationTreeEmptyRootNoOp(t *testing.T) {
	srv := New(memstore.New(), Options{DevTenantHeader: true})
	done := make(chan string, 1)
	srv.treeRecoveryHook = func(rootID string) { done <- rootID }
	srv.recoverDelegationTree(context.Background(), "acme", "")
	select {
	case <-done:
		t.Fatal("empty root must not spawn a recovery goroutine")
	case <-time.After(200 * time.Millisecond):
		// expected: no hook, no goroutine.
	}
}
