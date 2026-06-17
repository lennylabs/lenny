// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// routeModifyInterceptor returns a MODIFY that replaces the content with
// the configured bytes. It is external (priority > 100) so it is legal
// at PreRoute and PostRoute.
type routeModifyInterceptor struct {
	priority int32
	out      []byte
}

func (routeModifyInterceptor) Name() string                       { return "route-modifier" }
func (r routeModifyInterceptor) Priority() int32                  { return r.priority }
func (routeModifyInterceptor) Builtin() bool                      { return false }
func (routeModifyInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (routeModifyInterceptor) Timeout() time.Duration             { return 0 }
func (r routeModifyInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: r.out}, nil
}

func newRouteChain(t *testing.T, phase interceptor.Phase, ic interceptor.Interceptor) *interceptor.Chain {
	t.Helper()
	chain := interceptor.NewChain()
	if err := chain.Register(phase, ic); err != nil {
		t.Fatalf("register: %v", err)
	}
	return chain
}

// spec: §4.8 line 1048 — an unwired or empty PreRoute chain admits the
// request unchanged.
func TestRunRouteChainNoChainPassesThrough(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", nil)
	in := routeTaskSpec{TenantID: "acme", RequestedRuntime: "claude-code"}
	out, ok := s.runRouteChain(rec, req, interceptor.PhasePreRoute, in)
	if !ok {
		t.Fatal("empty chain rejected the request")
	}
	if out != in {
		t.Errorf("spec mutated by empty chain: %+v", out)
	}
}

// spec: §4.8 line 1052 — a deliberate REJECT on the PostRoute chain
// returns 403 INTERCEPTOR_REJECTED and aborts the create.
func TestRunRouteChainDeliberateRejectReturns403(t *testing.T) {
	s := &Server{interceptors: newRouteChain(t, interceptor.PhasePostRoute, rejectInterceptor{})}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", nil)
	if _, ok := s.runRouteChain(rec, req, interceptor.PhasePostRoute, routeTaskSpec{TenantID: "acme", ResolvedRuntimeName: "claude-code"}); ok {
		t.Fatal("runRouteChain admitted a REJECT")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	body := decodeEnvelope(t, rec)
	if body.Code != "INTERCEPTOR_REJECTED" {
		t.Errorf("code = %q, want INTERCEPTOR_REJECTED", body.Code)
	}
	if got := body.Details["phase"]; got != string(interceptor.PhasePostRoute) {
		t.Errorf("phase = %v, want PostRoute", got)
	}
}

// spec: §4.8 line 1032, §15.1 line 1008 — a fail-closed timeout/error on
// the PreRoute chain returns 503 INTERCEPTOR_TIMEOUT with the
// interceptor_ref/phase/timeout_ms details.
func TestRunRouteChainTimeoutReturns503(t *testing.T) {
	s := &Server{interceptors: newRouteChain(t, interceptor.PhasePreRoute, failClosedInterceptor{timeout: 60 * time.Millisecond})}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", nil)
	if _, ok := s.runRouteChain(rec, req, interceptor.PhasePreRoute, routeTaskSpec{TenantID: "acme"}); ok {
		t.Fatal("runRouteChain admitted a fail-closed timeout")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeEnvelope(t, rec)
	if body.Code != interceptor.CodeInterceptorTimeout {
		t.Errorf("code = %q, want INTERCEPTOR_TIMEOUT", body.Code)
	}
	if got := body.Details["phase"]; got != string(interceptor.PhasePreRoute) {
		t.Errorf("phase = %v, want PreRoute", got)
	}
	if got, ok := body.Details["timeout_ms"].(float64); !ok || got != 60 {
		t.Errorf("timeout_ms = %v, want 60", body.Details["timeout_ms"])
	}
}

// spec: §4.8 line 1048 — a PreRoute MODIFY may rewrite the runtime hint
// (requested_runtime); runRouteChain returns the rewritten spec.
func TestRunRouteChainModifyRewritesRuntimeHint(t *testing.T) {
	modified, _ := json.Marshal(routeTaskSpec{TenantID: "acme", RequestedRuntime: "claude-code-canary"})
	s := &Server{interceptors: newRouteChain(t, interceptor.PhasePreRoute, routeModifyInterceptor{priority: 150, out: modified})}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", nil)
	out, ok := s.runRouteChain(rec, req, interceptor.PhasePreRoute, routeTaskSpec{TenantID: "acme", RequestedRuntime: "claude-code"})
	if !ok {
		t.Fatalf("runRouteChain rejected a legal MODIFY: status %d", rec.Code)
	}
	if out.RequestedRuntime != "claude-code-canary" {
		t.Errorf("requested_runtime = %q, want claude-code-canary", out.RequestedRuntime)
	}
}

// spec: §4.8 line 1048, line 1060 — a PreRoute MODIFY that alters the
// authenticated tenant_id is rejected by the chain with
// INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION before runRouteChain applies it;
// the helper surfaces it as a 403 carrying that code.
func TestRunRouteChainModifyImmutableTenantRejected(t *testing.T) {
	modified, _ := json.Marshal(routeTaskSpec{TenantID: "globex", RequestedRuntime: "claude-code"})
	s := &Server{interceptors: newRouteChain(t, interceptor.PhasePreRoute, routeModifyInterceptor{priority: 150, out: modified})}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", nil)
	if _, ok := s.runRouteChain(rec, req, interceptor.PhasePreRoute, routeTaskSpec{TenantID: "acme", RequestedRuntime: "claude-code"}); ok {
		t.Fatal("runRouteChain admitted a MODIFY that altered tenant_id")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	body := decodeEnvelope(t, rec)
	if got := body.Details["interceptorCode"]; got != interceptor.CodeInterceptorImmutableFieldViolation {
		t.Errorf("interceptorCode = %v, want %s", got, interceptor.CodeInterceptorImmutableFieldViolation)
	}
}

// spec: §4.8 line 1052, line 1060 — a PostRoute MODIFY that alters the
// resolved_runtime_name is rejected with the immutable-field violation.
func TestRunRouteChainPostRouteModifyResolvedRuntimeRejected(t *testing.T) {
	modified, _ := json.Marshal(routeTaskSpec{TenantID: "acme", ResolvedRuntimeName: "other-runtime"})
	s := &Server{interceptors: newRouteChain(t, interceptor.PhasePostRoute, routeModifyInterceptor{priority: 150, out: modified})}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", nil)
	if _, ok := s.runRouteChain(rec, req, interceptor.PhasePostRoute, routeTaskSpec{TenantID: "acme", ResolvedRuntimeName: "claude-code"}); ok {
		t.Fatal("runRouteChain admitted a MODIFY that altered resolved_runtime_name")
	}
	if body := decodeEnvelope(t, rec); body.Details["interceptorCode"] != interceptor.CodeInterceptorImmutableFieldViolation {
		t.Errorf("interceptorCode = %v, want %s", body.Details["interceptorCode"], interceptor.CodeInterceptorImmutableFieldViolation)
	}
}

// recordingRouteInterceptor records each invocation by name and returns
// ALLOW. It is external (priority > 100) so it is legal at PreRoute.
type recordingRouteInterceptor struct {
	name     string
	priority int32
	calls    *[]string
}

func (r recordingRouteInterceptor) Name() string                     { return r.name }
func (r recordingRouteInterceptor) Priority() int32                  { return r.priority }
func (recordingRouteInterceptor) Builtin() bool                      { return false }
func (recordingRouteInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (recordingRouteInterceptor) Timeout() time.Duration             { return 0 }
func (r recordingRouteInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	*r.calls = append(*r.calls, r.name)
	return interceptor.Result{Action: interceptor.ActionAllow}, nil
}

// spec: §4.8 line 115 — runRouteChainRange splits the PreRoute chain
// around the ExperimentRouter built-in (priority 300): the below segment
// runs only the priority 101–299 interceptors, the at-or-above segment
// runs only the priority ≥ 300 interceptors.
func TestRunRouteChainRangeSplitsAroundExperimentPivot_spec_4_8(t *testing.T) {
	var calls []string
	chain := interceptor.NewChain()
	for _, ic := range []recordingRouteInterceptor{
		{name: "before", priority: 150, calls: &calls},
		{name: "after", priority: 350, calls: &calls},
	} {
		if err := chain.Register(interceptor.PhasePreRoute, ic); err != nil {
			t.Fatalf("register %s: %v", ic.name, err)
		}
	}
	s := &Server{interceptors: chain}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", nil)
	if _, ok := s.runRouteChainRange(rec, req, interceptor.PhasePreRoute,
		routeTaskSpec{TenantID: "acme"}, -2147483648, ExperimentRouterPriority); !ok {
		t.Fatalf("below-pivot segment rejected: status %d", rec.Code)
	}
	if len(calls) != 1 || calls[0] != "before" {
		t.Fatalf("below-pivot calls = %v, want [before]", calls)
	}

	calls = nil
	rec = httptest.NewRecorder()
	if _, ok := s.runRouteChainRange(rec, req, interceptor.PhasePreRoute,
		routeTaskSpec{TenantID: "acme"}, ExperimentRouterPriority, 2147483647); !ok {
		t.Fatalf("at-or-above-pivot segment rejected: status %d", rec.Code)
	}
	if len(calls) != 1 || calls[0] != "after" {
		t.Fatalf("at-or-above-pivot calls = %v, want [after]", calls)
	}
}

// spec: §4.8 line 115 — the at-or-above segment of the PreRoute chain
// may rewrite the runtime hint after experiment routing; runRouteChain
// (full window) is unchanged for PostRoute and the other phases.
func TestRunRouteChainRangeAfterPivotRewritesRuntime_spec_4_8(t *testing.T) {
	modified, _ := json.Marshal(routeTaskSpec{TenantID: "acme", RequestedRuntime: "claude-code-override"})
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreRoute, routeModifyInterceptor{priority: 350, out: modified}); err != nil {
		t.Fatalf("register: %v", err)
	}
	s := &Server{interceptors: chain}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", nil)

	// The below-pivot segment selects no interceptor (the only one is at
	// 350) and passes the spec through unchanged.
	out, ok := s.runRouteChainRange(rec, req, interceptor.PhasePreRoute,
		routeTaskSpec{TenantID: "acme", RequestedRuntime: "claude-code"}, -2147483648, ExperimentRouterPriority)
	if !ok || out.RequestedRuntime != "claude-code" {
		t.Fatalf("below-pivot = %q (ok=%v), want claude-code unchanged", out.RequestedRuntime, ok)
	}
	// The at-or-above segment applies the priority-350 MODIFY.
	out, ok = s.runRouteChainRange(rec, req, interceptor.PhasePreRoute,
		routeTaskSpec{TenantID: "acme", RequestedRuntime: "claude-code"}, ExperimentRouterPriority, 2147483647)
	if !ok || out.RequestedRuntime != "claude-code-override" {
		t.Fatalf("at-or-above-pivot = %q (ok=%v), want claude-code-override", out.RequestedRuntime, ok)
	}
}
