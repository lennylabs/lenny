// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
)

// spec: §8.3 line 157 / §4.8 line 974 — a `lenny/delegate_task` whose
// TaskSpec.input exceeds the effective contentPolicy.maxInputSize is
// rejected by the §4.8 DelegationPolicyEvaluator. The MCP shim surfaces
// the evaluator's canonical INPUT_TOO_LARGE code (not the generic
// INTERCEPTOR_REJECTED) so REST and MCP envelopes share the same
// (category, retryable) pair, and no child session is created.
// F-13.5.1 / F-8.2.9.
func TestDelegateTaskInputTooLargeSurfacesCanonicalCode_spec_8_3_157(t *testing.T) {
	chain := interceptor.NewChain()
	// A 16-byte cap with a nil resolver: every delegation falls back to
	// this default, so an over-cap input trips the evaluator.
	if err := chain.Register(interceptor.PhasePreDelegation,
		policy.NewDelegationPolicyEvaluator(nil, 16)); err != nil {
		t.Fatalf("register evaluator: %v", err)
	}
	srv, store := newDelegateMCPWithChain(t, chain)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"`+strings.Repeat("x", 64)+`"}]}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("an over-cap taskInput must be a tool error: %+v", resp)
	}
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != policy.CodeInputTooLarge {
		t.Errorf("code = %v, want %s", env["code"], policy.CodeInputTooLarge)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("an INPUT_TOO_LARGE-rejected delegation must not create a child session")
	}
}

// spec: §8.3 line 157 — a TaskSpec.input within the effective
// maxInputSize is admitted; the evaluator does not block it. F-13.5.1.
func TestDelegateTaskInputWithinCapAdmitted_spec_8_3_157(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreDelegation,
		policy.NewDelegationPolicyEvaluator(nil, 4096)); err != nil {
		t.Fatalf("register evaluator: %v", err)
	}
	srv, store := newDelegateMCPWithChain(t, chain)

	text := resultText(t, call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"short input"}]}}`))
	if !strings.Contains(text, "sess_child") {
		t.Fatalf("a within-cap delegation should proceed: %q", text)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
		t.Fatalf("child session should exist after an admitted delegation: %v", err)
	}
}
