// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// The tests in this file pin each exit of POST /v1/sessions/{id}/start
// across the ready → starting write: the exits before the write change
// nothing, every error exit after it fails the session through the
// starting → failed write and releases the coordination lease, an exit that
// finds another writer's terminal state answers that state, and a success
// publishes status_change(starting) before the launch and
// status_change(running) after the commit.

const startTestTenant = "acme"

// errStoreInjected is the store error a startHook injects.
var errStoreInjected = errors.New("injected store error")

// startHook acts on the first Update whose mutation, probed on a copy of the
// current row, writes `to` from `from` (from empty matches any source).
type startHook struct {
	from, to        session.State
	before          func()
	failNoCommit    bool
	failAfterCommit bool
	fired           bool
}

// startStore wraps a memstore. It refuses an Update on a cancelled context,
// so a writer that does not detach from the request is observable, runs
// afterRead once after the first entry read, and acts on the Updates its
// hooks select.
type startStore struct {
	*memstore.Store
	mu        sync.Mutex
	hooks     []*startHook
	readOnce  sync.Once
	afterRead func()
}

func (s *startStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := s.Store.GetPrimary(ctx, tenantID, id)
	if s.afterRead != nil {
		s.readOnce.Do(s.afterRead)
	}
	return row, err
}

func (s *startStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if err := ctx.Err(); err != nil {
		return sessionstore.Session{}, err
	}
	hook := s.selectHook(ctx, tenantID, id, mutate)
	if hook == nil {
		return s.Store.Update(ctx, tenantID, id, mutate)
	}
	if hook.before != nil {
		hook.before()
	}
	switch {
	case hook.failNoCommit:
		return sessionstore.Session{}, errStoreInjected
	case hook.failAfterCommit:
		if _, err := s.Store.Update(ctx, tenantID, id, mutate); err != nil {
			return sessionstore.Session{}, err
		}
		return sessionstore.Session{}, errStoreInjected
	}
	return s.Store.Update(ctx, tenantID, id, mutate)
}

func (s *startStore) selectHook(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) *startHook {
	cur, err := s.Store.Get(ctx, tenantID, id)
	if err != nil {
		return nil
	}
	probe := cur
	if mutate(&probe) != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.hooks {
		if !h.fired && h.to == probe.State && (h.from == "" || h.from == cur.State) {
			h.fired = true
			return h
		}
	}
	return nil
}

func (s *startStore) addHook(h *startHook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = append(s.hooks, h)
}

// commit writes st and reason through the unwrapped store, as another writer
// would.
func (s *startStore) commit(t *testing.T, id string, st session.State, reason string) {
	t.Helper()
	if _, err := s.Store.Update(context.Background(), startTestTenant, id, func(r *sessionstore.Session) error {
		r.State = st
		r.FailureReason = reason
		return nil
	}); err != nil {
		t.Errorf("commit %s: %v", st, err)
	}
}

// launchGate is the adapter's StartSession interceptor. onStart runs at the
// launch; a non-nil error it returns fails the launch, and hold blocks the
// launch until release is closed.
type launchGate struct {
	mu      sync.Mutex
	onStart func() error
	hold    chan struct{}
	entered chan struct{}
	calls   int
}

func newLaunchGate() *launchGate { return &launchGate{entered: make(chan struct{}, 4)} }

func (g *launchGate) intercept(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
	if info.FullMethod != "/lenny.adapter.v1.Adapter/StartSession" {
		return h(ctx, req)
	}
	g.mu.Lock()
	g.calls++
	onStart, hold := g.onStart, g.hold
	g.mu.Unlock()
	g.entered <- struct{}{}
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if onStart != nil {
		if err := onStart(); err != nil {
			return nil, err
		}
	}
	return h(ctx, req)
}

func (g *launchGate) launches() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// startFixture is one finalize-ready session on a warm pod, served by a
// Server with a coordination lease store, a replica identity, and an event
// bus.
type startFixture struct {
	id       string
	srv      *sessionserver.Server
	store    *startStore
	leases   *componentLeaseStore
	registry *podsession.Registry
	bus      *sessionevents.Bus
	gate     *launchGate
	cluster  client.Client
	binder   *podsession.Binder
	opts     sessionserver.Options
	// base is the count of the session's events published before /start,
	// so the event assertions read the /start events alone.
	base int
}

// newStartFixture creates and finalizes id, leaving its row ready on sbx-1.
func newStartFixture(t *testing.T, id string) *startFixture {
	t.Helper()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceBase = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}
	gate := newLaunchGate()
	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	f := &startFixture{
		id:       id,
		store:    &startStore{Store: memstore.New()},
		leases:   newComponentLeaseStore(),
		registry: podsession.NewRegistry(),
		bus:      sessionevents.NewBus(256),
		gate:     gate,
		cluster:  cluster,
		binder:   podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv, grpc.ChainUnaryInterceptor(gate.intercept))),
	}
	f.opts = sessionserver.Options{
		DevTenantHeader:         true,
		IDFunc:                  func() string { return id },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               f.binder,
		PodRegistry:             f.registry,
		AgentNamespace:          podTestNS,
		CoordinationLeaseStore:  f.leases,
		ReplicaID:               "replica-a",
		Events:                  f.bus,
		Executor:                executor.NewPodExecutor(f.registry, f.binder),
	}
	f.srv = sessionserver.New(f.store, f.opts)
	createAndFinalize(t, f.srv.Handler(), id)
	f.base = len(f.bus.History(id, 0))
	if got := f.row(t); got.State != session.StateReady || got.PodAssignment == "" {
		t.Fatalf("seed row = %q on %q, want ready on a pod", got.State, got.PodAssignment)
	}
	return f
}

// peer is a second Server over the fixture's store, lease store, and pod
// binder, as another gateway replica.
func (f *startFixture) peer(store sessionstore.Store, replicaID string) *sessionserver.Server {
	opts := f.opts
	opts.ReplicaID = replicaID
	reg := podsession.NewRegistry()
	opts.PodRegistry = reg
	opts.Executor = executor.NewPodExecutor(reg, f.binder)
	return sessionserver.New(store, opts)
}

func (f *startFixture) row(t *testing.T) sessionstore.Session {
	t.Helper()
	row, err := f.store.Store.Get(context.Background(), startTestTenant, f.id)
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	return row
}

// start posts /start for the fixture's session on ctx.
func (f *startFixture) start(ctx context.Context, srv *sessionserver.Server) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+f.id+"/start", nil).WithContext(ctx)
	req.Header.Set("X-Lenny-Tenant-ID", startTestTenant)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

// statusStates lists the states of the session's status_change events in
// publish order.
func (f *startFixture) statusStates() []string {
	var out []string
	for _, e := range f.startEvents() {
		if e.Type != "status_change" {
			continue
		}
		var body struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal([]byte(e.Data), &body)
		out = append(out, body.State)
	}
	return out
}

// startEvents is the session's event history after the fixture's base.
func (f *startFixture) startEvents() []sessionevents.Event {
	h := f.bus.History(f.id, 0)
	if f.base > len(h) {
		return nil
	}
	return h[f.base:]
}

func (f *startFixture) eventCount(typ string) int {
	n := 0
	for _, e := range f.startEvents() {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func (f *startFixture) leaseHolder(t *testing.T) string {
	t.Helper()
	l, err := f.leases.Get(context.Background(), startTestTenant, f.id)
	if err != nil {
		return ""
	}
	return l.Holder
}

func (f *startFixture) claimBound(t *testing.T) bool {
	t.Helper()
	var c lennyv1.SandboxClaim
	err := f.cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &c)
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	return c.Status.Phase == string(claimstate.Bound)
}

func (f *startFixture) claimPresent(t *testing.T) bool {
	t.Helper()
	err := f.cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &lennyv1.SandboxClaim{})
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	return true
}

// startErrBody decodes the §15.1 error envelope of a /start answer.
type startErrBody struct {
	Error struct {
		Code    string `json:"code"`
		Details struct {
			CurrentState string `json:"currentState"`
		} `json:"details"`
	} `json:"error"`
}

func decodeStartErr(t *testing.T, rr *httptest.ResponseRecorder) startErrBody {
	t.Helper()
	var b startErrBody
	if err := json.Unmarshal(rr.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode /start error %q: %v", rr.Body.String(), err)
	}
	return b
}

func assertConflict(t *testing.T, rr *httptest.ResponseRecorder, want session.State) {
	t.Helper()
	if rr.Code != http.StatusConflict {
		t.Fatalf("/start = %d %s, want 409", rr.Code, rr.Body.String())
	}
	b := decodeStartErr(t, rr)
	if b.Error.Code != "INVALID_STATE_TRANSITION" || b.Error.Details.CurrentState != string(want) {
		t.Errorf("/start error = %s currentState %q, want INVALID_STATE_TRANSITION with %q", b.Error.Code, b.Error.Details.CurrentState, want)
	}
}

// assertFailedExit asserts the columns every F row shares: the row is failed
// with STARTING_FAILED and no failure class, its retry count is unchanged,
// one status_change(starting) precedes one status_change(failed), one
// session_complete is published, and the session holds no lease and no
// binding.
func (f *startFixture) assertFailedExit(t *testing.T, retryBefore int64) {
	t.Helper()
	row := f.row(t)
	if row.State != session.StateFailed || row.FailureReason != "STARTING_FAILED" {
		t.Errorf("row = %q (%q), want failed (STARTING_FAILED)", row.State, row.FailureReason)
	}
	if row.FailureClass != "" {
		t.Errorf("FailureClass = %q, want empty", row.FailureClass)
	}
	if row.RetryCount != retryBefore {
		t.Errorf("RetryCount = %d, want %d unchanged", row.RetryCount, retryBefore)
	}
	if got := strings.Join(f.statusStates(), ","); got != "starting,failed" {
		t.Errorf("status_change states = %q, want starting,failed", got)
	}
	if n := f.eventCount("session_complete"); n != 1 {
		t.Errorf("session_complete events = %d, want 1", n)
	}
	if h := f.leaseHolder(t); h == "replica-a" {
		t.Errorf("lease holder = %q, want the lease released", h)
	}
	if _, ok := f.registry.Get(f.id); ok {
		t.Error("registry holds a binding after a failed /start")
	}
}

// cancelOn returns a request context and a func that cancels it, so a case
// cancels the request before failStarting runs.
func cancelOn() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

// TestStartEntryPreconditionRefusalTakesNoLease_spec_15_1 pins the exit
// whose entry read finds a non-ready row: 409 with the entry state, the row
// unchanged, no lease entry, and no status_change.
//
// spec: §15.1 (/start precondition), §7.2 (Session state machine)
func TestStartEntryPreconditionRefusalTakesNoLease_spec_15_1(t *testing.T) {
	t.Parallel()
	f := newStartFixture(t, "sess-p1")
	f.store.commit(t, f.id, session.StateSuspended, "")
	rr := f.start(context.Background(), f.srv)
	assertConflict(t, rr, session.StateSuspended)
	if got := f.row(t).State; got != session.StateSuspended {
		t.Errorf("row = %q, want suspended unchanged", got)
	}
	if h := f.leaseHolder(t); h != "" {
		t.Errorf("lease holder = %q, want no lease entry", h)
	}
	if n := f.eventCount("status_change"); n != 0 {
		t.Errorf("status_change events = %d, want 0", n)
	}
}

// TestStartLosingConcurrentWriteAnswersStarting_spec_15_1 pins the exit
// whose starting write finds a row a concurrent /start on another replica
// moved to starting after this request's entry read: 409 with starting, no
// lease taken by the loser, and the winner ends running.
//
// spec: §15.1 (/start), §7.2 (Session state machine: ready → starting),
// §10.1.1 (Primary:)
func TestStartLosingConcurrentWriteAnswersStarting_spec_15_1(t *testing.T) {
	t.Parallel()
	f := newStartFixture(t, "sess-p2")
	f.gate.hold = make(chan struct{})
	winnerDone := make(chan *httptest.ResponseRecorder, 1)
	loserStore := &startStore{Store: f.store.Store}
	loserStore.afterRead = func() {
		go func() { winnerDone <- f.start(context.Background(), f.srv) }()
		<-f.gate.entered
	}
	loser := f.peer(loserStore, "replica-b")
	rr := f.start(context.Background(), loser)
	assertConflict(t, rr, session.StateStarting)
	if h := f.leaseHolder(t); h != "replica-a" {
		t.Errorf("lease holder while the winner launches = %q, want replica-a", h)
	}
	close(f.gate.hold)
	if w := <-winnerDone; w.Code != http.StatusOK {
		t.Fatalf("winner /start = %d %s, want 200", w.Code, w.Body.String())
	}
	if got := f.row(t).State; got != session.StateRunning {
		t.Errorf("row = %q, want running", got)
	}
	if got := strings.Join(f.statusStates(), ","); got != "starting,running" {
		t.Errorf("status_change states = %q, want starting,running", got)
	}
}

// TestStartWriteStoreErrorAnswersInternal_spec_15_1 pins the exit whose
// starting write returns a store error: 500, no lease, and no failStarting.
// A write that committed before the error leaves the row starting for the
// starting watchdog.
//
// spec: §15.1 (/start), §6.2 (starting watchdog)
func TestStartWriteStoreErrorAnswersInternal_spec_15_1(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		hook     startHook
		wantRow  session.State
		launches int
	}{
		{"not committed", startHook{from: session.StateReady, to: session.StateStarting, failNoCommit: true}, session.StateReady, 0},
		{"committed", startHook{from: session.StateReady, to: session.StateStarting, failAfterCommit: true}, session.StateStarting, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStartFixture(t, "sess-p3")
			h := tc.hook
			f.store.addHook(&h)
			rr := f.start(context.Background(), f.srv)
			if rr.Code != http.StatusInternalServerError || decodeStartErr(t, rr).Error.Code != "INTERNAL_ERROR" {
				t.Fatalf("/start = %d %s, want 500 INTERNAL_ERROR", rr.Code, rr.Body.String())
			}
			if got := f.row(t).State; got != tc.wantRow {
				t.Errorf("row = %q, want %q", got, tc.wantRow)
			}
			if hold := f.leaseHolder(t); hold != "" {
				t.Errorf("lease holder = %q, want no lease entry", hold)
			}
			if n := f.gate.launches(); n != tc.launches {
				t.Errorf("launches = %d, want %d", n, tc.launches)
			}
			if n := f.eventCount("status_change"); n != 0 {
				t.Errorf("status_change events = %d, want 0", n)
			}
		})
	}
}

// TestStartForeignLeaseHolderFailsTheSession_spec_7_2 pins the exit whose
// lease acquire meets a live foreign holder: the launch never runs, the
// foreign lease survives the release, and the session is failed with
// STARTING_FAILED, carrying Retry-After, by a write that runs after the
// request context ended.
//
// spec: §7.2 (Session state machine: starting → failed), §10.1
// (per-session coordination lease), §15.1 (/start)
func TestStartForeignLeaseHolderFailsTheSession_spec_7_2(t *testing.T) {
	t.Parallel()
	f := newStartFixture(t, "sess-f1")
	if _, err := f.leases.Acquire(context.Background(), startTestTenant, f.id, "replica-b", time.Minute); err != nil {
		t.Fatalf("seed foreign holder: %v", err)
	}
	ctx, cancel := cancelOn()
	// The request context ends as the lease refusal returns, before the
	// failed write.
	f.leases.onAcquire = func() { cancel() }
	retry := f.row(t).RetryCount
	rr := f.start(ctx, f.srv)
	if rr.Code != http.StatusServiceUnavailable || decodeStartErr(t, rr).Error.Code != "STARTING_FAILED" {
		t.Fatalf("/start = %d %s, want 503 STARTING_FAILED", rr.Code, rr.Body.String())
	}
	// spec: §15.1 — the STARTING_FAILED catalog row sets Retry-After on
	// every answer, including the lease-acquire refusal, at the default of
	// five seconds.
	if got, want := rr.Header().Get("Retry-After"), "5"; got != want {
		t.Errorf("Retry-After = %q, want %q", got, want)
	}
	if n := f.gate.launches(); n != 0 {
		t.Errorf("launches = %d, want 0", n)
	}
	if h := f.leaseHolder(t); h != "replica-b" {
		t.Errorf("lease holder = %q, want replica-b kept", h)
	}
	f.assertFailedExit(t, retry)
	if f.claimPresent(t) {
		t.Error("claim-sbx-1 still present, want the by-name reclaim to delete it")
	}
}

// TestStartLaunchFailureBeforeTheBinderReclaimsByName_spec_7_2 pins the exit
// whose launch fails before the binder runs (the pool no longer resolves):
// the failed write's post-terminal hook reclaims the still-claimed pod by
// name.
//
// spec: §7.2 (Session state machine: starting → failed), §4.6 (durable
// binding)
func TestStartLaunchFailureBeforeTheBinderReclaimsByName_spec_7_2(t *testing.T) {
	t.Parallel()
	f := newStartFixture(t, "sess-f2")
	if err := f.cluster.Delete(context.Background(), podBindWarmPool("echo-pool", "echo-tmpl")); err != nil {
		t.Fatalf("delete warm pool: %v", err)
	}
	if !f.claimPresent(t) {
		t.Fatal("claim-sbx-1 absent before /start")
	}
	retry := f.row(t).RetryCount
	rr := f.start(context.Background(), f.srv)
	if rr.Code < 400 {
		t.Fatalf("/start = %d, want an error", rr.Code)
	}
	if n := f.gate.launches(); n != 0 {
		t.Errorf("launches = %d, want 0", n)
	}
	f.assertFailedExit(t, retry)
	if f.claimPresent(t) {
		t.Error("claim-sbx-1 still present, want the by-name reclaim to delete it")
	}
}

// TestStartLaunchFailureInsideTheBinderFailsTheSession_spec_7_2 pins the exit
// whose launch RPC fails inside the binder: the binder releases the pod, the
// session is failed with STARTING_FAILED on a request context that ended
// before the write, and the answer is the STARTING_FAILED fallback.
//
// spec: §7.2 (Session state machine: starting → failed), §7.1
// (Gateway-owned terminal writes), §15.1 (/start)
func TestStartLaunchFailureInsideTheBinderFailsTheSession_spec_7_2(t *testing.T) {
	t.Parallel()
	f := newStartFixture(t, "sess-f3")
	ctx, cancel := cancelOn()
	f.gate.onStart = func() error {
		cancel()
		return status.Error(codes.Unavailable, "launch refused")
	}
	retry := f.row(t).RetryCount
	rr := f.start(ctx, f.srv)
	if rr.Code != http.StatusServiceUnavailable || decodeStartErr(t, rr).Error.Code != "STARTING_FAILED" {
		t.Fatalf("/start = %d %s, want 503 STARTING_FAILED", rr.Code, rr.Body.String())
	}
	f.assertFailedExit(t, retry)
	if f.claimBound(t) {
		t.Error("claim-sbx-1 still bound after the binder's release and the reclaim")
	}
}

// TestStartBindingRegistrationErrorRollsBackBeforeTheFailedWrite_spec_7_2
// pins the exit whose binding registration fails: the unpublished bind is
// rolled back before the failed write, and the answer is 503 STARTING_FAILED.
//
// spec: §7.2 (Session state machine: starting → failed), §4.6.1
// (coordinating replica holds the lease)
func TestStartBindingRegistrationErrorRollsBackBeforeTheFailedWrite_spec_7_2(t *testing.T) {
	t.Parallel()
	f := newStartFixture(t, "sess-f4")
	f.leases.mu.Lock()
	f.leases.failAcquireAfter = f.leases.acquires + 1
	f.leases.mu.Unlock()
	boundAtFailedWrite := true
	f.store.addHook(&startHook{from: session.StateStarting, to: session.StateFailed, before: func() {
		boundAtFailedWrite = f.claimBound(t)
	}})
	retry := f.row(t).RetryCount
	rr := f.start(context.Background(), f.srv)
	if rr.Code != http.StatusServiceUnavailable || decodeStartErr(t, rr).Error.Code != "STARTING_FAILED" {
		t.Fatalf("/start = %d %s, want 503 STARTING_FAILED", rr.Code, rr.Body.String())
	}
	if boundAtFailedWrite {
		t.Error("the claim was still bound at the failed write, want the rollback to release it first")
	}
	f.assertFailedExit(t, retry)
}

// TestStartCommitStoreErrorFailsOrKeepsRunning_spec_7_2 pins the exit whose
// starting → running commit returns a store error. Uncommitted, the session
// is failed and its published binding released. Committed, failStarting
// refuses the running row, the answer is 409 with running, the binding stays
// published, and the lease is released.
//
// spec: §7.2 (Session state machine: starting → running, starting → failed),
// §15.1 (/start)
func TestStartCommitStoreErrorFailsOrKeepsRunning_spec_7_2(t *testing.T) {
	t.Parallel()
	t.Run("not committed", func(t *testing.T) {
		t.Parallel()
		f := newStartFixture(t, "sess-f5a")
		f.store.addHook(&startHook{from: session.StateStarting, to: session.StateRunning, failNoCommit: true})
		retry := f.row(t).RetryCount
		rr := f.start(context.Background(), f.srv)
		if rr.Code != http.StatusInternalServerError || decodeStartErr(t, rr).Error.Code != "INTERNAL_ERROR" {
			t.Fatalf("/start = %d %s, want 500 INTERNAL_ERROR", rr.Code, rr.Body.String())
		}
		f.assertFailedExit(t, retry)
	})
	t.Run("committed", func(t *testing.T) {
		t.Parallel()
		f := newStartFixture(t, "sess-f5b")
		f.store.addHook(&startHook{from: session.StateStarting, to: session.StateRunning, failAfterCommit: true})
		rr := f.start(context.Background(), f.srv)
		assertConflict(t, rr, session.StateRunning)
		if got := f.row(t).State; got != session.StateRunning {
			t.Errorf("row = %q, want running", got)
		}
		if got := strings.Join(f.statusStates(), ","); got != "starting" {
			t.Errorf("status_change states = %q, want starting only", got)
		}
		if _, ok := f.registry.Get(f.id); !ok {
			t.Error("registry holds no binding, want the published binding kept")
		}
		if h := f.leaseHolder(t); h != "" {
			t.Errorf("lease holder = %q, want the lease released", h)
		}
	})
}

// TestStartFindsAnotherWritersTerminalState_spec_7_2 pins the exit whose
// commit or failed write finds the row another writer made terminal (the
// starting watchdog): the answer is 409 with failed, the watchdog's reason
// stays, no status_change(running) is published, and the lease is released.
//
// spec: §7.2 (Session state machine: starting → failed), §6.2 (starting
// watchdog), §15.1 (/start)
func TestStartFindsAnotherWritersTerminalState_spec_7_2(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		failLaunch bool
		hookTo     session.State
	}{
		{"refused at the commit", false, session.StateRunning},
		{"refused at the failed write", true, session.StateFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStartFixture(t, "sess-x1")
			if tc.failLaunch {
				f.gate.onStart = func() error { return status.Error(codes.Unavailable, "launch refused") }
			}
			f.store.addHook(&startHook{from: session.StateStarting, to: tc.hookTo, before: func() {
				f.store.commit(t, f.id, session.StateFailed, "STARTING_TIMEOUT")
			}})
			rr := f.start(context.Background(), f.srv)
			assertConflict(t, rr, session.StateFailed)
			row := f.row(t)
			if row.State != session.StateFailed || row.FailureReason != "STARTING_TIMEOUT" {
				t.Errorf("row = %q (%q), want failed (STARTING_TIMEOUT) kept", row.State, row.FailureReason)
			}
			for _, st := range f.statusStates() {
				if st == string(session.StateRunning) {
					t.Error("status_change(running) published after another writer's terminal write")
				}
			}
			if h := f.leaseHolder(t); h != "" {
				t.Errorf("lease holder = %q, want the lease released", h)
			}
		})
	}
}

// TestStartFailedWriteStoreErrorLeavesStartingForTheWatchdog_spec_6_2 pins
// the exit whose failed write itself returns a store error: the row stays
// starting for the starting watchdog, the answer is the launch exit's own,
// and the lease is released.
//
// spec: §6.2 (starting watchdog), §7.2 (Session state machine:
// starting → failed)
func TestStartFailedWriteStoreErrorLeavesStartingForTheWatchdog_spec_6_2(t *testing.T) {
	t.Parallel()
	f := newStartFixture(t, "sess-x2")
	f.gate.onStart = func() error { return status.Error(codes.Unavailable, "launch refused") }
	f.store.addHook(&startHook{from: session.StateStarting, to: session.StateFailed, failNoCommit: true})
	rr := f.start(context.Background(), f.srv)
	if rr.Code != http.StatusServiceUnavailable || decodeStartErr(t, rr).Error.Code != "STARTING_FAILED" {
		t.Fatalf("/start = %d %s, want 503 STARTING_FAILED", rr.Code, rr.Body.String())
	}
	if got := f.row(t).State; got != session.StateStarting {
		t.Errorf("row = %q, want starting for the watchdog", got)
	}
	if h := f.leaseHolder(t); h != "" {
		t.Errorf("lease holder = %q, want the lease released", h)
	}
}

// TestStartPublishesStartingBeforeTheLaunch_spec_7_2 pins the success exit:
// status_change(starting) is published and the lease names the serving
// replica while the launch runs, and the session ends running with
// status_change(running) after it.
//
// spec: §7.2 (Session state machine: ready → starting, starting → running),
// §10.1.1 (Primary:), §15.1 (/start)
func TestStartPublishesStartingBeforeTheLaunch_spec_7_2(t *testing.T) {
	t.Parallel()
	f := newStartFixture(t, "sess-s1")
	var atLaunch []string
	var holderAtLaunch string
	f.gate.onStart = func() error {
		atLaunch = f.statusStates()
		holderAtLaunch = f.leaseHolder(t)
		if got := f.row(t).State; got != session.StateStarting {
			t.Errorf("row at launch = %q, want starting", got)
		}
		return nil
	}
	rr := f.start(context.Background(), f.srv)
	if rr.Code != http.StatusOK {
		t.Fatalf("/start = %d %s, want 200", rr.Code, rr.Body.String())
	}
	if strings.Join(atLaunch, ",") != "starting" {
		t.Errorf("status_change states at launch = %v, want [starting]", atLaunch)
	}
	if holderAtLaunch != "replica-a" {
		t.Errorf("lease holder at launch = %q, want replica-a", holderAtLaunch)
	}
	if got := strings.Join(f.statusStates(), ","); got != "starting,running" {
		t.Errorf("status_change states = %q, want starting,running", got)
	}
	if got := f.row(t).State; got != session.StateRunning {
		t.Errorf("row = %q, want running", got)
	}
	if _, ok := f.registry.Get(f.id); !ok {
		t.Error("registry holds no binding after a successful /start")
	}
}

// TestServiceModeStartTakesNoLease_spec_10_1_1 pins the success exit of a row
// on a service-mode pool, which names no pod: the session ends running and
// no coordination lease is taken.
//
// spec: §10.1.1 (Primary:), §5.2 (service mode is claimless), §15.1 (/start)
func TestServiceModeStartTakesNoLease_spec_10_1_1(t *testing.T) {
	t.Parallel()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceBase = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}
	cluster := podBindClient(
		t,
		podBindWarmPool("svc-pool", "svc-tmpl"),
		podBindServiceTemplate("svc-tmpl", "svc-runtime", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("svc-sbx-1", "svc-pool", "10.244.3.9"),
	)
	store := memstore.New()
	leases := newComponentLeaseStore()
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:         true,
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv)),
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		Pools:                   podBindServicePool(t, "svc-pool", "svc-runtime", 8),
		CoordinationLeaseStore:  leases,
		ReplicaID:               "replica-a",
	})
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess-svc", TenantID: startTestTenant, RuntimeRef: "svc-runtime",
		IsolationProfile: isolation.ProfileSandboxed, State: session.StateReady,
	}); err != nil {
		t.Fatalf("seed ready row: %v", err)
	}
	f := &startFixture{id: "sess-svc"}
	rr := f.start(context.Background(), srv)
	if rr.Code != http.StatusOK {
		t.Fatalf("/start = %d %s, want 200", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), startTestTenant, "sess-svc")
	if err != nil || row.State != session.StateRunning {
		t.Fatalf("row = %q (%v), want running", row.State, err)
	}
	if leases.acquires != 0 {
		t.Errorf("lease acquires = %d, want 0 for a row that names no pod", leases.acquires)
	}
}
