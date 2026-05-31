// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// mkRuntimeLimits registers a runtime carrying the given §5.1 limits block.
func mkRuntimeLimits(t *testing.T, rts runtimestore.Store, name string, limits *runtimestore.Limits) {
	t.Helper()
	if err := rts.Create(context.Background(), runtimestore.Runtime{
		Name: name, Type: runtimestore.TypeAgent, Image: "lenny/" + name + "@sha256:abc",
		Limits: limits,
	}); err != nil {
		t.Fatalf("create runtime %s: %v", name, err)
	}
}

// newMCPForElicitationWithRuntimes builds an MCP server wired with a §9.2
// interaction store and a §5.1 runtime registry so the §11.3 per-pool
// elicitation-limit overrides can be exercised. The global ElicitationTimeout
// and MaxElicitationsPerSession are the platform defaults the per-runtime
// `limits:` block overrides. F-11.3.6.
func newMCPForElicitationWithRuntimes(t *testing.T, globalTimeout time.Duration, globalMax int) (*mcp.Server, sessionstore.Store, interactionstore.Store, runtimestore.Store) {
	t.Helper()
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	rts := runtimestore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:                     store,
		Interactions:              interactions,
		Runtimes:                  rts,
		ElicitationTimeout:        globalTimeout,
		MaxElicitationsPerSession: globalMax,
		IDFunc:                    func() string { return "elic_gen" },
		TenantID:                  "acme",
	})
	return srv, store, interactions, rts
}

// TestElicitationBudgetHonorsPerRuntimeLimit_spec_11_3_203 proves a runtime's
// `limits.maxElicitationsPerSession` overrides the platform default (50): a
// single previously-recorded elicitation trips a per-runtime budget of 1,
// which the default-50 path would not. spec: §11.3 line 203. F-11.3.6.
func TestElicitationBudgetHonorsPerRuntimeLimit_spec_11_3_203(t *testing.T) {
	srv, store, interactions, rts := newMCPForElicitationWithRuntimes(t, 5*time.Second, 50)
	mkRuntimeLimits(t, rts, "rt-budget", &runtimestore.Limits{MaxElicitationsPerSession: 1})
	mkSessionRuntime(t, store, "sess_e", "rt-budget")

	// Pre-seed one elicitation against the origin session so the budget
	// check sees count == 1 == the per-runtime cap.
	if err := interactions.Put(context.Background(), interactionstore.Interaction{
		ID: "prior", Kind: interactionstore.KindElicitation, SessionID: "sess_e", TenantID: "acme",
	}); err != nil {
		t.Fatalf("seed prior elicitation: %v", err)
	}

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_e","message":"ok?","schema":{},"elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "QUOTA_EXCEEDED" {
		t.Fatalf("envelope.code = %v, want QUOTA_EXCEEDED (per-runtime budget of 1 tripped)", env["code"])
	}
}

// TestElicitationWaitHonorsPerRuntimeLimit_spec_11_3_202 proves a runtime's
// `limits.maxElicitationWaitSeconds` overrides the platform default: the
// elicitation times out after the 1s per-runtime wait even though the global
// ElicitationTimeout is 30s, and the returned error reports the 1s value.
// spec: §11.3 line 202. F-11.3.6.
func TestElicitationWaitHonorsPerRuntimeLimit_spec_11_3_202(t *testing.T) {
	srv, store, _, rts := newMCPForElicitationWithRuntimes(t, 30*time.Second, 50)
	mkRuntimeLimits(t, rts, "rt-wait", &runtimestore.Limits{MaxElicitationWaitSeconds: 1})
	mkSessionRuntime(t, store, "sess_w", "rt-wait")

	done := make(chan map[string]any, 1)
	go func() {
		done <- call(t, srv.Handler(), "lenny/request_elicitation",
			`{"sessionId":"sess_w","message":"ok?","schema":{},"elicitationId":"elic_w"}`)
	}()
	var resp map[string]any
	select {
	case resp = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("request_elicitation did not time out — the 30s global wait was used, not the 1s per-runtime cap")
	}
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "ELICITATION_TIMEOUT" {
		t.Fatalf("envelope.code = %v, want ELICITATION_TIMEOUT", env["code"])
	}
	details, _ := env["details"].(map[string]any)
	if got, _ := details["timeoutSeconds"].(float64); got != 1 {
		t.Errorf("details.timeoutSeconds = %v, want 1 (per-runtime override, not the 30s global)", details["timeoutSeconds"])
	}
}

// TestRequestInputExpiredPublishedToParentStream_spec_11_3_238 proves that on
// a lenny/request_input timeout the gateway emits a `request_input_expired`
// event on the parent session's stream — the channel the parent observes via
// lenny/await_children — carrying childId, requestId, and expiredAt so the
// parent can distinguish an input-request timeout from other child failures.
// spec: §11.3 line 238. F-11.3.4.
func TestRequestInputExpiredPublishedToParentStream_spec_11_3_238(t *testing.T) {
	store := memstore.New()
	reg := inputwait.NewRegistry()
	bus := sessionevents.NewBus(0)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:               store,
		Events:              bus,
		InputWaits:          reg,
		RequestInputTimeout: 10 * time.Millisecond,
		Clock:               func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:              func() string { return "sess_mcp" },
		TenantID:            "acme",
	})
	mkSession(t, store, "sess_child", session.StateRunning, "sess_parent")

	// The call blocks until the 10ms request_input timeout fires.
	resp := call(t, srv.Handler(), "lenny/request_input",
		`{"sessionId":"sess_child","requestId":"req-x","parts":[{"type":"text","text":"hi"}]}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "REQUEST_INPUT_TIMEOUT" {
		t.Fatalf("envelope.code = %v, want REQUEST_INPUT_TIMEOUT", env["code"])
	}

	// The parent's stream carries exactly the request_input_expired event;
	// the elicitation_request event went to the child's own stream.
	hist := bus.History("sess_parent", 0)
	if len(hist) != 1 {
		t.Fatalf("parent stream has %d events, want 1 (request_input_expired): %+v", len(hist), hist)
	}
	if hist[0].Type != "request_input_expired" {
		t.Errorf("parent event type = %q, want request_input_expired", hist[0].Type)
	}
	for _, want := range []string{`"request_input_expired"`, `"childId":"sess_child"`, `"requestId":"req-x"`, `"expiredAt"`} {
		if !strings.Contains(hist[0].Data, want) {
			t.Errorf("parent event data missing %s: %s", want, hist[0].Data)
		}
	}
}

// TestRequestInputExpiredSkippedForRootSession_spec_11_3_238 proves a root
// session (no parent) that times out a request_input publishes no
// request_input_expired event — there is no awaiter to notify. The child's
// own stream still carries only the elicitation_request prompt. F-11.3.4.
func TestRequestInputExpiredSkippedForRootSession_spec_11_3_238(t *testing.T) {
	store := memstore.New()
	reg := inputwait.NewRegistry()
	bus := sessionevents.NewBus(0)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:               store,
		Events:              bus,
		InputWaits:          reg,
		RequestInputTimeout: 10 * time.Millisecond,
		Clock:               func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:              func() string { return "sess_mcp" },
		TenantID:            "acme",
	})
	mkSession(t, store, "sess_root", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/request_input",
		`{"sessionId":"sess_root","requestId":"req-r","parts":[{"type":"text","text":"hi"}]}`)
	result, _ := resp["result"].(map[string]any)
	if env := readLennyErrorEnvelope(t, result); env["code"] != "REQUEST_INPUT_TIMEOUT" {
		t.Fatalf("envelope.code = %v, want REQUEST_INPUT_TIMEOUT", env["code"])
	}
	// Only the elicitation_request prompt — no request_input_expired.
	hist := bus.History("sess_root", 0)
	for _, e := range hist {
		if e.Type == "request_input_expired" {
			t.Errorf("a root session must not publish request_input_expired: %+v", e)
		}
	}
}

// mkSessionRuntime seeds a running session bound to a runtime.
func mkSessionRuntime(t *testing.T, store sessionstore.Store, id, runtimeRef string) {
	t.Helper()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning, UserID: "alice",
		RuntimeRef: runtimeRef, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}
