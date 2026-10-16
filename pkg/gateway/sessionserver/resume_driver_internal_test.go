// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
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

// serviceModeResumeServer builds a server whose binder resolves the session's
// runtime to a service-mode pool, so a restore claims no pod, and seeds a
// resume_pending row at coordination generation 4.
func serviceModeResumeServer(t *testing.T, store sessionstore.Store, inner sessionstore.Store, inc func(pool, outcome string)) (*Server, sessionstore.Session) {
	t.Helper()
	const ns = "lenny-agents"
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-pool", Namespace: ns},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "svc-tmpl", MinWarm: 1, MaxWarm: 5},
	}
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-tmpl", Namespace: ns},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "svc-runtime",
			IsolationProfile: string(isolation.ProfileSandboxed),
			ExecutionMode:    string(runtimestore.ExecutionModeService),
		},
	}
	c := fake.NewClientBuilder().WithScheme(claimAtCreateScheme(t)).WithObjects(pool, tmpl).Build()
	if err := inner.Create(context.Background(), sessionstore.Session{
		ID: "svc", TenantID: "acme", RuntimeRef: "svc-runtime", IsolationProfile: isolation.ProfileSandboxed,
		State: session.StateResumePending, CoordinationGeneration: 4, Pool: "svc-pool", PoolRef: "svc-pool",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	row, err := inner.Get(context.Background(), "acme", "svc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	srv := New(store, Options{
		PodBinder:               &podsession.Binder{Client: c, Namespace: ns},
		AgentNamespace:          ns,
		IncSessionResumeAttempt: inc,
	})
	t.Cleanup(srv.Close)
	return srv, row
}

// TestServiceModeRestoreRunsEntryHookBeforeReturning_spec_7_3 asserts that a
// restore of a service-mode session, which claims no pod, still runs the
// resume driver's post-claim hook, and runs it before the restore returns:
// when the restore hands control back to the driver, the row already reads
// resuming at the attempt's generation G. The resuming entry write is
// therefore the hook's write on every path, and no restore work runs with
// the row at resume_pending. A hook error is returned to the driver as the
// restore's error, so a superseded entry write is not followed by a commit.
//
// spec: §7.3 (Resume driver), §5.2 (Pool Configuration and Execution Modes)
func TestServiceModeRestoreRunsEntryHookBeforeReturning_spec_7_3(t *testing.T) {
	store := memstore.New()
	srv, row := serviceModeResumeServer(t, store, store, nil)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	bound := newAttemptBound(srv.afterFuncOrDefault(), cancel)
	defer bound.stop()
	entry := srv.newEntryHook(row, bound)
	calls := 0
	hook := func(ctx context.Context) error {
		calls++
		return entry.run(ctx)
	}

	_, err := srv.releaseThenResumeOnPod(context.Background(), row, resumeBind{onClaimed: hook, generation: 5})
	if err != nil {
		t.Fatalf("service-mode restore: %v", err)
	}
	if calls != 1 {
		t.Fatalf("post-claim hook calls = %d, want 1 (a claimless restore runs the hook before it returns)", calls)
	}
	got, _ := store.Get(context.Background(), "acme", "svc")
	if got.State != session.StateResuming || got.CoordinationGeneration != 5 {
		t.Errorf("row after the restore returned = %s at %d, want resuming at 5", got.State, got.CoordinationGeneration)
	}

	// A second restore of the same row finds it resuming, so the hook's
	// entry write is superseded and the restore returns that error.
	stale := srv.newEntryHook(row, bound)
	_, err = srv.releaseThenResumeOnPod(context.Background(), row, resumeBind{onClaimed: stale.run, generation: 5})
	if !errors.Is(err, errResumeSuperseded) {
		t.Errorf("restore with a superseded entry write = %v, want errResumeSuperseded", err)
	}
}

// supersedingStore moves the row's coordination generation once, immediately
// before the first Update that would write the resuming entry, as a
// concurrent writer that landed between the driver's read and its entry
// write does.
type supersedingStore struct {
	sessionstore.Store
	once sync.Once
}

func (s *supersedingStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	cur, err := s.Get(ctx, tenantID, id)
	if err == nil {
		probe := cur
		if mut(&probe) == nil && probe.State == session.StateResuming && cur.State == session.StateResumePending {
			s.once.Do(func() {
				_, _ = s.Store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
					r.CoordinationGeneration++
					return nil
				})
			})
		}
	}
	return s.Store.Update(ctx, tenantID, id, mut)
}

// TestServiceModeResumeAttemptExits_spec_7_3 drives one resume attempt for a
// service-mode session through the driver. When the entry write applies, the
// row is committed to running at G and the attempt counts exactly one
// success. When a concurrent writer moved the row's generation first, the
// entry write is superseded, nothing is committed, and no resume-attempts
// count is recorded.
//
// spec: §7.3 (Resume driver), §5.2 (Pool Configuration and Execution Modes),
// §16.1 (Metrics)
func TestServiceModeResumeAttemptExits_spec_7_3(t *testing.T) {
	for _, tc := range []struct {
		name      string
		supersede bool
		wantState session.State
		wantGen   int64
		wantCount []string
	}{
		{"entry write applies", false, session.StateRunning, 5, []string{"svc-pool:success"}},
		{"entry write superseded", true, session.StateResumePending, 5, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := memstore.New()
			var store sessionstore.Store = inner
			if tc.supersede {
				store = &supersedingStore{Store: inner}
			}
			var counts []string
			srv, row := serviceModeResumeServer(t, store, inner, func(pool, outcome string) {
				counts = append(counts, pool+":"+outcome)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			srv.attemptResume(ctx, cancel, row)

			got, _ := inner.Get(context.Background(), "acme", "svc")
			if got.State != tc.wantState || got.CoordinationGeneration != tc.wantGen {
				t.Errorf("row = %s at %d, want %s at %d", got.State, got.CoordinationGeneration, tc.wantState, tc.wantGen)
			}
			if tc.supersede && got.RecoveryGeneration != row.RecoveryGeneration {
				t.Errorf("recovery generation = %d, want %d (nothing committed)", got.RecoveryGeneration, row.RecoveryGeneration)
			}
			if len(counts) != len(tc.wantCount) || (len(counts) == 1 && counts[0] != tc.wantCount[0]) {
				t.Errorf("resume attempts recorded %v, want %v", counts, tc.wantCount)
			}
		})
	}
}
