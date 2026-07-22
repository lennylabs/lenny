// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §26.11 crewai reference runtime's
// delegation-to-lenny/delegate_task translation: a CrewAI crew's
// Task.delegate call is mapped to a lenny/delegate_task invocation
// whose child session lands on the pool the crew's
// delegationPolicyRef selects, and a delegation beyond the crew's
// delegationLease.maxDepth is rejected.
//
// The shared mcpClient, callTool, toolResultText, delegateChild, and
// lennyErrorEnvelope helpers live in elicitation_test.go and
// delegation_adversarial_test.go (same package).
package tier4_integration_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: §26.11 ("Bootstrap: ... maps any Task.delegate calls to
// lenny/delegate_task invocations that create Lenny sub-sessions
// under the parent's delegation budget." "The child session runs on a
// pool selected by the delegationPolicyRef — typically a specialist
// runtime (e.g., langgraph for a research sub-agent, claude-code for
// a coding sub-agent)." "Recursive delegation depth is bounded by the
// crew's delegationLease.maxDepth.")
//
// diagnosis: once unskipped, a failure here means a live crewai
// session either did not translate a Task.delegate call into a
// lenny/delegate_task invocation that lands a child session under the
// delegationPolicyRef-selected specialist pool, or admitted a
// delegation past the crew's configured maxDepth ceiling instead of
// rejecting it with the documented §8 depth-exceeded error.
func TestCrewAIRuntimeTranslatesTaskDelegateAndEnforcesMaxDepth(t *testing.T) {
	// The crewai reference runtime (github.com/lennylabs/runtime-crewai)
	// is not vendored in this repo and ships no runnable image digest
	// here: tests/spec-map-exceptions.yaml defers §26.11 to Wave 6 /
	// Phase 11, cmd/runtimes/crewai does not exist, and the reference
	// catalog entry (pkg/embedded/stack/catalog.go) carries no
	// delegationPolicyRef to select a specialist child pool from. No
	// in-repo runtime or stub crew performs the CrewAI
	// Task.delegate -> lenny/delegate_task translation today. This
	// mirrors the identical blocker recorded against the langgraph and
	// mastra bootstrap contracts (see framework_runtime_langgraph_test.go
	// and framework_runtime_mastra_test.go in tests/tier5_e2e_kind).
	// Unskip once a runnable crewai image or an in-repo stand-in
	// adapter implementing the Task.delegate mapping exists, together
	// with a crewai-default DelegationPolicy fixture and a specialist
	// child pool.
	t.Skip("no runnable crewai reference-runtime image or in-repo stub crew implementing the §26.11 Task.delegate -> lenny/delegate_task translation exists yet")

	gw := gateway.StartWith(t, "--dev-mode", "--delegation-default-max-depth=2")
	c := mcpClient{t: t, base: gw.BaseURL()}

	// A crewai orchestrator session delegates a specialist subtask; the
	// stub crew's Task.delegate call is expected to surface as a
	// lenny/delegate_task invocation whose child lands on the
	// delegationPolicyRef-selected specialist pool.
	root := c.runningSession()
	child := c.delegateChild(root, "crewai-specialist")

	code, body := c.rest(http.MethodGet, "/v1/sessions/"+root+"/tree", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/sessions/%s/tree: status %d (%v)", root, code, body)
	}
	treeRoot, _ := body["root"].(map[string]any)
	children, _ := treeRoot["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("crew delegation tree root children = %d, want 1: %v", len(children), children)
	}
	c0, _ := children[0].(map[string]any)
	if got, _ := c0["taskId"].(string); got != child {
		t.Errorf("tree child taskId = %q, want %q", got, child)
	}
	if got, _ := c0["runtimeRef"].(string); got != "crewai-specialist" {
		t.Errorf("tree child runtimeRef = %q, want the delegationPolicyRef-selected specialist pool", got)
	}

	// A delegation beyond the crew's configured maxDepth must be
	// rejected with the documented §8 depth-ceiling error rather than
	// silently admitted.
	c.startSession(child)
	grandchild := c.delegateChild(child, "crewai-specialist-2")
	c.startSession(grandchild)

	rpc := c.callTool("lenny/delegate_task",
		`{"parentSessionId":"`+grandchild+`","target":"crewai-too-deep"}`)
	_, isErr := toolResultText(t, rpc)
	if !isErr {
		t.Fatalf("delegation beyond the crew's maxDepth was admitted, want a depth-ceiling rejection: %v", rpc)
	}
	env := lennyErrorEnvelope(t, rpc)
	if got, _ := env["code"].(string); got != "DELEGATION_DEPTH_EXCEEDED" {
		t.Fatalf("error code = %q, want DELEGATION_DEPTH_EXCEEDED: %v", got, env)
	}
}
