// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 HTTP contract tests for the targetRuntime rule of
// POST /v1/sessions/{id}/replay in both replay modes. A targetRuntime whose
// executionMode differs from the source session's answers
// 400 INCOMPATIBLE_RUNTIME with details.sourceExecutionMode and
// details.targetExecutionMode, an unregistered targetRuntime answers
// 400 VALIDATION_ERROR, and a registered runtime of the source's mode is
// accepted. Every error body is validated against the 400 response the
// served OpenAPI document declares for the operation.
//
// spec: §15.1 (REST API: Session Replay Semantics, Error code catalog), §5.2
// (Pool Configuration and Execution Modes)
package rest_sessions_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §15.1 (REST API: Session Replay Semantics, Error code catalog), §5.2 (Pool Configuration and Execution Modes)
// diagnosis: a failure means replay accepted a targetRuntime of another
// executionMode or an unregistered one, answered the mismatch with a code
// other than INCOMPATIBLE_RUNTIME, omitted the catalog row's
// sourceExecutionMode or targetExecutionMode detail, refused a runtime of
// the source's mode, left a row behind, or wrote a body the served OpenAPI
// document does not declare. Check requireReplayRuntimeCompatible in
// pkg/gateway/sessionserver/replay_runtime.go and the 400 response of the
// replay operation in pkg/gateway/externalapi/openapi/openapi.json.
func TestReplayIncompatibleRuntimeContract_spec_15_1(t *testing.T) {
	v := newSnapCopyValidator(t)
	reg := runtimestore.NewMemory()
	for _, rt := range []runtimestore.Runtime{
		{Name: "echo", ExecutionMode: runtimestore.ExecutionModeSession},
		{Name: "echo-service", ExecutionMode: runtimestore.ExecutionModeService},
	} {
		if err := reg.Create(context.Background(), rt); err != nil {
			t.Fatalf("register %s: %v", rt.Name, err)
		}
	}
	cases := []struct {
		name   string
		target string
		status int
		code   string
	}{
		{name: "same executionMode", target: "echo", status: http.StatusCreated},
		{name: "different executionMode", target: "echo-service", status: http.StatusBadRequest, code: "INCOMPATIBLE_RUNTIME"},
		{name: "unregistered runtime", target: "no-such-runtime", status: http.StatusBadRequest, code: "VALIDATION_ERROR"},
	}
	for _, mode := range []sessionserver.ReplayMode{sessionserver.ReplayPromptHistory, sessionserver.ReplayWorkspaceDerive} {
		for _, tc := range cases {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				f := newSnapCopyFixture(t)
				h := f.handler(func(o *sessionserver.Options) { o.Runtimes = reg })
				ep := snapCopyEndpoint{name: string(mode), path: "/v1/sessions/{id}/replay", body: sessionserver.ReplayRequest{
					ReplayMode: mode, TargetRuntime: tc.target,
				}}
				rr := ep.call(t, h)
				if rr.Code != tc.status {
					t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.status, rr.Body.String())
				}
				if tc.status == http.StatusCreated {
					return
				}
				status := strconv.Itoa(rr.Code)
				if _, ok := servedOperation(t, v.doc, ep.path, "post").Responses[status]; !ok {
					t.Errorf("POST %s does not declare the %s response it answers", ep.path, status)
				}
				v.validate(t, ep.path, status, rr.Body.Bytes())
				var env struct {
					Error struct {
						Code      string         `json:"code"`
						Category  string         `json:"category"`
						Retryable bool           `json:"retryable"`
						Details   map[string]any `json:"details"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
					t.Fatalf("decode error envelope: %v", err)
				}
				if env.Error.Code != tc.code {
					t.Errorf("code = %s, want %s", env.Error.Code, tc.code)
				}
				// The §15.1 catalog row: INCOMPATIBLE_RUNTIME is PERMANENT, 400,
				// and carries both execution modes.
				if tc.code == "INCOMPATIBLE_RUNTIME" {
					if env.Error.Category != "PERMANENT" || env.Error.Retryable {
						t.Errorf("category/retryable = %s/%v, want PERMANENT/false", env.Error.Category, env.Error.Retryable)
					}
					if env.Error.Details["sourceExecutionMode"] != "session" || env.Error.Details["targetExecutionMode"] != "service" {
						t.Errorf("details = %v, want sourceExecutionMode session and targetExecutionMode service", env.Error.Details)
					}
				}
				if _, err := f.store.Get(context.Background(), "acme", "sess_snap_new"); !errors.Is(err, sessionstore.ErrNotFound) {
					t.Errorf("the rejected replay left a row behind (err %v)", err)
				}
			})
		}
	}
}
