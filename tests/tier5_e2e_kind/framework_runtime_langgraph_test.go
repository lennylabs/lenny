// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §26.8 langgraph reference runtime's
// bootstrap and message-flow contract: the adapter imports the user's
// graph module, compiles it, attaches a RunnableConfig, and drives
// subsequent message deliveries through graph.ainvoke / graph.astream.
// See chat_runtime_session_test.go for the sibling journey this mirrors
// against the §26.7 chat reference runtime.
package tier5_e2e_kind_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// langgraphRuntimeSessionTenant is the synthetic tenant this test
// bootstraps, per-run-suffixed for the same reason
// promptRoundtripTenant is in prompt_roundtrip_test.go.
const langgraphRuntimeSessionTenant = "langgraph-runtime-session-tenant"

// spec: §26.8 ("Bootstrap: adapter imports the module specified by
// runtimeOptions.graphModule, invokes .compile() on the graph, and
// attaches a LangGraph RunnableConfig whose configurable field is
// populated from runtimeOptions.configSchema. Subsequent message
// deliveries invoke graph.ainvoke / graph.astream depending on the
// graph's declared output style.")
//
// diagnosis: once unskipped, a failure here means a live session
// against the langgraph reference runtime did not complete the
// bootstrap sequence (import the graph module, compile it, attach a
// RunnableConfig) or did not deliver a streamed response part back
// after invoking the compiled graph on a message.
func TestLanggraphRuntimeSessionBootstrapsAndStreamsResponse(t *testing.T) {
	// The langgraph reference runtime (github.com/lennylabs/runtime-langgraph)
	// is not vendored in this repo and ships no runnable image digest
	// here; tests/spec-map.json marks §26.8 blocked_until_phase 11 and
	// lists no package (cmd/runtimes/langgraph does not exist). No
	// in-repo runtime adapter performs the graphModule import / .compile()
	// / ainvoke bootstrap sequence today. Unskip once a runnable
	// langgraph image or an in-repo adapter implementing this bootstrap
	// contract exists and is registered with a warm pool, and a fixture
	// LangGraph graph module is available under tests/testdata.
	t.Skip("no runnable langgraph reference-runtime image or in-repo adapter implementing the §26.8 graphModule/compile/ainvoke bootstrap contract exists yet")

	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tenant := fmt.Sprintf("%s-%d", langgraphRuntimeSessionTenant, time.Now().UnixNano())
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sess, err := d.CreateAndStart(ctx, tenant, "langgraph")
	if errors.Is(err, sessiondriver.ErrPoolNotReady) {
		t.Skipf("precondition not met: warm pool not ready, no session to drive a message through: %v", err)
	}
	if err != nil {
		t.Fatalf("create-and-start langgraph session: %v", err)
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

	const prompt = "hello"
	msgResp, err := d.SendMessage(ctx, tenant, sess.ID, prompt)
	if err != nil {
		t.Fatalf("send message %q: %v", prompt, err)
	}
	if msgResp.DeliveryReceipt.Status != "delivered" {
		t.Fatalf("delivery receipt status = %q, want delivered (body: %s)",
			msgResp.DeliveryReceipt.Status, msgResp.Output)
	}

	// The synchronous POST /messages response must already carry a
	// non-empty response part produced by invoking the compiled graph
	// (graph.ainvoke / graph.astream) — a literal stub or a 500-tolerant
	// no-op would not produce this.
	assertNonEmptyResponsePart(t, "POST /messages response", msgResp.Output)

	// Confirm the same response also arrives over the AttachSession
	// bidirectional stream proxy, proving the compiled graph's output
	// reaches the events channel rather than only the request body.
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("events stream closed before a response event arrived for prompt %q", prompt)
			}
			if ev.Type == "transcript.entry" {
				t.Logf("observed response event on the events stream: type=%q data=%s", ev.Type, ev.Data)
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for an events-stream frame carrying the runtime's response for prompt %q", prompt)
		}
	}
}
