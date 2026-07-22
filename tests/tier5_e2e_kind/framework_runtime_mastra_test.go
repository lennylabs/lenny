// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §26.9 mastra reference runtime's
// bootstrap and message-flow contract: on message, the adapter calls
// agent.stream(message), maps Mastra tool calls to Lenny's tool_call
// envelope, and returns the final assistant message as response. See
// chat_runtime_session_test.go and framework_runtime_langgraph_test.go
// for the sibling journeys this mirrors against the other reference
// agent runtimes.
package tier5_e2e_kind_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// mastraRuntimeSessionTenant is the synthetic tenant this test
// bootstraps, per-run-suffixed for the same reason
// promptRoundtripTenant is in prompt_roundtrip_test.go.
const mastraRuntimeSessionTenant = "mastra-runtime-session-tenant"

// spec: §26.9 ("Bootstrap: on message, adapter calls agent.stream(message);
// maps Mastra tool calls to Lenny's tool_call envelope; returns the
// final assistant message as response.")
//
// diagnosis: once unskipped, a failure here means a live session
// against the mastra reference runtime either did not invoke the
// underlying Mastra agent's stream() call, did not map an emitted
// Mastra tool call onto a §15.1 tool_use event (the adapter-facing
// projection of Lenny's tool_call envelope), or did not deliver the
// final assistant message as a response part.
func TestMastraRuntimeSessionMapsToolCallsAndStreamsResponse(t *testing.T) {
	// The mastra reference runtime (github.com/lennylabs/runtime-mastra)
	// is not vendored in this repo and ships no runnable image digest
	// here; tests/spec-map.json marks §26.9 blocked_until_phase 11 and
	// lists no package (cmd/runtimes/mastra does not exist). No in-repo
	// runtime adapter performs the agent.stream() bootstrap or the
	// Mastra-tool-call-to-tool_call-envelope mapping today. Unskip once
	// a runnable mastra image or an in-repo adapter implementing this
	// bootstrap contract exists and is registered with a warm pool, and
	// a fixture Mastra agent definition (with at least one tool) is
	// available under tests/testdata.
	t.Skip("no runnable mastra reference-runtime image or in-repo adapter implementing the §26.9 agent.stream()/tool_call-mapping bootstrap contract exists yet")

	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tenant := fmt.Sprintf("%s-%d", mastraRuntimeSessionTenant, time.Now().UnixNano())
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sess, err := d.CreateAndStart(ctx, tenant, "mastra")
	if errors.Is(err, sessiondriver.ErrPoolNotReady) {
		t.Skipf("precondition not met: warm pool not ready, no session to drive a message through: %v", err)
	}
	if err != nil {
		t.Fatalf("create-and-start mastra session: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = d.Terminate(ctx, tenant, sess.ID)
	})

	events, stopEvents, err := d.StreamEvents(ctx, tenant, sess.ID, 0)
	if err != nil {
		t.Fatalf("attach session events stream: %v", err)
	}
	defer stopEvents()

	// The fixture agent's prompt is expected to trigger at least one
	// Mastra tool call, so the test can observe the adapter's mapping
	// onto Lenny's tool_call envelope on the events stream before the
	// final response arrives.
	const prompt = "use the fixture tool and report its result"
	msgResp, err := d.SendMessage(ctx, tenant, sess.ID, prompt)
	if err != nil {
		t.Fatalf("send message %q: %v", prompt, err)
	}
	if msgResp.DeliveryReceipt.Status != "delivered" {
		t.Fatalf("delivery receipt status = %q, want delivered (body: %s)",
			msgResp.DeliveryReceipt.Status, msgResp.Output)
	}

	// The synchronous POST /messages response must already carry a
	// non-empty response part carrying the final assistant message
	// produced by agent.stream() — a literal stub or a 500-tolerant
	// no-op would not produce this.
	assertNonEmptyResponsePart(t, "POST /messages response", msgResp.Output)

	// Confirm a tool_use event (the §15.1 adapter-facing projection of
	// the tool_call envelope) arrives on the events stream, proving the
	// adapter mapped the Mastra agent's tool call rather than swallowing
	// it or leaking a framework-native shape.
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	sawToolUse := false
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("events stream closed before both a tool_use and a transcript.entry event arrived for prompt %q (saw tool_use: %v)", prompt, sawToolUse)
			}
			switch ev.Type {
			case "tool_use":
				var toolUse struct {
					ToolCallID string          `json:"toolCallId"`
					Tool       string          `json:"tool"`
					Phase      string          `json:"phase"`
					Arguments  json.RawMessage `json:"arguments"`
				}
				if err := json.Unmarshal(ev.Data, &toolUse); err != nil {
					t.Fatalf("decode tool_use event: %v; raw %s", err, ev.Data)
				}
				if toolUse.ToolCallID == "" || toolUse.Tool == "" {
					t.Fatalf("tool_use event missing toolCallId or tool: %s", ev.Data)
				}
				t.Logf("observed mapped tool_call: toolCallId=%q tool=%q phase=%q", toolUse.ToolCallID, toolUse.Tool, toolUse.Phase)
				sawToolUse = true
			case "transcript.entry":
				if !sawToolUse {
					t.Fatalf("response arrived before any tool_use event for prompt %q; expected the fixture agent's tool call to be mapped first", prompt)
				}
				t.Logf("observed response event on the events stream: type=%q data=%s", ev.Type, ev.Data)
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for both a tool_use event and a transcript.entry response event for prompt %q (saw tool_use: %v)", prompt, sawToolUse)
		}
	}
}
