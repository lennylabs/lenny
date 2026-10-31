// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// policyRuntimes are the runtimes the setupCommandPolicy read cases use: a
// runtime whose policy admits only `make`, a derived runtime inheriting
// that policy from its base, and a derived runtime whose base is missing.
func policyRuntimes() []runtimestore.Runtime {
	return []runtimestore.Runtime{
		{
			Name: "sp-base", Type: runtimestore.TypeAgent,
			SetupCommandPolicy: &runtimestore.SetupCommandPolicy{
				Mode: runtimestore.SetupCommandModeAllowlist, Allowlist: []string{"make"}, MaxCommands: 2,
			},
		},
		{Name: "sp-derived", BaseRuntime: "sp-base"},
		{Name: "sp-orphan", BaseRuntime: "sp-gone"},
	}
}

func planWithCommands(cmds ...string) workspaceplan.Plan {
	p := workspaceplan.Plan{SchemaVersion: 1}
	for _, c := range cmds {
		p.SetupCommands = append(p.SetupCommands, workspaceplan.SetupCommand{Cmd: c})
	}
	return p
}

// TestSetupCommandPolicyReadFailsClosed_spec_7_5 drives
// enforceSetupCommandPolicy directly, so no later runtime read can mask its
// outcome. A runtime with no registry entry admits the plan, a transient
// read of the runtime or of a derived runtime's base answers 503
// SERVICE_UNAVAILABLE with Retry-After, and a missing base answers 500
// INTERNAL_ERROR. A plan without setup commands reads nothing, so a store
// fault cannot fail it, and a readable policy still admits and rejects.
//
// spec: 7.5 (Setup Commands, Command policy), 14.1 (WorkspacePlan Schema Versioning, Runtime workspace defaults), 5.1 (Runtime)
func TestSetupCommandPolicyReadFailsClosed_spec_7_5(t *testing.T) {
	transient := errors.New("injected: connection reset")
	cases := []struct {
		name       string
		runtime    string
		arm        string
		plan       workspaceplan.Plan
		wantOK     bool
		wantStatus int
		wantCode   string
		wantReason string
	}{
		{name: "unregistered runtime admits", runtime: "sp-unregistered", plan: planWithCommands("curl evil"), wantOK: true},
		{
			name: "runtime read transient", runtime: "sp-base", arm: "sp-base", plan: planWithCommands("make build"),
			wantStatus: http.StatusServiceUnavailable, wantCode: "SERVICE_UNAVAILABLE",
		},
		{
			name: "derived base read transient", runtime: "sp-derived", arm: "sp-base", plan: planWithCommands("make build"),
			wantStatus: http.StatusServiceUnavailable, wantCode: "SERVICE_UNAVAILABLE",
		},
		{
			name: "derived runtime read transient", runtime: "sp-derived", arm: "sp-derived", plan: planWithCommands("make build"),
			wantStatus: http.StatusServiceUnavailable, wantCode: "SERVICE_UNAVAILABLE",
		},
		{
			name: "derived base missing", runtime: "sp-orphan", plan: planWithCommands("make build"),
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR",
		},
		{name: "no commands reads nothing", runtime: "sp-base", arm: "sp-base", plan: planWithCommands(), wantOK: true},
		{name: "allowed command admitted", runtime: "sp-derived", plan: planWithCommands("make build"), wantOK: true},
		{
			name: "forbidden command rejected", runtime: "sp-derived", plan: planWithCommands("curl evil"),
			wantStatus: http.StatusBadRequest, wantCode: "WORKSPACE_PLAN_INVALID", wantReason: "setup_command_policy_violation",
		},
		{
			name: "over cap rejected", runtime: "sp-base", plan: planWithCommands("make a", "make b", "make c"),
			wantStatus: http.StatusBadRequest, wantCode: "WORKSPACE_PLAN_INVALID", wantReason: "setup_commands_max_exceeded",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newWDFixture(t, policyRuntimes(), wdOpts{})
			if tc.arm != "" {
				f.runtimes.arm(tc.arm, transient)
			}
			rr := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
			ok := f.srv.enforceSetupCommandPolicy(rr, r, tc.runtime, tc.plan)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v; status %d body %s", ok, tc.wantOK, rr.Code, rr.Body.String())
			}
			if tc.wantOK {
				if rr.Body.Len() != 0 {
					t.Errorf("admitted plan wrote a response: %s", rr.Body.String())
				}
				return
			}
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			code, details := wdErrorCode(t, rr)
			if code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
			if tc.wantReason != "" && details["reason"] != tc.wantReason {
				t.Errorf("reason = %v, want %s", details["reason"], tc.wantReason)
			}
			gotRetry := rr.Header().Get("Retry-After") != ""
			if wantRetry := tc.wantStatus == http.StatusServiceUnavailable; gotRetry != wantRetry {
				t.Errorf("Retry-After present = %v, want %v", gotRetry, wantRetry)
			}
		})
	}
}

// TestSetupCommandPolicyReadFailureLeavesStateUnchanged_spec_7_5 drives
// every binding route with a plan whose setup commands the policy must
// check, under a transient read of the runtime's base. Each route answers
// 503 SERVICE_UNAVAILABLE with Retry-After and leaves no session state:
// no row for a create route, and a created row with no plan for finalize.
//
// spec: 7.5 (Setup Commands, Command policy), 14.1 (WorkspacePlan Schema Versioning, Plan binding; Runtime workspace defaults)
func TestSetupCommandPolicyReadFailureLeavesStateUnchanged_spec_7_5(t *testing.T) {
	plan := `{"schemaVersion":1,"sources":[],"setupCommands":[{"cmd":"make build"}]}`
	for _, route := range []string{"/v1/sessions", "/v1/sessions/start", "finalize"} {
		t.Run(route, func(t *testing.T) {
			f := newWDFixture(t, policyRuntimes(), wdOpts{})
			f.runtimes.arm("sp-base", errors.New("injected: connection reset"))
			rr := f.bind(t, route, "sess_sp", "sp-derived", plan)
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("status %d, want 503; body=%s", rr.Code, rr.Body.String())
			}
			if code, _ := wdErrorCode(t, rr); code != "SERVICE_UNAVAILABLE" {
				t.Errorf("code = %q, want SERVICE_UNAVAILABLE", code)
			}
			if rr.Header().Get("Retry-After") == "" {
				t.Error("SERVICE_UNAVAILABLE carries no Retry-After")
			}
			f.assertUnbound(t, route, "sess_sp")
		})
	}
}
