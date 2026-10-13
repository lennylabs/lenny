// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// updateFault is one scripted outcome for the next Update a faultStore runs.
type updateFault int

const (
	faultNone updateFault = iota
	// faultApplyThenError applies the mutation and then returns an error,
	// as a commit whose acknowledgement was lost does.
	faultApplyThenError
	// faultErrorNoApply returns an error without applying the mutation.
	faultErrorNoApply
	// faultNotFound returns ErrNotFound without applying the mutation.
	faultNotFound
)

var errInjectedCommit = errors.New("injected store error")

// faultStore runs each Update through the next scripted fault and records how
// many Updates it ran.
type faultStore struct {
	sessionstore.Store
	mu      sync.Mutex
	plan    []updateFault
	updates int
}

func (f *faultStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	f.mu.Lock()
	f.updates++
	fault := faultNone
	if len(f.plan) > 0 {
		fault, f.plan = f.plan[0], f.plan[1:]
	}
	f.mu.Unlock()
	switch fault {
	case faultApplyThenError:
		if _, err := f.Store.Update(ctx, tenantID, id, mut); err != nil {
			return sessionstore.Session{}, err
		}
		return sessionstore.Session{}, errInjectedCommit
	case faultErrorNoApply:
		var probe sessionstore.Session
		if _, err := f.Store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
			probe = *r
			_ = mut(&probe)
			return errInjectedCommit
		}); !errors.Is(err, errInjectedCommit) {
			return sessionstore.Session{}, err
		}
		return sessionstore.Session{}, errInjectedCommit
	case faultNotFound:
		return sessionstore.Session{}, sessionstore.ErrNotFound
	default:
		return f.Store.Update(ctx, tenantID, id, mut)
	}
}

func eventCount(bus *sessionevents.Bus, id, typ string) int {
	n := 0
	for _, e := range bus.History(id, 0) {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// TestClassifyCommitCoversEveryRow_spec_7_3 asserts that classifyCommit
// returns the outcome of every row of the commit classification table, for the
// pod-binder and the binderless baselines.
//
// spec: §7.3 (Resume driver)
func TestClassifyCommitCoversEveryRow_spec_7_3(t *testing.T) {
	binder := commitBaseline{gen: 7, sandbox: "sbx-new", recoveryGen: 2}
	binderless := commitBaseline{recoveryGen: 2, binderless: true}
	row := func(st session.State, pod string, gen, rec int64) sessionstore.Session {
		return sessionstore.Session{State: st, PodAssignment: pod, CoordinationGeneration: gen, RecoveryGeneration: rec}
	}
	for _, tc := range []struct {
		name string
		obs  commitObservation
		base commitBaseline
		want commitOutcome
	}{
		{"applied", commitObservation{landed: true}, binder, commitLanded},
		{"not found", commitObservation{notFound: true, storeErr: true}, binder, commitNotLanded},
		{"guard miss without a store error", commitObservation{row: row(session.StateRunning, "sbx-new", 7, 3)}, binder, commitNotLanded},
		{"miss after an error: our running row at G", commitObservation{storeErr: true, row: row(session.StateRunning, "sbx-new", 7, 3)}, binder, commitLanded},
		{"miss after an error: our terminal row at G", commitObservation{storeErr: true, row: row(session.StateCancelled, "sbx-new", 7, 3)}, binder, commitLanded},
		{"miss after an error: our suspended row at G", commitObservation{storeErr: true, row: row(session.StateSuspended, "sbx-new", 7, 3)}, binder, commitLanded},
		{"miss after an error: our running row above G", commitObservation{storeErr: true, row: row(session.StateRunning, "sbx-new", 8, 3)}, binder, commitAboveG},
		{"miss after an error: our terminal row above G", commitObservation{storeErr: true, row: row(session.StateCancelled, "sbx-new", 8, 3)}, binder, commitAboveG},
		{"miss after an error: another pod", commitObservation{storeErr: true, row: row(session.StateRunning, "sbx-other", 7, 3)}, binder, commitNotLanded},
		{"miss after an error: recovery generation not advanced", commitObservation{storeErr: true, row: row(session.StateRunning, "sbx-new", 7, 2)}, binder, commitNotLanded},
		{"miss after an error: still resuming", commitObservation{storeErr: true, row: row(session.StateResuming, "sbx-new", 7, 3)}, binder, commitNotLanded},
		{"miss after an error: resume_pending", commitObservation{storeErr: true, row: row(session.StateResumePending, "sbx-new", 8, 3)}, binder, commitNotLanded},
		{"binderless applied", commitObservation{landed: true}, binderless, commitLanded},
		{"binderless miss without a store error", commitObservation{row: row(session.StateRunning, "", 9, 3)}, binderless, commitNotLanded},
		{"binderless miss after an error, advanced", commitObservation{storeErr: true, row: row(session.StateRunning, "", 9, 3)}, binderless, commitLanded},
		{"binderless miss after an error, coordination advanced only", commitObservation{storeErr: true, row: row(session.StateRunning, "", 9, 2)}, binderless, commitNotLanded},
		{"binderless not found", commitObservation{notFound: true, storeErr: true}, binderless, commitNotLanded},
	} {
		if got := classifyCommit(tc.obs, tc.base); got != tc.want {
			t.Errorf("%s: classifyCommit = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestBinderlessCommitIsClassifiedOnce_spec_7_3 asserts that an ambiguous
// commit is classified once on the binderless path: a commit that applies and
// then errors advances RecoveryGeneration exactly once and runs the Landed
// column once; one that errors without applying commits on the repeat;
// ErrNotFound ends the repeat after one try and runs Not landed; and a guard
// miss with no store error on a row another commit already moved runs Not
// landed.
//
// spec: §7.3 (Resume driver)
func TestBinderlessCommitIsClassifiedOnce_spec_7_3(t *testing.T) {
	for _, tc := range []struct {
		name        string
		plan        []updateFault
		premove     bool
		wantState   session.State
		wantRecGen  int64
		wantResumed int
		wantUpdates int
		wantExit    resumeExit
	}{
		{"applies then errors", []updateFault{faultApplyThenError}, false, session.StateRunning, 1, 1, 2, resumeExitCommitLanded},
		{"errors without applying", []updateFault{faultErrorNoApply}, false, session.StateRunning, 1, 1, 2, resumeExitCommitLanded},
		{"not found", []updateFault{faultNotFound}, false, session.StateResumePending, 0, 0, 1, resumeExitCommitNotLanded},
		{"another commit moved the row", nil, true, session.StateRunning, 1, 0, 1, resumeExitCommitNotLanded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := memstore.New()
			if err := inner.Create(context.Background(), sessionstore.Session{
				ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateResumePending,
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			listed, _ := inner.Get(context.Background(), "acme", "s")
			if tc.premove {
				if _, err := inner.Update(context.Background(), "acme", "s", func(r *sessionstore.Session) error {
					transitionResume(r)
					r.RecoveryGeneration++
					return nil
				}); err != nil {
					t.Fatalf("premove: %v", err)
				}
			}
			store := &faultStore{Store: inner, plan: tc.plan}
			bus := sessionevents.NewBus(64)
			srv := New(store, Options{Events: bus})
			defer srv.Close()
			exit := srv.completeResume(listed, 0, resumeOutcome{})
			got, _ := inner.Get(context.Background(), "acme", "s")
			if exit != tc.wantExit || got.State != tc.wantState || got.RecoveryGeneration != tc.wantRecGen {
				t.Errorf("exit %v row %q rec %d, want %v %q rec %d", exit, got.State, got.RecoveryGeneration, tc.wantExit, tc.wantState, tc.wantRecGen)
			}
			if n := eventCount(bus, "s", "session.resumed"); n != tc.wantResumed {
				t.Errorf("session.resumed events = %d, want %d", n, tc.wantResumed)
			}
			if store.updates != tc.wantUpdates {
				t.Errorf("commit Updates = %d, want %d", store.updates, tc.wantUpdates)
			}
			if srv.deliveryLocks.size() != 0 {
				t.Errorf("delivery locks held = %d after the commit, want 0", srv.deliveryLocks.size())
			}
		})
	}
}

// TestDriveResumeRefusals_spec_7_3 asserts that DriveResume does nothing on a
// replica that does not hold the session's coordination lease or cannot read
// it, starts no second attempt for a session already in flight on the replica,
// and starts no restore for a row whose active age exceeds its cap, leaving
// the row and the attempt record unchanged in each case.
//
// spec: §7.3 (Resume driver)
func TestDriveResumeRefusals_spec_7_3(t *testing.T) {
	seed := func(t *testing.T, st sessionstore.Store, id string) sessionstore.Session {
		t.Helper()
		if err := st.Create(context.Background(), sessionstore.Session{
			ID: id, TenantID: "acme", RuntimeRef: "echo", State: session.StateResumePending,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		row, _ := st.Get(context.Background(), "acme", id)
		return row
	}
	unchanged := func(t *testing.T, st sessionstore.Store, before sessionstore.Session) {
		t.Helper()
		got, _ := st.Get(context.Background(), "acme", before.ID)
		if got.State != before.State || got.CoordinationGeneration != before.CoordinationGeneration ||
			!got.UpdatedAt.Equal(before.UpdatedAt) {
			t.Errorf("row = %q gen %d updated %v, want unchanged %q gen %d updated %v",
				got.State, got.CoordinationGeneration, got.UpdatedAt, before.State, before.CoordinationGeneration, before.UpdatedAt)
		}
	}

	t.Run("lease held by a peer", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		row := seed(t, f.store, "s-peer")
		f.leases.holders[leaseK("acme", "s-peer")] = "rep-2"
		f.srv.DriveResume(context.Background(), row)
		if f.srv.resumeAttempts.size() != 0 {
			t.Error("an attempt started on a replica without the lease")
		}
		unchanged(t, f.store, row)
	})
	t.Run("lease read error", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		row := seed(t, f.store, "s-nolease")
		// No lease entry: the read returns ErrNotFound.
		f.srv.DriveResume(context.Background(), row)
		if f.srv.resumeAttempts.size() != 0 {
			t.Error("an attempt started after a failed lease read")
		}
		unchanged(t, f.store, row)
	})
	t.Run("attempt already in flight", func(t *testing.T) {
		store := memstore.New()
		srv := New(store, Options{})
		defer srv.Close()
		row := seed(t, store, "s-busy")
		cancelled := false
		if !srv.resumeAttempts.begin("s-busy", func() { cancelled = true }) {
			t.Fatal("begin refused the first attempt")
		}
		srv.DriveResume(context.Background(), row)
		time.Sleep(20 * time.Millisecond)
		unchanged(t, store, row)
		if cancelled {
			t.Error("the refused second attempt cancelled the attempt in flight")
		}
		srv.resumeAttempts.end("s-busy")
	})
	t.Run("active age over the cap", func(t *testing.T) {
		store := memstore.New()
		srv := New(store, Options{AgeCap: func(context.Context, sessionstore.Session) time.Duration { return -time.Second }})
		defer srv.Close()
		row := seed(t, store, "s-old")
		srv.DriveResume(context.Background(), row)
		srv.WaitResumeIdle("s-old")
		unchanged(t, store, row)
	})
}

// TestResumeAttemptRecordIsEmptyAfterEveryExit_spec_7_3 asserts that the
// attempt record, the terminal claim record, and the delivery locks hold no
// entry after an attempt exits by a binderless commit, a refusal, a pre-claim
// failure, a CancelResumeAttempt, and a server-lifetime cancel.
//
// spec: §7.3 (Resume driver), §10.1.1 (Stateless Replicas and Per-Session
// Coordination)
func TestResumeAttemptRecordIsEmptyAfterEveryExit_spec_7_3(t *testing.T) {
	assertEmpty := func(t *testing.T, srv *Server) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for srv.resumeAttempts.size() != 0 && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		if n := srv.resumeAttempts.size(); n != 0 {
			t.Errorf("attempt record = %d entries, want 0", n)
		}
		if n := srv.terminalClaims.size(); n != 0 {
			t.Errorf("terminal claim record = %d entries, want 0", n)
		}
		if n := srv.deliveryLocks.size(); n != 0 {
			t.Errorf("delivery locks = %d entries, want 0", n)
		}
	}
	newRow := func(t *testing.T, st sessionstore.Store, id string) sessionstore.Session {
		t.Helper()
		if err := st.Create(context.Background(), sessionstore.Session{
			ID: id, TenantID: "acme", RuntimeRef: "echo", State: session.StateResumePending,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		row, _ := st.Get(context.Background(), "acme", id)
		return row
	}

	t.Run("binderless commit", func(t *testing.T) {
		store := memstore.New()
		srv := New(store, Options{})
		defer srv.Close()
		srv.DriveResumeAndWait(context.Background(), newRow(t, store, "s"))
		assertEmpty(t, srv)
	})
	t.Run("row left resume_pending", func(t *testing.T) {
		store := memstore.New()
		srv := New(store, Options{})
		defer srv.Close()
		row := newRow(t, store, "s")
		_, _ = store.Update(context.Background(), "acme", "s", func(r *sessionstore.Session) error {
			r.State = session.StateRunning
			return nil
		})
		srv.DriveResumeAndWait(context.Background(), row)
		assertEmpty(t, srv)
	})
	t.Run("pre-claim failure", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{noPool: true})
		row := newRow(t, f.store, "s")
		f.leases.holders[leaseK("acme", "s")] = "rep-1"
		f.srv.DriveResumeAndWait(context.Background(), row)
		assertEmpty(t, f.srv)
		if got := f.state(t, "s"); got.State != session.StateResumePending {
			t.Errorf("state = %q, want resume_pending after a pre-claim failure", got.State)
		}
	})
	t.Run("cancel and lifetime end", func(t *testing.T) {
		for _, how := range []string{"CancelResumeAttempt", "Close"} {
			store := memstore.New()
			srv := New(store, Options{})
			row := newRow(t, store, "s-"+how)
			ctx, cancel := context.WithCancel(srv.lifetime())
			if !srv.resumeAttempts.begin(row.ID, cancel) {
				t.Fatal("begin refused")
			}
			go func() {
				defer srv.resumeAttempts.end(row.ID)
				<-ctx.Done()
			}()
			if how == "CancelResumeAttempt" {
				srv.CancelResumeAttempt(row.ID)
				srv.CancelResumeAttempt(row.ID) // idempotent
			} else {
				srv.Close()
			}
			assertEmpty(t, srv)
			srv.Close()
		}
	})
}

// TestRecordResumeAttemptCountsOnlyEnteredIncarnations_spec_16_1 asserts the
// resume-attempts counter mapping: a landed commit counts one success with
// the row's pool, a reported failure and a commit that did not land count one
// failure, and an uncounted or abandoned exit records nothing; a nil recorder
// records nothing and does not panic.
//
// spec: §16.1 (Metrics), §7.3 (Resume driver)
func TestRecordResumeAttemptCountsOnlyEnteredIncarnations_spec_16_1(t *testing.T) {
	type entry struct{ pool, outcome string }
	var got []entry
	srv := New(memstore.New(), Options{IncSessionResumeAttempt: func(pool, outcome string) {
		got = append(got, entry{pool, outcome})
	}})
	defer srv.Close()
	for _, exit := range []resumeExit{resumeExitUncounted, resumeExitReported, resumeExitCommitLanded, resumeExitCommitNotLanded, resumeExitAbandoned} {
		srv.recordResumeAttempt("pool-a", exit)
	}
	want := []entry{{"pool-a", "failure"}, {"pool-a", "success"}, {"pool-a", "failure"}}
	if len(got) != len(want) {
		t.Fatalf("recorded %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %v, want %v", i, got[i], want[i])
		}
	}
	New(memstore.New(), Options{}).recordResumeAttempt("pool-a", resumeExitCommitLanded)
}
