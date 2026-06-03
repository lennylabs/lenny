// SPDX-License-Identifier: MIT

//go:build smoke

// Source Mode smoke test (§17.4 line 276). `make test-smoke` runs this:
// it boots the real cmd/lenny-gateway binary in dev mode with the
// built-in echo runtime, creates a session, sends a prompt, verifies an
// echo response, and terminates. This validates the gateway + session
// pipeline + echo executor compose end-to-end through one native
// process — the substitution Source Mode promises in place of a full
// Kubernetes pod path. The Compose Mode counterpart
// (`docker compose run smoke-test`) ships with the Compose bundle under
// F-17.4.3.
package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: §17.4 line 276 — "creates a session with the echo runtime,
// sends a prompt, verifies a response, and exits." The pipeline runs
// against the in-process echo executor selected by --agent-runtime echo
// (LENNY_AGENT_RUNTIME=echo), the §17.4 line 262 zero-credential mode.
func TestSourceModeSmoke_spec_17_4_276(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	// --agent-runtime echo pins the §17.4 zero-credential built-in echo
	// runtime explicitly, exercising the F-17.4.15 selector end-to-end.
	gw := gateway.StartWith(t, "--dev-mode", "--agent-runtime", "echo")
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

	// Bootstrap the tenant + echo runtime (with injection support so the
	// mid-session prompt is accepted).
	code, _ := do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name":  "echo",
			"image": "lenny/echo@sha256:abc",
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

	// Create + start the echo session.
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

	// Send a prompt; expect the echo runtime to reflect it back.
	const prompt = "smoke test hello"
	code, msgResp := do(http.MethodPost, "/v1/sessions/"+sid+"/messages", "", map[string]any{
		"messages": []map[string]any{{"role": "user", "content": prompt}},
	})
	if code != http.StatusOK {
		t.Fatalf("send message: %d (%v)", code, msgResp)
	}
	out, _ := msgResp["output"].([]any)
	if len(out) == 0 {
		t.Fatal("smoke test: message produced no output")
	}
	first, _ := out[0].(map[string]any)
	if txt, _ := first["text"].(string); !strings.Contains(txt, prompt) {
		t.Fatalf("smoke test: echo response did not contain the prompt: %q", txt)
	}

	// Terminate cleanly.
	if code, _ := do(http.MethodPost, "/v1/sessions/"+sid+"/terminate", "", nil); code != http.StatusOK {
		t.Fatalf("terminate: %d", code)
	}
}
