// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// errRuntimeRead is the registry failure brokenRuntimes injects.
var errRuntimeRead = errors.New("test: runtime registry read failed")

// brokenRuntimes fails every Get with an error other than not found, as a
// database outage does.
type brokenRuntimes struct {
	*runtimestore.Memory
}

func (brokenRuntimes) Get(context.Context, string) (runtimestore.Runtime, error) {
	return runtimestore.Runtime{}, errRuntimeRead
}

// replayRuntimes returns a registry holding a session-mode runtime, a
// service-mode runtime, a runtime that declares no mode, and a derived
// runtime whose base is the service-mode runtime.
func replayRuntimes(t *testing.T) *runtimestore.Memory {
	t.Helper()
	reg := runtimestore.NewMemory()
	for _, rt := range []runtimestore.Runtime{
		{Name: "session-rt", ExecutionMode: runtimestore.ExecutionModeSession},
		{Name: "service-rt", ExecutionMode: runtimestore.ExecutionModeService},
		{Name: "unmoded-rt"},
		{Name: "derived-service-rt", BaseRuntime: "service-rt"},
	} {
		if err := reg.Create(context.Background(), rt); err != nil {
			t.Fatalf("register %s: %v", rt.Name, err)
		}
	}
	return reg
}

// TestReplayRequiresMatchingExecutionMode_spec_15_1 pins the §15.1
// targetRuntime rule in both replay modes. A registered runtime with the
// source session's executionMode is accepted. A different mode answers
// 400 INCOMPATIBLE_RUNTIME with details.sourceExecutionMode and
// details.targetExecutionMode, including a derived runtime whose mode comes
// from its base and a source row whose empty mode reads as the session
// default. An unregistered runtime answers 400 VALIDATION_ERROR on
// targetRuntime, and a registry that cannot be read fails closed with a
// retryable 503. No rejected replay persists a row.
//
// spec: 15.1 (REST API) Session Replay Semantics (targetRuntime) and the
// INCOMPATIBLE_RUNTIME error catalog row, 5.2 (Pool Configuration and
// Execution Modes), 7.1 (Normal Flow) sessionIsolationLevel.
func TestReplayRequiresMatchingExecutionMode_spec_15_1(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		sourceMode   string
		target       string
		broken       bool
		status       int
		code         string
		sourceDetail string
		targetDetail string
		reason       string
	}{
		{name: "session source, session runtime", sourceMode: "session", target: "session-rt", status: http.StatusCreated},
		{name: "unresolved source, runtime with no mode", sourceMode: "", target: "unmoded-rt", status: http.StatusCreated},
		{name: "unresolved source, session runtime", sourceMode: "", target: "session-rt", status: http.StatusCreated},
		{name: "service source, service runtime", sourceMode: "service", target: "service-rt", status: http.StatusCreated},
		{name: "service source, derived runtime of a service base", sourceMode: "service", target: "derived-service-rt", status: http.StatusCreated},
		{
			name: "session source, service runtime", sourceMode: "session", target: "service-rt",
			status: http.StatusBadRequest, code: "INCOMPATIBLE_RUNTIME", sourceDetail: "session", targetDetail: "service",
		},
		{
			name: "unresolved source, service runtime", sourceMode: "", target: "service-rt",
			status: http.StatusBadRequest, code: "INCOMPATIBLE_RUNTIME", sourceDetail: "session", targetDetail: "service",
		},
		{
			name: "session source, derived runtime of a service base", sourceMode: "session", target: "derived-service-rt",
			status: http.StatusBadRequest, code: "INCOMPATIBLE_RUNTIME", sourceDetail: "session", targetDetail: "service",
		},
		{
			name: "service source, session runtime", sourceMode: "service", target: "session-rt",
			status: http.StatusBadRequest, code: "INCOMPATIBLE_RUNTIME", sourceDetail: "service", targetDetail: "session",
		},
		{
			name: "unregistered runtime", sourceMode: "session", target: "no-such-rt",
			status: http.StatusBadRequest, code: "VALIDATION_ERROR", reason: "runtime_not_registered",
		},
		{
			name: "registry unreadable", sourceMode: "session", target: "session-rt", broken: true,
			status: http.StatusServiceUnavailable, code: "SERVICE_UNAVAILABLE", reason: "runtime_lookup_unavailable",
		},
	}
	for _, mode := range []sessionserver.ReplayMode{sessionserver.ReplayPromptHistory, sessionserver.ReplayWorkspaceDerive} {
		for _, tc := range cases {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				store := memstore.New()
				seedTerminalSource(t, store, func(s *sessionstore.Session) { s.ExecutionMode = tc.sourceMode })
				var reg runtimestore.Store = replayRuntimes(t)
				if tc.broken {
					reg = brokenRuntimes{Memory: runtimestore.NewMemory()}
				}
				srv := sessionserver.New(store, snapshotFor(t, "sess_src").wire(sessionserver.Options{
					DevTenantHeader: true,
					IDFunc:          func() string { return "sess_replay" },
					Runtimes:        reg,
				}))
				rr := replayReq(t, asOwner(srv.Handler()), sessionserver.ReplayRequest{ReplayMode: mode, TargetRuntime: tc.target})
				if rr.Code != tc.status {
					t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.status, rr.Body.String())
				}
				_, err := store.Get(context.Background(), "acme", "sess_replay")
				if tc.status == http.StatusCreated {
					if err != nil {
						t.Errorf("the accepted replay persisted no row: %v", err)
					}
					return
				}
				if err == nil {
					t.Error("a rejected replay persisted a row")
				}
				code, _, details := decodeError(t, rr)
				if code != tc.code {
					t.Errorf("code = %s, want %s; details %v", code, tc.code, details)
				}
				if tc.code == "INCOMPATIBLE_RUNTIME" {
					if details["sourceExecutionMode"] != tc.sourceDetail || details["targetExecutionMode"] != tc.targetDetail {
						t.Errorf("details = %v, want sourceExecutionMode %s and targetExecutionMode %s",
							details, tc.sourceDetail, tc.targetDetail)
					}
					return
				}
				if details["reason"] != tc.reason || details["field"] != "targetRuntime" {
					t.Errorf("details = %v, want reason %s on field targetRuntime", details, tc.reason)
				}
				if tc.status == http.StatusServiceUnavailable && rr.Header().Get("Retry-After") == "" {
					t.Error("Retry-After missing on the retryable registry failure")
				}
			})
		}
	}
}
