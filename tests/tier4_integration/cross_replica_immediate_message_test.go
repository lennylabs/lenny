// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §7.2 path-6 coordinator routing of a
// `delivery: "immediate"` message that lands on a replica other than the
// session's coordinating gateway replica. Two real cmd/lenny-gateway
// processes share one Postgres session store and one Redis, so the session
// created and coordinated by replica A is visible to replica B while only A
// holds the session's runtime. No existing tier-4 test sends a message to a
// replica that does not coordinate the target session; the single-replica
// interactive-iteration test always sends to the coordinator.
package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §7.2 path 6 (spec/07_session-lifecycle.md:330) "When a `delivery:
// immediate` message lands on a non-coordinator replica, that replica
// forwards the message to the session's coordinator (identified via the
// coordination lease in Redis/Postgres). The coordinator executes the atomic
// resume-and-deliver sequence (or the `resume_pending` transition for podless
// sessions). If the coordinator is unreachable (e.g., crashed, network
// partition), the forwarding replica falls back to inbox buffering with a
// `queued` delivery receipt status — the message is not silently dropped. The
// coordinator forwarding mechanism reuses the same internal gRPC
// `ForwardMessage` RPC used for all cross-replica message routing"; §10.1
// (per-session coordination — one replica coordinates a given session).
//
// diagnosis: a failure means an immediate message that lands on a replica
// other than the session's coordinator is not routed to the coordinator. The
// first half fails when the receiving replica serves the message itself
// against its own runtime instance instead of forwarding, so the message never
// reaches the session's runtime and the session's conversation forks across
// replicas. The second half fails when an unreachable coordinator causes the
// message to be dropped, rejected, or errored instead of buffered with a
// `queued` receipt.
func TestImmediateMessageOnNonCoordinatorReplicaRoutesToCoordinator(t *testing.T) {
	t.Skip("non-blocking: the cross-replica ForwardMessage transport is unbuilt — the gateway exposes no gateway-to-gateway RPC surface, so a non-coordinator replica serves an immediate message from its own runtime instead of forwarding it to the session's coordinator")

	gateway.SkipUnlessAvailable(t)

	// One Postgres and one Redis shared by both replicas: the session row,
	// the transcript, and the §7.2 inbox/DLQ coordinator are the same durable
	// state on both, which is what makes replica B a genuine non-coordinator
	// peer rather than an unrelated gateway.
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	args := []string{
		"--dev-mode",
		"--postgres-dsn=" + pg.DSN,
		"--redis-url=redis://" + rd.Addr + "/0",
		// The bootstrapped tenant declares no environment, so §11.1/§10.6
		// admission needs the permissive no-environment policy for the
		// session create to reach the message paths under test.
		"--no-environment-policy", "allow-all",
	}
	replicaA := gateway.StartWith(t, args...)
	replicaB := gateway.StartWith(t, args...)

	client := &http.Client{Timeout: 30 * time.Second}
	do := func(base, method, path, roles string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		if roles != "" {
			req.Header.Set("X-Lenny-Roles", roles)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s%s: %v", method, base, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out
	}

	// ---- bootstrap a tenant + an injection-capable echo runtime ----
	code, boot := do(replicaA.BaseURL(), http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name":   "echo",
			"image":  "lenny/echo@sha256:abc",
			"labels": map[string]string{"tier": "test"},
			"capabilities": map[string]any{
				"injection": map[string]any{
					"supported": true,
					"modes":     []string{"immediate", "queued"},
				},
			},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap on replica A: status %d (%v)", code, boot)
	}

	// ---- replica A creates and coordinates the session ----
	code, created := do(replicaA.BaseURL(), http.MethodPost, "/v1/sessions/start", "", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusCreated {
		t.Fatalf("create session on replica A: %d (%v)", code, created)
	}
	sid, _ := created["id"].(string)
	if sid == "" {
		t.Fatal("session id missing")
	}

	sendTo := func(base, content, delivery string) (int, map[string]any) {
		t.Helper()
		payload := map[string]any{"role": "user", "content": content}
		if delivery != "" {
			payload["delivery"] = delivery
		}
		return do(base, http.MethodPost, "/v1/sessions/"+sid+"/messages", "", map[string]any{
			"messages": []map[string]any{payload},
		})
	}

	// Two prompts delivered by the coordinator establish the session's
	// runtime conversation state. The reference echo runtime stamps a
	// per-session sequence number on each response (§15.4.4), so the runtime
	// that answers a later prompt is identifiable from the response text.
	for i, p := range []string{"first prompt", "second prompt"} {
		code, resp := sendTo(replicaA.BaseURL(), p, "")
		if code != http.StatusOK {
			t.Fatalf("prompt %d on replica A: status %d (%v)", i+1, code, resp)
		}
		receipt, _ := resp["deliveryReceipt"].(map[string]any)
		if receipt["status"] != "delivered" {
			t.Fatalf("prompt %d: deliveryReceipt.status = %v, want delivered", i+1, receipt["status"])
		}
	}

	// ---- the session is suspended by an interrupt on its coordinator ----
	code, interrupted := do(replicaA.BaseURL(), http.MethodPost, "/v1/sessions/"+sid+"/interrupt", "", nil)
	if code != http.StatusOK {
		t.Fatalf("interrupt on replica A: %d (%v)", code, interrupted)
	}
	if interrupted["state"] != "suspended" {
		t.Fatalf("state after interrupt: %v, want suspended", interrupted["state"])
	}

	// ---- an immediate message lands on replica B, which does not
	// coordinate the session. B must forward it to A; A runs the atomic
	// resume-and-deliver and the client sees a delivered receipt. ----
	code, forwarded := sendTo(replicaB.BaseURL(), "third prompt", "immediate")
	if code != http.StatusOK {
		t.Fatalf("immediate message on non-coordinator replica B: status %d (%v), want 200 with a forwarded delivery", code, forwarded)
	}
	receipt, _ := forwarded["deliveryReceipt"].(map[string]any)
	if receipt["status"] != "delivered" {
		t.Fatalf("immediate message on replica B: deliveryReceipt.status = %v, want delivered (B forwards to the coordinator, which resumes and delivers)", receipt["status"])
	}
	out, _ := forwarded["output"].([]any)
	if len(out) == 0 {
		t.Fatalf("immediate message on replica B: forwarded delivery produced no runtime output")
	}
	part, _ := out[0].(map[string]any)
	text, _ := part["text"].(string)
	if !strings.Contains(text, "third prompt") {
		t.Errorf("forwarded delivery output %q missing the prompt text", text)
	}
	// The response must come from the session's own runtime, which the
	// coordinator holds and which has already answered two prompts. A
	// response stamped seq=1 means replica B answered from a runtime
	// instance of its own instead of forwarding, forking the session's
	// conversation across replicas.
	if !strings.Contains(text, "seq=3") {
		t.Errorf("forwarded delivery output %q, want the session runtime's third exchange (seq=3); a restarted sequence means replica B served the message locally instead of forwarding to the coordinator", text)
	}

	code, afterForward := do(replicaA.BaseURL(), http.MethodGet, "/v1/sessions/"+sid, "", nil)
	if code != http.StatusOK {
		t.Fatalf("get session after forwarded delivery: %d", code)
	}
	if afterForward["state"] != "running" {
		t.Errorf("state after forwarded resume-and-deliver: %v, want running", afterForward["state"])
	}

	// ---- suspend again, then take the coordinator away ----
	code, reinterrupted := do(replicaA.BaseURL(), http.MethodPost, "/v1/sessions/"+sid+"/interrupt", "", nil)
	if code != http.StatusOK {
		t.Fatalf("second interrupt on replica A: %d (%v)", code, reinterrupted)
	}
	if reinterrupted["state"] != "suspended" {
		t.Fatalf("state after second interrupt: %v, want suspended", reinterrupted["state"])
	}
	replicaA.Stop()

	// ---- with the coordinator unreachable, replica B falls back to inbox
	// buffering and reports `queued`; the message is preserved rather than
	// dropped, and the session stays suspended. ----
	code, fallback := sendTo(replicaB.BaseURL(), "fourth prompt", "immediate")
	if code != http.StatusOK {
		t.Fatalf("immediate message with the coordinator down: status %d (%v), want 200 with a queued receipt", code, fallback)
	}
	fallbackReceipt, _ := fallback["deliveryReceipt"].(map[string]any)
	if fallbackReceipt["status"] != "queued" {
		t.Fatalf("immediate message with the coordinator down: deliveryReceipt.status = %v, want queued (the forwarding replica falls back to inbox buffering)", fallbackReceipt["status"])
	}
	depth, _ := fallbackReceipt["queueDepth"].(float64)
	if depth < 1 {
		t.Errorf("queued receipt queueDepth = %v, want at least 1 (the message is buffered, not silently dropped)", fallbackReceipt["queueDepth"])
	}

	code, afterFallback := do(replicaB.BaseURL(), http.MethodGet, "/v1/sessions/"+sid, "", nil)
	if code != http.StatusOK {
		t.Fatalf("get session after the queued fallback: %d", code)
	}
	if afterFallback["state"] != "suspended" {
		t.Errorf("state after the queued fallback: %v, want suspended (no non-coordinator resume happened)", afterFallback["state"])
	}

	// The transcript is unchanged by the buffered message: it holds the three
	// delivered exchanges and not the buffered fourth prompt.
	code, transcript := do(replicaB.BaseURL(), http.MethodGet, "/v1/sessions/"+sid+"/transcript", "", nil)
	if code != http.StatusOK {
		t.Fatalf("transcript: %d", code)
	}
	items, _ := transcript["items"].([]any)
	if len(items) != 6 {
		t.Errorf("transcript entries = %d, want 6 (3 delivered prompts + 3 responses; the buffered message is not a delivered exchange)", len(items))
	}
	for _, it := range items {
		entry, _ := it.(map[string]any)
		if content, _ := entry["content"].(string); strings.Contains(content, "fourth prompt") {
			t.Errorf("transcript records %q, want the buffered message absent until it is drained to the runtime", content)
		}
	}
}
