// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

const (
	// spRequested is the runtime a create request names.
	spRequested = "sp-requested"
	// spRouted is the runtime routing selects in its place.
	spRouted = "sp-routed"
)

// makeOnlyPolicy admits only commands starting with `make`.
func makeOnlyPolicy() *runtimestore.SetupCommandPolicy {
	return &runtimestore.SetupCommandPolicy{
		Mode: runtimestore.SetupCommandModeAllowlist, Allowlist: []string{"make"},
	}
}

// routedPolicyExperiments routes every session of spRequested to spRouted.
func routedPolicyExperiments(t *testing.T) experimentstore.Store {
	t.Helper()
	s := experimentstore.NewMemory()
	if err := s.Create(context.Background(), experimentstore.Experiment{
		ID: "exp_sp", TenantID: wdTenant, Status: experiment.StatusActive, BaseRuntime: spRequested,
		Variants:      []experimentstore.Variant{{ID: "treatment", Runtime: spRouted, Pool: spRouted + "-pool", Weight: 0.999999}},
		TargetingMode: experiment.TargetingPercentage, Sticky: experiment.StickySession,
		Propagation: experiment.PropagationInherit,
	}); err != nil {
		t.Fatalf("seed experiment: %v", err)
	}
	return s
}

// routedPolicyChain rewrites the requested runtime to spRouted with a
// PreRoute MODIFY at priority.
func routedPolicyChain(t *testing.T, priority int32) *interceptor.Chain {
	t.Helper()
	modified, _ := json.Marshal(routeTaskSpec{TenantID: wdTenant, UserID: "alice", RequestedRuntime: spRouted})
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreRoute, routeModifyInterceptor{priority: priority, out: modified}); err != nil {
		t.Fatalf("register: %v", err)
	}
	return chain
}

// TestSetupCommandPolicyFollowsRoutedRuntime_spec_7_5 pins that a create
// route checks the client's setup commands against the policy of the runtime
// routing selected rather than the runtime the request names. Each routing
// mechanism is driven with a routed runtime whose policy is stricter than the
// requested one (a forbidden command is refused although the requested
// runtime admits it) and looser (the command is admitted although the
// requested runtime forbids it), and a control without routing applies the
// requested runtime's policy.
//
// spec: 7.5 (Setup Commands, Command policy), 14.1 (WorkspacePlan Schema Versioning, Runtime workspace defaults), 10.7 (Experiment Primitives), 4.8 (Gateway Policy Engine)
func TestSetupCommandPolicyFollowsRoutedRuntime_spec_7_5(t *testing.T) {
	plan := `{"schemaVersion":1,"sources":[],"setupCommands":[{"cmd":"curl https://example.com/install.sh"}]}`
	routings := []struct {
		name   string
		routes []string
		opts   func(t *testing.T) wdOpts
	}{
		{"experiment", []string{"/v1/sessions", "/v1/sessions/start"}, func(t *testing.T) wdOpts {
			return wdOpts{experiments: routedPolicyExperiments(t)}
		}},
		{"preroute-modify-below-pivot", []string{"/v1/sessions/start"}, func(t *testing.T) wdOpts {
			return wdOpts{chain: routedPolicyChain(t, 150)}
		}},
		{"preroute-modify-above-pivot", []string{"/v1/sessions/start"}, func(t *testing.T) wdOpts {
			return wdOpts{chain: routedPolicyChain(t, 350)}
		}},
		{"no-routing", []string{"/v1/sessions", "/v1/sessions/start"}, func(*testing.T) wdOpts { return wdOpts{} }},
	}
	strictness := []struct {
		name            string
		requestedPolicy *runtimestore.SetupCommandPolicy
		routedPolicy    *runtimestore.SetupCommandPolicy
	}{
		{"routed-stricter", nil, makeOnlyPolicy()},
		{"routed-looser", makeOnlyPolicy(), nil},
	}
	for _, rt := range routings {
		for _, st := range strictness {
			for _, route := range rt.routes {
				t.Run(rt.name+"/"+st.name+route, func(t *testing.T) {
					f := newWDFixture(t, []runtimestore.Runtime{
						{Name: spRequested, Type: runtimestore.TypeAgent, SetupCommandPolicy: st.requestedPolicy},
						{Name: spRouted, Type: runtimestore.TypeAgent, SetupCommandPolicy: st.routedPolicy},
					}, rt.opts(t))
					rr := f.post(t, route, "sess_sp", spRequested, plan)
					// The policy that decides is the routed runtime's, or the
					// requested runtime's when nothing routes the session.
					deciding := st.routedPolicy
					if rt.name == "no-routing" {
						deciding = st.requestedPolicy
					}
					if deciding == nil {
						if rr.Code/100 != 2 {
							t.Fatalf("status %d, want the plan admitted; body=%s", rr.Code, rr.Body.String())
						}
						wantRuntime := spRouted
						if rt.name == "no-routing" {
							wantRuntime = spRequested
						}
						if got := f.row(t, "sess_sp").RuntimeRef; got != wantRuntime {
							t.Errorf("runtime = %q, want %q", got, wantRuntime)
						}
						return
					}
					if rr.Code != http.StatusBadRequest {
						t.Fatalf("status %d, want 400; body=%s", rr.Code, rr.Body.String())
					}
					code, details := wdErrorCode(t, rr)
					if code != "WORKSPACE_PLAN_INVALID" || details["reason"] != "setup_command_policy_violation" {
						t.Errorf("code %q reason %v, want WORKSPACE_PLAN_INVALID setup_command_policy_violation", code, details["reason"])
					}
					f.assertUnbound(t, route, "sess_sp")
				})
			}
		}
	}
}

// TestSetupCommandPolicyReadsRoutedRuntimeOnly_spec_7_5 pins that the
// create-time policy read targets the routed runtime: a fault on the
// requested runtime's registry entry does not fail a routed create, and a
// fault on the routed runtime's entry fails it with 503.
//
// spec: 7.5 (Setup Commands, Command policy), 14.1 (WorkspacePlan Schema Versioning, Runtime workspace defaults), 10.7 (Experiment Primitives)
func TestSetupCommandPolicyReadsRoutedRuntimeOnly_spec_7_5(t *testing.T) {
	plan := `{"schemaVersion":1,"sources":[],"setupCommands":[{"cmd":"make build"}]}`
	for _, route := range []string{"/v1/sessions", "/v1/sessions/start"} {
		for _, tc := range []struct {
			arm        string
			wantStatus int
		}{
			{spRequested, 0},
			{spRouted, http.StatusServiceUnavailable},
		} {
			t.Run(route+"/fault-"+tc.arm, func(t *testing.T) {
				f := newWDFixture(t, []runtimestore.Runtime{
					{Name: spRequested, Type: runtimestore.TypeAgent, SetupCommandPolicy: makeOnlyPolicy()},
					{Name: spRouted, Type: runtimestore.TypeAgent, SetupCommandPolicy: makeOnlyPolicy()},
				}, wdOpts{experiments: routedPolicyExperiments(t)})
				f.runtimes.arm(tc.arm, errTransientRead)
				rr := f.post(t, route, "sess_sp", spRequested, plan)
				if tc.wantStatus == 0 {
					if rr.Code/100 != 2 {
						t.Fatalf("status %d, want admitted; body=%s", rr.Code, rr.Body.String())
					}
					return
				}
				if rr.Code != tc.wantStatus {
					t.Fatalf("status %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
				}
				f.assertUnbound(t, route, "sess_sp")
			})
		}
	}
}

// errTransientRead is the injected transient runtime-store error.
var errTransientRead = errors.New("injected: connection reset")
