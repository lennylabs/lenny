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
// diagnosis: a failure means the gateway-to-pod bidirectional data path
// is broken on a real cluster: either a client prompt never reaches the
// runtime running in the claimed agent pod, or the runtime's output
// never makes it back to the client through the synchronous message
// response and the AttachSession event stream. This is the platform's
// central guarantee (§6.3 / §15.1) and, unlike the in-process tier4
// tests, exercises the real SandboxClaim-bound pod over the network.
func TestPromptRoundTripsToRealPodAndReturnsContent(t *testing.T) {
	// The e2e agent-workload.yaml Runtime CR and install.sh bootstrap
	// overlay now declare capabilities.injection.supported: true on
	// echo-runtime-sidecar so a mid-session message can reach the pod,
	// but re-deploying that change onto the shared persistent e2e Kind
	// cluster hit an unrelated stale-image/chart skew (the running
	// gateway image predates a chart flag it no longer recognizes) that
	// needs a full image rebuild + reinstall to clear. Un-skip once a
	// fresh `tests/testinfra/kind/install.sh` run (without
	// LENNY_SKIP_BUILD) has been verified green against this test.
	t.Skip("precondition not met: the e2e cluster needs a fresh (non-skip-build) install.sh run " +
		"to pick up the echo-runtime-sidecar injection-capability change and clear an unrelated " +
		"stale gateway image/chart flag mismatch before this test can be verified")

	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tenant := fmt.Sprintf("%s-%d", promptRoundtripTenant, time.Now().UnixNano())
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

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

	// 17. Gateway ↔ Pod: confirm the same content also arrives over the
	// AttachSession bidirectional stream proxy, proving the events
	// channel carries live pod output rather than only the request body.
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("events stream closed before an echoed response event arrived for prompt %q", prompt)
			}
			if strings.Contains(string(ev.Data), prompt) {
				t.Logf("observed echoed content on the events stream: type=%q data=%s", ev.Type, ev.Data)
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for an events-stream frame echoing prompt %q", prompt)
		}
	}
}

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
