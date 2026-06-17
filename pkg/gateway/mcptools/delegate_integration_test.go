// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/cycle"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// fakeMetrics captures §8.2 / §16.1 delegation metric calls so the
// integration test can assert emission without depending on the
// prometheus registry. *gatewaymetrics.Metrics is the production
// implementation; this fake satisfies delegation.MetricsRecorder.
type fakeMetrics struct {
	depths []depthCall
	blocks []blockCall
}

type depthCall struct {
	pool  string
	depth int
}

type blockCall struct {
	pool, tenantID, layer, mode string
}

func (m *fakeMetrics) ObserveDelegationDepth(pool string, depth int) {
	m.depths = append(m.depths, depthCall{pool, depth})
}

func (m *fakeMetrics) IncDelegationWouldHaveBlocked(pool, tenantID, layer, mode string) {
	m.blocks = append(m.blocks, blockCall{pool, tenantID, layer, mode})
}

// rewritingInterceptor is the PreDelegation / PreRoute fake that
// rewrites the input to a fixed string so the test can assert that
// the child receives the modified content (and not the original).
// It implements the full §4.8 Interceptor interface (Name, Priority,
// Builtin, FailPolicy, Timeout, Intercept).
type rewritingInterceptor struct {
	phase   interceptor.Phase
	rewrite string
}

func (rewritingInterceptor) Name() string                       { return "rewriter" }
func (rewritingInterceptor) Priority() int32                    { return 300 }
func (rewritingInterceptor) Builtin() bool                      { return false }
func (rewritingInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (rewritingInterceptor) Timeout() time.Duration             { return 0 }

func (r rewritingInterceptor) Intercept(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
	if req.Phase != r.phase {
		return interceptor.Result{Action: interceptor.ActionAllow}, nil
	}
	if r.phase != interceptor.PhasePreRoute {
		return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: []byte(r.rewrite)}, nil
	}
	// PreRoute receives the augmented TaskSpec JSON; rewrite the input
	// field rather than the whole payload so the chain's tenant/user
	// immutability guard still passes.
	var spec struct {
		TenantID         string `json:"tenant_id"`
		UserID           string `json:"user_id,omitempty"`
		RequestedRuntime string `json:"requested_runtime,omitempty"`
		Input            string `json:"input,omitempty"`
	}
	if err := json.Unmarshal(req.Content, &spec); err != nil {
		return interceptor.Result{Action: interceptor.ActionAllow}, nil
	}
	spec.Input = r.rewrite
	out, _ := json.Marshal(spec)
	return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: out}, nil
}

// spec: §8.2 lines 56-65 — the full delegate_task flow walks
// PreDelegation → PreRoute → cycle gate → depth check → child INSERT
// → taskInput delivery, and emits the §16.1 delegation metrics on
// admission. This single integration test asserts every step on one
// invocation. spec: §8.2; §16.1 lines 27, 79.
func TestDelegateTaskFullFlowIntegration_spec_8_2(t *testing.T) {
	store := memstore.New()
	exec := newRecordingExecutor()
	rec := &fakeMetrics{}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "claude", Image: "lenny/claude@sha256:abc",
	}); err != nil {
		t.Fatalf("seed claude: %v", err)
	}
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "research", Image: "lenny/research@sha256:def",
	}); err != nil {
		t.Fatalf("seed research: %v", err)
	}

	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreDelegation, rewritingInterceptor{
		phase: interceptor.PhasePreDelegation, rewrite: "PreDel-rewrite",
	}); err != nil {
		t.Fatalf("register PreDelegation: %v", err)
	}
	if err := chain.Register(interceptor.PhasePreRoute, rewritingInterceptor{
		phase: interceptor.PhasePreRoute, rewrite: "PreRoute-rewrite",
	}); err != nil {
		t.Fatalf("register PreRoute: %v", err)
	}

	svc := delegation.NewService(store, delegation.Options{
		Clock:     func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:    func() string { return "sess_child" },
		Runtimes:  runtimes,
		Metrics:   rec,
		CycleMode: cycle.ModeEnforce,
	})

	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        store,
		Executor:     exec,
		Interceptors: chain,
		Delegation:   svc,
		Runtimes:     runtimes,
		Clock:        func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:       func() string { return "sess_mcp" },
		TenantID:     "acme",
	})

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		RuntimeRef: "claude", PoolRef: "pool-a",
		State: session.StateRunning, IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"research","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"original-input"}]}}`)
	text := resultText(t, resp)

	// Step 5/6 — TaskHandle envelope decodes against the typed shape.
	var handle struct {
		ChildSessionID string `json:"childSessionId"`
		State          string `json:"state"`
		RuntimeRef     string `json:"runtimeRef"`
		Depth          int    `json:"depth"`
	}
	if err := json.Unmarshal([]byte(text), &handle); err != nil {
		t.Fatalf("TaskHandle envelope decode: %v (raw=%q)", err, text)
	}
	if handle.ChildSessionID != "sess_child" || handle.RuntimeRef != "research" ||
		handle.State != string(session.StateCreated) || handle.Depth != 1 {
		t.Errorf("handle = %+v, want sess_child/research/created/depth=1", handle)
	}

	// Step 5 — child INSERT happened with the parent lineage.
	child, err := store.Get(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("child not stored: %v", err)
	}
	if child.ParentSessionID != "sess_parent" {
		t.Errorf("child.ParentSessionID = %q, want sess_parent", child.ParentSessionID)
	}
	if child.IsolationProfile != isolation.ProfileSandboxed {
		t.Errorf("child isolation = %q, want sandboxed (SEC-001 default)", child.IsolationProfile)
	}

	// Step 7 — the child receives the PreRoute-rewritten input as its
	// first message. The chain runs PreDelegation → PreRoute, so the
	// final delivered content is the PreRoute rewrite.
	got := exec.received("sess_child")
	if len(got) != 1 || got[0] != "PreRoute-rewrite" {
		t.Errorf("child received %v, want [\"PreRoute-rewrite\"]", got)
	}

	// Step 8 — the §16.1 delegation depth histogram observed the
	// admitted child's depth.
	if len(rec.depths) != 1 || rec.depths[0] != (depthCall{pool: "pool-b", depth: 1}) {
		t.Errorf("depth observations = %+v, want [{pool-b 1}]", rec.depths)
	}
	// Non-self-recursive hop — no would-have-blocked attribution rows.
	if len(rec.blocks) != 0 {
		t.Errorf("admitted hop produced would-have-blocked rows = %+v", rec.blocks)
	}

	// Step 9 — a follow-up cycle hop is rejected and emits the
	// per-layer attribution rows.
	rec.blocks = rec.blocks[:0]
	rec.depths = rec.depths[:0]
	cycleResp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"claude","poolRef":"pool-a"}`)
	cycleResult, _ := cycleResp["result"].(map[string]any)
	if cycleResult["isError"] != true {
		t.Errorf("self-recursive hop must be rejected: %+v", cycleResp)
	}
	cycleContent, _ := cycleResult["content"].([]any)
	if len(cycleContent) == 0 {
		t.Fatal("rejected hop returned no error content")
	}
	c0, _ := cycleContent[0].(map[string]any)
	errText, _ := c0["text"].(string)
	if !strings.Contains(errText, "cycle detected on claude/pool-a") {
		t.Errorf("error text = %q, want a cycle-detected rejection for claude/pool-a", errText)
	}
	// Under mode=enforce with no runtime / policy opt-in, all three
	// layers fail and produce one row each.
	if len(rec.blocks) != 3 {
		t.Errorf("rejected hop attribution rows = %d, want 3 (platform/runtime/policy): %+v",
			len(rec.blocks), rec.blocks)
	}
	if len(rec.depths) != 0 {
		t.Errorf("rejected hop must not emit depth, got %+v", rec.depths)
	}
}
