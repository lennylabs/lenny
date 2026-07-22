// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §8.2 delegation admission gates driven
// adversarially through the real cmd/lenny-gateway binary — a
// self-recursive cycle, a depth-ceiling overrun, and a fan-out
// lease-slice widening — asserting the canonical rejection codes and
// their `details` payloads the §15.1 error catalog and the §8.2
// decision matrix define.
//
// The shared mcpClient, callTool, and delegateChild helpers live in
// elicitation_test.go (same package).

package tier4_integration_test

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// lennyErrorEnvelope decodes the §15.2.1 `lenny/error` content block
// from a tools/call JSON-RPC response, failing the test if the
// response carried no such block.
func lennyErrorEnvelope(t *testing.T, rpc map[string]any) map[string]any {
	t.Helper()
	result, ok := rpc["result"].(map[string]any)
	if !ok {
		t.Fatalf("MCP response carried no result: %v", rpc)
	}
	content, _ := result["content"].([]any)
	for _, c := range content {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cm["type"] != "lenny/error" {
			continue
		}
		var env map[string]any
		text, _ := cm["text"].(string)
		if err := json.Unmarshal([]byte(text), &env); err != nil {
			t.Fatalf("decode lenny/error envelope: %v (%q)", err, text)
		}
		return env
	}
	t.Fatalf("MCP response carried no lenny/error content block: %v", result)
	return nil
}

// spec: §8.2 "Cycle-detection decision matrix (three-layer AND gate
// under mode: enforce)" — "A self-recursive hop ... is admitted if
// and only if all three layers opt in. If any layer is false, the
// delegation is rejected with DELEGATION_CYCLE_DETECTED" and
// "`details.blockedBy` carries the first layer (in declared order
// platform -> runtime -> policy) whose value is false".
//
// diagnosis: a self-recursive lenny/delegate_task hop (target ==
// the caller's own runtime identity) that should be rejected with
// DELEGATION_CYCLE_DETECTED instead surfaced as a generic
// INTERNAL_ERROR (or was admitted) when driven through the real
// cmd/lenny-gateway binary. This pins the §8.2 three-layer AND gate
// and its `details.blockedBy`/`details.effectiveSettings` payload to
// the wire contract an agent actually observes, not just the
// pkg/gateway/mcpfabric/delegation unit tests that call
// Service.Delegate directly.
//
// The platform layer is forced on (--gateway-allow-self-recursion=true)
// so the test's expected outcome is independent of the flag's
// zero-value default; the runtime and policy layers stay at their
// spec default (false) because this harness registers no Runtime or
// DelegationPolicy resource for "echo", so neither layer can opt in.
func TestDelegationCycleDetectedRejectsSelfRecursion(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode", "--gateway-allow-self-recursion=true")
	c := mcpClient{t: t, base: gw.BaseURL()}

	root := c.runningSession()

	// The target names the root's own runtime identity, so the
	// resolved target's (runtime_name, pool_name) tuple already
	// appears in the caller's lineage (the caller IS that identity).
	rpc := c.callTool("lenny/delegate_task",
		`{"parentSessionId":"`+root+`","target":"echo"}`)
	_, isErr := toolResultText(t, rpc)
	if !isErr {
		t.Fatalf("self-recursive delegate_task was admitted, want DELEGATION_CYCLE_DETECTED: %v", rpc)
	}

	env := lennyErrorEnvelope(t, rpc)
	if got, _ := env["code"].(string); got != "DELEGATION_CYCLE_DETECTED" {
		t.Fatalf("error code = %q, want DELEGATION_CYCLE_DETECTED: %v", got, env)
	}
	details, _ := env["details"].(map[string]any)
	if details == nil {
		t.Fatalf("DELEGATION_CYCLE_DETECTED carried no details: %v", env)
	}
	// spec: §8.2 — "`details.blockedBy` ... names the first layer in
	// declared order whose value was `false`". Platform is forced
	// true above; the runtime layer (default false, no Runtime
	// resource registered for "echo") is the first false layer.
	if got, _ := details["blockedBy"].(string); got != "runtime" {
		t.Errorf("details.blockedBy = %q, want %q: %v", got, "runtime", details)
	}
	if got, _ := details["cycleRuntimeName"].(string); got != "echo" {
		t.Errorf("details.cycleRuntimeName = %q, want %q", got, "echo")
	}
	// spec: §8.2 — "`details.effectiveSettings` is the resolved
	// `{mode, platform, runtime, policy}` tuple at decision time".
	eff, _ := details["effectiveSettings"].(map[string]any)
	if eff == nil {
		t.Fatalf("details.effectiveSettings missing: %v", details)
	}
	if got, _ := eff["mode"].(string); got != "enforce" {
		t.Errorf("effectiveSettings.mode = %q, want %q", got, "enforce")
	}
	if got, ok := eff["platform"].(bool); !ok || !got {
		t.Errorf("effectiveSettings.platform = %v, want true", eff["platform"])
	}
	if got, ok := eff["runtime"].(bool); !ok || got {
		t.Errorf("effectiveSettings.runtime = %v, want false", eff["runtime"])
	}
}

// spec: §8.2 "2a-bis. Effective maxDepth resolution (normative,
// always enforced)" — "Every effective delegation lease MUST carry a
// positive integer maxDepth ... and applies it to every hop
// regardless of `gateway.cycleDetection.mode`." Combined with the
// lease package's CheckDepth contract (pkg/delegation/lease/lease.go)
// that the gateway rejects a hop once `currentDepth + 1 > maxDepth`.
//
// diagnosis: a delegation hop that would push the chain past the
// resolved maxDepth ceiling did not reject through the real
// cmd/lenny-gateway binary — either it was wrongly admitted, or the
// rejection surfaced as a generic INTERNAL_ERROR rather than the
// canonical depth-exceeded code the lease package documents. This
// pins the §8.2.bis positive-integer maxDepth bound to the wire the
// gateway actually serves, not just pkg/gateway/mcpfabric/delegation's
// direct Service.Delegate unit tests.
func TestDelegationDepthExceededRejectsChainBeyondCeiling(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode", "--delegation-default-max-depth=2")
	c := mcpClient{t: t, base: gw.BaseURL()}

	// root is depth 0. Two successful hops reach depth 2, the
	// configured ceiling; a third hop would need depth 3 and must be
	// rejected. Each hop uses a distinct runtime identity so the §8.2
	// cycle detector (a separate gate) does not reject the chain
	// first.
	root := c.runningSession()
	child1 := c.delegateChild(root, "echo-mid")
	child2 := c.delegateChild(child1, "echo-leaf")

	rpc := c.callTool("lenny/delegate_task",
		`{"parentSessionId":"`+child2+`","target":"echo-toodeep"}`)
	_, isErr := toolResultText(t, rpc)
	if !isErr {
		t.Fatalf("depth-4 delegate_task under maxDepth=2 was admitted, want a depth-ceiling rejection: %v", rpc)
	}

	env := lennyErrorEnvelope(t, rpc)
	if got, _ := env["code"].(string); got != "DELEGATION_DEPTH_EXCEEDED" {
		t.Fatalf("error code = %q, want DELEGATION_DEPTH_EXCEEDED: %v", got, env)
	}
}

// spec: §8.2 LeaseSlice table — "All fields are optional ... the
// gateway rejects any `lease_slice` that exceeds the parent's
// remaining budget" and the `leaseSlice` MCP field description: "Each
// axis may only tighten the parent's granted budget; a slice
// exceeding the parent's remaining budget on any axis is rejected
// with BUDGET_EXHAUSTED."
//
// diagnosis: a child lease_slice that widens the fan-out axis
// (maxChildrenTotal) beyond what the parent's own granted slice
// allows did not reject with BUDGET_EXHAUSTED through the real
// cmd/lenny-gateway binary. This pins the §8.2 lease-slice
// narrowing invariant to the wire contract, not just the
// pkg/delegation/lease unit tests that call ValidateChildSlice
// directly.
func TestDelegationBudgetExhaustedRejectsWidenedFanOutSlice(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode")
	c := mcpClient{t: t, base: gw.BaseURL()}

	root := c.runningSession()

	// Grant child1 a fan-out ceiling of 1 (root itself is unbounded,
	// so this is the first slice with a real MaxChildrenTotal ceiling
	// in the chain).
	rpc := c.callTool("lenny/delegate_task",
		`{"parentSessionId":"`+root+`","target":"echo-mid","leaseSlice":{"maxChildrenTotal":1}}`)
	text, isErr := toolResultText(t, rpc)
	if isErr {
		t.Fatalf("lenny/delegate_task (grant maxChildrenTotal=1) failed: %s", text)
	}
	var granted struct {
		ChildSessionID string `json:"childSessionId"`
	}
	if err := json.Unmarshal([]byte(text), &granted); err != nil || granted.ChildSessionID == "" {
		t.Fatalf("delegate_task result is not a childSessionId JSON object: %v (%q)", err, text)
	}

	// child1 (granted maxChildrenTotal=1) tries to hand its own child
	// a wider fan-out ceiling of 5. §8.2: a child may only tighten,
	// never widen, so this must reject.
	rpc = c.callTool("lenny/delegate_task",
		`{"parentSessionId":"`+granted.ChildSessionID+`","target":"echo-leaf","leaseSlice":{"maxChildrenTotal":5}}`)
	_, isErr = toolResultText(t, rpc)
	if !isErr {
		t.Fatalf("widened leaseSlice.maxChildrenTotal was admitted, want BUDGET_EXHAUSTED: %v", rpc)
	}

	env := lennyErrorEnvelope(t, rpc)
	if got, _ := env["code"].(string); got != "BUDGET_EXHAUSTED" {
		t.Fatalf("error code = %q, want BUDGET_EXHAUSTED: %v", got, env)
	}
}
