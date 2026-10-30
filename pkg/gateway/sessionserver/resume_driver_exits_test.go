// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

var errDriverStore = errors.New("injected store error")

// driverStore wraps the session store so a test can act after the attempt's
// primary read and on each Update by ordinal.
type driverStore struct {
	*memstore.Store
	mu          sync.Mutex
	updates     int
	afterRead   func()
	readOnce    sync.Once
	afterUpdate map[int]func()
	failUpdate  map[int]bool // true: applied then error; false: error without applying
}

func (d *driverStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := d.Get(ctx, tenantID, id)
	if d.afterRead != nil {
		d.readOnce.Do(d.afterRead)
	}
	return row, err
}

func (d *driverStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	d.mu.Lock()
	d.updates++
	n := d.updates
	applyThenFail, fail := d.failUpdate[n]
	after := d.afterUpdate[n]
	d.mu.Unlock()
	if fail && !applyThenFail {
		return sessionstore.Session{}, errDriverStore
	}
	out, err := d.Store.Update(ctx, tenantID, id, mut)
	if after != nil {
		after()
	}
	if fail && err == nil {
		return sessionstore.Session{}, errDriverStore
	}
	return out, err
}

type attemptCounter struct {
	mu      sync.Mutex
	entries []string
}

func (c *attemptCounter) record(pool, outcome string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, pool+":"+outcome)
}

func (c *attemptCounter) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.entries...)
}

// driverFixture is a pod-backed server whose resume driver runs against a
// real adapter on an envtest cluster with one idle Sandbox, sbx-1.
type driverFixture struct {
	srv      *sessionserver.Server
	store    *driverStore
	registry *podsession.Registry
	cluster  client.Client
	counter  *attemptCounter
	row      sessionstore.Session
}

func newDriverFixture(t *testing.T, brokenResume bool) *driverFixture {
	t.Helper()
	adapterSrv := adapter.New("adapter-test")
	if !brokenResume {
		adapterSrv.WorkspaceBase = t.TempDir()
	}
	adapterSrv.Runtime = &podBindRuntime{}
	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	f := &driverFixture{
		store:    &driverStore{Store: memstore.New(), afterUpdate: map[int]func(){}, failUpdate: map[int]bool{}},
		registry: podsession.NewRegistry(),
		cluster:  cluster,
		counter:  &attemptCounter{},
	}
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))
	f.srv = sessionserver.New(f.store, sessionserver.Options{
		DevTenantHeader:         true,
		IDFunc:                  func() string { return "unused" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             f.registry,
		Executor:                executor.NewPodExecutor(f.registry, binder),
		AgentNamespace:          podTestNS,
		IncSessionResumeAttempt: f.counter.record,
	})
	t.Cleanup(f.srv.Close)
	if err := f.store.Create(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", RuntimeRef: "echo", PoolRef: "echo-pool", State: session.StateResumePending,
		IsolationProfile:  isolation.ProfileSandboxed,
		WorkspaceSnapshot: checkpointed(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.row, _ = f.store.Get(context.Background(), "acme", "s")
	return f
}

func (f *driverFixture) drive(t *testing.T) sessionstore.Session {
	t.Helper()
	f.srv.DriveResumeAndWait(context.Background(), f.row)
	got, err := f.store.Get(context.Background(), "acme", "s")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return got
}

func (f *driverFixture) claimBound(t *testing.T) bool {
	t.Helper()
	var claim lennyv1.SandboxClaim
	err := f.cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim)
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	return claim.Status.Phase == string(claimstate.Bound)
}

// spec: §7.3 (Resume driver), §16.1 (Metrics)
// diagnosis: an exit of the resume driver left a pod claimed, published a
// binding, wrote a state, or counted an attempt that its row of the exit table
// does not allow. Each case drives one exit on a real adapter and envtest
// cluster: a landed commit, an entry write a terminal write superseded, an
// entry write a store error refused before or after applying, a generation
// advance between the entry write and the commit, and a restore failure
// after the entry write.
func TestResumeDriverExits_spec_7_3(t *testing.T) {
	t.Parallel()
	t.Run("landed commit", func(t *testing.T) {
		t.Parallel()
		f := newDriverFixture(t, false)
		got := f.drive(t)
		if got.State != session.StateRunning || got.CoordinationGeneration != f.row.CoordinationGeneration+1 ||
			got.RecoveryGeneration != f.row.RecoveryGeneration+1 || got.PodAssignment != "sbx-1" {
			t.Errorf("row = %q gen %d rec %d pod %q, want running at G with the recovery generation advanced on sbx-1",
				got.State, got.CoordinationGeneration, got.RecoveryGeneration, got.PodAssignment)
		}
		if b, ok := f.registry.Get("s"); !ok || b.CoordinationGeneration != got.CoordinationGeneration {
			t.Errorf("binding = %+v ok=%v, want published at G", b, ok)
		}
		if c := f.counter.list(); len(c) != 1 || c[0] != "echo-pool:success" {
			t.Errorf("counter = %v, want one echo-pool success", c)
		}
	})

	t.Run("terminal write after the primary read supersedes the entry", func(t *testing.T) {
		t.Parallel()
		f := newDriverFixture(t, false)
		f.store.afterRead = func() {
			_, _ = f.store.Store.Update(context.Background(), "acme", "s", func(r *sessionstore.Session) error {
				r.State = session.StateCancelled
				sessionstore.MarkTerminated(r, session.StateResumePending, r.UpdatedAt)
				return nil
			})
		}
		got := f.drive(t)
		if got.State != session.StateCancelled {
			t.Errorf("state = %q, want cancelled kept", got.State)
		}
		if f.claimBound(t) {
			t.Error("the superseded attempt left its claim bound")
		}
		if _, ok := f.registry.Get("s"); ok {
			t.Error("the superseded attempt published a binding")
		}
		if c := f.counter.list(); len(c) != 0 {
			t.Errorf("counter = %v, want nothing for an entry write that did not apply", c)
		}
	})

	t.Run("entry store error before applying", func(t *testing.T) {
		t.Parallel()
		f := newDriverFixture(t, false)
		f.store.failUpdate[1] = false
		got := f.drive(t)
		if got.State != session.StateResumePending || got.CoordinationGeneration != f.row.CoordinationGeneration {
			t.Errorf("row = %q gen %d, want resume_pending unchanged", got.State, got.CoordinationGeneration)
		}
		if f.claimBound(t) {
			t.Error("the claim survived the hook error")
		}
		if c := f.counter.list(); len(c) != 0 {
			t.Errorf("counter = %v, want nothing", c)
		}
	})

	t.Run("entry store error after applying", func(t *testing.T) {
		t.Parallel()
		f := newDriverFixture(t, false)
		f.store.failUpdate[1] = true
		got := f.drive(t)
		if got.State != session.StateResuming || got.CoordinationGeneration != f.row.CoordinationGeneration+1 {
			t.Errorf("row = %q gen %d, want resuming at G for the watchdog", got.State, got.CoordinationGeneration)
		}
		if f.claimBound(t) {
			t.Error("the claim survived the hook error")
		}
		if c := f.counter.list(); len(c) != 0 {
			t.Errorf("counter = %v, want nothing for an applied hook store error", c)
		}
		// The next tick starts no claim for a row in resuming.
		f.srv.DriveResumeAndWait(context.Background(), got)
		if f.claimBound(t) {
			t.Error("a later drive claimed a pod for a resuming row")
		}
	})

	t.Run("generation advanced between the entry and the commit", func(t *testing.T) {
		t.Parallel()
		f := newDriverFixture(t, false)
		f.store.afterUpdate[1] = func() {
			_, _ = f.store.Store.Update(context.Background(), "acme", "s", func(r *sessionstore.Session) error {
				r.CoordinationGeneration++
				return nil
			})
		}
		got := f.drive(t)
		if got.State != session.StateResuming || got.CoordinationGeneration != f.row.CoordinationGeneration+2 {
			t.Errorf("row = %q gen %d, want resuming at G+1 untouched by the commit", got.State, got.CoordinationGeneration)
		}
		if _, ok := f.registry.Get("s"); ok {
			t.Error("a commit that did not land published a binding")
		}
		if f.claimBound(t) {
			t.Error("a commit that did not land left its claim bound")
		}
		if c := f.counter.list(); len(c) != 1 || c[0] != "echo-pool:failure" {
			t.Errorf("counter = %v, want one failure", c)
		}
	})

	t.Run("restore failure after the entry write", func(t *testing.T) {
		t.Parallel()
		f := newDriverFixture(t, true)
		got := f.drive(t)
		if got.State != session.StateResumePending || got.RetryCount != f.row.RetryCount+1 ||
			got.CoordinationGeneration != f.row.CoordinationGeneration+1 || got.FailureReason != string(session.FailureRuntimeCrash) {
			t.Errorf("row = %q retry %d gen %d reason %q, want resume_pending retry+1 at G with runtime_crash",
				got.State, got.RetryCount, got.CoordinationGeneration, got.FailureReason)
		}
		if _, ok := f.registry.Get("s"); ok {
			t.Error("a failed restore published a binding")
		}
		if f.claimBound(t) {
			t.Error("a failed restore left its claim bound")
		}
		if c := f.counter.list(); len(c) != 1 || c[0] != "echo-pool:failure" {
			t.Errorf("counter = %v, want one failure", c)
		}
	})

	t.Run("pod drained between the entry write and the restore", func(t *testing.T) {
		t.Parallel()
		f := newDriverFixture(t, false)
		f.store.afterUpdate[1] = func() {
			// spec: §4.6.1 — an external drain removes the claimed pod's
			// Sandbox and claim before the restore RPC reaches it.
			_ = f.cluster.Delete(context.Background(), &lennyv1.SandboxClaim{ObjectMeta: metav1.ObjectMeta{
				Name: "claim-sbx-1", Namespace: podTestNS,
			}})
			_ = f.cluster.Delete(context.Background(), &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{
				Name: "sbx-1", Namespace: podTestNS,
			}})
		}
		got := f.drive(t)
		if got.State == session.StateResuming || got.State == session.StateRunning {
			t.Fatalf("state = %q, want the attempt's failure reported out of resuming", got.State)
		}
		if _, ok := f.registry.Get("s"); ok {
			t.Error("an attempt whose pod was drained published a binding")
		}
	})

	t.Run("terminal write after the commit", func(t *testing.T) {
		t.Parallel()
		f := newDriverFixture(t, false)
		f.store.afterUpdate[2] = func() {
			_, _ = f.store.Store.Update(context.Background(), "acme", "s", func(r *sessionstore.Session) error {
				from := r.State
				r.State = session.StateCancelled
				sessionstore.MarkTerminated(r, from, r.UpdatedAt)
				return nil
			})
		}
		got := f.drive(t)
		if got.State != session.StateCancelled {
			t.Fatalf("state = %q, want cancelled", got.State)
		}
		// spec: §7.2 — the tail's terminal re-check hands the published
		// binding to the terminal binding release.
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, ok := f.registry.Get("s"); !ok {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the binding published before the terminal write was never released")
			}
			time.Sleep(10 * time.Millisecond)
		}
	})

	t.Run("terminal write on the resuming row cancels the attempt", func(t *testing.T) {
		t.Parallel()
		f := newDriverFixture(t, false)
		f.store.afterUpdate[1] = func() {
			// A same-replica DELETE lands while the attempt restores.
			go func() {
				req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/s", nil)
				req.Header.Set("X-Lenny-Tenant-ID", "acme")
				asOwner(f.srv.Handler()).ServeHTTP(httptest.NewRecorder(), req)
			}()
			time.Sleep(50 * time.Millisecond)
		}
		got := f.drive(t)
		if got.State != session.StateCancelled {
			t.Fatalf("state = %q, want cancelled", got.State)
		}
		if got.CoordinationGeneration != f.row.CoordinationGeneration+2 {
			t.Errorf("coordination_generation = %d, want G+1 from the cancel of the resuming row", got.CoordinationGeneration)
		}
		deadline := time.Now().Add(10 * time.Second)
		for f.claimBound(t) || func() bool { _, ok := f.registry.Get("s"); return ok }() {
			if time.Now().After(deadline) {
				t.Fatal("the cancelled attempt left its pod claimed or a binding published")
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}
