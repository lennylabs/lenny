// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: drives the §7.1 step-18 developer iteration
// loop (multiple prompts, an interrupt, and a further message attempt)
// against the real cmd/lenny-gateway binary in dev mode, and checks the
// resulting transcript ordering. No existing tier-4 test sends more than
// one prompt or interrupts a session that has already exchanged prompts.

package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: §7.1 (spec/07_session-lifecycle.md:45) "18. Client ↔ Gateway:
// Full interactive session (prompts, responses, tool use, interrupts,
// elicitation, credential rotation on RATE_LIMITED)"; §7.2 path 6
// (lines 326-330), in particular line 327: a `delivery:"immediate"`
// message whose target session is `suspended` and whose pod is still
// held atomically resumes the session (`suspended → running`) and
// delivers the message to the runtime, returning a `delivered` receipt.
// On the single coordinating replica the in-process echo executor is
// always ready, so the fourth prompt resumes-and-delivers, produces a
// fourth echo exchange, and returns the session to `running`.
//
// diagnosis: a failure means the real gateway binary does not sustain a
// multi-prompt developer session, does not record every delivered
// prompt/response pair to the transcript in order, does not suspend on
// interrupt, or does not atomically resume-and-deliver a
// `delivery:"immediate"` message sent to a suspended, pod-held session
// (returning `delivered` and returning the session to `running`).
func TestInteractiveIterationInterruptThenResumeAndDeliver(t *testing.T) {
	// A real Redis backs the §7.2 inbox/DLQ coordinator (see
	// pkg/gateway/sessionserver/messages.go and
	// cmd/lenny-gateway/stores.go buildSessionMessaging): without it,
	// a buffered message fails closed with a "error"/"inbox_unavailable"
	// receipt instead of the "queued" receipt this test verifies. This
	// mirrors the existing tier4 tests/tier4_integration/quota_test.go
	// and policy_test.go pattern of wiring a real Redis via --redis-url.
	gateway.SkipUnlessAvailable(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	gw := gateway.StartWith(t, "--dev-mode", "--redis-url=redis://"+rd.Addr+"/0")
	base := gw.BaseURL()
	client := http.DefaultClient

	do := func(method, path, roles string, body any) (int, map[string]any) {
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
			t.Fatalf("%s %s: %v", method, path, err)
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
	code, _ := do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
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
		t.Fatalf("bootstrap: status %d", code)
	}

	// ---- create + start the session ----
	code, created := do(http.MethodPost, "/v1/sessions/start", "", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusCreated {
		t.Fatalf("create session: %d (%v)", code, created)
	}
	sid, _ := created["id"].(string)
	if sid == "" {
		t.Fatal("session id missing")
	}
	if created["state"] != "running" {
		t.Fatalf("session state after start: %v", created["state"])
	}

	sendMessage := func(content, delivery string) (int, map[string]any) {
		payload := map[string]any{"role": "user", "content": content}
		if delivery != "" {
			payload["delivery"] = delivery
		}
		return do(http.MethodPost, "/v1/sessions/"+sid+"/messages", "", map[string]any{
			"messages": []map[string]any{payload},
		})
	}

	// ---- the developer iterates: three prompts, each delivered and
	// echoed while the session is running ----
	prompts := []string{"first prompt", "second prompt", "third prompt"}
	for i, p := range prompts {
		code, resp := sendMessage(p, "")
		if code != http.StatusOK {
			t.Fatalf("prompt %d: status %d (%v)", i+1, code, resp)
		}
		receipt, _ := resp["deliveryReceipt"].(map[string]any)
		if receipt["status"] != "delivered" {
			t.Fatalf("prompt %d: deliveryReceipt.status = %v, want delivered", i+1, receipt["status"])
		}
		out, _ := resp["output"].([]any)
		if len(out) == 0 {
			t.Fatalf("prompt %d: produced no output", i+1)
		}
		first, _ := out[0].(map[string]any)
		text, _ := first["text"].(string)
		wantSeq := fmt.Sprintf("seq=%d", i+1)
		if !strings.Contains(text, wantSeq) || !strings.Contains(text, p) {
			t.Errorf("prompt %d: echo output %q missing %q and/or the prompt text", i+1, text, wantSeq)
		}
	}

	// ---- the developer interrupts the agent ----
	code, interrupted := do(http.MethodPost, "/v1/sessions/"+sid+"/interrupt", "", nil)
	if code != http.StatusOK {
		t.Fatalf("interrupt: %d (%v)", code, interrupted)
	}
	if interrupted["state"] != "suspended" {
		t.Fatalf("state after interrupt: %v, want suspended", interrupted["state"])
	}

	// ---- a fourth prompt with delivery:"immediate" sent to the now
	// suspended, pod-held session atomically resumes the session
	// (suspended → running) and delivers the message to the runtime:
	// §7.2 path 6 line 327. The receipt is delivered with a non-empty
	// deliveredAt, the echo executor produces a fourth exchange, and
	// the session returns to running. ----
	code, delivered := sendMessage("fourth prompt", "immediate")
	if code != http.StatusOK {
		t.Fatalf("fourth prompt: status %d (%v)", code, delivered)
	}
	receipt, _ := delivered["deliveryReceipt"].(map[string]any)
	if receipt["status"] != "delivered" {
		t.Fatalf("fourth prompt: deliveryReceipt.status = %v, want delivered", receipt["status"])
	}
	if da, _ := receipt["deliveredAt"].(string); da == "" {
		t.Errorf("fourth prompt: deliveryReceipt.deliveredAt is empty, want a timestamp on the delivered receipt")
	}
	out, _ := delivered["output"].([]any)
	if len(out) == 0 {
		t.Fatalf("fourth prompt: resumed-and-delivered message produced no executor output")
	}
	fourthEcho, _ := out[0].(map[string]any)
	fourthText, _ := fourthEcho["text"].(string)
	if !strings.Contains(fourthText, "fourth prompt") {
		t.Errorf("fourth prompt: echo output %q missing the prompt text", fourthText)
	}

	code, afterDeliver := do(http.MethodGet, "/v1/sessions/"+sid, "", nil)
	if code != http.StatusOK {
		t.Fatalf("get session after resume-and-deliver: %d", code)
	}
	if afterDeliver["state"] != "running" {
		t.Errorf("state after resume-and-deliver: %v, want running", afterDeliver["state"])
	}

	// ---- the transcript records exactly the four delivered
	// exchanges, in order; the fourth prompt is among them because the
	// resume-and-deliver delivered it to the executor ----
	code, transcript := do(http.MethodGet, "/v1/sessions/"+sid+"/transcript", "", nil)
	if code != http.StatusOK {
		t.Fatalf("transcript: %d", code)
	}
	items, _ := transcript["items"].([]any)
	exchanges := append(append([]string{}, prompts...), "fourth prompt")
	if len(items) != 2*len(exchanges) {
		t.Fatalf("transcript entries = %d, want %d (4 prompts + 4 responses)", len(items), 2*len(exchanges))
	}
	for i, p := range exchanges {
		userEntry, _ := items[2*i].(map[string]any)
		if userEntry["role"] != "user" {
			t.Errorf("entry %d role = %v, want user", 2*i, userEntry["role"])
		}
		if userEntry["content"] != p {
			t.Errorf("entry %d content = %v, want %q", 2*i, userEntry["content"], p)
		}
		assistantEntry, _ := items[2*i+1].(map[string]any)
		if assistantEntry["role"] != "assistant" {
			t.Errorf("entry %d role = %v, want assistant", 2*i+1, assistantEntry["role"])
		}
		wantSeq := fmt.Sprintf("seq=%d", i+1)
		content, _ := assistantEntry["content"].(string)
		if !strings.Contains(content, wantSeq) {
			t.Errorf("entry %d content = %q, missing %q", 2*i+1, content, wantSeq)
		}
	}

	// ---- terminate cleanly ----
	code, terminated := do(http.MethodPost, "/v1/sessions/"+sid+"/terminate", "", nil)
	if code != http.StatusOK {
		t.Fatalf("terminate: %d (%v)", code, terminated)
	}
	if terminated["state"] != "completed" {
		t.Errorf("state after terminate: %v, want completed", terminated["state"])
	}
}
