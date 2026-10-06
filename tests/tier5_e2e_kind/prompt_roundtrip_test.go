// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test that drives a prompt from the client through the
// gateway onto a real agent pod and confirms the echoed content comes
// back both in the synchronous message response and over the
// AttachSession bidirectional stream proxy. No existing tier5/tier6 test
// asserts real echoed content: tests/tier6_e2e_cloud/session_lifecycle_test.go
// tolerates a 500 EXECUTOR_FAILURE because it never starts the session,
// and the other tier5/tier9 tests that call CreateAndStart check only
// session state, pool replenishment, or cross-tenant isolation.

package tier5_e2e_kind_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// promptRoundtripTenant is the synthetic tenant this test bootstraps.
// The driver best-effort deletes it on Close; a per-run suffix (below)
// sidesteps a stale tenant left in the deleted state by a prior run on
// this persistent e2e cluster, matching the pattern in
// tests/tier9_security/live_session_test.go.
const promptRoundtripTenant = "prompt-roundtrip-tenant"

// spec: §7.1 (spec/07_session-lifecycle.md, Normal Flow) "16. Client →
// Gateway: AttachSession(session_id) / 17. Gateway ↔ Pod: Bidirectional
// stream proxy / 18. Client ↔ Gateway: Full interactive session
// (prompts, responses, ...)"
//
// spec: 28.5.1 (Gateway-to-pod), 7.2 (Interactive Session Model)
//
// diagnosis: a failure means the gateway-to-pod bidirectional data path
// is broken on a real cluster: either a client prompt never reaches the
// runtime running in the claimed agent pod, or the runtime's output
// never makes it back to the client through the synchronous message
// response and the AttachSession event stream. This is the platform's
// central guarantee (§6.3 / §15.1) and, unlike the in-process tier4
// tests, exercises the real SandboxClaim-bound pod over the network. A
// failure on the second prompt alone means the session's Attach stream
// ended with the request that delivered the first prompt, rather than
// being held for the life of the session's binding.
func TestPromptRoundTripsToRealPodAndReturnsContent(t *testing.T) {
	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tenant := fmt.Sprintf("%s-%d", promptRoundtripTenant, time.Now().UnixNano())
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	// The journey creates its session against a runtime with no
	// explicit environment, which a freshly bootstrapped tenant's
	// deny-all §10.6 noEnvironmentPolicy rejects with 403 FORBIDDEN.
	ensureTenantAllowsSessionsWithNoEnvironment(t, d, tenant)

	runEchoPromptJourney(ctx, t, d, tenant)
}

// runEchoPromptJourney drives the §7.1 steps 16-18 prompt round-trip on
// an already-provisioned tenant: it starts a session on the echo pool,
// attaches the AttachSession event stream, sends a prompt, and asserts
// the real pod's echoed output returns both in the synchronous
// POST /messages response and over the bidirectional stream proxy. It
// then sends a second prompt on the same session and asserts its own echo,
// because each POST /messages request ends when it returns and the
// session's Attach stream must outlive the request that opened it. It is
// the reusable core of TestPromptRoundTripsToRealPodAndReturnsContent so
// the same journey can be replayed across auth modes
// (prompt_journey_auth_modes_test.go) without duplicating the
// assertions. The caller owns tenant provisioning; d's session-surface
// auth (dev headers or a §10.2 Bearer) is whatever d was constructed
// with.
func runEchoPromptJourney(ctx context.Context, t *testing.T, d *sessiondriver.Driver, tenant string) {
	t.Helper()
	sess, err := d.CreateAndStart(ctx, tenant, sessiondriver.EchoRuntimeSidecar)
	if errors.Is(err, sessiondriver.ErrPoolNotReady) {
		// §4.6 warm pool never settled an idle pod within the retry
		// window. This test exercises the §7.1 steps 16-18 data path,
		// not pool warm-up; skip cleanly as the sibling tier9 live-
		// session test does on the same precondition.
		t.Skipf("precondition not met: warm pool not ready, no session to drive a prompt through: %v", err)
	}
	if err != nil {
		t.Fatalf("create-and-start session: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = d.Terminate(ctx, tenant, sess.ID)
	})
	t.Logf("created session %s in state %q", sess.ID, sess.State)

	// 16. Client → Gateway: AttachSession(session_id) — open the SSE
	// stream before sending the prompt so the echoed response is
	// observed live over the bidirectional stream proxy, not only
	// through the synchronous POST response below.
	events, stopEvents, err := d.StreamEvents(ctx, tenant, sess.ID, 0)
	if err != nil {
		t.Fatalf("attach session events stream: %v", err)
	}
	defer stopEvents()

	// 18. Client ↔ Gateway: deliver a prompt to the running agent.
	const prompt = "ping"
	msgResp, err := d.SendMessage(ctx, tenant, sess.ID, prompt)
	if err != nil {
		t.Fatalf("send message %q: %v", prompt, err)
	}
	if msgResp.DeliveryReceipt.Status != "delivered" {
		t.Fatalf("delivery receipt status = %q, want delivered (body: %s)",
			msgResp.DeliveryReceipt.Status, msgResp.Output)
	}

	// The synchronous POST /messages response body already carries the
	// real pod's echoed output — the echo runtime prefixes every text
	// part with "[echo seq=N] " (pkg/runtimekit/echocore), so a literal
	// stub or a 500-tolerant no-op would not produce this content.
	assertOutputEchoes(t, "POST /messages response", msgResp.Output, prompt)

	// 17. Gateway ↔ Pod: confirm the runtime's OUTPUT also arrives over
	// the AttachSession bidirectional stream proxy, proving the events
	// channel carries live pod output rather than only the request body.
	// Match on the echo runtime's "[echo seq=N] " prefix
	// (pkg/runtimekit/echocore) together with the prompt: the gateway
	// also emits a message_delivered frame that reflects the user input
	// verbatim, so matching the bare prompt alone would pass even if no
	// pod output ever returned. Requiring the echo prefix pins the
	// assertion to the runtime's response travelling back across the
	// stream proxy.
	awaitEchoEvent(t, events, prompt)

	// spec: §28.5.1 (CH-ATTACH Timing.) — the first prompt's request has
	// returned, so its context has ended. A second prompt on the same
	// session reaches the runtime over the stream the first one opened and
	// returns its own echo.
	const second = "pong"
	msgResp, err = d.SendMessage(ctx, tenant, sess.ID, second)
	if err != nil {
		t.Fatalf("send second message %q: %v", second, err)
	}
	if msgResp.DeliveryReceipt.Status != "delivered" {
		t.Fatalf("second delivery receipt status = %q, want delivered (body: %s)",
			msgResp.DeliveryReceipt.Status, msgResp.Output)
	}
	assertOutputEchoes(t, "second POST /messages response", msgResp.Output, second)
}

// awaitEchoEvent waits for an events-stream frame carrying the runtime's
// echoed output for prompt.
func awaitEchoEvent(t *testing.T, events <-chan sessiondriver.Event, prompt string) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("events stream closed before an echoed response event arrived for prompt %q", prompt)
			}
			data := string(ev.Data)
			if strings.Contains(data, echoOutputPrefix) && strings.Contains(data, prompt) {
				t.Logf("observed runtime echo output on the events stream: type=%q data=%s", ev.Type, ev.Data)
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for an events-stream frame carrying the runtime's echoed output for prompt %q", prompt)
		}
	}
}

// spec: 28.5.1 (Gateway-to-pod), 7.2 (Interactive Session Model), 5.2
// (Pool Configuration and Execution Modes)
//
// diagnosis: on a concurrent pool two sessions share one agent pod, each in
// its own slot, and each holds its own Attach stream to the pod's adapter.
// A failure on a session's second message means that session's stream
// ended with the request that delivered its first message; a reply that
// names the sibling's prompt means the gateway or the adapter routed a turn
// to the wrong slot's stream.
func TestConcurrentSlotSessionsEachCarryTwoMessages_spec_28_5_1(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	requirePoolReadyPods(t, d.Cluster(), concurrentPoolName, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	tenant := uniqueName("held-stream-concurrent")
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	ensureTenantAllowsSessionsWithNoEnvironment(t, d, tenant)

	var ids []string
	pod := ""
	for _, name := range []string{"A", "B"} {
		sess, err := d.CreateAndStart(ctx, tenant, concurrentRuntimeRef)
		if errors.Is(err, sessiondriver.ErrPoolNotReady) {
			t.Skipf("precondition not met: concurrent pool not ready: %v", err)
		}
		if err != nil {
			t.Fatalf("create session %s on %s: %v", name, concurrentRuntimeRef, err)
		}
		t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sess.ID) })
		if pod == "" {
			pod = sess.PodAssignment
		} else if sess.PodAssignment != pod {
			t.Fatalf("session %s landed on pod %q, want the first session's pod %q; the pool did not "+
				"multiplex both sessions onto one pod", name, sess.PodAssignment, pod)
		}
		ids = append(ids, sess.ID)
	}

	for round := 1; round <= 2; round++ {
		for i, id := range ids {
			prompt := fmt.Sprintf("slot-%d-round-%d", i, round)
			resp, err := d.SendMessage(ctx, tenant, id, prompt)
			if err != nil {
				t.Fatalf("session %s round %d: send %q: %v", id, round, prompt, err)
			}
			if resp.DeliveryReceipt.Status != "delivered" {
				t.Fatalf("session %s round %d: receipt %q, want delivered (body: %s)",
					id, round, resp.DeliveryReceipt.Status, resp.Output)
			}
			assertOutputEchoes(t, fmt.Sprintf("session %s round %d", id, round), resp.Output, prompt)
		}
	}
}

// echoOutputPrefix is the literal prefix the echo runtime prepends to
// every text part it emits (pkg/runtimekit/echocore). Its presence on a
// stream frame distinguishes the runtime's own output from the gateway's
// message_delivered reflection of the client's input.
const echoOutputPrefix = "[echo seq="

// assertOutputEchoes decodes a §15.1 message-response output array and
// fails the test unless at least one text part contains want.
func assertOutputEchoes(t *testing.T, where string, output json.RawMessage, want string) {
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
		if strings.Contains(p.Text, want) {
			return
		}
	}
	t.Fatalf("%s: no output part echoed %q; got %+v", where, want, parts)
}
