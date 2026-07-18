// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// phaseInterceptor is a built-in interceptor that returns a fixed result
// only for the phase it is registered on. The fixed-result staticInterceptor
// already covers single-phase cases; this variant exists so a test can pin
// the MODIFY payload (which must echo the immutable id at PreToolResult).
type phaseInterceptor struct{ result interceptor.Result }

func (phaseInterceptor) Name() string                       { return "phase-test" }
func (phaseInterceptor) Priority() int32                    { return 200 }
func (phaseInterceptor) Builtin() bool                      { return true }
func (phaseInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (phaseInterceptor) Timeout() time.Duration             { return 0 }
func (p phaseInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return p.result, nil
}

func chainAt(t *testing.T, phase interceptor.Phase, res interceptor.Result) *interceptor.Chain {
	t.Helper()
	chain := interceptor.NewChain()
	if err := chain.Register(phase, phaseInterceptor{result: res}); err != nil {
		t.Fatalf("register %s: %v", phase, err)
	}
	return chain
}

// errorEnvelope extracts the §15.2.1 lenny error code from an isError tool
// result. It fails the test when the response is not an error.
func errorEnvelope(t *testing.T, resp map[string]any) string {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %+v", resp)
	}
	if result["isError"] != true {
		t.Fatalf("expected isError result, got %+v", result)
	}
	content, _ := result["content"].([]any)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if block["type"] == "lenny/error" {
			var env map[string]any
			if err := json.Unmarshal([]byte(block["text"].(string)), &env); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			return env["code"].(string)
		}
	}
	t.Fatalf("no lenny/error block in %+v", content)
	return ""
}

// errorEnvelopeMap extracts the full §15.2.1 lenny error envelope (code plus
// details) from an isError tool result so a test can assert the structured
// details the surface attaches. It fails the test when the response is not an
// error.
func errorEnvelopeMap(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %+v", resp)
	}
	if result["isError"] != true {
		t.Fatalf("expected isError result, got %+v", result)
	}
	content, _ := result["content"].([]any)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if block["type"] == "lenny/error" {
			var env map[string]any
			if err := json.Unmarshal([]byte(block["text"].(string)), &env); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			return env
		}
	}
	t.Fatalf("no lenny/error block in %+v", content)
	return nil
}

func runningSession(t *testing.T, store sessionstore.Store) {
	t.Helper()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_x", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

// spec: §4.8 line 1053 — a PreToolResult MODIFY rewrites the tool result
// content delivered back to the agent while preserving the correlation id.
func TestPreToolResultModifyRewritesContent_spec_4_8_line_1053(t *testing.T) {
	// The JSON-RPC call id is 1 (set by the call() helper); the MODIFY must
	// echo it as the immutable `id` or the chain would reject the MODIFY.
	modified, _ := json.Marshal(map[string]any{
		"id":      "1",
		"content": []map[string]string{{"type": "text", "text": "redacted-tool"}},
	})
	chain := chainAt(t, interceptor.PhasePreToolResult,
		interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: modified})
	srv, store := newMCPWithChain(t, chain)
	runningSession(t, store)

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	if got := resultText(t, resp); got != "redacted-tool" {
		t.Errorf("tool result = %q, want redacted-tool — PreToolResult MODIFY not applied", got)
	}
}

// spec: §4.8 line 1053 — a PreToolResult REJECT blocks delivery of the tool
// result to the agent; the dispatcher surfaces it as an isError result.
func TestPreToolResultReject_spec_4_8_line_1053(t *testing.T) {
	chain := chainAt(t, interceptor.PhasePreToolResult,
		interceptor.Result{Action: interceptor.ActionReject, Reason: "secret in tool output"})
	srv, store := newMCPWithChain(t, chain)
	runningSession(t, store)

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	if code := errorEnvelope(t, resp); code != "INTERCEPTOR_REJECTED" {
		t.Errorf("code = %q, want INTERCEPTOR_REJECTED", code)
	}
}

// spec: §4.8 line 1053, line 1060 — a PreToolResult MODIFY that alters the
// immutable correlation id is rejected by the chain with the immutable-field
// violation before the gateway applies it.
func TestPreToolResultModifyImmutableIdRejected_spec_4_8_line_1060(t *testing.T) {
	modified, _ := json.Marshal(map[string]any{
		"id":      "999",
		"content": []map[string]string{{"type": "text", "text": "x"}},
	})
	chain := chainAt(t, interceptor.PhasePreToolResult,
		interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: modified})
	srv, store := newMCPWithChain(t, chain)
	runningSession(t, store)

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	if code := errorEnvelope(t, resp); code != interceptor.CodeInterceptorImmutableFieldViolation {
		t.Errorf("code = %q, want %s", code, interceptor.CodeInterceptorImmutableFieldViolation)
	}
}

// spec: §4.8 (PreToolResult id immutability), §15.1 — a PreToolResult MODIFY
// altering the immutable tool_call id surfaces the §15.1
// INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION code and carries the violated field
// names in details.violated_fields, so a caller or SIEM can distinguish an
// interceptor tampering with the correlation id from an ordinary policy reject.
func TestPreToolResultModifyImmutableIdCarriesViolatedFields_spec_4_8_15_1(t *testing.T) {
	modified, _ := json.Marshal(map[string]any{
		"id":      "999",
		"content": []map[string]string{{"type": "text", "text": "x"}},
	})
	chain := chainAt(t, interceptor.PhasePreToolResult,
		interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: modified})
	srv, store := newMCPWithChain(t, chain)
	runningSession(t, store)

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	env := errorEnvelopeMap(t, resp)
	if code, _ := env["code"].(string); code != interceptor.CodeInterceptorImmutableFieldViolation {
		t.Fatalf("code = %q, want %s", code, interceptor.CodeInterceptorImmutableFieldViolation)
	}
	details, _ := env["details"].(map[string]any)
	if details == nil {
		t.Fatalf("no details in envelope %+v", env)
	}
	violated, _ := details["violated_fields"].([]any)
	if len(violated) != 1 || violated[0] != "id" {
		t.Errorf("details.violated_fields = %v, want [id]", details["violated_fields"])
	}
}

// spec: §4.8 (PreToolResult), §15.1 — a generic PreToolResult REJECT that is
// not an immutable-field violation falls back to INTERCEPTOR_REJECTED and
// carries no violated_fields, so the immutable branch does not steal an
// ordinary policy reject.
func TestPreToolResultGenericRejectCarriesNoViolatedFields_spec_4_8_15_1(t *testing.T) {
	chain := chainAt(t, interceptor.PhasePreToolResult,
		interceptor.Result{Action: interceptor.ActionReject, Reason: "secret in tool output"})
	srv, store := newMCPWithChain(t, chain)
	runningSession(t, store)

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	env := errorEnvelopeMap(t, resp)
	if code, _ := env["code"].(string); code != "INTERCEPTOR_REJECTED" {
		t.Fatalf("code = %q, want INTERCEPTOR_REJECTED", code)
	}
	details, _ := env["details"].(map[string]any)
	if _, ok := details["violated_fields"]; ok {
		t.Errorf("generic reject carries violated_fields = %v, want absent", details["violated_fields"])
	}
}

// spec: §4.8 line 1054 — a PostAgentOutput MODIFY rewrites the agent output
// parts before delivery to the client.
func TestPostAgentOutputModifyRewritesOutput_spec_4_8_line_1054(t *testing.T) {
	modified, _ := json.Marshal([]map[string]string{{"type": "text", "text": "redacted-output"}})
	chain := chainAt(t, interceptor.PhasePostAgentOutput,
		interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: modified})
	srv, store := newMCPWithChain(t, chain)
	runningSession(t, store)

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "redacted-output") {
		t.Errorf("output = %q, want redacted-output — PostAgentOutput MODIFY not applied", text)
	}
	if strings.Contains(text, "ping") {
		t.Errorf("output = %q still carries the original agent text", text)
	}
}

// spec: §4.8 line 1054 — a PostAgentOutput REJECT blocks delivery of the
// agent output to the client.
func TestPostAgentOutputReject_spec_4_8_line_1054(t *testing.T) {
	chain := chainAt(t, interceptor.PhasePostAgentOutput,
		interceptor.Result{Action: interceptor.ActionReject, Reason: "policy violation in output"})
	srv, store := newMCPWithChain(t, chain)
	runningSession(t, store)

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	if code := errorEnvelope(t, resp); code != "INTERCEPTOR_REJECTED" {
		t.Errorf("code = %q, want INTERCEPTOR_REJECTED", code)
	}
}

// spec: §4.8 line 1053 — with no interceptor registered at PreToolResult, the
// tool result reaches the agent unchanged.
func TestPreToolResultNoChainPassesThrough_spec_4_8_line_1053(t *testing.T) {
	srv, store := newMCP(t)
	runningSession(t, store)
	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	if got := resultText(t, resp); !strings.Contains(got, "ping") {
		t.Errorf("tool result = %q, want it to echo ping", got)
	}
}
