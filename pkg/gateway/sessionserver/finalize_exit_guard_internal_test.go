// SPDX-License-Identifier: MIT

package sessionserver

// spec: 15.1 (finalize precondition), 7.1 (steps 11-13, step 23), 6.2 (finalize timeout), 7.2 (terminal states)
//
// These cases drive handleFinalize's exit writes against a terminal writer
// that overtakes the call. A hook store commits the terminal state directly to
// the wrapped store just before the selected finalize Update runs, so every
// lease release, claim delete, and lifecycle emission a case observes is the
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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// exitHookMode is what an exitHook does on the Update it selects.
type exitHookMode int

const (
	// hookBeforeDelegate runs the callback, then delegates the Update.
	hookBeforeDelegate exitHookMode = iota
	// hookFailNoCommit returns an injected error without committing.
	hookFailNoCommit
	// hookFailAfterCommit delegates and commits, then returns an injected
	// error (an ambiguous commit).
	hookFailAfterCommit
)

var errInjectedStore = errors.New("injected store error")

// exitHook selects the first Update whose mutation, probed on a copy of the
// current row, writes state.
type exitHook struct {
	state  session.State
	mode   exitHookMode
	before func()
	fired  bool
}

// exitHookStore wraps a session store and acts on the Updates its hooks
// select. The probe runs the mutation on a copy of the current row, which is
// safe because the finalize mutations only assign fields.
type exitHookStore struct {
	sessionstore.Store
	mu    sync.Mutex
	hooks []*exitHook
}

func (h *exitHookStore) Update(
	ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error,
) (sessionstore.Session, error) {
	hook := h.selectHook(ctx, tenantID, id, mutate)
	if hook == nil {
		return h.Store.Update(ctx, tenantID, id, mutate)
	}
	switch hook.mode {
	case hookFailNoCommit:
		return sessionstore.Session{}, errInjectedStore
	case hookFailAfterCommit:
		if _, err := h.Store.Update(ctx, tenantID, id, mutate); err != nil {
			return sessionstore.Session{}, err
		}
		return sessionstore.Session{}, errInjectedStore
	default:
		if hook.before != nil {
			hook.before()
		}
		return h.Store.Update(ctx, tenantID, id, mutate)
	}
}

func (h *exitHookStore) selectHook(
	ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error,
) *exitHook {
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
	for _, hk := range h.hooks {
		if !hk.fired && hk.state == probe.State {
			hk.fired = true
			return hk
		}
	}
	return nil
}

// exitFixture is one finalize server over a hook store, a fake-client binder
// holding claim-sbx-1, and a recording credential assigner.
type exitFixture struct {
	srv      *Server
	inner    sessionstore.Store
	hooks    *exitHookStore
	kube     client.Client
	assigner *reclaimRecordingAssigner
	bus      *sessionevents.Bus
	audit    *captureLifecycleAudit
}

const exitPod = "sbx-1"

func newExitFixture(t *testing.T, opts Options) *exitFixture {
	t.Helper()
	const ns = "lenny-agents"
	inner := memstore.New()
	hooks := &exitHookStore{Store: inner}
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName(exitPod), Namespace: ns},
	}
	kube := fake.NewClientBuilder().WithScheme(claimAtCreateScheme(t)).WithObjects(claim).Build()
	assigner := &reclaimRecordingAssigner{}
	bus := sessionevents.NewBus(64)
	audit := &captureLifecycleAudit{}
	opts.Events = bus
	opts.LifecycleAuditSink = audit
	opts.Clock = func() time.Time { return time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC) }
	srv := New(hooks, opts)
	srv.podBinder = &podsession.Binder{Client: kube, Namespace: ns, Credentials: assigner}
	srv.agentNamespace = ns
	return &exitFixture{
		srv: srv, inner: inner, hooks: hooks, kube: kube, assigner: assigner, bus: bus, audit: audit,
	}
}

// seed creates a `created` row. podAssignment and plan are optional.
func (f *exitFixture) seed(t *testing.T, id, podAssignment, plan string, mut func(*sessionstore.Session)) {
	t.Helper()
	row := sessionstore.Session{
		ID: id, TenantID: "default", UserID: "alice", RuntimeRef: "claude-code",
		State: session.StateCreated, PodAssignment: podAssignment,
		CreatedAt: time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC),
	}
	row.UpdatedAt = row.CreatedAt
	if plan != "" {
		row.WorkspacePlan = []byte(plan)
	}
	if mut != nil {
		mut(&row)
	}
	if err := f.inner.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// commitTerminal returns a callback that writes st to the row directly
// through the wrapped store, bypassing every handler.
func (f *exitFixture) commitTerminal(t *testing.T, id string, st session.State) func() {
	return func() {
		if _, err := f.inner.Update(context.Background(), "default", id, func(row *sessionstore.Session) error {
			row.State = st
			return nil
		}); err != nil {
			t.Errorf("commit %s: %v", st, err)
		}
	}
}

func (f *exitFixture) state(t *testing.T, id string) session.State {
	t.Helper()
	row, err := f.inner.Get(context.Background(), "default", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row.State
}

func (f *exitFixture) auditCount(eventType string) int {
	n := 0
	for _, ev := range f.audit.events {
		if ev.EventType == eventType {
			n++
		}
	}
	return n
}

func (f *exitFixture) claimPresent(t *testing.T) bool {
	t.Helper()
	return claimExists(t, f.kube, exitPod)
}

// assertOvertaken checks the 409 a finalize call overtaken by a terminal
// writer returns: INVALID_STATE_TRANSITION with the terminal state and the
// finalize row's allowed states.
func assertOvertaken(t *testing.T, code int, body []byte, want session.State) {
	t.Helper()
	if code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", code, body)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				CurrentState  string   `json:"currentState"`
				AllowedStates []string `json:"allowedStates"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode body: %v; body %s", err, body)
	}
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

// A terminal writer that commits while the prepare phase runs keeps its
// state: the lost ready write answers 409 with that state, emits no ready
// status change and no finalize audit row, revokes the session's lease, and
// deletes no claim.
// spec: 15.1 (finalize row), 6.2 (finalize timeout), 7.2 (terminal states), 7.1 (step 23)
func TestFinalizeReadyWriteLostKeepsTerminalState_spec_15_1(t *testing.T) {
	for _, st := range []session.State{session.StateCancelled, session.StateCompleted, session.StateFailed} {
		t.Run(string(st), func(t *testing.T) {
			f := newExitFixture(t, Options{})
			f.seed(t, "s1", "", "", nil)
			f.hooks.hooks = []*exitHook{{state: session.StateReady, mode: hookBeforeDelegate, before: f.commitTerminal(t, "s1", st)}}

			rr := postFinalize(t, f.srv, "s1", "")

			assertOvertaken(t, rr.Code, rr.Body.Bytes(), st)
			if got := f.state(t, "s1"); got != st {
				t.Errorf("state = %q, want %q kept", got, st)
			}
			for _, ev := range sseEventsOfType(f.bus, "s1", "status_change") {
				var body struct {
					State string `json:"state"`
				}
				_ = json.Unmarshal([]byte(ev.Data), &body)
				if body.State == string(session.StateReady) {
					t.Errorf("status_change to ready emitted for an overtaken call")
				}
			}
			if n := f.auditCount(auditSessionWorkspaceFinalized); n != 0 {
				t.Errorf("finalize audit rows = %d, want 0", n)
			}
			if n := f.auditCount(auditSessionFailed); n != 0 {
				t.Errorf("session.failed rows = %d, want 0", n)
			}
			if !reflect.DeepEqual(f.assigner.released, []string{"s1"}) {
				t.Errorf("ReleaseSession calls = %v, want [s1]", f.assigner.released)
			}
		})
	}
}

// A plan-parse failure whose failure write loses to a terminal writer answers
// 409 with the terminal state, revokes the lease, and does not reclaim the pod
// by name, because the terminal writer's reclaim owns it.
// spec: 15.1 (finalize row), 7.2 (terminal states), 7.1 (step 23)
func TestFinalizePlanParseFailureOvertakenSkipsReclaim_spec_15_1(t *testing.T) {
	f := newExitFixture(t, Options{})
	f.seed(t, "s1", exitPod, "{not a plan", nil)
	f.hooks.hooks = []*exitHook{{
		state: session.StateFailed, mode: hookBeforeDelegate, before: f.commitTerminal(t, "s1", session.StateCancelled),
	}}

	rr := postFinalize(t, f.srv, "s1", "")

	assertOvertaken(t, rr.Code, rr.Body.Bytes(), session.StateCancelled)
	if got := f.state(t, "s1"); got != session.StateCancelled {
		t.Errorf("state = %q, want cancelled kept", got)
	}
	if !f.claimPresent(t) {
		t.Errorf("claim-%s deleted by an overtaken call, want it left to the terminal writer", exitPod)
	}
	if !reflect.DeepEqual(f.assigner.released, []string{"s1"}) {
		t.Errorf("ReleaseSession calls = %v, want [s1]", f.assigner.released)
	}
	if n := f.auditCount(auditSessionFailed); n != 0 {
		t.Errorf("session.failed rows = %d, want 0", n)
	}
}

// A plan-parse failure whose failure write commits fails the row, reclaims the
// pod after the write, and emits one terminal lifecycle.
// spec: 7.1 (steps 11-13), 7.2 (terminal states)
func TestFinalizePlanParseFailureCommittedReclaims_spec_7_1(t *testing.T) {
	f := newExitFixture(t, Options{})
	f.seed(t, "s1", exitPod, "{not a plan", nil)

	rr := postFinalize(t, f.srv, "s1", "")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body %s", rr.Code, rr.Body.String())
	}
	if got := f.state(t, "s1"); got != session.StateFailed {
		t.Errorf("state = %q, want failed", got)
	}
	if f.claimPresent(t) {
		t.Errorf("claim-%s present, want reclaimed", exitPod)
	}
	if n := f.auditCount(auditSessionFailed); n != 1 {
		t.Errorf("session.failed rows = %d, want 1", n)
	}
}

// A plan-parse failure whose failure write returns a store error still
// reclaims the pod and answers 500; the row stays `finalizing` for the
// watchdog.
// spec: 7.1 (steps 11-13), 6.2 (finalize timeout)
func TestFinalizePlanParseFailureStoreErrorReclaims_spec_7_1(t *testing.T) {
	f := newExitFixture(t, Options{})
	f.seed(t, "s1", exitPod, "{not a plan", nil)
	f.hooks.hooks = []*exitHook{{state: session.StateFailed, mode: hookFailNoCommit}}

	rr := postFinalize(t, f.srv, "s1", "")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body %s", rr.Code, rr.Body.String())
	}
	if got := f.state(t, "s1"); got != session.StateFinalizing {
		t.Errorf("state = %q, want finalizing (the hook fired)", got)
	}
	if f.claimPresent(t) {
		t.Errorf("claim-%s present, want reclaimed", exitPod)
	}
}

// A prepare failure whose failure write loses to a terminal writer answers 409
// with the terminal state instead of the prepare-phase error, and revokes the
// session's lease. The pool-resolution failure here is a pre-Prepare exit, so
// prepareAtFinalize's own reclaim released the lease once before the revoke.
// spec: 15.1 (finalize row), 7.2 (terminal states), 7.1 (step 23)
func TestFinalizePrepareFailureOvertakenAnswers409_spec_15_1(t *testing.T) {
	f := newExitFixture(t, Options{})
	f.seed(t, "s1", exitPod, "", nil)
	f.hooks.hooks = []*exitHook{{
		state: session.StateFailed, mode: hookBeforeDelegate, before: f.commitTerminal(t, "s1", session.StateCancelled),
	}}

	rr := postFinalize(t, f.srv, "s1", "")

	assertOvertaken(t, rr.Code, rr.Body.Bytes(), session.StateCancelled)
	if got := f.state(t, "s1"); got != session.StateCancelled {
		t.Errorf("state = %q, want cancelled kept", got)
	}
	if !reflect.DeepEqual(f.assigner.released, []string{"s1", "s1"}) {
		t.Errorf("ReleaseSession calls = %v, want [s1 s1] (pre-Prepare reclaim, then the handler's revoke)",
			f.assigner.released)
	}
	if n := f.auditCount(auditSessionFailed); n != 0 {
		t.Errorf("session.failed rows = %d, want 0", n)
	}
}

// A prepare failure whose failure write commits fails the row, revokes the
// session's lease, and answers the prepare-phase envelope.
// spec: 7.1 (steps 11-13, step 23), 6.2
func TestFinalizePrepareFailureCommittedRevokes_spec_7_1(t *testing.T) {
	f := newExitFixture(t, Options{})
	f.seed(t, "s1", exitPod, "", nil)

	rr := postFinalize(t, f.srv, "s1", "")

	if rr.Code == http.StatusOK || rr.Code == http.StatusConflict {
		t.Fatalf("status = %d, want the prepare-failure envelope; body %s", rr.Code, rr.Body.String())
	}
	if got := f.state(t, "s1"); got != session.StateFailed {
		t.Errorf("state = %q, want failed", got)
	}
	if !reflect.DeepEqual(f.assigner.released, []string{"s1", "s1"}) {
		t.Errorf("ReleaseSession calls = %v, want [s1 s1] (pre-Prepare reclaim, then the handler's revoke)",
			f.assigner.released)
	}
	if n := f.auditCount(auditSessionFailed); n != 1 {
		t.Errorf("session.failed rows = %d, want 1", n)
	}
}

// A ready write that returns a store error without committing, followed by a
// failure write that loses to a terminal writer, answers 409 with the
// terminal state, revokes the lease, and reclaims nothing.
// spec: 15.1 (finalize row), 7.2 (terminal states), 7.1 (step 23)
func TestFinalizeReadyStoreErrorThenTerminalAnswers409_spec_15_1(t *testing.T) {
	f := newExitFixture(t, Options{})
	f.seed(t, "s1", "", "", nil)
	f.hooks.hooks = []*exitHook{
		{state: session.StateReady, mode: hookFailNoCommit},
		{state: session.StateFailed, mode: hookBeforeDelegate, before: f.commitTerminal(t, "s1", session.StateCancelled)},
	}

	rr := postFinalize(t, f.srv, "s1", "")

	assertOvertaken(t, rr.Code, rr.Body.Bytes(), session.StateCancelled)
	if got := f.state(t, "s1"); got != session.StateCancelled {
		t.Errorf("state = %q, want cancelled kept", got)
	}
	if !reflect.DeepEqual(f.assigner.released, []string{"s1"}) {
		t.Errorf("ReleaseSession calls = %v, want [s1]", f.assigner.released)
	}
}

// A ready write that commits and then reports an error leaves the session
// `ready`: the failure write loses to `ready`, the call answers 500 with the
// ready-write error, releases nothing, and emits no terminal lifecycle.
// spec: 15.1 (finalize row), 7.2 (terminal states)
func TestFinalizeAmbiguousReadyCommitKeepsReady_spec_15_1(t *testing.T) {
	f := newExitFixture(t, Options{})
	f.seed(t, "s1", "", "", nil)
	f.hooks.hooks = []*exitHook{{state: session.StateReady, mode: hookFailAfterCommit}}

	rr := postFinalize(t, f.srv, "s1", "")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body %s", rr.Code, rr.Body.String())
	}
	if got := f.state(t, "s1"); got != session.StateReady {
		t.Errorf("state = %q, want ready kept", got)
	}
	if len(f.assigner.released) != 0 {
		t.Errorf("ReleaseSession calls = %v, want none", f.assigner.released)
	}
	if n := f.auditCount(auditSessionFailed); n != 0 {
		t.Errorf("session.failed rows = %d, want 0", n)
	}
}

// A ready write that returns a store error without committing, followed by a
// committed failure write, fails the row and answers 500.
// spec: 7.1 (steps 11-13), 7.2 (terminal states)
func TestFinalizeReadyStoreErrorFailsRow_spec_7_1(t *testing.T) {
	f := newExitFixture(t, Options{})
	f.seed(t, "s1", "", "", nil)
	f.hooks.hooks = []*exitHook{{state: session.StateReady, mode: hookFailNoCommit}}

	rr := postFinalize(t, f.srv, "s1", "")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body %s", rr.Code, rr.Body.String())
	}
	if got := f.state(t, "s1"); got != session.StateFailed {
		t.Errorf("state = %q, want failed", got)
	}
	if n := f.auditCount(auditSessionFailed); n != 1 {
		t.Errorf("session.failed rows = %d, want 1", n)
	}
}

// consumedTracker reports every digest as already consumed and records the
// row state it observed at consume time.
type consumedTracker struct {
	store sessionstore.Store
	id    string
	seen  []session.State
}

func (c *consumedTracker) MarkConsumed(string, time.Time) error {
	if row, err := c.store.Get(context.Background(), "default", c.id); err == nil {
		c.seen = append(c.seen, row.State)
	}
	return uploadtoken.ErrConsumed
}

func (*consumedTracker) IsConsumed(string) bool { return true }

// A failed upload-token consume after the ready write commits is logged, and
// the call still answers 200 with the session `ready`; the pod and lease stay
// with the session.
// spec: 7.1 (single-use uploadToken invalidation), 15.1 (finalize row)
func TestFinalizeConsumeFailureKeepsReady_spec_7_1(t *testing.T) {
	tracker := &consumedTracker{id: "s1"}
	f := newExitFixture(t, Options{UploadTokenVerifier: uploadtoken.NewVerifier(nil, tracker, nil)})
	tracker.store = f.inner
	f.seed(t, "s1", "", "", func(row *sessionstore.Session) {
		row.UploadTokenDigest = "digest-1"
		row.UploadTokenExpiry = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	})

	rr := postFinalize(t, f.srv, "s1", "")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if !reflect.DeepEqual(tracker.seen, []session.State{session.StateReady}) {
		t.Errorf("tracker saw states %v, want [ready]", tracker.seen)
	}
	if got := f.state(t, "s1"); got != session.StateReady {
		t.Errorf("state = %q, want ready", got)
	}
	if len(f.assigner.released) != 0 {
		t.Errorf("ReleaseSession calls = %v, want none", f.assigner.released)
	}
}
