// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §26.7 chat reference runtime's bootstrap
// contract: on message, the adapter constructs a provider-dialect
// request, sends it via the LLM proxy, and streams the response back
// as response output parts. See runEchoPromptJourney in
// prompt_roundtrip_test.go for the sibling journey this mirrors against
// the credential-free echo runtime.
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

// chatRuntimeSessionTenant is the synthetic tenant this test
// bootstraps, per-run-suffixed for the same reason
// promptRoundtripTenant is in prompt_roundtrip_test.go.
const chatRuntimeSessionTenant = "chat-runtime-session-tenant"

// spec: §26.7 ("Bootstrap: adapter binary is the only process; on
// message, the adapter constructs a provider-dialect request (selected
// by pool provider identity), sends it via the LLM proxy, and streams
// the response back as response output parts.")
//
// diagnosis: once unskipped, a failure here means a live session
// against the chat reference runtime did not deliver a streamed
// response part back to the client: either the adapter did not
// construct and send a provider-dialect request through the LLM proxy,
// or the translated response did not return over the synchronous
// message response / AttachSession event stream.
func TestChatRuntimeSessionStreamsResponse(t *testing.T) {
	// The chat reference runtime (github.com/lennylabs/runtime-chat) is
	// not vendored in this repo and ships no runnable image digest
	// here; tests/spec-map.json marks §26.7 blocked_until_phase 11 and
	// lists no package. No in-repo runtime adapter performs a real
	// provider-dialect request through the LLM proxy from inside a live
	// pod session today (the cmd/runtimes/* stubs, e.g. streaming-echo,
	// are explicitly credential-free / proxy-free). Unskip once a
	// runnable chat image or an in-repo adapter implementing this
	// bootstrap contract exists and is registered with a warm pool.
	t.Skip("no runnable chat reference-runtime image or in-repo adapter implementing the §26.7 LLM-proxy bootstrap contract exists yet")

	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tenant := fmt.Sprintf("%s-%d", chatRuntimeSessionTenant, time.Now().UnixNano())
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sess, err := d.CreateAndStart(ctx, tenant, "chat")
	if errors.Is(err, sessiondriver.ErrPoolNotReady) {
		t.Skipf("precondition not met: warm pool not ready, no session to drive a message through: %v", err)
	}
	if err != nil {
		t.Fatalf("create-and-start chat session: %v", err)
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
	// non-empty response part constructed from the LLM proxy's
	// translated upstream reply — a literal stub or a 500-tolerant
	// no-op would not produce this.
	assertNonEmptyResponsePart(t, "POST /messages response", msgResp.Output)

	// Confirm the same response also arrives over the AttachSession
	// bidirectional stream proxy, proving the events channel carries
	// live pod output rather than only the request body.
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

// assertNonEmptyResponsePart decodes a §15.1 message-response output
// array and fails the test unless at least one part carries non-empty
// text.
func assertNonEmptyResponsePart(t *testing.T, where string, output json.RawMessage) {
	t.Helper()
	if len(output) == 0 {
		t.Fatalf("%s: produced no output", where)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(output, &parts); err != nil {
		t.Fatalf("%s: decode output: %v; raw %s", where, err, output)
	}
	for _, p := range parts {
		if p.Text != "" {
			return
		}
	}
	t.Fatalf("%s: no part carried non-empty text; raw %s", where, output)
}
