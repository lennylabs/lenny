// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// fakeChildMaterializer is a test double for mcptools.ChildMaterializer. It
// records every child id the delegate handler drives through materialization
// and returns a configured state and error, so a test can assert both the
// sentinel-to-tool-code mapping the handler applies and that the handler builds
// the taskHandle from the returned post-materialization state.
type fakeChildMaterializer struct {
	state session.State
	err   error
	calls []string
}

func (f *fakeChildMaterializer) MaterializeDelegatedChild(_ context.Context, _, childID string) (session.State, error) {
	f.calls = append(f.calls, childID)
	return f.state, f.err
}

// newDelegateMaterializeMCP builds a same-environment delegation MCP server
// with the §8.2 ChildMaterializer seam wired to the supplied double (nil leaves
// the seam unset) and a running parent session. The delegation service issues
// child ids of "sess_child". The §8.3 credential pre-check is left unwired so
// the handler reaches the materialization step for every admitted child.
func newDelegateMaterializeMCP(t *testing.T, materializer mcptools.ChildMaterializer) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:             store,
		Executor:          executor.NewEchoExecutor(),
		Runtimes:          runtimes,
		ChildMaterializer: materializer,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	ctxbg := context.Background()
	_ = runtimes.Create(ctxbg, runtimestore.Runtime{Name: "child-agent", Type: runtimestore.TypeAgent})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Create(ctxbg, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:            session.StateRunning,
		RuntimeRef:       "child-agent",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt:        now, UpdatedAt: now,
	})
	return srv, store
}

func delegateMaterializeCall(t *testing.T, srv *mcp.Server) map[string]any {
	t.Helper()
	return call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","credentialPropagation":"independent","task":{"input":[{"type":"text","inline":"do work"}]}}`)
}

// spec: 8.2 (lines 93-97 delegated-child materialization) — when the seam is
// wired, the handler drives the admitted StateCreated child through
// materialization and builds the taskHandle from the returned running state, so
// the parent receives a running child. Against the pre-materialization code the
// handle serialized the StateCreated snapshot.
func TestDelegateTaskMaterializesChildToRunning_spec_8_2(t *testing.T) {
	m := &fakeChildMaterializer{state: session.StateRunning}
	srv, _ := newDelegateMaterializeMCP(t, m)

	resp := delegateMaterializeCall(t, srv)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("a successful materialization must return a running child: %+v", resp)
	}
	text := resultText(t, resp)
	var handle struct {
		ChildSessionID string `json:"childSessionId"`
		State          string `json:"state"`
	}
	if err := json.Unmarshal([]byte(text), &handle); err != nil {
		t.Fatalf("TaskHandle is not valid JSON: %v (raw=%q)", err, text)
	}
	if handle.State != string(session.StateRunning) {
		t.Errorf("state = %q, want running (the post-materialization state)", handle.State)
	}
	if len(m.calls) != 1 || m.calls[0] != "sess_child" {
		t.Errorf("Materialize calls = %v, want one call for sess_child", m.calls)
	}
}

// spec: 8.2 (lines 93-97) — a nil ChildMaterializer (the minimal in-process
// gateway wires none) falls through unchanged: the handler never calls
// Materialize and the handle carries the pre-materialization created snapshot.
func TestDelegateTaskNilMaterializerFallsThrough_spec_8_2(t *testing.T) {
	srv, _ := newDelegateMaterializeMCP(t, nil)

	resp := delegateMaterializeCall(t, srv)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("a nil materializer must fall through and admit the child: %+v", resp)
	}
	text := resultText(t, resp)
	var handle struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(text), &handle); err != nil {
		t.Fatalf("TaskHandle is not valid JSON: %v (raw=%q)", err, text)
	}
	if handle.State != string(session.StateCreated) {
		t.Errorf("state = %q, want created (the nil-seam fall-through snapshot)", handle.State)
	}
}

// spec: 8.2 (lines 93-97), 8.3 (line 470 post-claim assignment race), 15.2.1
// (shared error taxonomy) — a materialization failure fails closed and surfaces
// the canonical MCP tool code for each typed engine sentinel. The assignment
// race and the pre-claim exhaustion both map to CREDENTIAL_POOL_EXHAUSTED, a
// user-only-policy miss to USER_CREDENTIAL_NOT_FOUND, a warming pool to
// RUNTIME_UNAVAILABLE, and a Token Service outage to TOKEN_SERVICE_UNAVAILABLE.
// Against a handler that let the raw error fall through, every case would
// collapse to the INTERNAL_ERROR dispatch fallback.
func TestDelegateTaskMaterializeFailureMapping_spec_8_2(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantCode  string
		wantCat   string
		wantRetry bool
	}{
		{
			name:      "assignment race",
			err:       &podsession.CredentialAssignmentError{Provider: "anthropic_direct", Pool: "pool-a", Err: errors.New("lease raced")},
			wantCode:  "CREDENTIAL_POOL_EXHAUSTED",
			wantCat:   "POLICY",
			wantRetry: true,
		},
		{
			name:      "pre-claim exhaustion",
			err:       credrouter.ErrNoCredentialAvailable,
			wantCode:  "CREDENTIAL_POOL_EXHAUSTED",
			wantCat:   "POLICY",
			wantRetry: true,
		},
		{
			name:      "user credential not found",
			err:       credrouter.ErrUserCredentialNotFound,
			wantCode:  "USER_CREDENTIAL_NOT_FOUND",
			wantCat:   "PERMANENT",
			wantRetry: false,
		},
		{
			name:      "pool warming",
			err:       &podsession.PoolWarmingError{Pool: "pool-a", PodsWarming: 2},
			wantCode:  "RUNTIME_UNAVAILABLE",
			wantCat:   "TRANSIENT",
			wantRetry: true,
		},
		{
			name:      "token service unavailable",
			err:       credassign.ErrTokenServiceUnavailable,
			wantCode:  "TOKEN_SERVICE_UNAVAILABLE",
			wantCat:   "UPSTREAM",
			wantRetry: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &fakeChildMaterializer{err: tc.err}
			srv, _ := newDelegateMaterializeMCP(t, m)

			resp := delegateMaterializeCall(t, srv)
			result, _ := resp["result"].(map[string]any)
			env := readLennyErrorEnvelope(t, result)
			if env["code"] != tc.wantCode {
				t.Fatalf("code = %v, want %s", env["code"], tc.wantCode)
			}
			if env["category"] != tc.wantCat {
				t.Errorf("category = %v, want %s", env["category"], tc.wantCat)
			}
			if env["retryable"] != tc.wantRetry {
				t.Errorf("retryable = %v, want %v", env["retryable"], tc.wantRetry)
			}
			details, _ := env["details"].(map[string]any)
			if details["childSessionId"] != "sess_child" {
				t.Errorf("details.childSessionId = %v, want sess_child", details["childSessionId"])
			}
			if len(m.calls) != 1 {
				t.Errorf("Materialize calls = %d, want 1", len(m.calls))
			}
		})
	}
}

// spec: 8.2 (lines 93-97) — a materialization failure whose error is not one of
// the mapped credential sentinels falls through to the generic dispatch, which
// surfaces INTERNAL_ERROR, so an unexpected engine fault is not silently
// swallowed nor mislabeled as a credential outcome.
func TestDelegateTaskMaterializeUnmappedFailure_spec_8_2(t *testing.T) {
	m := &fakeChildMaterializer{err: errors.New("binder stream aborted")}
	srv, _ := newDelegateMaterializeMCP(t, m)

	resp := delegateMaterializeCall(t, srv)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "INTERNAL_ERROR" {
		t.Fatalf("code = %v, want INTERNAL_ERROR for an unmapped materialization failure", env["code"])
	}
	if len(m.calls) != 1 {
		t.Errorf("Materialize calls = %d, want 1", len(m.calls))
	}
}
