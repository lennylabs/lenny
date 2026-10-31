// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// markerExecutor counts releases and returns err from each.
type markerExecutor struct {
	err      error
	releases atomic.Int32
}

func (e *markerExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (e *markerExecutor) Close(ctx context.Context, sessionID string) error {
	return e.Release(ctx, sessionID, "")
}

func (e *markerExecutor) Release(context.Context, string, executor.Disposition) error {
	e.releases.Add(1)
	return e.err
}

// markerStore counts the Updates that set the release marker on a row that
// had none, and fails every Update with failErr when it is set.
type markerStore struct {
	sessionstore.Store
	failErr error
	marks   atomic.Int32
}

func (m *markerStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if m.failErr != nil {
		return sessionstore.Session{}, m.failErr
	}
	var before time.Time
	out, err := m.Store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
		before = r.PodReleasedAt
		return mutate(r)
	})
	if err == nil && before.IsZero() && !out.PodReleasedAt.IsZero() {
		m.marks.Add(1)
	}
	return out, err
}

// markerServer returns a Server over a markerStore seeded with a terminal
// row "s" naming pod-a at coordination generation sealBoundGeneration, with
// reg as its pod registry and exec as its executor.
func markerServer(t *testing.T, reg *podsession.Registry, exec executor.Executor) (*Server, *markerStore) {
	t.Helper()
	store := &markerStore{Store: memstore.New()}
	if err := store.Create(context.Background(), sessionstore.Session{
		UserID: testOwner,
		ID:     "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateCompleted, PodAssignment: "pod-a",
		TerminatedAt: time.Now(), CoordinationGeneration: sealBoundGeneration,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	opts := Options{DevTenantHeader: true, Executor: exec}
	if reg != nil {
		opts.PodRegistry = reg
	}
	srv := New(store, opts)
	t.Cleanup(srv.Close)
	return srv, store
}

// markerRow reads row "s".
func markerRow(t *testing.T, srv *Server) sessionstore.Session {
	t.Helper()
	row, err := srv.store.Get(context.Background(), "acme", "s")
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	return row
}

// TestHookReleaseRecordsTheRelease_spec_7_1 pins the post-terminal hook's
// executor release site: with a binding held it records the release once,
// both when the release succeeds and when it returns an error; with no
// binding held, at shutdown, and when the store write fails it leaves the
// row unreleased.
//
// spec: §7.1 (Post-terminal window)
func TestHookReleaseRecordsTheRelease_spec_7_1(t *testing.T) {
	for _, tc := range []struct {
		name       string
		bound      bool
		releaseErr error
		closed     bool
		storeErr   error
		want       bool
	}{
		{name: "binding held, release succeeds", bound: true, want: true},
		{name: "binding held, release fails", bound: true, releaseErr: errors.New("adapter unreachable"), want: true},
		{name: "no binding held", bound: false, want: false},
		{name: "shutdown skip", bound: true, closed: true, want: false},
		{name: "store write fails", bound: true, storeErr: errors.New("primary unreachable"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := podsession.NewRegistry()
			if tc.bound {
				reg = sealBoundRegistry("s")
			}
			exec := &markerExecutor{err: tc.releaseErr}
			srv, store := markerServer(t, reg, exec)
			row := markerRow(t, srv)
			if tc.closed {
				// The shutdown skip is the lock wait that ends because the
				// server is shutting down: another holder has the lock.
				unlock, err := srv.slotAccountLocks.Lock(context.Background(), "s")
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
				srv.Close()
			}
			store.failErr = tc.storeErr
			srv.releaseAfterTerminal(context.Background(), session.StateRunning, row, executor.DispositionCompleted)
			store.failErr = nil
			if got := !markerRow(t, srv).PodReleasedAt.IsZero(); got != tc.want {
				t.Errorf("release recorded = %v, want %v", got, tc.want)
			}
			if tc.want && store.marks.Load() != 1 {
				t.Errorf("marker writes = %d, want 1", store.marks.Load())
			}
		})
	}
}

// TestTerminalBindingReleaseRecordsTheRelease_spec_7_1 pins the terminal
// binding release site, which serves the Sweeper, the resume tail, the
// Checkpointer's gone-row release, and the full_revoke release: a current
// binding released, or whose release returned an error, records the release
// once; a stale binding evicted with no binder call, no binding, and a
// slot-lock wait error record nothing.
//
// spec: §7.1 (Post-terminal window), §10.1.5 (Stale Replica Behavior)
func TestTerminalBindingReleaseRecordsTheRelease_spec_7_1(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reg        func() *podsession.Registry
		releaseErr error
		lockHeld   bool
		want       bool
	}{
		{name: "current binding released", reg: func() *podsession.Registry { return sealBoundRegistry("s") }, want: true},
		{name: "current binding release fails", reg: func() *podsession.Registry { return sealBoundRegistry("s") }, releaseErr: errors.New("adapter unreachable"), want: true},
		{name: "stale binding evicted", reg: func() *podsession.Registry {
			r := podsession.NewRegistry()
			r.Put(&podsession.BindResult{SessionID: "s", TenantID: "acme", CoordinationGeneration: sealBoundGeneration - 1})
			return r
		}, want: false},
		{name: "no binding", reg: podsession.NewRegistry, want: false},
		{name: "slot-lock wait error", reg: func() *podsession.Registry { return sealBoundRegistry("s") }, lockHeld: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := &markerExecutor{err: tc.releaseErr}
			srv, store := markerServer(t, tc.reg(), exec)
			ctx := context.Background()
			if tc.lockHeld {
				unlock, err := srv.slotAccountLocks.Lock(ctx, "s")
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
				waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
				ctx = waitCtx
			}
			_, _ = srv.ReleaseRevokedBinding(ctx, "acme", "s")
			if got := !markerRow(t, srv).PodReleasedAt.IsZero(); got != tc.want {
				t.Errorf("release recorded = %v, want %v", got, tc.want)
			}
			if tc.want && store.marks.Load() != 1 {
				t.Errorf("marker writes = %d, want 1", store.marks.Load())
			}
		})
	}
}

// TestPreRunningReclaimRecordsTheRelease_spec_7_1 pins the pre-running
// reclaim site: the reclaim of a session that never started records the
// release, both when the claim delete succeeds and when it fails.
//
// spec: §7.1 (Post-terminal window), §4.6.3 (CRD Field Ownership and Write Boundaries)
func TestPreRunningReclaimRecordsTheRelease_spec_7_1(t *testing.T) {
	for _, failDelete := range []bool{false, true} {
		name := "claim delete succeeds"
		if failDelete {
			name = "claim delete fails"
		}
		t.Run(name, func(t *testing.T) {
			srv, store := markerServer(t, podsession.NewRegistry(), &markerExecutor{})
			scheme := claimAtCreateScheme(t)
			funcs := interceptor.Funcs{}
			if failDelete {
				funcs.Delete = func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
					return errors.New("apiserver unreachable")
				}
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				&lennyv1.SandboxClaim{ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName("pod-a"), Namespace: "lenny-agents"}},
			).WithInterceptorFuncs(funcs).Build()
			srv.podBinder = &podsession.Binder{Client: c, Namespace: "lenny-agents", Credentials: &reclaimRecordingAssigner{}}
			if !srv.terminalReclaimPreRunning(context.Background(), session.StateCreated, markerRow(t, srv)) {
				t.Fatal("the pre-running reclaim did not run")
			}
			if markerRow(t, srv).PodReleasedAt.IsZero() || store.marks.Load() != 1 {
				t.Errorf("release recorded %v with %d marker writes, want recorded once", markerRow(t, srv).PodReleasedAt, store.marks.Load())
			}
		})
	}
}

// TestReleaseMarkerUnderConcurrentReleases_spec_7_1 runs the hook release,
// the Sweeper's terminal binding release, the full_revoke binding release,
// and a concurrent rehydration seed in randomized interleavings, and asserts
// one marker write and no lost marker.
//
// spec: §7.1 (Post-terminal window), §5.2 (Post-recovery rehydration atomicity)
func TestReleaseMarkerUnderConcurrentReleases_spec_7_1(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for round := 0; round < 50; round++ {
		exec := &markerExecutor{}
		srv, store := markerServer(t, sealBoundRegistry("s"), exec)
		row := markerRow(t, srv)
		order := rng.Perm(4)
		var wg sync.WaitGroup
		for _, step := range order {
			wg.Add(1)
			go func(step int) {
				defer wg.Done()
				switch step {
				case 0:
					srv.releaseAfterTerminal(context.Background(), session.StateRunning, row, executor.DispositionCompleted)
				case 1:
					srv.ReleaseTerminalBinding(row)
				case 2:
					_, _ = srv.ReleaseRevokedBinding(context.Background(), "acme", "s")
				case 3:
					_, _ = store.GetActiveSlotsByPod(context.Background(), "pod-a")
				}
			}(step)
		}
		wg.Wait()
		deadline := time.Now().Add(2 * time.Second)
		for srv.terminalClaims.size() != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if markerRow(t, srv).PodReleasedAt.IsZero() {
			t.Fatalf("round %d (order %v): the release marker was lost", round, order)
		}
		if n := store.marks.Load(); n != 1 {
			t.Fatalf("round %d (order %v): marker writes = %d, want 1", round, order, n)
		}
	}
}
