// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

const (
	rcRequested  = "rc-requested"
	rcExperiment = "rc-experiment"
	rcFinal      = "rc-final"
)

// rcRoutes names both session-creation routes, each of which must run the
// same §4.8 route chains.
var rcRoutes = []string{"/v1/sessions", "/v1/sessions/start"}

// routeStep is an external §4.8 interceptor that records the payload it saw
// and runs a configured behavior.
type routeStep struct {
	name     string
	priority int32
	run      func(routeTaskSpec) (interceptor.Result, error)
	log      *[]routeStepCall
}

// routeStepCall is one recorded routeStep invocation.
type routeStepCall struct {
	name string
	spec routeTaskSpec
}

func (r routeStep) Name() string                     { return r.name }
func (r routeStep) Priority() int32                  { return r.priority }
func (routeStep) Builtin() bool                      { return false }
func (routeStep) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (routeStep) Timeout() time.Duration             { return 0 }
func (r routeStep) Intercept(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
	var spec routeTaskSpec
	if err := json.Unmarshal(req.Content, &spec); err != nil {
		return interceptor.Result{}, fmt.Errorf("decode route payload: %w", err)
	}
	if r.log != nil {
		*r.log = append(*r.log, routeStepCall{name: r.name, spec: spec})
	}
	if r.run == nil {
		return interceptor.Result{Action: interceptor.ActionAllow}, nil
	}
	return r.run(spec)
}

// rewriteRuntime returns a MODIFY of the runtime hint.
func rewriteRuntime(to string) func(routeTaskSpec) (interceptor.Result, error) {
	return func(in routeTaskSpec) (interceptor.Result, error) {
		in.RequestedRuntime = to
		raw, err := json.Marshal(in)
		return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: raw}, err
	}
}

// rewriteOwner returns a MODIFY of the immutable user_id.
func rewriteOwner(in routeTaskSpec) (interceptor.Result, error) {
	in.UserID = "bob"
	raw, err := json.Marshal(in)
	return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: raw}, err
}

// rewriteResolved returns a MODIFY of the immutable resolved runtime.
func rewriteResolved(in routeTaskSpec) (interceptor.Result, error) {
	in.ResolvedRuntimeName = rcFinal
	raw, err := json.Marshal(in)
	return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: raw}, err
}

// rejectRoute returns a deliberate REJECT.
func rejectRoute(routeTaskSpec) (interceptor.Result, error) {
	return interceptor.Result{Action: interceptor.ActionReject, Reason: "denied by policy"}, nil
}

// failRoute returns an interceptor fault, which a fail-closed step resolves
// to a rejection.
func failRoute(routeTaskSpec) (interceptor.Result, error) {
	return interceptor.Result{}, errors.New("injected interceptor fault")
}

// rcChain registers each step at its phase.
func rcChain(t *testing.T, steps map[interceptor.Phase][]routeStep) *interceptor.Chain {
	t.Helper()
	chain := interceptor.NewChain()
	for phase, list := range steps {
		for _, st := range list {
			if err := chain.Register(phase, st); err != nil {
				t.Fatalf("register %s: %v", st.name, err)
			}
		}
	}
	return chain
}

// rcExperiments routes every session of base to rcExperiment.
func rcExperiments(t *testing.T, base string) experimentstore.Store {
	t.Helper()
	s := experimentstore.NewMemory()
	if err := s.Create(context.Background(), experimentstore.Experiment{
		ID: "exp_rc", TenantID: wdTenant, Status: experiment.StatusActive, BaseRuntime: base,
		Variants:      []experimentstore.Variant{{ID: "treatment", Runtime: rcExperiment, Weight: 0.999999}},
		TargetingMode: experiment.TargetingPercentage, Sticky: experiment.StickySession,
		Propagation: experiment.PropagationInherit,
	}); err != nil {
		t.Fatalf("seed experiment: %v", err)
	}
	return s
}

// rcRuntimes registers the runtimes the route tests select between.
func rcRuntimes() []runtimestore.Runtime {
	return []runtimestore.Runtime{
		{Name: rcRequested, Type: runtimestore.TypeAgent},
		{Name: rcExperiment, Type: runtimestore.TypeAgent},
		{Name: rcFinal, Type: runtimestore.TypeAgent},
	}
}

// TestCreateRoutesRunRouteChainsInOrder_spec_4_8 pins that each creation route
// runs the PreRoute segment below the ExperimentRouter, the ExperimentRouter,
// the segment at or above it, and PostRoute, in that order, with each step
// reading the runtime the previous step selected and the owner the principal
// names.
//
// spec: 4.8 (Gateway Policy Engine), 10.7 (Experiment Primitives), 10.2 (Session ownership)
func TestCreateRoutesRunRouteChainsInOrder_spec_4_8(t *testing.T) {
	for _, route := range rcRoutes {
		t.Run(route, func(t *testing.T) {
			var calls []routeStepCall
			chain := rcChain(t, map[interceptor.Phase][]routeStep{
				interceptor.PhasePreRoute: {
					{name: "below", priority: 150, log: &calls},
					{name: "above", priority: 350, log: &calls, run: rewriteRuntime(rcFinal)},
				},
				interceptor.PhasePostRoute: {{name: "post", priority: 700, log: &calls}},
			})
			f := newWDFixture(t, rcRuntimes(), wdOpts{chain: chain, experiments: rcExperiments(t, rcRequested)})
			rr := f.post(t, route, "sess_rc", rcRequested, "")
			if rr.Code/100 != 2 {
				t.Fatalf("status %d; body=%s", rr.Code, rr.Body.String())
			}
			want := []routeStepCall{
				{"below", routeTaskSpec{TenantID: wdTenant, UserID: testOwner, RequestedRuntime: rcRequested}},
				{"above", routeTaskSpec{TenantID: wdTenant, UserID: testOwner, RequestedRuntime: rcExperiment}},
				{"post", routeTaskSpec{TenantID: wdTenant, UserID: testOwner, ResolvedRuntimeName: rcFinal}},
			}
			if len(calls) != len(want) {
				t.Fatalf("calls = %+v, want %+v", calls, want)
			}
			for i := range want {
				if calls[i] != want[i] {
					t.Errorf("call %d = %+v, want %+v", i, calls[i], want[i])
				}
			}
			row := f.row(t, "sess_rc")
			if row.RuntimeRef != rcFinal {
				t.Errorf("runtime = %q, want %q", row.RuntimeRef, rcFinal)
			}
			if row.UserID != testOwner {
				t.Errorf("owner = %q, want %q", row.UserID, testOwner)
			}
		})
	}
}

// TestCreateRoutesRuntimeHintSelectsRuntime_spec_4_8 pins what a runtime-hint
// MODIFY does on each side of the ExperimentRouter: below it the hint decides
// which experiment applies, and at or above it the hint overrides the variant.
//
// spec: 4.8 (Gateway Policy Engine, ExperimentRouter field dependencies), 10.7 (Experiment Primitives)
func TestCreateRoutesRuntimeHintSelectsRuntime_spec_4_8(t *testing.T) {
	cases := []struct {
		name           string
		priority       int32
		rewriteTo      string
		experimentBase string
		wantRuntime    string
		wantEnrolled   bool
	}{
		{"below-pivot-enters-experiment", 150, rcFinal, rcFinal, rcExperiment, true},
		{"below-pivot-leaves-experiment", 150, rcFinal, rcRequested, rcFinal, false},
		{"priority-299-is-below-pivot", 299, rcFinal, rcRequested, rcFinal, false},
		{"priority-300-is-above-pivot", 300, rcFinal, rcRequested, rcFinal, true},
		{"above-pivot-overrides-variant", 350, rcFinal, rcRequested, rcFinal, true},
		{"empty-hint-keeps-requested", 150, "", "unrelated", rcRequested, false},
		{"above-empty-hint-keeps-variant", 350, "", rcRequested, rcExperiment, true},
	}
	for _, tc := range cases {
		for _, route := range rcRoutes {
			t.Run(tc.name+route, func(t *testing.T) {
				chain := rcChain(t, map[interceptor.Phase][]routeStep{
					interceptor.PhasePreRoute: {{name: "hint", priority: tc.priority, run: rewriteRuntime(tc.rewriteTo)}},
				})
				f := newWDFixture(t, rcRuntimes(), wdOpts{chain: chain, experiments: rcExperiments(t, tc.experimentBase)})
				rr := f.post(t, route, "sess_rc", rcRequested, "")
				if rr.Code/100 != 2 {
					t.Fatalf("status %d; body=%s", rr.Code, rr.Body.String())
				}
				row := f.row(t, "sess_rc")
				if row.RuntimeRef != tc.wantRuntime {
					t.Errorf("runtime = %q, want %q", row.RuntimeRef, tc.wantRuntime)
				}
				if enrolled := row.ExperimentContext != nil; enrolled != tc.wantEnrolled {
					t.Errorf("enrolled = %v, want %v", enrolled, tc.wantEnrolled)
				}
			})
		}
	}
}

// TestCreateRoutesRejectLeavesNothingBehind_spec_4_8 pins that a rejection at
// any route step answers its §15.1 envelope and leaves no row and no pod claim.
// The MODIFY cases show the chain's immutability holds the §10.2 owner and the
// resolved runtime on the two-step route.
//
// spec: 4.8 (Gateway Policy Engine, Immutable field enforcement on MODIFY), 15.1 (REST API), 10.2 (Session ownership), 7.1 (Atomicity of session creation (steps 2–8))
func TestCreateRoutesRejectLeavesNothingBehind_spec_4_8(t *testing.T) {
	cases := []struct {
		name       string
		phase      interceptor.Phase
		priority   int32
		run        func(routeTaskSpec) (interceptor.Result, error)
		wantStatus int
		wantCode   string
		wantField  string
	}{
		{"preroute-below-reject", interceptor.PhasePreRoute, 150, rejectRoute, 403, "INTERCEPTOR_REJECTED", ""},
		{"preroute-above-reject", interceptor.PhasePreRoute, 350, rejectRoute, 403, "INTERCEPTOR_REJECTED", ""},
		{"postroute-reject", interceptor.PhasePostRoute, 150, rejectRoute, 403, "INTERCEPTOR_REJECTED", ""},
		{"preroute-below-fault", interceptor.PhasePreRoute, 150, failRoute, 503, interceptor.CodeInterceptorTimeout, ""},
		{"preroute-above-fault", interceptor.PhasePreRoute, 350, failRoute, 503, interceptor.CodeInterceptorTimeout, ""},
		{"postroute-fault", interceptor.PhasePostRoute, 150, failRoute, 503, interceptor.CodeInterceptorTimeout, ""},
		{"preroute-below-owner-modify", interceptor.PhasePreRoute, 150, rewriteOwner, 400, interceptor.CodeInterceptorImmutableFieldViolation, "user_id"},
		{"preroute-above-owner-modify", interceptor.PhasePreRoute, 350, rewriteOwner, 400, interceptor.CodeInterceptorImmutableFieldViolation, "user_id"},
		{"postroute-resolved-modify", interceptor.PhasePostRoute, 150, rewriteResolved, 400, interceptor.CodeInterceptorImmutableFieldViolation, "resolved_runtime_name"},
	}
	for _, tc := range cases {
		for _, route := range rcRoutes {
			t.Run(tc.name+route, func(t *testing.T) {
				chain := rcChain(t, map[interceptor.Phase][]routeStep{
					tc.phase: {{name: "step", priority: tc.priority, run: tc.run}},
				})
				f := newWDFixture(t, rcRuntimes(), wdOpts{chain: chain, claims: true})
				rr := f.post(t, route, "sess_rc", rcRequested, "")
				if rr.Code != tc.wantStatus {
					t.Fatalf("status %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
				}
				code, details := wdErrorCode(t, rr)
				if code != tc.wantCode {
					t.Errorf("code = %q, want %q", code, tc.wantCode)
				}
				if details["phase"] != string(tc.phase) {
					t.Errorf("details.phase = %v, want %s", details["phase"], tc.phase)
				}
				if tc.wantField != "" && !violatedFieldsContain(details["violated_fields"], tc.wantField) {
					t.Errorf("violated_fields = %v, want %q", details["violated_fields"], tc.wantField)
				}
				if f.exists("sess_rc") {
					t.Error("a rejected create left a session row")
				}
				if got := f.claimCount(t); got != 0 {
					t.Errorf("claims = %d, want 0", got)
				}
			})
		}
	}
}

// TestCreateDrainGateJudgesRoutedRuntime_spec_15_1 pins that the two-step
// route's drain gate judges the runtime routing selected, whichever step
// selected it, and does not refuse a request routing moved off a draining pool.
//
// spec: 15.1 (REST API, POOL_DRAINING), 4.8 (Gateway Policy Engine), 10.7 (Experiment Primitives)
func TestCreateDrainGateJudgesRoutedRuntime_spec_15_1(t *testing.T) {
	cases := []struct {
		name       string
		chain      map[interceptor.Phase][]routeStep
		experiment bool
		draining   string
		wantStatus int
	}{
		{"no-routing-draining", nil, false, rcRequested, http.StatusServiceUnavailable},
		{"below-hint-to-draining", map[interceptor.Phase][]routeStep{
			interceptor.PhasePreRoute: {{name: "h", priority: 150, run: rewriteRuntime(rcFinal)}},
		}, false, rcFinal, http.StatusServiceUnavailable},
		{"below-hint-off-draining", map[interceptor.Phase][]routeStep{
			interceptor.PhasePreRoute: {{name: "h", priority: 150, run: rewriteRuntime(rcFinal)}},
		}, false, rcRequested, http.StatusCreated},
		{"above-hint-to-draining", map[interceptor.Phase][]routeStep{
			interceptor.PhasePreRoute: {{name: "h", priority: 350, run: rewriteRuntime(rcFinal)}},
		}, false, rcFinal, http.StatusServiceUnavailable},
		{"experiment-to-draining", nil, true, rcExperiment, http.StatusServiceUnavailable},
		{"nothing-draining", nil, false, "", http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := wdOpts{}
			if tc.chain != nil {
				opts.chain = rcChain(t, tc.chain)
			}
			if tc.experiment {
				opts.experiments = rcExperiments(t, rcRequested)
			}
			f := newWDFixture(t, rcRuntimes(), opts)
			pools := poolstore.NewMemory()
			for _, rt := range []string{rcRequested, rcExperiment, rcFinal} {
				if err := pools.Create(context.Background(), poolstore.Pool{Name: rt + "-pool", RuntimeRef: rt}); err != nil {
					t.Fatalf("create pool: %v", err)
				}
			}
			if tc.draining != "" {
				if _, err := pools.Update(context.Background(), tc.draining+"-pool", func(p *poolstore.Pool) error {
					p.DrainingSince = f.srv.clock().Add(-time.Minute)
					return nil
				}); err != nil {
					t.Fatalf("drain pool: %v", err)
				}
			}
			f.srv.pools = pools
			f.srv.poolNameResolver = func(_ context.Context, runtime string, _ isolation.Profile, _ string) (string, bool) {
				return runtime + "-pool", true
			}
			rr := f.post(t, "/v1/sessions", "sess_rc", rcRequested, "")
			if rr.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if tc.wantStatus == http.StatusServiceUnavailable {
				if code, _ := wdErrorCode(t, rr); code != "POOL_DRAINING" {
					t.Errorf("code = %q, want POOL_DRAINING", code)
				}
			}
		})
	}
}

// TestCreateEnvelopeJudgesPreRouteRuntime_spec_4_8 pins that the §14 request
// envelope reads the runtime the below-pivot PreRoute segment selected: the
// timeout cap is the selected runtime's.
//
// spec: 4.8 (Gateway Policy Engine), 14 (Workspace Plan Schema), 14.1 (WorkspacePlan Schema Versioning)
func TestCreateEnvelopeJudgesPreRouteRuntime_spec_4_8(t *testing.T) {
	limited := &runtimestore.Limits{MaxSessionAgeSeconds: 60}
	cases := []struct {
		name          string
		requested     *runtimestore.Limits
		selected      *runtimestore.Limits
		wantStatus    int
		wantRowLimits bool
	}{
		{"selected-runtime-caps", nil, limited, http.StatusBadRequest, false},
		{"requested-runtime-cap-does-not-apply", limited, nil, http.StatusCreated, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chain := rcChain(t, map[interceptor.Phase][]routeStep{
				interceptor.PhasePreRoute: {{name: "h", priority: 150, run: rewriteRuntime(rcFinal)}},
			})
			f := newWDFixture(t, []runtimestore.Runtime{
				{Name: rcRequested, Type: runtimestore.TypeAgent, Limits: tc.requested},
				{Name: rcFinal, Type: runtimestore.TypeAgent, Limits: tc.selected},
			}, wdOpts{chain: chain})
			f.nextID = "sess_rc"
			raw, _ := json.Marshal(map[string]any{
				"runtimeRef": rcRequested,
				"timeouts":   map[string]any{"maxSessionAgeSeconds": 120},
			})
			r := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(raw))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Lenny-Tenant-ID", wdTenant)
			rr := httptest.NewRecorder()
			asOwner(f.srv.Handler()).ServeHTTP(rr, r)
			if rr.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if f.exists("sess_rc") != tc.wantRowLimits {
				t.Errorf("row exists = %v, want %v", f.exists("sess_rc"), tc.wantRowLimits)
			}
		})
	}
}

// TestCreateRoutesComposeDefaultsOfRoutedRuntime_spec_14_1 pins that a
// two-step create whose plan binds at creation carries the workspaceDefaults
// of the runtime the route chains selected.
//
// spec: 14.1 (WorkspacePlan Schema Versioning, Runtime workspace defaults), 4.8 (Gateway Policy Engine)
func TestCreateRoutesComposeDefaultsOfRoutedRuntime_spec_14_1(t *testing.T) {
	for _, priority := range []int32{150, 350} {
		t.Run(fmt.Sprintf("priority-%d", priority), func(t *testing.T) {
			chain := rcChain(t, map[interceptor.Phase][]routeStep{
				interceptor.PhasePreRoute: {{name: "h", priority: priority, run: rewriteRuntime(rcFinal)}},
			})
			f := newWDFixture(t, []runtimestore.Runtime{
				{Name: rcRequested, Type: runtimestore.TypeAgent},
				{Name: rcFinal, Type: runtimestore.TypeAgent, WorkspaceDefaults: wdDefaults()},
			}, wdOpts{chain: chain})
			rr := f.post(t, "/v1/sessions", "sess_rc", rcRequested, wdClientPlan)
			if rr.Code != http.StatusCreated {
				t.Fatalf("status %d; body=%s", rr.Code, rr.Body.String())
			}
			plan := f.getPlan(t, "sess_rc")
			if len(plan.SetupCommands) == 0 || plan.SetupCommands[0].Cmd != "echo defaults" {
				t.Errorf("setupCommands = %+v, want the routed runtime's defaults first", plan.SetupCommands)
			}
		})
	}
}
