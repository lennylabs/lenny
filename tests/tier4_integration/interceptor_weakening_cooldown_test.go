// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.8 / §8.3 SEC-013 interceptor
// fail-policy weakening cooldown, driven as a composed cross-cutting
// sequence rather than in isolated unit slices. It wires the real admin
// interceptor-registry HTTP handler, the real interceptor registry
// store, the real delegation service (its InterceptorCooldown resolver
// reading that same store), and the real platform MCP tool dispatch
// (lenny/delegate_task and lenny/send_message), all sharing one
// interceptor registry and one injected clock. The sequence exercised
// end to end is: an admin weakens an interceptor's failPolicy from
// fail-closed to fail-open through the running admin API; the write
// emits the interceptor.fail_policy_weakened and
// interceptor.weakening_cooldown_active audit events and arms the
// mandatory cooldown; every delegate_task and send_message whose
// effective DelegationPolicy names that interceptor is then rejected
// with INTERCEPTOR_WEAKENING_COOLDOWN for the window; after the clock
// advances past the cooldown both calls succeed; and a subsequent
// strengthening (fail-open to fail-closed) emits
// interceptor.fail_policy_strengthened and lifts the cooldown
// immediately with no window.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/delegation/cycle"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// weakeningClock is a mutable, advanceable clock shared by the admin
// router (which mints the server-side transition timestamp) and the
// delegation service (which computes now - transition_ts against the
// same instant). Advancing it past the cooldown is what makes the
// window-expiry assertion deterministic without a wall-clock wait.
type weakeningClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *weakeningClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *weakeningClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// recordingAdminAudit captures admin audit events so the test can assert
// the §11.7 interceptor.fail_policy_* and interceptor.weakening_cooldown_active
// events the weaken/strengthen writes emit.
type recordingAdminAudit struct {
	mu     sync.Mutex
	events []admin.AuditEvent
}

func (r *recordingAdminAudit) EmitAdminEvent(_ context.Context, ev admin.AuditEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingAdminAudit) find(t *testing.T, eventType string) admin.AuditEvent {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if ev.Type == eventType {
			return ev
		}
	}
	t.Fatalf("no %q audit event recorded (events so far: %v)", eventType, r.eventTypes())
	return admin.AuditEvent{}
}

func (r *recordingAdminAudit) has(eventType string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if ev.Type == eventType {
			return true
		}
	}
	return false
}

func (r *recordingAdminAudit) eventTypes() []string {
	out := make([]string, 0, len(r.events))
	for _, ev := range r.events {
		out = append(out, ev.Type)
	}
	return out
}

// allowScanner is an external (non-built-in) PreMessageDelivery content
// scanner named "scan" that always ALLOWs. It stands in for the deployer
// scanner a DelegationPolicy names via contentPolicy.interceptorRef, so
// that once the cooldown expires the policy-scoped scan at
// PreMessageDelivery resolves and send_message delivers instead of
// failing closed on an unresolvable ref.
type allowScanner struct{ name string }

func (s allowScanner) Name() string                       { return s.name }
func (s allowScanner) Priority() int32                    { return 500 }
func (s allowScanner) Builtin() bool                      { return false }
func (s allowScanner) FailPolicy() interceptor.FailPolicy { return interceptor.FailOpen }
func (s allowScanner) Timeout() time.Duration             { return 0 }
func (s allowScanner) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return interceptor.Result{Action: interceptor.ActionAllow}, nil
}

const (
	weakeningTenant       = "acme"
	weakeningInterceptor  = "scan"
	weakeningPolicy       = "scanpol"
	weakeningCooldownSecs = 60
)

// spec: §4.8 line 1058 — "the weakening transition additionally triggers
// a mandatory cooldown during which affected `delegate_task` and
// `lenny/send_message` calls reject with `INTERCEPTOR_WEAKENING_COOLDOWN`
// ... The reverse change (`fail-open` to `fail-closed`) emits an
// `interceptor.fail_policy_strengthened` audit event ... and is **not**
// subject to the cooldown — tightening posture takes effect immediately."
// §8.3 rule 5 — "During `gateway.interceptorWeakeningCooldownSeconds`
// ... following such a transition, the gateway rejects every
// `delegate_task` and `lenny/send_message` call whose effective
// `DelegationPolicy` references the affected interceptor with
// `INTERCEPTOR_WEAKENING_COOLDOWN`."
//
// diagnosis: the composed weakening-cooldown sequence regressed across a
// component boundary. The admin write path, the interceptor registry
// store, the delegation-service cooldown resolver, and the two MCP tool
// surfaces are each unit-covered in isolation; this test fails when they
// stop agreeing end to end — e.g. the admin weaken no longer arms the
// store the delegation service reads, delegate_task or send_message stop
// consulting the cooldown, the window never expires on clock advance, or
// a strengthening does not lift the cooldown immediately.
func TestInterceptorWeakeningCooldownComposed_spec_4_8_1058(t *testing.T) {
	ctx := context.Background()
	clk := &weakeningClock{t: time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)}

	// ---- shared real stores ----
	sessions := memstore.New()
	runtimes := runtimestore.NewMemory()
	pols := delegationpolicystore.NewMemory()
	ics := interceptorstore.NewMemory()

	// The delegation target `worker` names the DelegationPolicy whose
	// contentPolicy.interceptorRef points at the weakened interceptor; the
	// parent runs a distinct runtime so the §8.2 cycle detector admits the
	// hop.
	for _, rt := range []runtimestore.Runtime{
		{Name: "claude", Image: "lenny/claude@sha256:abc"},
		{Name: "worker", Image: "lenny/worker@sha256:def", DelegationPolicyRef: weakeningPolicy},
	} {
		if err := runtimes.Create(ctx, rt); err != nil {
			t.Fatalf("seed runtime %s: %v", rt.Name, err)
		}
	}
	if err := pols.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID:      weakeningTenant,
		Name:          weakeningPolicy,
		ContentPolicy: delegationpolicystore.ContentPolicy{InterceptorRef: weakeningInterceptor},
	}); err != nil {
		t.Fatalf("seed delegation policy: %v", err)
	}

	// A routable root id so the delegation service mints valid child ids.
	parentID := session.NewID()
	const targetID = "sess_target"
	for _, s := range []sessionstore.Session{
		{ID: parentID, TenantID: weakeningTenant, UserID: "user_alice", RuntimeRef: "claude", PoolRef: "pool-a", State: session.StateRunning, IsolationProfile: isolation.ProfileSandboxed, CreatedAt: clk.now(), UpdatedAt: clk.now()},
		{ID: targetID, TenantID: weakeningTenant, UserID: "user_alice", RuntimeRef: "worker", PoolRef: "pool-b", State: session.StateRunning, IsolationProfile: isolation.ProfileSandboxed, CreatedAt: clk.now(), UpdatedAt: clk.now()},
	} {
		if err := sessions.Create(ctx, s); err != nil {
			t.Fatalf("seed session %s: %v", s.ID, err)
		}
	}

	// ---- real admin interceptor-registry API sharing the store + clock ----
	auditRec := &recordingAdminAudit{}
	adminRouter := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: clk.now,
		Audit: auditRec,
	}).WithInterceptors(ics, weakeningCooldownSecs).WithDelegationPolicies(pols)
	adminH := adminRouter.Handler()

	// ---- real delegation service whose cooldown resolver reads the same store ----
	svc := delegation.NewService(sessions, delegation.Options{
		Clock:                        clk.now,
		Runtimes:                     runtimes,
		Policies:                     pols,
		CycleMode:                    cycle.ModeEnforce,
		InterceptorCooldown:          interceptorstore.NewCooldownResolver(ics),
		InterceptorWeakeningCooldown: weakeningCooldownSecs * time.Second,
	})

	// ---- real MCP tool dispatch: delegate_task + send_message ----
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreMessageDelivery, allowScanner{name: weakeningInterceptor}); err != nil {
		t.Fatalf("register PreMessageDelivery scanner: %v", err)
	}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:           sessions,
		Executor:        executor.NewEchoExecutor(),
		Delegation:      svc,
		Runtimes:        runtimes,
		Interceptors:    chain,
		ContentPolicies: svc,
		CooldownChecker: svc,
		Clock:           clk.now,
		TenantID:        weakeningTenant,
	})

	// ---- register the interceptor (fail-closed) through the admin API ----
	if rr := weakenAdminReq(t, adminH, http.MethodPost, "/v1/admin/interceptors", map[string]any{
		"name":       weakeningInterceptor,
		"endpoint":   "scanner.acme.svc:9000",
		"priority":   500,
		"failPolicy": "fail-closed",
		"timeoutMs":  500,
		"phases":     []string{"PreDelegation", "PreMessageDelivery"},
	}); rr.Code != http.StatusCreated {
		t.Fatalf("create interceptor: status %d, body %s", rr.Code, rr.Body.String())
	}

	// Baseline: with the interceptor fail-closed no cooldown is armed, so a
	// delegate_task admits. This proves the gate under test — not the
	// surrounding wiring — is what blocks once weakened.
	if code, _ := dispatchDelegateWeakening(t, srv, parentID); code != "" {
		t.Fatalf("baseline delegate_task before weakening returned error %q, want success", code)
	}

	// ---- weaken failPolicy fail-closed -> fail-open through the admin API ----
	etag := interceptorETag(t, adminH)
	weaken := map[string]any{
		"name":       weakeningInterceptor,
		"endpoint":   "scanner.acme.svc:9000",
		"priority":   500,
		"failPolicy": "fail-open",
		"timeoutMs":  500,
		"phases":     []string{"PreDelegation", "PreMessageDelivery"},
	}
	if rr := weakenAdminReqWithETag(t, adminH, http.MethodPut, "/v1/admin/interceptors/"+weakeningInterceptor, weaken, etag); rr.Code != http.StatusOK {
		t.Fatalf("weaken interceptor: status %d, body %s", rr.Code, rr.Body.String())
	}

	// The weaken write emits the §11.7 audit events and arms the store.
	weakened := auditRec.find(t, "interceptor.fail_policy_weakened")
	if weakened.Detail["new_fail_policy"] != "fail-open" {
		t.Errorf("fail_policy_weakened new_fail_policy = %v, want fail-open", weakened.Detail["new_fail_policy"])
	}
	if weakened.Detail["interceptor_ref"] != weakeningInterceptor {
		t.Errorf("fail_policy_weakened interceptor_ref = %v, want %s", weakened.Detail["interceptor_ref"], weakeningInterceptor)
	}
	if got, _ := weakened.Detail["affected_policy_count"].(int); got != 1 {
		t.Errorf("fail_policy_weakened affected_policy_count = %v, want 1", weakened.Detail["affected_policy_count"])
	}
	if !auditRec.has("interceptor.weakening_cooldown_active") {
		t.Errorf("weakening did not emit interceptor.weakening_cooldown_active (events: %v)", auditRec.eventTypes())
	}
	if row, err := ics.Get(ctx, weakeningInterceptor); err != nil || row.FailOpenTransitionAt.IsZero() || row.CooldownSecondsAtTransition != weakeningCooldownSecs {
		t.Fatalf("store not armed after weaken: row=%+v err=%v", row, err)
	}

	// ---- inside the window: delegate_task and send_message are rejected ----
	code, details := dispatchDelegateWeakening(t, srv, parentID)
	if code != "INTERCEPTOR_WEAKENING_COOLDOWN" {
		t.Fatalf("delegate_task inside cooldown: code = %q, want INTERCEPTOR_WEAKENING_COOLDOWN", code)
	}
	if details["interceptorRef"] != weakeningInterceptor {
		t.Errorf("delegate_task cooldown details.interceptorRef = %v, want %s", details["interceptorRef"], weakeningInterceptor)
	}

	sendCode, _ := dispatchSendMessageWeakening(t, srv, targetID)
	if sendCode != "INTERCEPTOR_WEAKENING_COOLDOWN" {
		t.Fatalf("send_message inside cooldown: code = %q, want INTERCEPTOR_WEAKENING_COOLDOWN", sendCode)
	}

	// ---- past the window: both calls succeed ----
	clk.advance(time.Duration(weakeningCooldownSecs+1) * time.Second)
	if code, _ := dispatchDelegateWeakening(t, srv, parentID); code != "" {
		t.Fatalf("delegate_task after cooldown expiry returned error %q, want success", code)
	}
	if code, _ := dispatchSendMessageWeakening(t, srv, targetID); code != "" {
		t.Fatalf("send_message after cooldown expiry returned error %q, want success", code)
	}

	// ---- strengthening (fail-open -> fail-closed) emits the audit event
	// and clears the armed transition ----
	// The interceptor is currently fail-open (its first window has expired).
	strengthen := map[string]any{
		"name":       weakeningInterceptor,
		"endpoint":   "scanner.acme.svc:9000",
		"priority":   500,
		"failPolicy": "fail-closed",
		"timeoutMs":  500,
		"phases":     []string{"PreDelegation", "PreMessageDelivery"},
	}
	etag = interceptorETag(t, adminH)
	if rr := weakenAdminReqWithETag(t, adminH, http.MethodPut, "/v1/admin/interceptors/"+weakeningInterceptor, strengthen, etag); rr.Code != http.StatusOK {
		t.Fatalf("strengthen interceptor: status %d, body %s", rr.Code, rr.Body.String())
	}
	auditRec.find(t, "interceptor.fail_policy_strengthened")
	if row, err := ics.Get(ctx, weakeningInterceptor); err != nil || !row.FailOpenTransitionAt.IsZero() || row.CooldownSecondsAtTransition != 0 {
		t.Fatalf("cooldown not cleared after strengthen: row=%+v err=%v", row, err)
	}

	// ---- a strengthening lifts a fresh cooldown immediately, with no window ----
	// Re-arm a fresh fail-closed -> fail-open transition at the current
	// clock; a delegate is inside the new window and rejects.
	etag = interceptorETag(t, adminH)
	if rr := weakenAdminReqWithETag(t, adminH, http.MethodPut, "/v1/admin/interceptors/"+weakeningInterceptor, weaken, etag); rr.Code != http.StatusOK {
		t.Fatalf("re-arm weaken interceptor: status %d, body %s", rr.Code, rr.Body.String())
	}
	if code, _ := dispatchDelegateWeakening(t, srv, parentID); code != "INTERCEPTOR_WEAKENING_COOLDOWN" {
		t.Fatalf("delegate_task inside re-armed cooldown: code = %q, want INTERCEPTOR_WEAKENING_COOLDOWN", code)
	}
	// Strengthen again without advancing the clock: tightening is not
	// subject to the cooldown, so the delegate blocked moments ago now
	// admits immediately.
	etag = interceptorETag(t, adminH)
	if rr := weakenAdminReqWithETag(t, adminH, http.MethodPut, "/v1/admin/interceptors/"+weakeningInterceptor, strengthen, etag); rr.Code != http.StatusOK {
		t.Fatalf("second strengthen interceptor: status %d, body %s", rr.Code, rr.Body.String())
	}
	if code, _ := dispatchDelegateWeakening(t, srv, parentID); code != "" {
		t.Fatalf("delegate_task after strengthen returned error %q, want immediate success", code)
	}
}

// dispatchDelegateWeakening invokes lenny/delegate_task with an empty
// task input (so the PreDelegation content scan is skipped and the
// delegation service's own cooldown gate is the sole blocker). It returns
// the lenny error code (empty on success) and the error details.
func dispatchDelegateWeakening(t *testing.T, srv *mcp.Server, parentID string) (string, map[string]any) {
	t.Helper()
	res, ok, err := srv.DispatchTool(context.Background(), "lenny/delegate_task",
		json.RawMessage(`{"parentSessionId":"`+parentID+`","target":"worker"}`))
	if err != nil || !ok {
		t.Fatalf("DispatchTool(delegate_task) = (ok=%v, err=%v)", ok, err)
	}
	return toolResultErrorWeakening(t, res)
}

// dispatchSendMessageWeakening invokes lenny/send_message against the
// target session and returns the lenny error code (empty on success) and
// the error details.
func dispatchSendMessageWeakening(t *testing.T, srv *mcp.Server, targetID string) (string, map[string]any) {
	t.Helper()
	res, ok, err := srv.DispatchTool(context.Background(), "lenny/send_message",
		json.RawMessage(`{"to":"`+targetID+`","message":"ping"}`))
	if err != nil || !ok {
		t.Fatalf("DispatchTool(send_message) = (ok=%v, err=%v)", ok, err)
	}
	return toolResultErrorWeakening(t, res)
}

// toolResultErrorWeakening extracts the lenny error code and details from
// a DispatchTool result. It returns ("", nil) when the result is not an
// error.
func toolResultErrorWeakening(t *testing.T, res mcp.ToolResult) (string, map[string]any) {
	t.Helper()
	if !res.IsError {
		return "", nil
	}
	for _, c := range res.Content {
		var env struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		}
		if json.Unmarshal([]byte(c.Text), &env) == nil && env.Code != "" {
			return env.Code, env.Details
		}
	}
	t.Fatalf("error tool result carried no lenny error envelope: %+v", res)
	return "", nil
}

// weakenAdminReq issues a platform-admin request against the admin router
// handler and returns the recorder.
func weakenAdminReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return weakenAdminReqWithETag(t, h, method, path, body, "")
}

// weakenAdminReqWithETag is weakenAdminReq with an optional If-Match
// header (required by the admin PUT precondition).
func weakenAdminReqWithETag(t *testing.T, h http.Handler, method, path string, body any, etag string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@acme.com",
		TenantID: "platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// interceptorETag GETs the interceptor and returns its current ETag for
// the If-Match precondition on the next PUT.
func interceptorETag(t *testing.T, h http.Handler) string {
	t.Helper()
	rr := weakenAdminReq(t, h, http.MethodGet, "/v1/admin/interceptors/"+weakeningInterceptor, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get interceptor for etag: status %d, body %s", rr.Code, rr.Body.String())
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("interceptor GET returned no ETag")
	}
	return etag
}
