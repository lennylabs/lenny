// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §4 PreDelegation interceptor + §8.2 lenny/delegate_task task input.

// recordingExecutor records every Send so a test can assert what a
// delegated child received as its first message.
type recordingExecutor struct {
	mu   sync.Mutex
	sent map[string][]string
}

func newRecordingExecutor() *recordingExecutor {
	return &recordingExecutor{sent: map[string][]string{}}
}

func (e *recordingExecutor) Send(_ context.Context, sessionID string, msgs []executor.Message) (executor.Response, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, m := range msgs {
		e.sent[sessionID] = append(e.sent[sessionID], m.Content)
	}
	return executor.Response{Parts: []executor.OutputPart{{Type: "text", Text: "ok"}}}, nil
}

func (e *recordingExecutor) Close(context.Context, string) error { return nil }

func (e *recordingExecutor) received(sessionID string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.sent[sessionID]...)
}

func newMCPForDelegate(t *testing.T, exec executor.Executor, chain *interceptor.Chain) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        store,
		Executor:     exec,
		Interceptors: chain,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		RuntimeRef: "claude", PoolRef: "pool-a",
		State:     session.StateRunning, IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	return srv, store
}

func TestDelegateTaskDeliversTaskInput(t *testing.T) {
	rec := newRecordingExecutor()
	srv, _ := newMCPForDelegate(t, rec, nil)
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"echo","task":{"input":[{"type":"text","inline":"summarize the logs"}]}}`)
	if got := resultText(t, resp); got == "" {
		t.Fatal("delegate_task returned no result")
	}
	if got := rec.received("sess_child"); len(got) != 1 || got[0] != "summarize the logs" {
		t.Errorf("child received %v, want [\"summarize the logs\"]", got)
	}
}

func TestDelegateTaskNoTaskInputSkipsDelivery(t *testing.T) {
	rec := newRecordingExecutor()
	srv, _ := newMCPForDelegate(t, rec, nil)
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"echo"}`)
	resultText(t, resp) // must not error
	if got := rec.received("sess_child"); len(got) != 0 {
		t.Errorf("child received %v with no taskInput, want nothing", got)
	}
}

func TestDelegateTaskPreDelegationRejects(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreDelegation, staticInterceptor{
		result: interceptor.Result{Action: interceptor.ActionReject, Reason: "unsafe task content"},
	}); err != nil {
		t.Fatalf("register interceptor: %v", err)
	}
	rec := newRecordingExecutor()
	srv, store := newMCPForDelegate(t, rec, chain)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"echo","task":{"input":[{"type":"text","inline":"do something bad"}]}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("a PreDelegation REJECT should be a tool error: %+v", resp)
	}
	// The delegation was blocked before the child was created.
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("a rejected delegation must not create the child session")
	}
}

// TestDelegateTaskPreDelegationRejectUsesSharedErrorTaxonomy pins
// §15.2.1 line 1386: the manual MCP-only lenny/delegate_task tool
// must use the shared error taxonomy. A deliberate PreDelegation
// REJECT surfaces as the canonical INTERCEPTOR_REJECTED envelope on
// the MCP wire (CategoryPolicy, retryable:false) instead of a bare
// fmt.Errorf the dispatcher would default to INTERNAL_ERROR/TRANSIENT.
// F-15.2.11.
func TestDelegateTaskPreDelegationRejectUsesSharedErrorTaxonomy_spec_15_2_1_1386(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreDelegation, staticInterceptor{
		result: interceptor.Result{Action: interceptor.ActionReject, Reason: "policy says no"},
	}); err != nil {
		t.Fatalf("register interceptor: %v", err)
	}
	rec := newRecordingExecutor()
	srv, _ := newMCPForDelegate(t, rec, chain)
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"echo","task":{"input":[{"type":"text","inline":"do something bad"}]}}`)
	if code := errorEnvelope(t, resp); code != "INTERCEPTOR_REJECTED" {
		t.Errorf("code = %q, want INTERCEPTOR_REJECTED", code)
	}
}

// TestDelegateTaskPreRouteRejectUsesSharedErrorTaxonomy mirrors the
// PreDelegation test for the §8.2 line 90 PreRoute hop. Same
// §15.2.1 line 1386 contract. F-15.2.11.
func TestDelegateTaskPreRouteRejectUsesSharedErrorTaxonomy_spec_15_2_1_1386(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreRoute, staticInterceptor{
		result: interceptor.Result{Action: interceptor.ActionReject, Reason: "route-time policy says no"},
	}); err != nil {
		t.Fatalf("register interceptor: %v", err)
	}
	rec := newRecordingExecutor()
	srv, _ := newMCPForDelegate(t, rec, chain)
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"echo","task":{"input":[{"type":"text","inline":"work"}]}}`)
	if code := errorEnvelope(t, resp); code != "INTERCEPTOR_REJECTED" {
		t.Errorf("code = %q, want INTERCEPTOR_REJECTED", code)
	}
}

func TestDelegateTaskPreDelegationModifies(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreDelegation, staticInterceptor{
		result: interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: []byte("scrubbed task")},
	}); err != nil {
		t.Fatalf("register interceptor: %v", err)
	}
	rec := newRecordingExecutor()
	srv, _ := newMCPForDelegate(t, rec, chain)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"echo","task":{"input":[{"type":"text","inline":"original task"}]}}`)
	resultText(t, resp) // must not error
	// §4: a PreDelegation MODIFY rewrites the input the child receives.
	if got := rec.received("sess_child"); len(got) != 1 || got[0] != "scrubbed task" {
		t.Errorf("child received %v, want [\"scrubbed task\"] (the interceptor-modified input)", got)
	}
}

// capturePolicyAppender records the §16.7 interceptor.rejected payload.
type capturePolicyAppender struct{ payload map[string]any }

func (a *capturePolicyAppender) Append(_ context.Context, _, _ string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	a.payload = map[string]any{}
	_ = json.Unmarshal(payload, &a.payload)
	return audit.Row{}, nil
}

// spec: §4.8 line 981, §11.7 — a PreDelegation chain REJECT writes an
// interceptor.rejected audit row naming the rejecting interceptor.
func TestDelegateTaskPreDelegationRejectAudits(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreDelegation, staticInterceptor{
		result: interceptor.Result{Action: interceptor.ActionReject, Reason: "unsafe task content"},
	}); err != nil {
		t.Fatalf("register interceptor: %v", err)
	}
	app := &capturePolicyAppender{}
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        store,
		Executor:     newRecordingExecutor(),
		Interceptors: chain,
		PolicyAudit:  policy.NewAuditSink(app, nil),
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		TenantID: "acme",
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		RuntimeRef: "claude", PoolRef: "pool-a",
		State:     session.StateRunning, IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"echo","task":{"input":[{"type":"text","inline":"do something bad"}]}}`)

	if app.payload == nil {
		t.Fatal("no interceptor.rejected audit row written on PreDelegation REJECT")
	}
	if got := app.payload["interceptor_ref"]; got != "test-static" {
		t.Errorf("interceptor_ref = %v, want test-static", got)
	}
	if got := app.payload["phase"]; got != string(interceptor.PhasePreDelegation) {
		t.Errorf("phase = %v, want PreDelegation", got)
	}
}
