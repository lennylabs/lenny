// SPDX-License-Identifier: MIT

package sessionserver_test

// spec: 15.1 (finalize precondition), 7.1 (steps 11-13, step 23), 6.2 (finalize timeout), 7.2 (terminal states)
//
// These cases drive POST /v1/sessions/{id}/finalize through deterministic
// entry and exit interleavings on the pod-bind fixture, so the prepare phase
// runs a real Binder.Prepare against an in-process adapter and mints a §4.9
// lease through recordingLeaseAssigner. A hookStore wraps the session store
// and acts on the finalize Update it selects. Every write that simulates a
// competing writer (a concurrent finalize that won the entry, or a terminal
// writer such as DELETE, terminate, or the finalizing watchdog) goes straight
// to the wrapped inner store and bypasses the handlers, so every lease
// release, claim delete, and lifecycle emission a case observes is the
// finalize handler's own.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// raceSessionID is the session every finalize race case creates.
const raceSessionID = "sess-fin-race"

// raceTenant is the tenant postSessionStep sends in X-Lenny-Tenant-ID.
const raceTenant = "acme"

// hookMode is what a storeHook does on the Update it selects.
type hookMode int

const (
	// hookCallback runs the hook's callback, then delegates the Update.
	hookCallback hookMode = iota
	// hookInjectNoCommit returns errInjectedRaceStore without committing.
	hookInjectNoCommit
	// hookCommitThenInject delegates and commits, then returns
	// errInjectedRaceStore (an ambiguous commit).
	hookCommitThenInject
)

// errInjectedRaceStore is the store error a hook injects.
var errInjectedRaceStore = errors.New("injected store error")

// storeHook selects the Update whose mutation, probed on a copy of the
// current row, writes state for the occurrence-th time.
type storeHook struct {
	state      session.State
	occurrence int
	mode       hookMode
	callback   func()
	fired      bool
}

// hookStore wraps a session store and acts on the Updates its hooks select.
// It selects by the State the mutation writes plus an occurrence count of
// that State, rather than by the Nth Update, because
// applyFinalizePrepareResult adds Updates (which write `finalizing` again)
// only when the prepare phase returned a result. The probe runs the mutation
// on a copy of the current row, which is safe because the finalize mutations
// only assign fields. A probe whose mutation refuses the row counts nothing.
type hookStore struct {
	sessionstore.Store

	// mu guards seen and each hook's fired flag.
	mu    sync.Mutex
	seen  map[session.State]int
	hooks []*storeHook
}

func newHookStore(inner sessionstore.Store) *hookStore {
	return &hookStore{Store: inner, seen: map[session.State]int{}}
}

// arm adds hooks. Arming after /create keeps the create-path Updates from
// ever matching, since those write only `created`.
func (h *hookStore) arm(hooks ...*storeHook) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hooks = append(h.hooks, hooks...)
}

func (h *hookStore) Update(
	ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error,
) (sessionstore.Session, error) {
	hook := h.selectHook(ctx, tenantID, id, mutate)
	if hook == nil {
		return h.Store.Update(ctx, tenantID, id, mutate)
	}
	switch hook.mode {
	case hookInjectNoCommit:
		return sessionstore.Session{}, errInjectedRaceStore
	case hookCommitThenInject:
		if _, err := h.Store.Update(ctx, tenantID, id, mutate); err != nil {
			return sessionstore.Session{}, err
		}
		return sessionstore.Session{}, errInjectedRaceStore
	default:
		if hook.callback != nil {
			hook.callback()
		}
		return h.Store.Update(ctx, tenantID, id, mutate)
	}
}

func (h *hookStore) selectHook(
	ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error,
) *storeHook {
	cur, err := h.Get(ctx, tenantID, id)
	if err != nil {
		return nil
	}
	probe := cur
	if mutate(&probe) != nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seen[probe.State]++
	n := h.seen[probe.State]
	for _, hk := range h.hooks {
		if !hk.fired && hk.state == probe.State && hk.occurrence == n {
			hk.fired = true
			return hk
		}
	}
	return nil
}

// raceLifecycleSink records every session-lifecycle audit event.
type raceLifecycleSink struct {
	mu     sync.Mutex
	events []sessionserver.SessionLifecycleEvent
}

func (s *raceLifecycleSink) EmitSessionLifecycle(_ context.Context, ev sessionserver.SessionLifecycleEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *raceLifecycleSink) count(eventTypes ...string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, ev := range s.events {
		for _, et := range eventTypes {
			if ev.EventType == et {
				n++
			}
		}
	}
	return n
}

// terminalAuditTypes are the §7.2 terminal lifecycle audit event types.
var terminalAuditTypes = []string{"session.completed", "session.failed", "session.cancelled", "session.expired"}

// raceTracker is an uploadtoken.ConsumedTracker whose MarkConsumed records the
// row State it observes and reports the digest as already consumed.
type raceTracker struct {
	mu    sync.Mutex
	store sessionstore.Store
	seen  []session.State
}

func (c *raceTracker) MarkConsumed(string, time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if row, err := c.store.Get(context.Background(), raceTenant, raceSessionID); err == nil {
		c.seen = append(c.seen, row.State)
	}
	return uploadtoken.ErrConsumed
}

func (*raceTracker) IsConsumed(string) bool { return true }

// raceOptions selects the fixture variants a case needs.
type raceOptions struct {
	// failAssignRPC makes the adapter's AssignCredentials RPC fail after
	// recordingLeaseAssigner.AssignProto has recorded the lease.
	failAssignRPC bool
	// tracker, when set, backs the server's upload-token verifier.
	tracker *raceTracker
}

// raceFixture is one finalize server on the pod-bind fixture: an envtest
// cluster holding a warm pool and the idle Sandbox sbx-1, an in-process
// adapter over bufconn, a real podsession.Binder with a recording credential
// assigner, and a hookStore over a memstore.
type raceFixture struct {
	h        http.Handler
	inner    *memstore.Store
	hooks    *hookStore
	cluster  client.Client
	assigner *recordingLeaseAssigner
	bus      *sessionevents.Bus
	audit    *raceLifecycleSink
}

// failAssignCredentialsInterceptor fails the adapter's AssignCredentials RPC
// and passes every other RPC through. It is the injection point for a
// partial credential assignment: Binder.assignCredentials has already
// recorded the lease through AssignProto when the RPC runs.
func failAssignCredentialsInterceptor(
	ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
) (any, error) {
	if info.FullMethod == adapterv1.Adapter_AssignCredentials_FullMethodName {
		return nil, status.Error(codes.Unavailable, "injected AssignCredentials failure")
	}
	return handler(ctx, req)
}

func newRaceFixture(t *testing.T, opt raceOptions) *raceFixture {
	t.Helper()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceBase = t.TempDir()
	adapterSrv.CredentialsDir = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	var grpcOpts []grpc.ServerOption
	if opt.failAssignRPC {
		grpcOpts = append(grpcOpts, grpc.ChainUnaryInterceptor(failAssignCredentialsInterceptor))
	}
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv, grpcOpts...))
	assigner := &recordingLeaseAssigner{}
	binder.Credentials = assigner

	tenants, runtimes, credPools := raceCredentialStores(t)
	inner := memstore.New()
	hooks := newHookStore(inner)
	bus := sessionevents.NewBus(256)
	audit := &raceLifecycleSink{}
	opts := sessionserver.Options{
		IDFunc:                  func() string { return raceSessionID },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
		Events:                  bus,
		LifecycleAuditSink:      audit,
	}
	if opt.tracker != nil {
		opt.tracker.store = inner
		opts.UploadTokenVerifier = uploadtoken.NewVerifier(nil, opt.tracker, nil)
	}
	srv := sessionserver.New(hooks, opts)
	return &raceFixture{
		h: srv.Handler(), inner: inner, hooks: hooks, cluster: cluster,
		assigner: assigner, bus: bus, audit: audit,
	}
}

// raceCredentialStores seeds the tenant policy, runtime, and credential pool
// that make the prepare phase assign one §4.9 lease from pool claude-prod.
func raceCredentialStores(t *testing.T) (tenantstore.Store, runtimestore.Store, credentialpoolstore.Store) {
	t.Helper()
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	policy := credential.CredentialPolicy{
		PreferredSource: credential.PreferredSourcePool,
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
		},
	}
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: raceTenant, CredentialPolicy: policy}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "echo", SupportedProviders: []string{"anthropic_direct"}}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	credPools := credentialpoolstore.NewMemory()
	if err := credPools.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID:              raceTenant,
		Name:                  "claude-prod",
		Provider:              "anthropic_direct",
		MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{
			{ID: "claude-prod-cred-a", SecretRef: "secret-claude-prod", Status: credentialpoolstore.CredentialActive},
		},
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	return tenants, runtimes, credPools
}

// create runs POST /v1/sessions, which claims sbx-1 through claim-sbx-1.
// plan is the create-time WorkspacePlan, or nil for none.
func (f *raceFixture) create(t *testing.T, plan json.RawMessage) {
	t.Helper()
	body, _ := json.Marshal(sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", UserID: "alice@acme.com", WorkspacePlan: plan,
	})
	if rr := postSessionStep(t, f.h, "/v1/sessions", body); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	if !f.claimPresent(t) {
		t.Fatalf("claim-sbx-1 missing after create")
	}
}

// finalize runs POST /v1/sessions/{id}/finalize with body (nil for none).
func (f *raceFixture) finalize(t *testing.T, body []byte) (int, []byte) {
	t.Helper()
	rr := postSessionStep(t, f.h, "/v1/sessions/"+raceSessionID+"/finalize", body)
	return rr.Code, rr.Body.Bytes()
}

// writeRow applies mut to the row directly through the inner store.
func (f *raceFixture) writeRow(t *testing.T, mut func(*sessionstore.Session)) {
	t.Helper()
	if _, err := f.inner.Update(context.Background(), raceTenant, raceSessionID, func(row *sessionstore.Session) error {
		mut(row)
		return nil
	}); err != nil {
		t.Errorf("direct row write: %v", err)
	}
}

// commitState returns a hook callback that commits st (and reason as the
// FailureReason when non-empty) directly through the inner store.
func (f *raceFixture) commitState(t *testing.T, st session.State, reason string) func() {
	return func() {
		f.writeRow(t, func(row *sessionstore.Session) {
			row.State = st
			if reason != "" {
				row.FailureReason = reason
			}
		})
	}
}

// seedMalformedPlan writes WorkspacePlan bytes workspaceplan.ParseStored
// rejects. /create validates the plan it accepts, so the row gets the bytes
// directly; an empty finalize body leaves the handler parsing them.
func (f *raceFixture) seedMalformedPlan(t *testing.T) {
	t.Helper()
	f.writeRow(t, func(row *sessionstore.Session) { row.WorkspacePlan = []byte(`{not a plan`) })
}

func (f *raceFixture) row(t *testing.T) sessionstore.Session {
	t.Helper()
	row, err := f.inner.Get(context.Background(), raceTenant, raceSessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	return row
}

func (f *raceFixture) claimPresent(t *testing.T) bool {
	t.Helper()
	var claim lennyv1.SandboxClaim
	err := f.cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim)
	return err == nil
}

// sseStates returns the states of every status_change event on the bus.
func (f *raceFixture) sseStates() []string {
	var out []string
	for _, ev := range f.bus.History(raceSessionID, 0) {
		if ev.Type != "status_change" {
			continue
		}
		var body struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal([]byte(ev.Data), &body)
		out = append(out, body.State)
	}
	return out
}

func (f *raceFixture) sseCount(eventType string) int {
	n := 0
	for _, ev := range f.bus.History(raceSessionID, 0) {
		if ev.Type == eventType {
			n++
		}
	}
	return n
}

// assertReleased checks recordingLeaseAssigner.released.
func (f *raceFixture) assertReleased(t *testing.T, want []string) {
	t.Helper()
	if !reflect.DeepEqual(f.assigner.released, want) {
		t.Errorf("ReleaseSession calls = %v, want %v", f.assigner.released, want)
	}
}

// assertNoTerminalLifecycle checks that the call emitted no terminal
// lifecycle: no terminal audit row and no session_complete event.
func (f *raceFixture) assertNoTerminalLifecycle(t *testing.T) {
	t.Helper()
	if n := f.audit.count(terminalAuditTypes...); n != 0 {
		t.Errorf("terminal lifecycle audit rows = %d, want 0", n)
	}
	if n := f.sseCount("session_complete"); n != 0 {
		t.Errorf("session_complete events = %d, want 0", n)
	}
}

// raceErrorEnvelope is the §15.1 error envelope a finalize call answers.
type raceErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Details struct {
			CurrentState  string   `json:"currentState"`
			AllowedStates []string `json:"allowedStates"`
		} `json:"details"`
	} `json:"error"`
}

func decodeRaceError(t *testing.T, body []byte) raceErrorEnvelope {
	t.Helper()
	var env raceErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v; body %s", err, body)
	}
	return env
}

// assertRefused checks the 409 INVALID_STATE_TRANSITION a refused or
// overtaken finalize call answers, carrying the locked state and the
// finalize row's allowed states.
func assertRefused(t *testing.T, code int, body []byte, want session.State) {
	t.Helper()
	if code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", code, body)
	}
	env := decodeRaceError(t, body)
	if env.Error.Code != "INVALID_STATE_TRANSITION" {
		t.Errorf("code = %q, want INVALID_STATE_TRANSITION", env.Error.Code)
	}
	if env.Error.Details.CurrentState != string(want) {
		t.Errorf("currentState = %q, want %q", env.Error.Details.CurrentState, want)
	}
	if !reflect.DeepEqual(env.Error.Details.AllowedStates, []string{"created"}) {
		t.Errorf("allowedStates = %v, want [created]", env.Error.Details.AllowedStates)
	}
}

// setupFailPlan is a create-time plan whose setup command exits non-zero,
// which fails the prepare phase in RunSetup.
var setupFailPlan = json.RawMessage(`{"schemaVersion": 1, "setupCommands": [{"cmd":"exit 3"}]}`)

// Two finalize calls both pass the pre-lock read of `created`; the winner
// commits `finalizing` before the loser's entry Update takes the row lock.
// The loser is refused under the lock with 409 and the locked state, binds
// none of its WorkspacePlan, makes no pod RPC, and reclaims nothing.
// spec: 15.1 (finalize precondition), 7.1 (steps 11-13)
func TestFinalizeEntryRefusesOverlappingCall_spec_15_1(t *testing.T) {
	f := newRaceFixture(t, raceOptions{})
	f.create(t, nil)
	f.hooks.arm(&storeHook{
		state: session.StateFinalizing, occurrence: 1, mode: hookCallback,
		callback: f.commitState(t, session.StateFinalizing, ""),
	})
	loserPlan := []byte(`{"schemaVersion":1,"sources":[{"type":"inlineFile","path":"LOSER.md","content":"x","mode":"0644"}]}`)

	code, body := f.finalize(t, loserPlan)

	assertRefused(t, code, body, session.StateFinalizing)
	row := f.row(t)
	if row.State != session.StateFinalizing {
		t.Errorf("state = %q, want the winner's finalizing", row.State)
	}
	if len(row.WorkspacePlan) != 0 {
		t.Errorf("WorkspacePlan = %s, want the loser's plan unbound", row.WorkspacePlan)
	}
	if len(f.assigner.assigns) != 0 {
		t.Errorf("AssignProto calls = %v, want none from a refused call", f.assigner.assigns)
	}
	f.assertNoTerminalLifecycle(t)
	if !f.claimPresent(t) {
		t.Errorf("claim-sbx-1 deleted by a refused call")
	}
}

// A terminal writer that commits between the handler's pre-lock read and its
// entry Update leaves the refused call answering 409 with the terminal state
// and the row unchanged.
// spec: 15.1 (finalize precondition), 7.2 (terminal states)
func TestFinalizeEntryRefusesAfterTerminalWrite_spec_15_1(t *testing.T) {
	f := newRaceFixture(t, raceOptions{})
	f.create(t, nil)
	var committed sessionstore.Session
	commit := f.commitState(t, session.StateCancelled, "")
	f.hooks.arm(&storeHook{
		state: session.StateFinalizing, occurrence: 1, mode: hookCallback,
		callback: func() {
			commit()
			committed = f.row(t)
		},
	})

	code, body := f.finalize(t, nil)

	assertRefused(t, code, body, session.StateCancelled)
	after := f.row(t)
	if after.State != session.StateCancelled {
		t.Errorf("state = %q, want cancelled kept", after.State)
	}
	if !after.UpdatedAt.Equal(committed.UpdatedAt) || len(after.WorkspacePlan) != 0 {
		t.Errorf("row changed by a refused call: terminal write left %+v, after the call %+v", committed, after)
	}
	if len(f.assigner.assigns) != 0 {
		t.Errorf("AssignProto calls = %v, want none", f.assigner.assigns)
	}
}

// A row deleted between the pre-lock read and the entry Update answers 404
// RESOURCE_NOT_FOUND.
// spec: 15.1 (finalize precondition)
func TestFinalizeEntryRowDeletedBeforeLock_spec_15_1(t *testing.T) {
	f := newRaceFixture(t, raceOptions{})
	f.create(t, nil)
	f.hooks.arm(&storeHook{
		state: session.StateFinalizing, occurrence: 1, mode: hookCallback,
		callback: func() {
			if err := f.inner.Delete(context.Background(), raceTenant, raceSessionID); err != nil {
				t.Errorf("delete row: %v", err)
			}
		},
	})

	code, body := f.finalize(t, nil)

	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", code, body)
	}
	if env := decodeRaceError(t, body); env.Error.Code != "RESOURCE_NOT_FOUND" {
		t.Errorf("code = %q, want RESOURCE_NOT_FOUND", env.Error.Code)
	}
}

// A terminal writer that commits while the prepare phase runs keeps its
// state: the lost ready write answers 409 with that state, emits no ready
// status change and no finalize audit row, revokes the session's lease, and
// deletes no claim, because the terminal writer's reclaim owns the pod.
// spec: 15.1 (finalize row), 6.2 (finalize timeout), 7.2 (terminal states), 7.1 (step 23)
func TestFinalizeReadyWriteLossKeepsTerminalState_spec_15_1(t *testing.T) {
	cases := []struct {
		state  session.State
		reason string
	}{
		{session.StateCancelled, ""},
		{session.StateCompleted, ""},
		{session.StateFailed, "FINALIZE_TIMEOUT"},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			f := newRaceFixture(t, raceOptions{})
			f.create(t, nil)
			f.hooks.arm(&storeHook{
				state: session.StateReady, occurrence: 1, mode: hookCallback,
				callback: f.commitState(t, tc.state, tc.reason),
			})

			code, body := f.finalize(t, nil)

			assertRefused(t, code, body, tc.state)
			row := f.row(t)
			if row.State != tc.state || row.FailureReason != tc.reason {
				t.Errorf("state/reason = %q/%q, want %q/%q kept", row.State, row.FailureReason, tc.state, tc.reason)
			}
			for _, st := range f.sseStates() {
				if st == string(session.StateReady) {
					t.Errorf("status_change to ready emitted for an overtaken call")
				}
			}
			if n := f.audit.count("session.finalize_workspace"); n != 0 {
				t.Errorf("session.finalize_workspace rows = %d, want 0", n)
			}
			if !reflect.DeepEqual(f.assigner.assigns, []string{raceSessionID}) {
				t.Errorf("AssignProto calls = %v, want [%s] (the prepare phase ran)", f.assigner.assigns, raceSessionID)
			}
			f.assertReleased(t, []string{raceSessionID})
			if !f.claimPresent(t) {
				t.Errorf("claim-sbx-1 deleted by an overtaken call, want it left to the terminal writer")
			}
		})
	}
}

// A setup failure whose failure write loses to a terminal writer answers 409
// with the terminal state rather than SETUP_COMMAND_FAILED, keeps the
// terminal state, writes no session.failed row, and still records the setup
// failure once.
// spec: 15.1 (finalize row), 7.2 (terminal states), 7.5 (Setup Commands)
func TestFinalizePrepareFailureAfterTerminalWrite_spec_15_1(t *testing.T) {
	f := newRaceFixture(t, raceOptions{})
	f.create(t, setupFailPlan)
	f.hooks.arm(&storeHook{
		state: session.StateFailed, occurrence: 1, mode: hookCallback,
		callback: f.commitState(t, session.StateCancelled, ""),
	})

	code, body := f.finalize(t, nil)

	assertRefused(t, code, body, session.StateCancelled)
	if got := f.row(t).State; got != session.StateCancelled {
		t.Errorf("state = %q, want cancelled kept", got)
	}
	if n := f.audit.count("session.failed"); n != 0 {
		t.Errorf("session.failed rows = %d, want 0", n)
	}
	if n := f.audit.count("session.setup_command_failed"); n != 1 {
		t.Errorf("session.setup_command_failed rows = %d, want 1", n)
	}
}

// A partial credential assignment (the lease is recorded, then the adapter's
// AssignCredentials RPC fails) whose failure write loses to a terminal writer
// answers 409 and revokes the recorded lease. Binder.failPhase releases
// nothing on this path, so only the handler's revoke releases it.
// spec: 15.1 (finalize row), 7.1 (step 23), 4.9
func TestFinalizeCredentialFailureAfterTerminalWriteRevokes_spec_7_1(t *testing.T) {
	f := newRaceFixture(t, raceOptions{failAssignRPC: true})
	f.create(t, nil)
	f.hooks.arm(&storeHook{
		state: session.StateFailed, occurrence: 1, mode: hookCallback,
		callback: f.commitState(t, session.StateCancelled, ""),
	})

	code, body := f.finalize(t, nil)

	assertRefused(t, code, body, session.StateCancelled)
	if !reflect.DeepEqual(f.assigner.assigns, []string{raceSessionID}) {
		t.Errorf("AssignProto calls = %v, want [%s]", f.assigner.assigns, raceSessionID)
	}
	f.assertReleased(t, []string{raceSessionID})
}

// A partial credential assignment whose failure write commits fails the row,
// reclaims the pod through the binder, answers the pod-claim error envelope,
// and revokes the recorded lease the attempt-scoped release omits.
// spec: 7.1 (steps 11-13, step 23), 4.9, 6.2
func TestFinalizeCredentialFailureCommittedRevokes_spec_7_1(t *testing.T) {
	f := newRaceFixture(t, raceOptions{failAssignRPC: true})
	f.create(t, nil)

	code, body := f.finalize(t, nil)

	// The adapter RPC failure is not a lease-assignment error, so
	// writePodClaimError answers it with the retryable setup-window fallback.
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want the 503 pod-claim envelope; body %s", code, body)
	}
	if env := decodeRaceError(t, body); env.Error.Code != "SESSION_CREATION_FAILED" {
		t.Errorf("code = %q, want SESSION_CREATION_FAILED", env.Error.Code)
	}
	if got := f.row(t).State; got != session.StateFailed {
		t.Errorf("state = %q, want failed", got)
	}
	if f.claimPresent(t) {
		t.Errorf("claim-sbx-1 present, want reclaimed by the binder")
	}
	if !reflect.DeepEqual(f.assigner.assigns, []string{raceSessionID}) {
		t.Errorf("AssignProto calls = %v, want [%s]", f.assigner.assigns, raceSessionID)
	}
	f.assertReleased(t, []string{raceSessionID})
}

// A malformed stored plan whose failure write loses to a terminal writer
// answers 409, revokes the lease, and does not reclaim the pod by name.
// spec: 15.1 (finalize row), 7.2 (terminal states), 7.1 (step 23)
func TestFinalizeMalformedPlanAfterTerminalWrite_spec_15_1(t *testing.T) {
	f := newRaceFixture(t, raceOptions{})
	f.create(t, nil)
	f.seedMalformedPlan(t)
	f.hooks.arm(&storeHook{
		state: session.StateFailed, occurrence: 1, mode: hookCallback,
		callback: f.commitState(t, session.StateCancelled, ""),
	})

	code, body := f.finalize(t, nil)

	assertRefused(t, code, body, session.StateCancelled)
	if !f.claimPresent(t) {
		t.Errorf("claim-sbx-1 deleted by an overtaken call, want it left to the terminal writer")
	}
	f.assertReleased(t, []string{raceSessionID})
}

// A malformed stored plan whose failure write commits fails the row, reclaims
// the pod after the write, answers 500, and emits one terminal lifecycle.
// spec: 7.1 (steps 11-13), 7.2 (terminal states)
func TestFinalizeMalformedPlanCommittedReclaimsPod_spec_7_1(t *testing.T) {
	f := newRaceFixture(t, raceOptions{})
	f.create(t, nil)
	f.seedMalformedPlan(t)

	code, body := f.finalize(t, nil)

	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body %s", code, body)
	}
	if got := f.row(t).State; got != session.StateFailed {
		t.Errorf("state = %q, want failed", got)
	}
	if f.claimPresent(t) {
		t.Errorf("claim-sbx-1 present, want reclaimed")
	}
	if n := f.audit.count(terminalAuditTypes...); n != 1 {
		t.Errorf("terminal lifecycle audit rows = %d, want 1", n)
	}
}

// The single-use upload-token consume runs after the ready write commits; a
// consume that reports the digest already invalidated is logged, and the call
// answers 200 with the session `ready`, keeping its pod and lease.
// spec: 7.1 (single-use uploadToken invalidation), 15.1 (finalize row)
func TestFinalizeConsumeAfterReadyWrite_spec_7_1(t *testing.T) {
	tracker := &raceTracker{}
	f := newRaceFixture(t, raceOptions{tracker: tracker})
	f.create(t, nil)
	f.writeRow(t, func(row *sessionstore.Session) {
		row.UploadTokenDigest = "digest-1"
		row.UploadTokenExpiry = time.Now().Add(time.Hour).UTC()
	})

	code, body := f.finalize(t, nil)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", code, body)
	}
	tracker.mu.Lock()
	seen := append([]session.State(nil), tracker.seen...)
	tracker.mu.Unlock()
	if !reflect.DeepEqual(seen, []session.State{session.StateReady}) {
		t.Errorf("tracker saw states %v, want [ready]", seen)
	}
	if got := f.row(t).State; got != session.StateReady {
		t.Errorf("state = %q, want ready", got)
	}
	if !f.claimPresent(t) {
		t.Errorf("claim-sbx-1 deleted, want the session to keep its pod")
	}
	f.assertReleased(t, nil)
}

// A ready write that returns a store error without committing, followed by a
// failure write that loses to a terminal writer, answers 409 with the
// terminal state, revokes the lease, and reclaims nothing.
// spec: 15.1 (finalize row), 7.2 (terminal states), 7.1 (step 23)
func TestFinalizeReadyStoreErrorFailureWriteLost_spec_15_1(t *testing.T) {
	f := newRaceFixture(t, raceOptions{})
	f.create(t, nil)
	f.hooks.arm(
		&storeHook{state: session.StateReady, occurrence: 1, mode: hookInjectNoCommit},
		&storeHook{
			state: session.StateFailed, occurrence: 1, mode: hookCallback,
			callback: f.commitState(t, session.StateCancelled, ""),
		},
	)

	code, body := f.finalize(t, nil)

	assertRefused(t, code, body, session.StateCancelled)
	if got := f.row(t).State; got != session.StateCancelled {
		t.Errorf("state = %q, want cancelled kept", got)
	}
	f.assertReleased(t, []string{raceSessionID})
	if !f.claimPresent(t) {
		t.Errorf("claim-sbx-1 deleted by an overtaken call, want it left to the terminal writer")
	}
}

// A ready write that commits and then reports an error leaves the session
// `ready`: the failure write loses to `ready`, and the call answers 500 with
// the ready-write error, releasing and reclaiming nothing and emitting no
// terminal lifecycle.
// spec: 15.1 (finalize row), 7.2 (terminal states)
func TestFinalizeReadyAmbiguousCommitKeepsReady_spec_15_1(t *testing.T) {
	f := newRaceFixture(t, raceOptions{})
	f.create(t, nil)
	f.hooks.arm(&storeHook{state: session.StateReady, occurrence: 1, mode: hookCommitThenInject})

	code, body := f.finalize(t, nil)

	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body %s", code, body)
	}
	if got := f.row(t).State; got != session.StateReady {
		t.Errorf("state = %q, want ready kept", got)
	}
	if !f.claimPresent(t) {
		t.Errorf("claim-sbx-1 deleted, want the ready session to keep its pod")
	}
	f.assertReleased(t, nil)
	f.assertNoTerminalLifecycle(t)
}

// A failure write that returns a store error without committing leaves the
// row `finalizing` for the §6.2 watchdog. The plan-parse and ready-write
// branches still reclaim the pod, and the prepare-failure branch still
// answers its own envelope and revokes the lease. A store error is not a
// lost write.
// spec: 7.1 (steps 11-13, step 23), 6.2 (finalize timeout)
func TestFinalizeFailureWriteStoreError_spec_6_2(t *testing.T) {
	t.Run("plan parse", func(t *testing.T) {
		f := newRaceFixture(t, raceOptions{})
		f.create(t, nil)
		f.seedMalformedPlan(t)
		f.hooks.arm(&storeHook{state: session.StateFailed, occurrence: 1, mode: hookInjectNoCommit})

		code, body := f.finalize(t, nil)

		if code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body %s", code, body)
		}
		if got := f.row(t).State; got != session.StateFinalizing {
			t.Errorf("state = %q, want finalizing (the hook fired)", got)
		}
		if f.claimPresent(t) {
			t.Errorf("claim-sbx-1 present, want reclaimed")
		}
	})
	t.Run("prepare failure", func(t *testing.T) {
		f := newRaceFixture(t, raceOptions{})
		f.create(t, setupFailPlan)
		f.hooks.arm(&storeHook{state: session.StateFailed, occurrence: 1, mode: hookInjectNoCommit})

		code, body := f.finalize(t, nil)

		if code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body %s", code, body)
		}
		if env := decodeRaceError(t, body); env.Error.Code != "SETUP_COMMAND_FAILED" {
			t.Errorf("code = %q, want SETUP_COMMAND_FAILED", env.Error.Code)
		}
		if got := f.row(t).State; got != session.StateFinalizing {
			t.Errorf("state = %q, want finalizing (the hook fired)", got)
		}
		f.assertReleased(t, []string{raceSessionID})
	})
	t.Run("ready write store error", func(t *testing.T) {
		f := newRaceFixture(t, raceOptions{})
		f.create(t, nil)
		f.hooks.arm(
			&storeHook{state: session.StateReady, occurrence: 1, mode: hookInjectNoCommit},
			&storeHook{state: session.StateFailed, occurrence: 1, mode: hookInjectNoCommit},
		)

		code, body := f.finalize(t, nil)

		if code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body %s", code, body)
		}
		if got := f.row(t).State; got != session.StateFinalizing {
			t.Errorf("state = %q, want finalizing (the hook fired)", got)
		}
		if f.claimPresent(t) {
			t.Errorf("claim-sbx-1 present, want reclaimed")
		}
	})
}

// An unraced finalize reaches `ready` with the 200 session response, and the
// session keeps its pod and lease.
// spec: 15.1 (finalize precondition), 7.1 (steps 11-13)
func TestFinalizeUnracedReachesReady_spec_7_1(t *testing.T) {
	f := newRaceFixture(t, raceOptions{})
	f.create(t, nil)

	code, body := f.finalize(t, nil)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", code, body)
	}
	var resp sessionserver.SessionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.State != string(session.StateReady) {
		t.Errorf("response state = %q, want ready", resp.State)
	}
	if got := f.row(t).State; got != session.StateReady {
		t.Errorf("row state = %q, want ready", got)
	}
	if !f.claimPresent(t) {
		t.Errorf("claim-sbx-1 deleted, want the session to keep its pod")
	}
	f.assertReleased(t, nil)
}
