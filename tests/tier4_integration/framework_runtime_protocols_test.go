// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration matrix for the §26.8 langgraph and §26.9 mastra
// framework-runtime catalog entries' declared client-protocol and
// injection-mode surface. Both entries declare
// "capabilities.interaction: multi_turn; injection: { supported: true,
// modes: [immediate, queued] }" (spec/26_reference-runtime-catalog.md:377,
// 407), and the platform exposes four client-facing protocols (REST, MCP,
// OpenAI Chat Completions, Open Responses; spec/15_external-api-surface.md).
// No existing test parameterizes a langgraph- or mastra-declaring runtime
// over that protocol set, and no test exercises the `delivery: "queued"`
// injection mode (spec/15_external-api-surface.md: `"queued"` | "Buffer
// for next natural pause. | Message appended to the session inbox.
// Delivered in FIFO order when the runtime next enters `ready_for_input`.
// Receipt: `queued`.") against either runtime.
//
// This file is currently a fully-skipped placeholder: closing it in full
// requires either the externally-published github.com/lennylabs/
// runtime-langgraph / runtime-mastra images (not vendored in this repo,
// same Phase-11 blocker as
// tests/tier5_e2e_kind/framework_runtime_langgraph_test.go and
// ..._mastra_test.go), or a decision to build an in-repo stand-in adapter
// ahead of Phase 11 — the same still-open question tracked against the
// bootstrap/message-flow gap for these two runtimes. Independently, the
// OpenAI Chat Completions and Open Responses cells cannot exercise
// `delivery: "queued"` at all against the current translators: both
// pkg/gateway/environment/translator/openai_chat.go and open_responses.go
// mint a fresh session ID per call and close it immediately after one
// exchange, so there is no persisted session for a second, queued message
// to target. See the per-cell skip reasons below.
package tier4_integration_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/matrix"
)

// spec: §26.8 (spec/26_reference-runtime-catalog.md:377) "capabilities.
// interaction: multi_turn; injection: { supported: true, modes:
// [immediate, queued] }." and §26.9 (spec/26_reference-runtime-catalog.md:
// 407), the identical sentence for mastra; spec/15_external-api-surface.md
// delivery-value table, `"queued"` row ("Buffer for next natural pause. |
// Message appended to the session inbox. Delivered in FIFO order when the
// runtime next enters `ready_for_input`. Receipt: `queued`.").
//
// diagnosis: once unskipped, a failure in a rest/mcp cell means a session
// against a runtime declaring the §26.8/§26.9 injection contract did not
// buffer a `delivery: "queued"` message sent while the runtime was busy
// processing another turn, or did not deliver the buffered message (in
// FIFO order, after the in-flight turn) once the runtime became
// `ready_for_input` again, on that protocol. A failure in a
// chat_completions/open_responses cell (once those translators grow
// session continuation) means the newly added continuation path does not
// honor the same buffering contract.
func TestFrameworkRuntimeQueuedInjectionAcrossProtocols(t *testing.T) {
	matrix.Run(
		t,
		matrix.Dim("runtime", []string{"langgraph", "mastra"}),
		matrix.Dim("protocol", []string{"rest", "mcp", "chat_completions", "open_responses"}),
	)(func(t *testing.T, cell map[string]string) {
		switch cell["protocol"] {
		case "chat_completions", "open_responses":
			// The OpenAI-style translators are single-exchange: each call
			// mints a new session ID and closes it right after the executor
			// responds (pkg/gateway/environment/translator/openai_chat.go
			// handleCreateCompletion calls h.exec.Close immediately after
			// h.exec.Send; open_responses.go does the same). There is no
			// session left running for a second, `delivery: "queued"`
			// message to target, so the §15.1 queued-injection contract has
			// no surface on these two protocols as currently implemented.
			// Whether that is the intended scope of the §26.8/§26.9
			// injection.modes declaration, or these two protocols are
			// expected to grow session continuation, is an open spec
			// question this finding surfaces rather than resolves.
			t.Skip("the OpenAI Chat Completions and Open Responses translators close the session immediately after one exchange, so a running session never exists for a queued injection to target; see the file comment for the open spec question")
		}

		// rest and mcp: the gateway's POST /v1/sessions/{id}/messages and
		// lenny/send_message both accept `delivery: "queued"` against a
		// running session, so the protocol surface exists. What is
		// missing is a runtime, registered under this cell's runtime name,
		// that can genuinely be mid-turn (busy, not yet ready_for_input)
		// when the queued message arrives:
		//
		//   - The real github.com/lennylabs/runtime-langgraph and
		//     runtime-mastra adapters are not vendored in this repo
		//     (same Phase-11 blocker as
		//     tests/tier5_e2e_kind/framework_runtime_langgraph_test.go and
		//     ..._mastra_test.go).
		//   - Every locally available stand-in executor completes a turn
		//     synchronously: the in-process EchoExecutor
		//     (pkg/gateway/session/executor/echo.go) returns from Send
		//     immediately, and the out-of-process streaming-echo adapter
		//     (cmd/runtimes/streaming-echo) answers `message` inline with
		//     no delay hook. pkg/gateway/session/messagerouting/
		//     messagerouting.go documents the consequence directly: "a
		//     synchronous in-process executor is ready by construction, so
		//     [the runtime-busy buffering path] collapses to direct
		//     delivery" — there is no in-repo way to observe a message
		//     arriving while a turn is genuinely in flight.
		t.Skip("no runtime (real langgraph/mastra adapter or a delay-capable stand-in) exists locally that can be genuinely mid-turn when a queued message arrives; see the file comment")
	})
}
