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
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// reqFn issues an HTTP request against a running gateway and returns the
// status code plus the decoded JSON body. It is the shared driver for
// the source-mode smoke tests below.
func newReqFn(t *testing.T, base string) func(method, path, roles string, body any) (int, map[string]any) {
	t.Helper()
	client := http.DefaultClient
	return func(method, path, roles string, body any) (int, map[string]any) {
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
}

// spec: §17.4 line 276 — "creates a session with the echo runtime,
// sends a prompt, verifies a response, and exits." The pipeline runs
// against the in-process echo executor selected by --agent-runtime echo
// (LENNY_AGENT_RUNTIME=echo), the §17.4 line 262 zero-credential mode.
func TestSourceModeSmoke_spec_17_4_276(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	// --agent-runtime echo pins the §17.4 zero-credential built-in echo
	// runtime explicitly, exercising the F-17.4.15 selector end-to-end.
	gw := gateway.StartWith(t, "--dev-mode", "--agent-runtime", "echo")
	do := newReqFn(t, gw.BaseURL())

	// Bootstrap the tenant + echo runtime (with injection support so the
	// mid-session prompt is accepted).
	code, _ := do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name":   "echo",
			"image":  "lenny/echo@sha256:abc",
			"labels": map[string]string{"tier": "test"}, // §5.1 line 51: labels required
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

// spec: §17.4 line 199 — "Embedded SQLite replaces Postgres for session
// and metadata storage." With --sqlite-path set, the gateway loads its
// session and metadata stores from the SQLite file on startup and
// flushes them on graceful shutdown, so a bootstrapped tenant + runtime
// and a created session survive a process restart without a Postgres
// dependency. This test boots one gateway against a stable SQLite file,
// bootstraps and creates a session, stops it (the graceful SIGINT path
// the harness drives on cleanup), then boots a second gateway against
// the same file and asserts the session and runtime are still present.
func TestSourceModeSmoke_SQLiteDurability_spec_17_4_199(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	dbPath := filepath.Join(t.TempDir(), "lenny.db")

	var sid string

	// First process: bootstrap acme + echo, create a session, then let
	// the subtest's cleanup SIGINT the gateway so the shutdown path
	// flushes the SQLite file.
	t.Run("write", func(t *testing.T) {
		gw := gateway.StartWith(t, "--dev-mode", "--agent-runtime", "echo", "--sqlite-path", dbPath)
		do := newReqFn(t, gw.BaseURL())

		code, _ := do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
			"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
			"runtimes": []map[string]any{{
				"name":   "echo",
				"image":  "lenny/echo@sha256:abc",
				"labels": map[string]string{"tier": "test"},
			}},
		})
		if code != http.StatusOK {
			t.Fatalf("bootstrap: status %d", code)
		}

		code, created := do(http.MethodPost, "/v1/sessions/start", "", map[string]any{
			"runtimeRef": "echo",
			"userId":     "alice@acme.com",
		})
		if code != http.StatusCreated {
			t.Fatalf("create session: %d (%v)", code, created)
		}
		sid, _ = created["id"].(string)
		if sid == "" {
			t.Fatal("session id missing")
		}
	})

	if sid == "" {
		t.Fatal("write phase did not record a session id")
	}

	// Second process: same SQLite file, fresh port. The runtime and the
	// session row must be recovered from the file without re-bootstrapping.
	t.Run("recover", func(t *testing.T) {
		gw := gateway.StartWith(t, "--dev-mode", "--agent-runtime", "echo", "--sqlite-path", dbPath)
		do := newReqFn(t, gw.BaseURL())

		// The runtime registered before the restart is still listed.
		code, runtimes := do(http.MethodGet, "/v1/admin/runtimes", "platform-admin", nil)
		if code != http.StatusOK {
			t.Fatalf("list runtimes after restart: %d (%v)", code, runtimes)
		}
		items, _ := runtimes["runtimes"].([]any)
		if len(items) == 0 {
			items, _ = runtimes["items"].([]any)
		}
		foundEcho := false
		for _, it := range items {
			if m, ok := it.(map[string]any); ok {
				if n, _ := m["name"].(string); n == "echo" {
					foundEcho = true
				}
			}
		}
		if !foundEcho {
			t.Fatalf("echo runtime not recovered after restart: %v", runtimes)
		}

		// The session row persisted: GET returns it without a 404.
		code, got := do(http.MethodGet, "/v1/sessions/"+sid, "", nil)
		if code != http.StatusOK {
			t.Fatalf("get session after restart: %d (%v)", code, got)
		}
		if id, _ := got["id"].(string); id != sid {
			t.Fatalf("recovered session id = %q, want %q", id, sid)
		}
	})
}
