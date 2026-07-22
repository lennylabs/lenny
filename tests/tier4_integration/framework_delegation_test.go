// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §26.8 langgraph and §26.9 mastra
// reference runtimes' delegation contract: the adapter's tool-registration
// hook exposes lenny/delegate_task only when the Runtime's
// delegationPolicyRef is set, and a delegate call through the tool
// lands a child session under the parent's delegation budget. See
// framework_runtime_crewai_delegation_test.go for the sibling §26.11
// delegation contract this mirrors.
//
// The shared mcpClient, callTool, toolResultText, delegateChild, and
// rest helpers live in elicitation_test.go (same package).
package tier4_integration_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: §26.8 ("Delegation: LangGraph graphs may call `lenny/delegate_task`
// as a tool. The runtime exposes the delegation tool via the adapter's
// tool-registration hook when the Runtime's `delegationPolicyRef` is
// set."); §26.9 ("Delegation: Mastra agents may call
// `lenny/delegate_task`; the adapter registers it as a Mastra tool at
// agent initialization time.")
//
// diagnosis: once unskipped, a failure here means a live langgraph or
// mastra session either registered lenny/delegate_task as a callable
// tool when the runtime's delegationPolicyRef was unset (the
// tool-registration hook is not actually conditional on the field, a
// §8 delegation-budget leak), or a graph/agent that did call the tool
// with delegationPolicyRef set failed to land a child session under
// the parent's delegation budget.
func TestLanggraphAndMastraRegisterDelegateTaskOnlyWhenPolicyRefSet(t *testing.T) {
	// The langgraph and mastra reference runtimes
	// (github.com/lennylabs/runtime-langgraph, github.com/lennylabs/runtime-mastra)
	// are not vendored in this repo and ship no runnable image digest
	// here: tests/spec-map-exceptions.yaml defers §26.8/§26.9 to Wave 6
	// / Phase 11, cmd/runtimes/langgraph and cmd/runtimes/mastra do not
	// exist, and the reference catalog entries
	// (pkg/embedded/stack/catalog.go) carry no delegationPolicyRef to
	// select a specialist child pool from. No in-repo runtime adapter
	// or fixture graph/agent performs the LangGraph
	// tool-registration-hook or Mastra agent-initialization-time
	// lenny/delegate_task registration today. This mirrors the
	// identical blocker recorded against the crewai delegation
	// contract (framework_runtime_crewai_delegation_test.go) and the
	// langgraph/mastra bootstrap contracts
	// (tests/tier5_e2e_kind/framework_runtime_langgraph_test.go,
	// ..._mastra_test.go). Unskip once a runnable langgraph/mastra
	// image or an in-repo stand-in adapter implementing the
	// conditional tool-registration hook exists, together with a
	// fixture graph/agent that calls lenny/delegate_task, a
	// delegationPolicyRef-bearing DelegationPolicy record, and a
	// specialist child pool.
	t.Skip("no runnable langgraph/mastra reference-runtime image or in-repo adapter implementing the §26.8/§26.9 conditional lenny/delegate_task tool-registration hook exists yet")

	gw := gateway.StartWith(t, "--dev-mode", "--delegation-default-max-depth=2")
	c := mcpClient{t: t, base: gw.BaseURL()}

	for _, runtime := range []string{"langgraph", "mastra"} {
		t.Run(runtime, func(t *testing.T) {
			// A session against a runtimeRef whose Runtime record has
			// delegationPolicyRef set must expose lenny/delegate_task
			// via the adapter's tool-registration hook, and the
			// fixture graph's/agent's call to it must land a child
			// session under the parent's delegation budget.
			withPolicy := runtime + "-with-policy"
			root := c.runningSession()
			child := c.delegateChild(root, withPolicy)

			code, body := c.rest(http.MethodGet, "/v1/sessions/"+root+"/tree", nil)
			if code != http.StatusOK {
				t.Fatalf("GET /v1/sessions/%s/tree: status %d (%v)", root, code, body)
			}
			treeRoot, _ := body["root"].(map[string]any)
			children, _ := treeRoot["children"].([]any)
			if len(children) != 1 {
				t.Fatalf("%s delegation tree root children = %d, want 1: %v", runtime, len(children), children)
			}
			c0, _ := children[0].(map[string]any)
			if got, _ := c0["taskId"].(string); got != child {
				t.Errorf("%s tree child taskId = %q, want %q", runtime, got, child)
			}

			// A session against a runtimeRef whose Runtime record has
			// no delegationPolicyRef set must not expose
			// lenny/delegate_task at all: the adapter's
			// tool-registration hook is conditional on the field, not
			// unconditional. Once a real (or in-repo stand-in) adapter
			// exists, this asserts against the adapter's own tool
			// registration surface (for example, the tool list a
			// stubbed LLM-provider capture records on the compiled
			// graph's/agent's outbound inference call), not the
			// platform gateway's always-on lenny/delegate_task MCP
			// tool, which every session can reach regardless of this
			// per-runtime hook.
			assertDelegateTaskNotRegistered(t, c, runtime+"-without-policy")
		})
	}
}

// assertDelegateTaskNotRegistered asserts that a session against a
// runtimeRef with no delegationPolicyRef never surfaces
// lenny/delegate_task on the adapter's own tool-registration surface.
// It is unimplemented pending the in-repo stand-in adapter this
// finding's Dependencies describe; see the package-level t.Skip above,
// which keeps this function unreachable until then.
func assertDelegateTaskNotRegistered(t *testing.T, c mcpClient, runtimeRef string) {
	t.Helper()
	_ = c
	t.Fatalf("assertDelegateTaskNotRegistered is unimplemented pending an in-repo langgraph/mastra stand-in adapter (runtimeRef=%q)", runtimeRef)
}
