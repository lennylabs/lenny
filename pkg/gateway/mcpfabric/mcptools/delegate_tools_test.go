// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func TestDelegateTaskRejectsMCPTargetSurfacesEnvelopeCode_spec_15_2_1_F_8_5_10(t *testing.T) {
	srv, store, rt := newMCPWithRuntimes(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent_810", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = rt.Create(context.Background(), runtimestore.Runtime{
		Name: "fs-mcp", Type: runtimestore.TypeMCP, Image: "lenny/fs-mcp@sha256:abc",
	})
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent_810","target":"fs-mcp","poolRef":"pool-b"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "TARGET_NOT_AN_AGENT" {
		t.Errorf("envelope.code = %v, want TARGET_NOT_AN_AGENT", env["code"])
	}
	if env["category"] != "POLICY" {
		t.Errorf("envelope.category = %v, want POLICY", env["category"])
	}
	if env["retryable"] != false {
		t.Errorf("envelope.retryable = %v, want false", env["retryable"])
	}
}

// TestRequestInputTimeoutSurfacesEnvelopeCode_spec_15_2_1_F_8_5_10
// verifies the §8.5 row `lenny/request_input` timeout surfaces
// REQUEST_INPUT_TIMEOUT through the lenny envelope. spec: §15.2.1
// rule 3; F-8.5.10.
func TestDelegateTaskRejectsInsideInterceptorWeakeningCooldown_spec_8_3_181(t *testing.T) {
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	policies := delegationpolicystore.NewMemory()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent_cd", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "gemini", Image: "lenny/gemini@sha256:abc",
		DelegationPolicyRef: "scan-policy",
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if err := policies.Create(context.Background(), delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "scan-policy",
		ContentPolicy: delegationpolicystore.ContentPolicy{
			InterceptorRef: "guardrails", ScanExportedFiles: false,
		},
		// 30 s into a 60 s cooldown window — retryAfter must surface 30.
		ScanExportedFilesWeakenedAt: now.Add(-30 * time.Second),
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	svc := delegation.NewService(store, delegation.Options{
		Runtimes:                     runtimes,
		Policies:                     policies,
		Clock:                        func() time.Time { return now },
		IDFunc:                       func() string { return "sess_child" },
		InterceptorWeakeningCooldown: 60 * time.Second,
	})

	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:      store,
		Executor:   executor.NewEchoExecutor(),
		Runtimes:   runtimes,
		Delegation: svc,
		Clock:      func() time.Time { return now },
		IDFunc:     func() string { return "sess_mcp" },
		TenantID:   "acme",
	})

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent_cd","target":"gemini","poolRef":"pool-b"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "INTERCEPTOR_WEAKENING_COOLDOWN" {
		t.Errorf("envelope.code = %v, want INTERCEPTOR_WEAKENING_COOLDOWN", env["code"])
	}
	if env["category"] != "TRANSIENT" {
		t.Errorf("envelope.category = %v, want TRANSIENT", env["category"])
	}
	if env["retryable"] != true {
		t.Errorf("envelope.retryable = %v, want true", env["retryable"])
	}
	details, _ := env["details"].(map[string]any)
	if details == nil {
		t.Fatalf("envelope.details missing: %+v", env)
	}
	if details["policyName"] != "scan-policy" {
		t.Errorf("details.policyName = %v, want scan-policy", details["policyName"])
	}
	// JSON numbers decode as float64.
	if details["retryAfterSeconds"] != float64(30) {
		t.Errorf("details.retryAfterSeconds = %v, want 30", details["retryAfterSeconds"])
	}
	if details["cooldownSeconds"] != float64(60) {
		t.Errorf("details.cooldownSeconds = %v, want 60", details["cooldownSeconds"])
	}
}

// TestRequestInputTimeoutCarriesExpiredAt_spec_11_3_238 verifies the
// §11.3 line 238 timeout error envelope details include the ISO 8601
// `expiredAt` timestamp plus `requestId` and `timeoutSeconds`. F-11.3.23.
func TestDelegateTaskTool(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"gemini","poolRef":"pool-b"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "sess_child") {
		t.Errorf("delegate result: %q", text)
	}
	child, err := store.Get(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("child not stored: %v", err)
	}
	if child.ParentSessionID != "sess_parent" {
		t.Errorf("child parent: %q", child.ParentSessionID)
	}
}

func TestDelegateTaskToolDetectsCycle(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})
	// Delegating back to the parent's own (runtime, pool) is a cycle.
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"claude","poolRef":"pool-a"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("cycle should be a tool error: %+v", resp)
	}
}

// spec: §8.2 line 17 — `lenny/delegate_task` returns a TaskHandle. v1
// ships the typed envelope (childSessionId, state, runtimeRef, depth)
// so callers can decode against a stable shape rather than a
// hand-rolled JSON string.
func TestDelegateTaskToolReturnsTaskHandleEnvelope(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"gemini","poolRef":"pool-b"}`)
	text := resultText(t, resp)
	var handle struct {
		ChildSessionID string `json:"childSessionId"`
		State          string `json:"state"`
		RuntimeRef     string `json:"runtimeRef"`
		Depth          int    `json:"depth"`
	}
	if err := json.Unmarshal([]byte(text), &handle); err != nil {
		t.Fatalf("TaskHandle is not valid JSON: %v (raw=%q)", err, text)
	}
	if handle.ChildSessionID != "sess_child" {
		t.Errorf("childSessionId = %q, want sess_child", handle.ChildSessionID)
	}
	if handle.State != string(session.StateCreated) {
		t.Errorf("state = %q, want %q", handle.State, session.StateCreated)
	}
	if handle.RuntimeRef != "gemini" {
		t.Errorf("runtimeRef = %q, want gemini", handle.RuntimeRef)
	}
	if handle.Depth != 1 {
		t.Errorf("depth = %d, want 1 (root parent → child)", handle.Depth)
	}
}

// spec: §8.2 line 58 — Delegate rejects a userless parent with
// ErrParentNoUser; the MCP shim surfaces DELEGATION_PARENT_NO_USER
// so callers can distinguish it from the generic error path.
func TestDelegateTaskToolRejectsUserlessParent(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		// UserID intentionally omitted.
		ID: "sess_parent", TenantID: "acme",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"gemini","poolRef":"pool-b"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("userless parent must be a tool error: %+v", resp)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("error result has no content: %+v", resp)
	}
	c0, _ := content[0].(map[string]any)
	errText, _ := c0["text"].(string)
	if !strings.Contains(errText, "DELEGATION_PARENT_NO_USER") {
		t.Errorf("error text = %q, want DELEGATION_PARENT_NO_USER reason", errText)
	}
	// No child must be created when the gate trips.
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("a child was created despite the userless-parent rejection")
	}
}

// mkSession inserts a session row for the cancel_child tree tests.
