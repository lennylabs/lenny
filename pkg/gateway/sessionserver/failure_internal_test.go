// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// funnelNS is the agent namespace the failure-funnel fixture resolves pools in.
const funnelNS = "lenny-agents"

// funnelWait bounds every wait in this file so a lock that never orders the
// two paths fails the test rather than hanging it.
const funnelWait = 5 * time.Second

// funnelLog records, in order, the drain stamps the binder issues and the
// slot releases the executor performs, so a test can assert the drain is
// stamped before the release.
type funnelLog struct {
	mu     sync.Mutex
	events []string
}

func (l *funnelLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *funnelLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

// count returns how many recorded events start with prefix.
func (l *funnelLog) count(prefix string) int {
	n := 0
	for _, e := range l.list() {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

// index returns the position of the first event starting with prefix, or -1.
func (l *funnelLog) index(prefix string) int {
	for i, e := range l.list() {
		if strings.HasPrefix(e, prefix) {
			return i
		}
	}
	return -1
}

// funnelExecutor stands in for the pod executor. Like PodExecutor.Release it
// unbinds the session before acting, so a release of a session that is no
// longer bound reaches no binder: it logs a "release:" event only when it
// removed a registry entry, which is the ReleaseSlot the real executor would
// issue. Every call is also counted in calls, so a test can assert the funnel
// made no release call at all. With no registry every call logs a release.
type funnelExecutor struct {
	log      *funnelLog
	registry *podsession.Registry
	// onRelease, when set, runs at the start of every Release call, so a
	// test can observe the funnel's state at the point it releases.
	onRelease func(sessionID string)

	mu    sync.Mutex
	calls []executor.Disposition
}

func (f *funnelExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (f *funnelExecutor) Close(ctx context.Context, sessionID string) error {
	return f.Release(ctx, sessionID, "")
}

func (f *funnelExecutor) Release(_ context.Context, sessionID string, d executor.Disposition) error {
	if f.onRelease != nil {
		f.onRelease(sessionID)
	}
	f.mu.Lock()
	f.calls = append(f.calls, d)
	f.mu.Unlock()
	if f.registry != nil {
		if _, ok := f.registry.Remove(sessionID); !ok {
			return nil
		}
	}
	f.log.add("release:" + string(d))
	return nil
}

func (f *funnelExecutor) callList() []executor.Disposition {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]executor.Disposition(nil), f.calls...)
}

// hookStore runs after, when set, once an Update commits, with the committed
// row. A test uses it to act between the funnel's commit and its release.
type hookStore struct {
	sessionstore.Store
	mu    sync.Mutex
	after func(sessionstore.Session)
}

func (h *hookStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	out, err := h.Store.Update(ctx, tenantID, id, mutate)
	if err == nil {
		h.mu.Lock()
		fn := h.after
		h.mu.Unlock()
		if fn != nil {
			fn(out)
		}
	}
	return out, err
}

func (h *hookStore) setAfter(fn func(sessionstore.Session)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.after = fn
}

// funnelOpts selects the collaborators a funnel fixture leaves out.
type funnelOpts struct {
	noRegistry bool
	noBinder   bool
	// noPool leaves the cluster without the warm pool and template, so the
	// funnel's pool resolution fails.
	noPool bool
}

// funnelFixture is a Server wired with the collaborators the failure funnel
// reads: a session store with a commit hook, a pod registry, a recording
// executor, a binder whose drain stamps are recorded, a pool of
// maxConcurrentSessions 4 (unhealthy threshold 2), and a lease store in which
// this replica, rep-1, holds every session's lease.
type funnelFixture struct {
	srv          *Server
	store        *hookStore
	registry     *podsession.Registry
	exec         *funnelExecutor
	leases       *fakeLeaseStore
	log          *funnelLog
	clientCalls  *funnelLog
	mu           sync.Mutex
	slotFailures []string
	replacements int
	retries      int
}

func newFunnelFixture(t *testing.T, o funnelOpts) *funnelFixture {
	t.Helper()
	f := &funnelFixture{
		store:       &hookStore{Store: memstore.New()},
		log:         &funnelLog{},
		clientCalls: &funnelLog{},
		leases:      newFakeLeaseStore(),
	}
	opts := Options{
		CoordinationLeaseStore: f.leases,
		ReplicaID:              "rep-1",
		AgentNamespace:         funnelNS,
		RetryPolicyCaps:        session.RetryPolicyCaps{MaxRetries: 2},
		SlotReplacement: func(string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.replacements++
		},
		IncSessionRetry: func(string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.retries++
		},
	}
	if !o.noRegistry {
		f.registry = podsession.NewRegistry()
		opts.PodRegistry = f.registry
	}
	f.exec = &funnelExecutor{log: f.log, registry: f.registry}
	opts.Executor = f.exec
	if !o.noBinder {
		opts.PodBinder = f.binder(t, !o.noPool)
		opts.Pools = funnelPools(t)
	}
	f.srv = New(f.store, opts)
	return f
}

// binder builds a Binder over a fake cluster whose Patch of an agent Pod, the
// drain-request stamp, is recorded as a "drain:" event. Every client call is
// counted in clientCalls so a test can assert no pod was claimed.
func (f *funnelFixture) binder(t *testing.T, withPool bool) *podsession.Binder {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	b := fake.NewClientBuilder().WithScheme(scheme)
	if withPool {
		b = b.WithObjects(
			&lennyv1.SandboxWarmPool{
				ObjectMeta: metav1.ObjectMeta{Name: "pool-c", Namespace: funnelNS},
				Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "tmpl-c", MinWarm: 1, MaxWarm: 2},
			},
			&lennyv1.SandboxTemplate{
				ObjectMeta: metav1.ObjectMeta{Name: "tmpl-c", Namespace: funnelNS},
				Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "echo"},
			},
		)
	}
	c := b.WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			f.clientCalls.add("patch")
			if pod, ok := obj.(*corev1.Pod); ok {
				f.log.add("drain:" + pod.Name)
			}
			return cl.Patch(ctx, obj, patch, opts...)
		},
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			f.clientCalls.add("list")
			return cl.List(ctx, list, opts...)
		},
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			f.clientCalls.add("create")
			return cl.Create(ctx, obj, opts...)
		},
	}).Build()
	return &podsession.Binder{
		Client:    c,
		Namespace: funnelNS,
		SlotFailure: func(errorType, pool, pod string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.slotFailures = append(f.slotFailures, errorType+"/"+pool+"/"+pod)
		},
	}
}

// funnelPools is the poolstore mirror that gives pool-c
// maxConcurrentSessions 4, so its unhealthy threshold is 2.
func funnelPools(t *testing.T) poolstore.Store {
	t.Helper()
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name:          "pool-c",
		RuntimeRef:    "echo",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions:            4,
			AcknowledgeProcessLevelIsolation: true,
		},
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	return pools
}

// seed inserts a session row in st with retries already spent, gives rep-1
// its lease, and, when slot is true, binds it to a slot on pod-a (or to the
// whole of pod-x otherwise). It returns the snapshot the store holds and the
// published binding.
func (f *funnelFixture) seed(t *testing.T, id string, st session.State, retries int64, slot bool) (sessionstore.Session, *podsession.BindResult) {
	t.Helper()
	if err := f.store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", RuntimeRef: "echo", State: st, RetryCount: retries,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	f.leases.holders[leaseK("acme", id)] = "rep-1"
	bind := &podsession.BindResult{SessionID: id, TenantID: "acme", SandboxName: "pod-x"}
	if slot {
		bind = &podsession.BindResult{SessionID: id, TenantID: "acme", SandboxName: "pod-a", SlotID: id}
	}
	if f.registry != nil {
		f.registry.Put(bind)
	}
	row, err := f.store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row, bind
}

// report runs the funnel on snap as ReportSessionFailure would after reading it.
func (f *funnelFixture) report(ctx context.Context, snap sessionstore.Session, reason string) (FailureDisposition, error) {
	classification := session.ClassifyFailure(reason, snap.RetryPolicy)
	return f.srv.applyFailureFromActive(ctx, snap, FailureReport{TenantID: "acme", SessionID: snap.ID, Reason: reason},
		classification, EffectiveMaxRetriesForRow(snap, f.srv.retryPolicyCaps))
}

func (f *funnelFixture) failureLabels() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.slotFailures...)
}

func (f *funnelFixture) replacementCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.replacements
}

func (f *funnelFixture) retryCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.retries
}

func (f *funnelFixture) leaseHolder(id string) string {
	return f.leases.holders[leaseK("acme", id)]
}

func (f *funnelFixture) state(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	row, err := f.store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row
}

// refCount reports how many goroutines hold or wait on sessionID's lock, so a
// test can observe a waiter without timing assumptions.
func (l *sessionLocks) refCount(sessionID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.locks[sessionID]; ok {
		return e.refs
	}
	return 0
}

// waitForRefs polls until n goroutines hold or wait on sessionID's lock.
func waitForRefs(t *testing.T, l *sessionLocks, sessionID string, n int) {
	t.Helper()
	deadline := time.Now().Add(funnelWait)
	for l.refCount(sessionID) != n {
		if time.Now().After(deadline) {
			t.Fatalf("lock refs for %s = %d, want %d", sessionID, l.refCount(sessionID), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// spec: §7.3 (Retry and Resume), §7.2 (Interactive Session Model)
// diagnosis: the failure funnel commits only from the state it read. A
// report whose stale `running` snapshot meets a row another path already moved
// must not account the slot, increment the counter, release the binding, or
// re-run the edge's retry or terminal side effects. A failure means a losing
// report treats a target state written by a concurrent report as its own
// commit, or overwrites an unrelated state change, and drains or releases a
// pod a second time.
func TestFailureFunnelCommitsOnlyFromSnapshotState_spec_7_3(t *testing.T) {
	for _, st := range []session.State{
		session.StateResumePending, session.StateAwaitingClientAction,
		session.StateFailed, session.StateInputRequired,
	} {
		for _, reason := range []string{"runtime_crash", "workspace_validation_failed"} {
			t.Run(string(st)+"/"+reason, func(t *testing.T) {
				f := newFunnelFixture(t, funnelOpts{})
				stored, _ := f.seed(t, "sess-cas", st, 0, true)
				f.srv.slotHealth.RecordFailure("pod-a")
				snap := stored
				snap.State = session.StateRunning

				disp, err := f.report(context.Background(), snap, reason)
				if err != nil {
					t.Fatalf("applyFailureFromActive: %v", err)
				}
				if disp.To != session.StateRunning {
					t.Errorf("To = %q, want the no-op disposition", disp.To)
				}
				if got := f.state(t, "sess-cas"); got.State != st || got.RetryCount != stored.RetryCount {
					t.Errorf("row = %q/%d, want %q/%d unchanged", got.State, got.RetryCount, st, stored.RetryCount)
				}
				if failed, _ := f.srv.slotHealth.Counts("pod-a"); failed != 1 {
					t.Errorf("failed count = %d, want 1: nothing accounted", failed)
				}
				if labels := f.failureLabels(); len(labels) != 0 {
					t.Errorf("slot failure counter = %v, want none", labels)
				}
				if calls := f.exec.callList(); len(calls) != 0 {
					t.Errorf("release calls = %v, want none (recordSessionCompleted must not run)", calls)
				}
				if f.log.count("drain:") != 0 {
					t.Errorf("drained: %v", f.log.list())
				}
				if n := f.retryCount(); n != 0 {
					t.Errorf("recordSessionRetry ran %d times, want 0", n)
				}
			})
		}
	}

	t.Run("resuming snapshot over a running row", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		if err := f.store.Create(context.Background(), sessionstore.Session{
			ID: "sess-res", TenantID: "acme", RuntimeRef: "echo",
			State: session.StateRunning, CoordinationGeneration: 7,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		snap := f.state(t, "sess-res")
		snap.State = session.StateResuming
		classification := session.ClassifyFailure("runtime_crash", nil)
		disp, err := f.srv.applyFailureFromResuming(context.Background(), snap,
			FailureReport{TenantID: "acme", SessionID: "sess-res", Reason: "runtime_crash"}, classification, 2)
		if err != nil {
			t.Fatalf("applyFailureFromResuming: %v", err)
		}
		if disp.To != session.StateResuming {
			t.Errorf("To = %q, want the no-op disposition", disp.To)
		}
		got := f.state(t, "sess-res")
		if got.State != session.StateRunning || got.RetryCount != 0 {
			t.Errorf("row = %q/%d, want running/0 unchanged", got.State, got.RetryCount)
		}
		if got.CoordinationGeneration != 7 {
			t.Errorf("CoordinationGeneration = %d, want 7 unchanged", got.CoordinationGeneration)
		}
	})
}

// spec: §5.2 (Pool Configuration and Execution Modes), §7.3 (Retry and Resume)
// diagnosis: a slot whose failure crosses ceil(n/2) must have its drain
// stamped before the funnel releases the slot, on the failed edge and on the
// resume_pending edge. A failure means ReleaseSlot can reach the
// occupancy-zero recycle edge of a recycling concurrent pool first, so the
// whole-pod scrub hands the pod on before the drain lands.
func TestFailureFunnelDrainsBeforeSlotRelease_spec_5_2(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		want         session.State
	}{
		{"failed edge", "workspace_validation_failed", session.StateFailed},
		{"resume_pending edge", "runtime_crash", session.StateResumePending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFunnelFixture(t, funnelOpts{})
			snap, _ := f.seed(t, "sess-drain", session.StateRunning, 0, true)
			f.srv.slotHealth.RecordFailure("pod-a")

			disp, err := f.report(context.Background(), snap, tc.reason)
			if err != nil {
				t.Fatalf("applyFailureFromActive: %v", err)
			}
			if disp.To != tc.want {
				t.Fatalf("To = %q, want %q", disp.To, tc.want)
			}
			drain, release := f.log.index("drain:pod-a"), f.log.index("release:failed")
			if drain < 0 || release < 0 || drain > release {
				t.Fatalf("events = %v, want drain:pod-a before release:failed", f.log.list())
			}
			if n := f.log.count("release:"); n != 1 {
				t.Errorf("releases = %d, want 1", n)
			}
			if n := f.replacementCount(); n != 1 {
				t.Errorf("replacements = %d, want 1", n)
			}
		})
	}
}

// spec: §5.2 (Pool Configuration and Execution Modes), §7.3 (Retry and
// Resume), §10.1.1 (Stateless Replicas and Per-Session Coordination)
// diagnosis: on every committed edge the funnel releases the per-session
// slot-accounting lock once the slot accounting returns, before it releases
// the binding, so the lock covers the accounting alone. On the failed edge
// that is before the terminal pipeline (seal, executor release, cascade,
// billing, and audit) runs. A failure means the lock is held across the
// release or the terminal pipeline's I/O, and a waiter on the session's lock
// blocks behind work the lock does not protect.
func TestFailureFunnelUnlocksSlotAccountingBeforeRelease_spec_5_2(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		retries      int64
		want         session.State
	}{
		{"failed edge", "workspace_validation_failed", 0, session.StateFailed},
		{"resume_pending edge", "runtime_crash", 0, session.StateResumePending},
		{"awaiting_client_action edge", "runtime_crash", 2, session.StateAwaitingClientAction},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFunnelFixture(t, funnelOpts{})
			snap, _ := f.seed(t, "sess-span", session.StateRunning, tc.retries, true)
			var mu sync.Mutex
			var refs []int
			f.exec.onRelease = func(id string) {
				mu.Lock()
				defer mu.Unlock()
				refs = append(refs, f.srv.slotAccountLocks.refCount(id))
			}

			disp, err := f.report(context.Background(), snap, tc.reason)
			if err != nil {
				t.Fatalf("applyFailureFromActive: %v", err)
			}
			if disp.To != tc.want {
				t.Fatalf("To = %q, want %q", disp.To, tc.want)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(refs) == 0 {
				t.Fatalf("no executor release observed; events = %v", f.log.list())
			}
			for i, n := range refs {
				if n != 0 {
					t.Errorf("release %d ran with %d lock refs on the session, want 0", i, n)
				}
			}
			if n := f.log.count("release:"); n != 1 {
				t.Errorf("releases = %d, want 1; events = %v", n, f.log.list())
			}
		})
	}
}

// spec: §5.2 (Pool Configuration and Execution Modes), §16.1 (Metrics)
// diagnosis: every slot the funnel fails increments lenny_slot_failure_total
// once, labeled with the §7.3 reason, or `unknown` for an unlisted reason, and
// a pod that is already tripped still counts its slot failures. A pool the
// funnel cannot resolve records the failure in the window and is still
// released, with no increment and no drain. A failure means mid-session slot
// failures are invisible on the series, an unlisted label adds a series, or a
// pool-resolution error leaks the binding.
func TestFailedSlotIncrementsSlotFailureTotal_spec_5_2(t *testing.T) {
	t.Run("retryable reason on a tripped pod", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		f.srv.slotHealth.RecordFailure("pod-a")
		f.srv.slotHealth.RecordFailure("pod-a")
		if !f.srv.slotHealth.Trip("pod-a", 4) {
			t.Fatal("precondition: pod-a did not trip")
		}
		snap, _ := f.seed(t, "sess-m1", session.StateRunning, 0, true)
		if _, err := f.report(context.Background(), snap, "runtime_crash"); err != nil {
			t.Fatalf("report: %v", err)
		}
		if got := f.failureLabels(); len(got) != 1 || got[0] != "runtime_crash/pool-c/pod-a" {
			t.Errorf("slot failure counter = %v, want [runtime_crash/pool-c/pod-a]", got)
		}
		if f.log.count("drain:") != 0 {
			t.Errorf("a tripped pod was drained again: %v", f.log.list())
		}
	})
	t.Run("unlisted reason", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		snap, _ := f.seed(t, "sess-m2", session.StateRunning, 0, true)
		if _, err := f.report(context.Background(), snap, "deployer_specific_cause"); err != nil {
			t.Fatalf("report: %v", err)
		}
		if got := f.failureLabels(); len(got) != 1 || got[0] != "unknown/pool-c/pod-a" {
			t.Errorf("slot failure counter = %v, want [unknown/pool-c/pod-a]", got)
		}
	})
	t.Run("pool resolution error", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{noPool: true})
		snap, _ := f.seed(t, "sess-m3", session.StateRunning, 0, true)
		if _, err := f.report(context.Background(), snap, "runtime_crash"); err != nil {
			t.Fatalf("report: %v", err)
		}
		if failed, _ := f.srv.slotHealth.Counts("pod-a"); failed != 1 {
			t.Errorf("failed count = %d, want 1", failed)
		}
		if got := f.failureLabels(); len(got) != 0 {
			t.Errorf("slot failure counter = %v, want none", got)
		}
		if f.log.count("drain:") != 0 {
			t.Errorf("drained on a pool-resolution error: %v", f.log.list())
		}
		if f.log.count("release:failed") != 1 {
			t.Errorf("events = %v, want one failed release", f.log.list())
		}
	})
}

// spec: §7.3 (Retry and Resume), §10.1.1 (Stateless Replicas and Per-Session
// Coordination)
// diagnosis: the funnel releases only the binding it snapshotted. A binding a
// later bind published between the commit and the release stays, a binding
// another path removed is not released again, and the coordination lease
// stays with this replica in every case. A failure means a report tears down
// a newer binding of the session, or the funnel hands the lease away so no
// replica renews it in resume_pending or awaiting_client_action.
func TestFailureFunnelReleasesOnlyTheReportedBinding_spec_7_3(t *testing.T) {
	for _, tc := range []struct {
		name        string
		hook        func(f *funnelFixture)
		wantCalls   int
		wantNewBind bool
	}{
		{"a newer binding is published", func(f *funnelFixture) {
			f.registry.Put(&podsession.BindResult{SessionID: "sess-own", TenantID: "acme", SandboxName: "pod-new"})
		}, 0, true},
		{"the binding is removed", func(f *funnelFixture) { f.registry.Remove("sess-own") }, 0, false},
		{"no hook", nil, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFunnelFixture(t, funnelOpts{})
			snap, _ := f.seed(t, "sess-own", session.StateRunning, 2, false)
			if tc.hook != nil {
				var once sync.Once
				f.store.setAfter(func(r sessionstore.Session) {
					if r.State == session.StateAwaitingClientAction {
						once.Do(func() { tc.hook(f) })
					}
				})
			}
			disp, err := f.report(context.Background(), snap, "runtime_crash")
			if err != nil {
				t.Fatalf("report: %v", err)
			}
			if disp.To != session.StateAwaitingClientAction {
				t.Fatalf("To = %q, want awaiting_client_action", disp.To)
			}
			calls := f.exec.callList()
			if len(calls) != tc.wantCalls {
				t.Errorf("release calls = %v, want %d", calls, tc.wantCalls)
			}
			for _, d := range calls {
				if d != executor.DispositionFailed {
					t.Errorf("release disposition = %q, want failed", d)
				}
			}
			if b, ok := f.registry.Get("sess-own"); tc.wantNewBind && (!ok || b.SandboxName != "pod-new") {
				t.Errorf("registry = %+v ok=%v, want the newer binding kept", b, ok)
			}
			if h := f.leaseHolder("sess-own"); h != "rep-1" {
				t.Errorf("lease holder = %q, want rep-1", h)
			}
		})
	}
}

// spec: §5.2 (Pool Configuration and Execution Modes), §10.1.1 (Stateless
// Replicas and Per-Session Coordination)
// diagnosis: a same-replica resume that releases the session's earlier
// binding while the funnel is accounting the same slot waits for the
// accounting, so the drain is stamped before the slot is released, and the
// slot is released once. A failure means the resume's ReleaseSlot can reach
// the occupancy-zero recycle edge before the drain lands.
func TestResumeReleaseWaitsForFunnelSlotAccounting_spec_5_2(t *testing.T) {
	f := newFunnelFixture(t, funnelOpts{})
	snap, _ := f.seed(t, "sess-wait", session.StateRunning, 2, true)
	f.srv.slotHealth.RecordFailure("pod-a")

	done := make(chan error, 1)
	var once sync.Once
	f.store.setAfter(func(r sessionstore.Session) {
		if r.State != session.StateAwaitingClientAction {
			return
		}
		once.Do(func() {
			go func() { done <- f.srv.releaseEarlierBinding(context.Background(), r) }()
			waitForRefs(t, &f.srv.slotAccountLocks, "sess-wait", 2)
		})
	})
	if _, err := f.report(context.Background(), snap, "runtime_crash"); err != nil {
		t.Fatalf("report: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("releaseEarlierBinding: %v", err)
		}
	case <-time.After(funnelWait):
		t.Fatal("releaseEarlierBinding did not return")
	}
	drain, release := f.log.index("drain:pod-a"), f.log.index("release:")
	if drain < 0 || release < 0 || drain > release {
		t.Fatalf("events = %v, want drain:pod-a before the release", f.log.list())
	}
	if n := f.log.count("release:"); n != 1 {
		t.Errorf("slot released %d times, want 1", n)
	}
	if n := f.srv.slotAccountLocks.refCount("sess-wait"); n != 0 {
		t.Errorf("lock refs = %d after both paths returned, want 0", n)
	}
}

// spec: §5.2 (Pool Configuration and Execution Modes), §7.3 (Retry and Resume)
// diagnosis: both waiters on the per-session slot-accounting lock honor their
// context. A resume whose context ends while it waits returns the context
// error before any binder call or pod claim, and a funnel whose context
// expires while it waits commits nothing. The lock keeps no entry once every
// holder and waiter has left. A failure means a cancelled request blocks
// behind a slow accounting, claims a pod after its client has gone, or the
// lock table grows without bound.
func TestSlotAccountLockContextCancel_spec_5_2(t *testing.T) {
	f := newFunnelFixture(t, funnelOpts{})
	snap, _ := f.seed(t, "sess-ctx", session.StateRunning, 0, true)
	unlock, err := f.srv.slotAccountLocks.Lock(context.Background(), "sess-ctx")
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, rerr := f.srv.releaseThenResumeOnPod(ctx, snap)
		done <- rerr
	}()
	waitForRefs(t, &f.srv.slotAccountLocks, "sess-ctx", 2)
	cancel()
	select {
	case rerr := <-done:
		if !errors.Is(rerr, context.Canceled) {
			t.Errorf("resume error = %v, want context.Canceled", rerr)
		}
	case <-time.After(funnelWait):
		t.Fatal("resume did not return on cancel")
	}
	if calls := f.exec.callList(); len(calls) != 0 {
		t.Errorf("release calls = %v, want none", calls)
	}
	if calls := f.clientCalls.list(); len(calls) != 0 {
		t.Errorf("cluster calls = %v, want none: no pod claimed", calls)
	}

	tctx, tcancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer tcancel()
	if _, ferr := f.report(tctx, snap, "runtime_crash"); !errors.Is(ferr, context.DeadlineExceeded) {
		t.Errorf("funnel error = %v, want context.DeadlineExceeded", ferr)
	}
	if got := f.state(t, "sess-ctx"); got.State != session.StateRunning || got.RetryCount != 0 {
		t.Errorf("row = %q/%d, want running/0 unchanged", got.State, got.RetryCount)
	}
	if failed, _ := f.srv.slotHealth.Counts("pod-a"); failed != 0 {
		t.Errorf("failed count = %d, want 0", failed)
	}
	if calls := f.exec.callList(); len(calls) != 0 {
		t.Errorf("release calls = %v, want none", calls)
	}

	unlock()
	if n := f.srv.slotAccountLocks.refCount("sess-ctx"); n != 0 {
		t.Errorf("lock refs = %d, want 0", n)
	}
	f.srv.slotAccountLocks.mu.Lock()
	entries := len(f.srv.slotAccountLocks.locks)
	f.srv.slotAccountLocks.mu.Unlock()
	if entries != 0 {
		t.Errorf("lock table holds %d entries, want 0", entries)
	}
}

// spec: §7.3 (Retry and Resume), §5.2 (Pool Configuration and Execution Modes)
// diagnosis: a server with no pod registry, or no pod binder, still commits
// the failure edge and releases through its executor, without a panic and
// without accounting a slot. A failure means a dev-mode or unit-test gateway
// crashes on a failure report, or a binderless gateway touches slot health.
func TestFailureFunnelWithoutPodRegistry_spec_7_3(t *testing.T) {
	t.Run("nil registry", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{noRegistry: true})
		snap, _ := f.seed(t, "sess-noreg", session.StateRunning, 0, true)
		disp, err := f.report(context.Background(), snap, "runtime_crash")
		if err != nil || disp.To != session.StateResumePending {
			t.Fatalf("report = %+v, %v; want resume_pending", disp, err)
		}
		if calls := f.exec.callList(); len(calls) != 1 || calls[0] != executor.DispositionFailed {
			t.Errorf("release calls = %v, want one failed release", calls)
		}
		if failed, _ := f.srv.slotHealth.Counts("pod-a"); failed != 0 {
			t.Errorf("failed count = %d, want 0", failed)
		}
	})
	t.Run("nil binder", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{noBinder: true})
		snap, _ := f.seed(t, "sess-nobind", session.StateRunning, 0, true)
		disp, err := f.report(context.Background(), snap, "workspace_validation_failed")
		if err != nil || disp.To != session.StateFailed {
			t.Fatalf("report = %+v, %v; want failed", disp, err)
		}
		if failed, _ := f.srv.slotHealth.Counts("pod-a"); failed != 0 {
			t.Errorf("failed count = %d, want 0", failed)
		}
		if n := f.log.count("release:failed"); n != 1 {
			t.Errorf("events = %v, want one failed release", f.log.list())
		}
	})
}
