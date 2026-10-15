// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// failClosedInterceptor errors on every call and is fail-closed, so the
// chain surfaces a CodeInterceptorTimeout REJECT.
type failClosedInterceptor struct{ timeout time.Duration }

func (f failClosedInterceptor) Name() string                       { return "slow-classifier" }
func (f failClosedInterceptor) Priority() int32                    { return 150 }
func (f failClosedInterceptor) Builtin() bool                      { return false }
func (f failClosedInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (f failClosedInterceptor) Timeout() time.Duration             { return f.timeout }
func (f failClosedInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return interceptor.Result{}, errors.New("classifier unreachable")
}

// rejectInterceptor returns a deliberate REJECT (no timeout code).
type rejectInterceptor struct{}

func (rejectInterceptor) Name() string                       { return "policy-block" }
func (rejectInterceptor) Priority() int32                    { return 150 }
func (rejectInterceptor) Builtin() bool                      { return false }
func (rejectInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (rejectInterceptor) Timeout() time.Duration             { return 0 }
func (rejectInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return interceptor.Result{Action: interceptor.ActionReject, Reason: "denied"}, nil
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env.Error
}

// spec: §4.8, §15.1 — a fail-closed interceptor
// timeout/error on the session-create path returns 503
// INTERCEPTOR_TIMEOUT (TRANSIENT, retryable) carrying interceptor_ref,
// phase, and timeout_ms, distinct from the 429 a deliberate REJECT gives.
func TestRequirePolicyChainTimeoutReturns503(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePostAuth, failClosedInterceptor{timeout: 75 * time.Millisecond}); err != nil {
		t.Fatalf("register: %v", err)
	}
	s := &Server{interceptors: chain}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)

	if ok := s.requirePolicyChain(rec, req, "acme"); ok {
		t.Fatal("requirePolicyChain admitted a fail-closed timeout")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeEnvelope(t, rec)
	if body.Code != interceptor.CodeInterceptorTimeout {
		t.Errorf("code = %q, want INTERCEPTOR_TIMEOUT", body.Code)
	}
	if !body.Retryable {
		t.Error("retryable = false, want true")
	}
	if got := body.Details["interceptor_ref"]; got != "slow-classifier" {
		t.Errorf("interceptor_ref = %v, want slow-classifier", got)
	}
	if got := body.Details["phase"]; got != string(interceptor.PhasePostAuth) {
		t.Errorf("phase = %v, want PostAuth", got)
	}
	if got, ok := body.Details["timeout_ms"].(float64); !ok || got != 75 {
		t.Errorf("timeout_ms = %v (ok=%v), want 75", body.Details["timeout_ms"], ok)
	}
}

// A deliberate REJECT still returns 429 QUOTA_EXCEEDED, not the 503
// timeout envelope.
func TestRequirePolicyChainDeliberateRejectReturns429(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePostAuth, rejectInterceptor{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	s := &Server{interceptors: chain}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)

	if ok := s.requirePolicyChain(rec, req, "acme"); ok {
		t.Fatal("requirePolicyChain admitted a REJECT")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if body := decodeEnvelope(t, rec); body.Code != "QUOTA_EXCEEDED" {
		t.Errorf("code = %q, want QUOTA_EXCEEDED", body.Code)
	}
}

// ----- resume admission -----

// errCountFailed is the count error the admission tests inject.
var errCountFailed = errors.New("count failed")

// capCountStore is a memstore whose four concurrent-session counts record
// their calls and fail with countErr when it is set.
type capCountStore struct {
	*memstore.Store
	calls    atomic.Int32
	countErr error
	// globalCalls counts CountActiveSessionsGlobal calls; globalFrom, when
	// positive, answers every global count from that call on with
	// globalValue.
	globalCalls atomic.Int32
	globalFrom  int32
	globalValue int
}

func (c *capCountStore) CountActiveSessions(ctx context.Context, tenantID string) (int, error) {
	c.calls.Add(1)
	if c.countErr != nil {
		return 0, c.countErr
	}
	return c.Store.CountActiveSessions(ctx, tenantID)
}

func (c *capCountStore) CountActiveSessionsByUser(ctx context.Context, tenantID, userID string) (int, error) {
	c.calls.Add(1)
	if c.countErr != nil {
		return 0, c.countErr
	}
	return c.Store.CountActiveSessionsByUser(ctx, tenantID, userID)
}

func (c *capCountStore) CountActiveSessionsByRuntime(ctx context.Context, tenantID, runtimeRef string) (int, error) {
	c.calls.Add(1)
	if c.countErr != nil {
		return 0, c.countErr
	}
	return c.Store.CountActiveSessionsByRuntime(ctx, tenantID, runtimeRef)
}

func (c *capCountStore) CountActiveSessionsGlobal(ctx context.Context) (int, error) {
	c.calls.Add(1)
	n := c.globalCalls.Add(1)
	if c.countErr != nil {
		return 0, c.countErr
	}
	if c.globalFrom > 0 && n >= c.globalFrom {
		return c.globalValue, nil
	}
	return c.Store.CountActiveSessionsGlobal(ctx)
}

// limitTenants is a tenant registry whose acme record carries a
// MaxConcurrentSessions the test changes between calls; getErr fails every
// lookup.
type limitTenants struct {
	*tenantstore.Memory
	mu     sync.Mutex
	limits []int
	getErr error
}

// Get answers acme with the next limit in limits; the last one repeats.
func (l *limitTenants) Get(_ context.Context, id string) (tenantstore.Tenant, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.getErr != nil {
		return tenantstore.Tenant{}, l.getErr
	}
	if id != "acme" {
		return tenantstore.Tenant{}, tenantstore.ErrNotFound
	}
	limit := l.limits[0]
	if len(l.limits) > 1 {
		l.limits = l.limits[1:]
	}
	return tenantstore.Tenant{ID: id, MaxConcurrentSessions: limit}, nil
}

// newLimitTenants returns a registry answering acme with limits in order.
func newLimitTenants(limits ...int) *limitTenants {
	return &limitTenants{Memory: tenantstore.NewMemory(), limits: limits}
}

// seedCounted seeds a running session of acme/alice/echo that holds a cap
// slot and no binding.
func seedCounted(t *testing.T, store sessionstore.Store, id string) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: "alice", RuntimeRef: "echo", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// postJSON posts body to path as acme and returns the recorder.
func postJSON(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// assertCapRefusalBody asserts a 429 QUOTA_EXCEEDED envelope carrying the
// message and details of want, the body a refused creation carries.
func assertCapRefusalBody(t *testing.T, rec *httptest.ResponseRecorder, want *CapRefusal) {
	t.Helper()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body %s", rec.Code, rec.Body.String())
	}
	body := decodeEnvelope(t, rec)
	if body.Code != "QUOTA_EXCEEDED" || body.Message != want.Message() {
		t.Errorf("envelope = %s %q, want QUOTA_EXCEEDED %q", body.Code, body.Message, want.Message())
	}
	for k, v := range want.Details() {
		got := body.Details[k]
		if n, ok := got.(float64); ok {
			got = int(n)
		}
		if got != v {
			t.Errorf("details[%s] = %v, want %v", k, body.Details[k], v)
		}
	}
	if len(body.Details) != len(want.Details()) {
		t.Errorf("details = %v, want %v", body.Details, want.Details())
	}
}

// TestAdmitResumeOutcomes_spec_11_1 pins the resume admission check for
// each scope at, below, and without its limit, for an unknown tenant, for a
// tenant lookup error, and for a count error, and that it issues no count
// query when every limit is disabled.
//
// spec: §11.1 (Concurrent-session count), §11.1 (Resume admission)
func TestAdmitResumeOutcomes_spec_11_1(t *testing.T) {
	type setup struct {
		tenants               *limitTenants
		user, runtime, global int
		countErr              error
	}
	row := sessionstore.Session{ID: "target", TenantID: "acme", UserID: "alice", RuntimeRef: "echo", State: session.StateSuspended}
	cases := []struct {
		name    string
		setup   setup
		row     sessionstore.Session
		refusal *CapRefusal
		wantErr error
		noCount bool
	}{
		{name: "every limit disabled", noCount: true},
		{name: "tenant limit zero", setup: setup{tenants: newLimitTenants(0)}, noCount: true},
		{name: "tenant below", setup: setup{tenants: newLimitTenants(3)}},
		{name: "tenant at", setup: setup{tenants: newLimitTenants(2)}, refusal: &CapRefusal{Scope: CapScopeTenant, Limit: 2, Active: 2}},
		{name: "unknown tenant", setup: setup{tenants: newLimitTenants(1)}, row: sessionstore.Session{ID: "x", TenantID: "globex", State: session.StateSuspended}, noCount: true},
		{name: "tenant lookup error", setup: setup{tenants: &limitTenants{Memory: tenantstore.NewMemory(), getErr: errCountFailed}}, wantErr: errCountFailed, noCount: true},
		{name: "tenant count error", setup: setup{tenants: newLimitTenants(5), countErr: errCountFailed}, wantErr: errCountFailed},
		{name: "user below", setup: setup{user: 3}},
		{name: "user at", setup: setup{user: 2}, refusal: &CapRefusal{Scope: CapScopeUser, Limit: 2, Active: 2}},
		{name: "user count error", setup: setup{user: 2, countErr: errCountFailed}, wantErr: errCountFailed},
		{name: "runtime below", setup: setup{runtime: 3}},
		{name: "runtime at", setup: setup{runtime: 1}, refusal: &CapRefusal{Scope: CapScopeRuntime, Limit: 1, Active: 2}},
		{name: "runtime count error", setup: setup{runtime: 2, countErr: errCountFailed}, wantErr: errCountFailed},
		{name: "global below", setup: setup{global: 4}},
		{name: "global at", setup: setup{global: 2}, refusal: &CapRefusal{Scope: CapScopeGlobal, Limit: 2, Active: 2}},
		{name: "global count error", setup: setup{global: 2, countErr: errCountFailed}, wantErr: errCountFailed},
		{name: "tenant refuses before the narrower scopes", setup: setup{tenants: newLimitTenants(2), user: 1, global: 1}, refusal: &CapRefusal{Scope: CapScopeTenant, Limit: 2, Active: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &capCountStore{Store: memstore.New()}
			seedCounted(t, store, "c1")
			seedCounted(t, store, "c2")
			// Suspended and terminal rows hold no slot.
			for i, st := range []session.State{session.StateSuspended, session.StateCompleted} {
				if err := store.Create(context.Background(), sessionstore.Session{
					ID: fmt.Sprintf("n%d", i), TenantID: "acme", UserID: "alice", RuntimeRef: "echo", State: st,
				}); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			store.countErr = tc.setup.countErr
			srv := &Server{
				store: store, maxConcSessPerUser: tc.setup.user,
				maxConcSessPerRuntime: tc.setup.runtime, maxConcSessGlobal: tc.setup.global,
			}
			if tc.setup.tenants != nil {
				srv.tenants = tc.setup.tenants
			}
			target := row
			if tc.row.ID != "" {
				target = tc.row
			}
			err := srv.admitResume(context.Background(), target)
			var refusal *CapRefusal
			switch {
			case tc.refusal != nil:
				if !errors.As(err, &refusal) || *refusal != *tc.refusal {
					t.Fatalf("admitResume = %v, want refusal %+v", err, *tc.refusal)
				}
			case tc.wantErr != nil:
				if errors.As(err, &refusal) || !errors.Is(err, tc.wantErr) {
					t.Fatalf("admitResume = %v, want a non-refusal error wrapping %v", err, tc.wantErr)
				}
			case err != nil:
				t.Fatalf("admitResume = %v, want nil", err)
			}
			if tc.noCount && store.calls.Load() != 0 {
				t.Errorf("count calls = %d, want none", store.calls.Load())
			}
		})
	}
}

// TestCapRefusalBodiesMatchCreation_spec_11_1 pins the refusal body each
// scope carries: the tenant scope names limit and active, and the other
// scopes also name the scope.
//
// spec: §11.1 (Concurrent-session count)
func TestCapRefusalBodiesMatchCreation_spec_11_1(t *testing.T) {
	tenant := &CapRefusal{Scope: CapScopeTenant, Limit: 3, Active: 4}
	if tenant.Message() != "the tenant has reached its concurrent-session limit" {
		t.Errorf("tenant message = %q", tenant.Message())
	}
	if d := tenant.Details(); len(d) != 2 || d["limit"] != 3 || d["active"] != 4 {
		t.Errorf("tenant details = %v", d)
	}
	for _, scope := range []string{CapScopeUser, CapScopeRuntime, CapScopeGlobal} {
		r := &CapRefusal{Scope: scope, Limit: 1, Active: 1}
		if r.Message() != "the "+scope+" concurrent-session limit was reached" {
			t.Errorf("%s message = %q", scope, r.Message())
		}
		if d := r.Details(); len(d) != 3 || d["scope"] != scope || d["limit"] != 1 || d["active"] != 1 {
			t.Errorf("%s details = %v", scope, d)
		}
		if !strings.Contains(r.Error(), scope) {
			t.Errorf("%s Error() = %q, want the scope named", scope, r.Error())
		}
	}
}

// TestResumeEndpointAdmission_spec_11_1 pins POST /resume at a full cap:
// from podless and held-pod suspended it answers 429 QUOTA_EXCEEDED with the
// creation body and leaves the row suspended and the DLQ empty; from
// awaiting_client_action, which already counts, it answers 200 with
// resume_pending; and on a count error it answers 500 and writes nothing.
//
// spec: §11.1 (Concurrent-session count), §11.1 (Resume admission), §15.1
// (REST API)
func TestResumeEndpointAdmission_spec_11_1(t *testing.T) {
	r := newSuspensionRig(t, true)
	r.srv.tenants = newLimitTenants(2)
	seedCounted(t, r.store, "other")
	r.seedPodless(t, "podless")
	r.seedSuspended(t, "held", r.now.Add(-time.Hour))
	if err := r.store.Create(context.Background(), sessionstore.Session{
		ID: "awaiting", TenantID: "acme", State: session.StateAwaitingClientAction,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	want := &CapRefusal{Scope: CapScopeTenant, Limit: 2, Active: 2}
	for _, id := range []string{"podless", "held"} {
		rec := postJSON(t, r.srv, "/v1/sessions/"+id+"/resume", "")
		assertCapRefusalBody(t, rec, want)
		if got := r.get(t, id); got.State != session.StateSuspended {
			t.Errorf("%s state = %q, want suspended", id, got.State)
		}
		if n := r.dlqLen(t, id); n != 0 {
			t.Errorf("%s DLQ = %d, want empty", id, n)
		}
	}
	if code, ans := postResume(t, r.srv, "awaiting"); code != http.StatusOK || ans.State != string(session.StateResumePending) {
		t.Fatalf("awaiting_client_action at the limit = %d %q, want 200 resume_pending", code, ans.State)
	}

	r.srv.store = &capCountStore{Store: r.store.(*memstore.Store), countErr: errCountFailed}
	rec := postJSON(t, r.srv, "/v1/sessions/podless/resume", "")
	if rec.Code != http.StatusInternalServerError || decodeEnvelope(t, rec).Code != "INTERNAL_ERROR" {
		t.Fatalf("count error = %d %s, want 500 INTERNAL_ERROR", rec.Code, rec.Body.String())
	}
	if got := r.get(t, "podless"); got.State != session.StateSuspended {
		t.Errorf("state after a count error = %q, want suspended", got.State)
	}
}

// TestRESTMessageToSuspendedAtCapIsRefused_spec_11_1 pins the REST message
// entry check: a message to a podless or held-pod suspended session at a
// full cap answers 429 QUOTA_EXCEEDED and is neither held nor sent, and a
// count error answers 500 INTERNAL_ERROR with the same outcome.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6), §11.1
// (Concurrent-session count)
func TestRESTMessageToSuspendedAtCapIsRefused_spec_11_1(t *testing.T) {
	const body = `{"messages":[{"role":"user","content":"x"}]}`
	r := newSuspensionRig(t, true)
	r.srv.maxConcSessGlobal = 1
	seedCounted(t, r.store, "other")
	r.seedPodless(t, "podless")
	r.seedSuspended(t, "held", r.now.Add(-time.Hour))
	for _, id := range []string{"podless", "held"} {
		rec := postJSON(t, r.srv, "/v1/sessions/"+id+"/messages", body)
		assertCapRefusalBody(t, rec, &CapRefusal{Scope: CapScopeGlobal, Limit: 1, Active: 1})
		if got := r.get(t, id); got.State != session.StateSuspended || r.dlqLen(t, id) != 0 {
			t.Errorf("%s = %q with DLQ %d, want suspended and nothing held", id, got.State, r.dlqLen(t, id))
		}
	}
	r.srv.store = &capCountStore{Store: r.store.(*memstore.Store), countErr: errCountFailed}
	rec := postJSON(t, r.srv, "/v1/sessions/podless/messages", body)
	if rec.Code != http.StatusInternalServerError || decodeEnvelope(t, rec).Code != "INTERNAL_ERROR" {
		t.Fatalf("count error = %d %s, want 500 INTERNAL_ERROR", rec.Code, rec.Body.String())
	}
	if r.dlqLen(t, "podless") != 0 || r.exec.sends != 0 {
		t.Errorf("after a count error DLQ %d sends %d, want nothing held or sent", r.dlqLen(t, "podless"), r.exec.sends)
	}
}

// TestMessageAdmittedThenRefusedAtResumeIsHeld_spec_7_2 pins the outcome
// when the entry check admits a message and the check at the resume write
// refuses it: the message is held, the answer is queued, and the row stays
// suspended, for a podless and a held-pod target.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6), §11.1
// (Resume admission)
func TestMessageAdmittedThenRefusedAtResumeIsHeld_spec_7_2(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held_pod=%v", held), func(t *testing.T) {
			r := newSuspensionRig(t, true)
			// The entry check counts one session against a global limit of
			// 2; every later check counts two.
			r.srv.maxConcSessGlobal = 2
			r.srv.store = &capCountStore{Store: r.store.(*memstore.Store), globalFrom: 2, globalValue: 2}
			seedCounted(t, r.store, "other")
			if held {
				r.seedSuspended(t, "s1", r.now.Add(-time.Hour))
			} else {
				r.seedPodless(t, "s1")
			}
			if got := postMessage(t, r.srv, "s1"); got != string(session.DeliveryStatusQueued) {
				t.Fatalf("receipt = %q, want queued", got)
			}
			if got := r.get(t, "s1"); got.State != session.StateSuspended {
				t.Errorf("state = %q, want suspended", got.State)
			}
			if n := r.dlqLen(t, "s1"); n != 1 {
				t.Errorf("DLQ = %d, want the message held", n)
			}
			if r.exec.sends != 0 {
				t.Errorf("sends = %d, want none", r.exec.sends)
			}
		})
	}
}

// TestFailureEdgeOutOfSuspendedIsNotAdmissionChecked_spec_11_1 pins that a
// failure report on a held-pod suspended session at a full cap is not a
// resume: it runs no admission check and writes resume_pending.
//
// spec: §11.1 (Resume admission), §7.3 (Retry and Resume)
func TestFailureEdgeOutOfSuspendedIsNotAdmissionChecked_spec_11_1(t *testing.T) {
	store := &capCountStore{Store: memstore.New(), countErr: errCountFailed}
	srv := New(store, Options{Tenants: newLimitTenants(1)})
	defer srv.Close()
	srv.maxConcSessGlobal = 1
	seedCounted(t, store, "other")
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", RuntimeRef: "echo", State: session.StateSuspended, PodAssignment: "pod-1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	disp, err := srv.ReportSessionFailure(context.Background(), FailureReport{TenantID: "acme", SessionID: "s1", Reason: "pod_evicted"})
	if err != nil || disp.To != session.StateResumePending {
		t.Fatalf("ReportSessionFailure = %+v, %v; want resume_pending", disp, err)
	}
	if n := store.calls.Load(); n != 0 {
		t.Errorf("count calls = %d, want none on a failure edge", n)
	}
}

// TestBacklogResumeNotAdmittedStartsNothing_spec_7_2 pins that the backlog
// check of a held-pod suspended session with a held message returns false
// and starts no resume when the cap refuses or the count fails, so the
// suspended sweep's hold timer releases the pod when the hold ends.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6), §11.1
// (Resume admission)
func TestBacklogResumeNotAdmittedStartsNothing_spec_7_2(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(r *suspensionRig)
	}{
		{"cap refuses", func(r *suspensionRig) { r.srv.maxConcSessGlobal = 1 }},
		{"count fails", func(r *suspensionRig) {
			r.srv.maxConcSessGlobal = 5
			r.srv.store = &capCountStore{Store: r.store.(*memstore.Store), countErr: errCountFailed}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newSuspensionRig(t, true)
			seedCounted(t, r.store, "other")
			row := r.seedSuspended(t, "s1", r.now.Add(-time.Hour))
			if _, err := r.coord.EnqueueDLQ(context.Background(), "acme", "s1", sessioninbox.Message{
				MessageID: "m1", Payload: []byte(`{"role":"user","content":"x"}`), EnqueuedAt: r.now,
			}, 0); err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			tc.setup(r)
			if r.srv.ResumeBacklog(context.Background(), row) {
				t.Fatal("ResumeBacklog = true, want false so the hold timer decides the release")
			}
			if r.srv.backlogResumes.has("s1") {
				t.Fatal("a backlog resume started")
			}
			r.srv.store = r.store
			r.srv.maxConcSessGlobal = 1
			// The hold ends: the release runs, and its backlog check is
			// refused too, so the row stays podless suspended with its
			// message held.
			r.srv.ReleaseSuspendedPod(context.Background(), row)
			r.wait(t, "s1")
			got := r.get(t, "s1")
			if got.State != session.StateSuspended || got.PodAssignment != "" || r.exec.count() != 1 {
				t.Fatalf("after the release row = %q on %q with %d releases, want podless suspended and one release",
					got.State, got.PodAssignment, r.exec.count())
			}
			if released, _ := r.metrics(); released != 1 {
				t.Errorf("release events = %d, want 1", released)
			}
			if r.dlqLen(t, "s1") != 1 {
				t.Errorf("DLQ = %d, want the message still held", r.dlqLen(t, "s1"))
			}
		})
	}
}

// TestBacklogRecheckAtCapRetriesAfterSlotFrees_spec_7_2 pins the gateway
// leader's re-check at a full cap: a podless suspended row with a held
// message stays suspended and the pass continues to the next row, and the
// first pass after a slot frees writes resume_pending.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6), §11.1
// (Resume admission)
func TestBacklogRecheckAtCapRetriesAfterSlotFrees_spec_7_2(t *testing.T) {
	r := newSuspensionRig(t, true)
	r.srv.tenants = newLimitTenants(1)
	seedCounted(t, r.store, "other")
	r.seedPodless(t, "a")
	r.seedPodless(t, "b")
	for _, id := range []string{"a", "b"} {
		if _, err := r.coord.EnqueueDLQ(context.Background(), "acme", id, sessioninbox.Message{
			MessageID: "m-" + id, Payload: []byte(`{}`), EnqueuedAt: r.now,
		}, 0); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	lister := &pagingLister{store: r.store.(*memstore.Store)}
	rc := &BacklogRechecker{
		Server: r.srv, Lister: lister,
		Tenants: func(context.Context) ([]string, error) { return []string{"acme"}, nil },
	}
	if n := rc.Pass(context.Background()); n != 0 {
		t.Fatalf("Pass at the cap resumed %d, want 0", n)
	}
	for _, id := range []string{"a", "b"} {
		if got := r.get(t, id); got.State != session.StateSuspended {
			t.Fatalf("%s = %q, want suspended", id, got.State)
		}
	}
	if len(lister.pages) == 0 {
		t.Fatal("the pass listed no page")
	}
	if _, err := r.store.Update(context.Background(), "acme", "other", func(s *sessionstore.Session) error {
		s.State = session.StateCompleted
		return nil
	}); err != nil {
		t.Fatalf("complete other: %v", err)
	}
	r.srv.tenants = newLimitTenants(2)
	if n := rc.Pass(context.Background()); n != 2 {
		t.Fatalf("Pass after slots freed resumed %d, want 2", n)
	}
	for _, id := range []string{"a", "b"} {
		if got := r.get(t, id); got.State != session.StateResumePending {
			t.Errorf("%s = %q, want resume_pending", id, got.State)
		}
	}
}

// TestReleaseBacklogCheckAtCap_spec_11_1 pins the release's backlog check at
// a full cap, from ReleaseSuspendedPod and from SuspendIdle with an inbox
// entry: the pod is released with one release event and no error, the row
// stays podless suspended, and only a later re-check pass after a slot frees
// writes resume_pending.
//
// spec: §11.1 (Resume admission), §6.2 (Pod State Machine), §7.2 (Message
// delivery routing — seven paths, path 6)
func TestReleaseBacklogCheckAtCap_spec_11_1(t *testing.T) {
	for _, path := range []string{"ReleaseSuspendedPod", "SuspendIdle"} {
		t.Run(path, func(t *testing.T) {
			r := newSuspensionRig(t, true)
			r.srv.maxConcSessGlobal = 1
			seedCounted(t, r.store, "other")
			msg := sessioninbox.Message{MessageID: "m1", Payload: []byte(`{}`), EnqueuedAt: r.now}
			var row sessionstore.Session
			if path == "SuspendIdle" {
				row = r.seedRunning(t, "s1")
				if _, err := r.coord.EnqueueInbox(context.Background(), "acme", "s1", msg); err != nil {
					t.Fatalf("enqueue: %v", err)
				}
				r.srv.SuspendIdle(context.Background(), row, 900)
			} else {
				row = r.seedSuspended(t, "s1", r.now.Add(-time.Hour))
				if _, err := r.coord.EnqueueDLQ(context.Background(), "acme", "s1", msg, 0); err != nil {
					t.Fatalf("enqueue: %v", err)
				}
				r.srv.ReleaseSuspendedPod(context.Background(), row)
			}
			r.wait(t, "s1")
			got := r.get(t, "s1")
			if got.State != session.StateSuspended || got.PodAssignment != "" {
				t.Fatalf("row = %q on %q, want podless suspended", got.State, got.PodAssignment)
			}
			if released, _ := r.metrics(); released != 1 || r.exec.count() != 1 {
				t.Fatalf("release events %d releases %d, want 1 and 1", released, r.exec.count())
			}
			rc := &BacklogRechecker{
				Server: r.srv, Lister: &pagingLister{store: r.store.(*memstore.Store)},
				Tenants: func(context.Context) ([]string, error) { return []string{"acme"}, nil },
			}
			if n := rc.Pass(context.Background()); n != 0 || r.get(t, "s1").State != session.StateSuspended {
				t.Fatalf("re-check at the cap resumed %d, state %q; want 0 and suspended", n, r.get(t, "s1").State)
			}
			r.srv.maxConcSessGlobal = 2
			if n := rc.Pass(context.Background()); n != 1 || r.get(t, "s1").State != session.StateResumePending {
				t.Fatalf("re-check after a slot freed resumed %d, state %q; want 1 and resume_pending", n, r.get(t, "s1").State)
			}
		})
	}
}
