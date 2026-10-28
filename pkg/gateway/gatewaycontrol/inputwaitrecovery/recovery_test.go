// SPDX-License-Identifier: MIT

package inputwaitrecovery_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/inputwaitrecovery"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
	"github.com/lennylabs/lenny/tests/testinfra/inputwaitrig"
)

const (
	sessA  = "00000000-0000-4000-8000-00000000000a"
	sessA2 = "00000000-0000-4000-8000-0000000000a2"
	sessB  = "00000000-0000-4000-8000-00000000000b"
)

// hookStore wraps a session store with injectable faults on Update and a
// hook after GetActiveSlotsByPod.
type hookStore struct {
	sessionstore.Store
	mu sync.Mutex
	// failUpdate, when set, decides per call whether Update fails before
	// committing (true, false) or after committing (false, true).
	failUpdate func(id string) (before, after bool)
	// afterList runs after each GetActiveSlotsByPod listing.
	afterList func()
}

var errInjected = errors.New("injected store failure")

func (h *hookStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	h.mu.Lock()
	fail := h.failUpdate
	h.mu.Unlock()
	if fail != nil {
		before, after := fail(id)
		if before {
			return sessionstore.Session{}, errInjected
		}
		if after {
			if _, err := h.Store.Update(ctx, tenantID, id, mutate); err != nil {
				return sessionstore.Session{}, err
			}
			return sessionstore.Session{}, errInjected
		}
	}
	return h.Store.Update(ctx, tenantID, id, mutate)
}

func (h *hookStore) GetActiveSlotsByPod(ctx context.Context, podID string) ([]string, error) {
	ids, err := h.Store.GetActiveSlotsByPod(ctx, podID)
	h.mu.Lock()
	hook := h.afterList
	h.mu.Unlock()
	if hook != nil {
		hook()
	}
	return ids, err
}

func (h *hookStore) setFail(f func(id string) (before, after bool)) {
	h.mu.Lock()
	h.failUpdate = f
	h.mu.Unlock()
}

func newStore(t *testing.T) *hookStore {
	t.Helper()
	return &hookStore{Store: memstore.New()}
}

// seed creates a session of pod in st naming waitID.
func seed(t *testing.T, store sessionstore.Store, id, pod string, st session.State, waitID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "echo",
		State: st, PodAssignment: pod, InputWaitID: waitID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func rowOf(t *testing.T, store sessionstore.Store, id string) sessionstore.Session {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row
}

// stale rewrites id to input_required naming a wait no registry holds.
func stale(t *testing.T, store sessionstore.Store, id, waitID string) {
	t.Helper()
	if _, err := store.Update(context.Background(), "acme", id, func(s *sessionstore.Session) error {
		s.State = session.StateInputRequired
		s.InputWaitID = waitID
		return nil
	}); err != nil {
		t.Fatalf("stale %s: %v", id, err)
	}
}

func start(t *testing.T, store sessionstore.Store) *inputwaitrig.Server {
	t.Helper()
	return inputwaitrig.Start(t, inputwaitrig.Options{Store: store, PKI: gatewaypki.Mint(t)})
}

func ping(t *testing.T, c adapterv1.GatewayControlClient, sessionID string) {
	t.Helper()
	if err := inputwaitrig.Ping(context.Background(), c, sessionID); err != nil {
		t.Fatalf("ping %s: %v", sessionID, err)
	}
}

func wantRow(t *testing.T, store sessionstore.Store, id string, st session.State, waitID string) {
	t.Helper()
	row := rowOf(t, store, id)
	if row.State != st || (waitID != "*" && row.InputWaitID != waitID) {
		t.Errorf("%s = %s/%q, want %s/%q", id, row.State, row.InputWaitID, st, waitID)
	}
}

// TestRecoveryRestoresStaleRowOnFirstCall_spec_28_3 pins the recovery a
// fresh connection runs: the first call carrying a session ID restores an
// input_required row whose named wait is not pending here to running with one
// status_change(running), and a later call on the recovered connection runs
// no recovery.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func TestRecoveryRestoresStaleRowOnFirstCall_spec_28_3(t *testing.T) {
	store := newStore(t)
	seed(t, store, sessA, "pod-a", session.StateInputRequired, "iw_gone")
	srv := start(t, store)
	c := srv.Dial(t, inputwaitrig.AgentURI("pod-a"))
	ping(t, c, sessA)
	row := rowOf(t, store, sessA)
	if row.State != session.StateRunning || row.LastAgentActivityAt.IsZero() {
		t.Errorf("row = %s, LastAgentActivityAt %v; want running with the idle clock restarted", row.State, row.LastAgentActivityAt)
	}
	if sc := srv.StatusChanges(sessA); len(sc) != 1 || sc[0] != "running" {
		t.Errorf("status_change = %v, want one running", sc)
	}
	stale(t, store, sessA, "iw_gone2")
	ping(t, c, sessA)
	wantRow(t, store, sessA, session.StateInputRequired, "iw_gone2")
}

// TestRecoveryRepointsToPendingPersistedWait_spec_28_3 pins that the recovery
// re-points a row whose named wait is gone to a persisted wait of the session
// pending here, with no status_change.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func TestRecoveryRepointsToPendingPersistedWait_spec_28_3(t *testing.T) {
	store := newStore(t)
	seed(t, store, sessA, "pod-a", session.StateInputRequired, "iw_gone")
	srv := start(t, store)
	if _, err := srv.Waits.RegisterPersisted(sessA, "req-live", "iw_live", nil); err != nil {
		t.Fatal(err)
	}
	ping(t, srv.Dial(t, inputwaitrig.AgentURI("pod-a")), sessA)
	wantRow(t, store, sessA, session.StateInputRequired, "iw_live")
	if sc := srv.StatusChanges(sessA); len(sc) != 0 {
		t.Errorf("status_change = %v, want none", sc)
	}
}

// TestRecoverySupersededRows_spec_28_3 pins that the recovery leaves a row
// that is not input_required, and a row that names a wait pending here,
// unchanged; and that it covers every listed session of the pod.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func TestRecoverySupersededRows_spec_28_3(t *testing.T) {
	store := newStore(t)
	seed(t, store, sessA, "pod-a", session.StateRunning, "iw_old")
	seed(t, store, sessA2, "pod-a", session.StateInputRequired, "iw_live")
	srv := start(t, store)
	if _, err := srv.Waits.RegisterPersisted(sessA2, "req-live", "iw_live", nil); err != nil {
		t.Fatal(err)
	}
	ping(t, srv.Dial(t, inputwaitrig.AgentURI("pod-a")), sessA)
	wantRow(t, store, sessA, session.StateRunning, "iw_old")
	wantRow(t, store, sessA2, session.StateInputRequired, "iw_live")
	if sc := append(srv.StatusChanges(sessA), srv.StatusChanges(sessA2)...); len(sc) != 0 {
		t.Errorf("status_change = %v, want none", sc)
	}
}

// TestRecoveryNeedsASessionScopedCall_spec_28_3 pins that a call whose
// request carries no session ID runs no recovery and does not mark the
// connection recovered, so the connection's next session-scoped call runs it.
//
// spec: §28.3 (Link register)
func TestRecoveryNeedsASessionScopedCall_spec_28_3(t *testing.T) {
	store := newStore(t)
	seed(t, store, sessA, "pod-a", session.StateInputRequired, "iw_gone")
	srv := start(t, store)
	c := srv.Dial(t, inputwaitrig.AgentURI("pod-a"))
	_, _ = c.ReportPodScrub(context.Background(), &adapterv1.ReportPodScrubRequest{PodId: "pod-a"})
	wantRow(t, store, sessA, session.StateInputRequired, "iw_gone")
	ping(t, c, sessA)
	wantRow(t, store, sessA, session.StateRunning, "*")
}

// TestFailedExitWriteRearmsEveryConnection_spec_28_3 covers the failed exit
// write. Before commit, the row keeps input_required naming a wait no
// registry holds, and every connection on the replica, including one already
// recovered, runs the recovery on its next call and restores running; with a
// second persisted wait still registered, the recovery re-points the row to
// it. After commit, the committed exit write stands and the recovery that
// the re-arm triggers is superseded. A superseded exit write re-arms
// nothing.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func TestFailedExitWriteRearmsEveryConnection_spec_28_3(t *testing.T) {
	for _, tc := range []struct {
		name        string
		after       bool
		secondWait  bool
		wantState   session.State
		wantRepoint bool
	}{
		{name: "before commit", wantState: session.StateRunning},
		{name: "before commit with a second wait", secondWait: true, wantState: session.StateInputRequired, wantRepoint: true},
		{name: "after commit", after: true, wantState: session.StateRunning},
		{name: "after commit with a second wait", after: true, secondWait: true, wantState: session.StateInputRequired, wantRepoint: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore(t)
			seed(t, store, sessA, "pod-a", session.StateRunning, "")
			seed(t, store, sessA2, "pod-a", session.StateRunning, "")
			srv := start(t, store)
			c := srv.Dial(t, inputwaitrig.AgentURI("pod-a"))
			other := srv.Dial(t, inputwaitrig.AgentURI("pod-a"))
			ping(t, c, sessA)
			ping(t, other, sessA2)
			done := inputwaitrig.RequestInput(context.Background(), c, sessA, "req-1")
			srv.WaitPending(t, sessA, "req-1")
			if tc.secondWait {
				second := inputwaitrig.RequestInput(context.Background(), c, sessA, "req-2")
				srv.WaitPending(t, sessA, "req-2")
				defer func() { srv.Waits.Cancel(sessA, "req-2"); inputwaitrig.Await(t, second) }()
				// The later wait's entry write named req-2's wait; point the row
				// back at req-1's so req-1's exit write is the one that applies.
				var first string
				for _, p := range srv.Waits.PendingDetailsForSession(sessA) {
					if p.RequestID == "req-1" {
						first = p.WaitID
					}
				}
				if _, err := store.Store.Update(context.Background(), "acme", sessA, func(s *sessionstore.Session) error { s.InputWaitID = first; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			store.setFail(func(id string) (bool, bool) {
				store.setFail(nil)
				return !tc.after, tc.after
			})
			_ = srv.Waits.Resolve(sessA, "req-1", "yes")
			inputwaitrig.Await(t, done)
			if !tc.after && !tc.secondWait {
				wantRow(t, store, sessA, session.StateInputRequired, "*")
			}
			// The other, already recovered connection runs the recovery again.
			ping(t, other, sessA2)
			row := rowOf(t, store, sessA)
			if row.State != tc.wantState {
				t.Errorf("state after the re-armed recovery = %s, want %s", row.State, tc.wantState)
			}
			if tc.wantRepoint {
				pend := srv.Waits.PendingDetailsForSession(sessA)
				if len(pend) != 1 || row.InputWaitID != pend[0].WaitID {
					t.Errorf("row names %q, pending %+v; want the remaining wait", row.InputWaitID, pend)
				}
			}
		})
	}
	t.Run("superseded exit re-arms nothing", func(t *testing.T) {
		store := newStore(t)
		seed(t, store, sessA, "pod-a", session.StateRunning, "")
		srv := start(t, store)
		c := srv.Dial(t, inputwaitrig.AgentURI("pod-a"))
		ping(t, c, sessA)
		done := inputwaitrig.RequestInput(context.Background(), c, sessA, "req-1")
		srv.WaitPending(t, sessA, "req-1")
		if _, err := store.Update(context.Background(), "acme", sessA, func(s *sessionstore.Session) error { s.InputWaitID = "iw_other"; return nil }); err != nil {
			t.Fatal(err)
		}
		_ = srv.Waits.Resolve(sessA, "req-1", "yes")
		inputwaitrig.Await(t, done)
		ping(t, c, sessA)
		wantRow(t, store, sessA, session.StateInputRequired, "iw_other")
	})
}

// TestRecoveryOnAnotherReplicaAfterProcessExit_spec_28_3 pins the lost wait:
// the waiting replica dies without draining, and the pod's first call to a
// second replica over the same store, with a fresh registry, restores the row.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func TestRecoveryOnAnotherReplicaAfterProcessExit_spec_28_3(t *testing.T) {
	store := newStore(t)
	seed(t, store, sessA, "pod-a", session.StateRunning, "")
	first := start(t, store)
	c := first.Dial(t, inputwaitrig.AgentURI("pod-a"))
	ping(t, c, sessA)
	_ = inputwaitrig.RequestInput(context.Background(), c, sessA, "req-1")
	first.WaitPending(t, sessA, "req-1")
	wantRow(t, store, sessA, session.StateInputRequired, "*")
	first.Kill()
	second := start(t, store)
	ping(t, second.Dial(t, inputwaitrig.AgentURI("pod-a")), sessA)
	wantRow(t, store, sessA, session.StateRunning, "*")
	if sc := second.StatusChanges(sessA); len(sc) != 1 || sc[0] != "running" {
		t.Errorf("status_change on the second replica = %v, want one running", sc)
	}
}

// TestRecoveryKeepsAWaitPendingHere_spec_28_3 pins that the recovery on a new
// connection leaves a row naming a wait still pending on this replica from
// the pod's earlier connection; that wait's own exit write restores running.
// In the concurrent variant a wait registers and applies its entry write on
// the same connection while the run is held between its listing and its
// write, and the run leaves the row naming that wait.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func TestRecoveryKeepsAWaitPendingHere_spec_28_3(t *testing.T) {
	t.Run("earlier connection", func(t *testing.T) {
		store := newStore(t)
		seed(t, store, sessA, "pod-a", session.StateRunning, "")
		srv := start(t, store)
		old := srv.Dial(t, inputwaitrig.AgentURI("pod-a"))
		ping(t, old, sessA)
		done := inputwaitrig.RequestInput(context.Background(), old, sessA, "req-1")
		srv.WaitPending(t, sessA, "req-1")
		ping(t, srv.Dial(t, inputwaitrig.AgentURI("pod-a")), sessA)
		wantRow(t, store, sessA, session.StateInputRequired, "*")
		_ = srv.Waits.Resolve(sessA, "req-1", "yes")
		inputwaitrig.Await(t, done)
		wantRow(t, store, sessA, session.StateRunning, "*")
	})
	t.Run("concurrent entry", func(t *testing.T) {
		store := newStore(t)
		seed(t, store, sessA, "pod-a", session.StateRunning, "")
		srv := start(t, store)
		c := srv.Dial(t, inputwaitrig.AgentURI("pod-a"))
		release := make(chan struct{})
		var held atomic.Bool
		var done <-chan inputwaitrig.Result
		store.afterList = func() {
			// Only the first run is held; the run the concurrent call's own
			// dispatch triggers passes through.
			if !held.CompareAndSwap(false, true) {
				return
			}
			func() {
				done = inputwaitrig.RequestInput(context.Background(), c, sessA, "req-1")
				go func() {
					srv.WaitPending(t, sessA, "req-1")
					for rowOf(t, store, sessA).State != session.StateInputRequired {
						time.Sleep(time.Millisecond)
					}
					close(release)
				}()
				<-release
			}()
		}
		ping(t, c, sessA)
		wantRow(t, store, sessA, session.StateInputRequired, "*")
		srv.Waits.Cancel(sessA, "req-1")
		inputwaitrig.Await(t, done)
		wantRow(t, store, sessA, session.StateRunning, "*")
	})
}

// TestIncompleteRecoveryRunsAgain_spec_28_3 pins that a recovery whose write
// fails, or whose call names a session of another pod, leaves the connection
// not recovered, so the next session-scoped call runs the recovery again.
//
// spec: §7.2 (Session state machine), §28.3 (Link register), §5.2 (Tenant
// pinning)
func TestIncompleteRecoveryRunsAgain_spec_28_3(t *testing.T) {
	t.Run("write fails", func(t *testing.T) {
		store := newStore(t)
		seed(t, store, sessA, "pod-a", session.StateInputRequired, "iw_gone")
		srv := start(t, store)
		c := srv.Dial(t, inputwaitrig.AgentURI("pod-a"))
		store.setFail(func(string) (bool, bool) { store.setFail(nil); return true, false })
		ping(t, c, sessA)
		wantRow(t, store, sessA, session.StateInputRequired, "iw_gone")
		ping(t, c, sessA)
		wantRow(t, store, sessA, session.StateRunning, "*")
	})
	t.Run("foreign pod", func(t *testing.T) {
		store := newStore(t)
		seed(t, store, sessA, "pod-a", session.StateInputRequired, "iw_gone")
		seed(t, store, sessB, "pod-b", session.StateInputRequired, "iw_gone_b")
		srv := start(t, store)
		c := srv.Dial(t, inputwaitrig.AgentURI("pod-b"))
		ping(t, c, sessA)
		wantRow(t, store, sessA, session.StateInputRequired, "iw_gone")
		wantRow(t, store, sessB, session.StateInputRequired, "iw_gone_b")
		ping(t, c, sessB)
		wantRow(t, store, sessB, session.StateRunning, "*")
		wantRow(t, store, sessA, session.StateInputRequired, "iw_gone")
	})
}

// TestCloseEndsWaitsAndRestoresRows_spec_28_3 pins the drain: Close ends a
// blocked wait, its exit write restores running, and a later registration on
// the replica is refused.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func TestCloseEndsWaitsAndRestoresRows_spec_28_3(t *testing.T) {
	store := newStore(t)
	seed(t, store, sessA, "pod-a", session.StateRunning, "")
	srv := start(t, store)
	c := srv.Dial(t, inputwaitrig.AgentURI("pod-a"))
	done := inputwaitrig.RequestInput(context.Background(), c, sessA, "req-1")
	srv.WaitPending(t, sessA, "req-1")
	srv.Waits.Close()
	if res := inputwaitrig.Await(t, done); res.Err != nil || !res.Resp.GetIsError() {
		t.Errorf("closed wait = %+v, want a cancellation tool error", res)
	}
	wantRow(t, store, sessA, session.StateRunning, "*")
	if _, err := srv.Waits.RegisterPersisted(sessA, "req-2", "iw_2", nil); !errors.Is(err, inputwait.ErrClosed) {
		t.Errorf("registration after Close = %v, want ErrClosed", err)
	}
}

// TestStaleRowWithNoLaterCallStays_spec_28_3 pins the accepted residue: a
// row left input_required by a lost wait stays input_required while no later
// call of its pod runs the recovery.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func TestStaleRowWithNoLaterCallStays_spec_28_3(t *testing.T) {
	store := newStore(t)
	seed(t, store, sessA, "pod-a", session.StateRunning, "")
	srv := start(t, store)
	c := srv.Dial(t, inputwaitrig.AgentURI("pod-a"))
	_ = inputwaitrig.RequestInput(context.Background(), c, sessA, "req-1")
	srv.WaitPending(t, sessA, "req-1")
	srv.Kill()
	start(t, store)
	wantRow(t, store, sessA, session.StateInputRequired, "*")
}

// TestNewContextRecordIsNotRecovered_spec_28_3 pins the record's generation
// arithmetic: a fresh record is not recovered, Rearm un-recovers every
// record, and FromContext reports the record a context carries.
//
// spec: §28.3 (Link register)
func TestNewContextRecordIsNotRecovered_spec_28_3(t *testing.T) {
	gen := inputwaitrecovery.NewGeneration()
	ctx := inputwaitrecovery.NewContext(context.Background(), gen)
	rec, ok := inputwaitrecovery.FromContext(ctx)
	if !ok || rec.Recovered() || rec.Generation() != gen {
		t.Fatalf("record = %+v ok %v, want a fresh record on gen", rec, ok)
	}
	if _, ok := inputwaitrecovery.FromContext(context.Background()); ok {
		t.Error("a bare context carries a record")
	}
	before := gen.Load()
	gen.Rearm()
	if gen.Load() != before+1 {
		t.Errorf("Rearm moved %d to %d", before, gen.Load())
	}
}
