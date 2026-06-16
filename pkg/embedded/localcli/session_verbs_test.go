// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGateway stands in for the gateway the §24.17 session CLI targets.
// It answers the MCP /mcp JSON-RPC surface (create/send/interrupt/cancel)
// and the REST list/get/logs endpoints so the verbs can be exercised
// end-to-end without a running Embedded Mode stack — the path a remote
// `--api-url` / `--token` invocation takes. F-24.17.2/.3/.9/.10.
func fakeGateway(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		write := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": result,
			})
		}
		text := func(s string) map[string]any {
			return map[string]any{"content": []map[string]any{{"type": "text", "text": s}}}
		}
		switch req.Method {
		case "initialize":
			write(map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "lenny-gateway", "version": "0.1.0"},
			})
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			// F-8.5.16 renamed the lenny/send_message arguments from the
			// legacy sessionId/content to the §8.5 to/message fields; the
			// interrupt and cancel tools keep sessionId/reason.
			var a struct {
				SessionID  string `json:"sessionId"`
				RuntimeRef string `json:"runtimeRef"`
				To         string `json:"to"`
				Message    string `json:"message"`
				Reason     string `json:"reason"`
			}
			_ = json.Unmarshal(p.Arguments, &a)
			switch p.Name {
			case "lenny/create_session":
				write(text(`{"sessionId":"sess_new","state":"running"}`))
			case "lenny/send_message":
				write(text("echo: " + a.Message))
			case "lenny/interrupt_session":
				write(text(`{"sessionId":"` + a.SessionID + `","state":"suspended"}`))
			case "lenny/cancel_session":
				write(text(`{"sessionId":"` + a.SessionID + `","state":"cancelled"}`))
			default:
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": json.RawMessage(req.ID),
					"error": map[string]any{"code": -32601, "message": "unknown tool"},
				})
			}
		default:
			write(map[string]any{})
		}
	})

	mux.HandleFunc("GET /v1/sessions/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"seq":1,"sessionId":"sess_1","type":"log","data":{"line":"hi"},"timestamp":"2026-06-03T12:00:00Z"}],"hasMore":false}`))
	})
	mux.HandleFunc("GET /v1/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","state":"running","runtimeRef":"claude-code"}`))
	})
	mux.HandleFunc("GET /v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		// §15.1 lines 1228-1253 canonical cursor-paginated envelope the
		// SDK's ListSessions decodes ({items, cursor, hasMore}).
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"sess_1","state":"running","runtimeRef":"claude-code"}],"hasMore":false}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// remoteArgs prefixes a verb's args with the §24.17 line 222 / §24 line 8
// discovery flags so the test targets the fake gateway instead of the
// Embedded Mode stack.
func remoteArgs(srv *httptest.Server, args ...string) []string {
	return append([]string{"--api-url", srv.URL, "--token", "test-bearer"}, args...)
}

// TestSessionNewRoutesThroughMCP_spec_24_17_209 confirms `session new`
// drives the §15.2 MCP lenny/create_session tool (not the REST start
// path) and prints the created id. F-24.17.3.
func TestSessionNewRoutesThroughMCP_spec_24_17_209(t *testing.T) {
	srv := fakeGateway(t)
	var stdout, stderr bytes.Buffer
	code := cmdSession(append([]string{"new"}, remoteArgs(srv, "--runtime", "claude-code")...), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("session new: code=%d stderr=%s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "sess_new" {
		t.Errorf("stdout = %q, want sess_new", stdout.String())
	}
}

// TestSessionSendInterruptCancel_spec_24_17 exercises the three MCP
// session-management verbs end-to-end. F-24.17.2/.5.
func TestSessionSendInterruptCancel_spec_24_17(t *testing.T) {
	srv := fakeGateway(t)
	cases := []struct {
		verb   string
		args   []string
		expect string
	}{
		{"send", []string{"sess_1", "hello"}, "echo: hello"},
		{"interrupt", []string{"sess_1"}, "sess_1 suspended"},
		{"cancel", []string{"sess_1"}, "sess_1 cancelled"},
	}
	for _, tc := range cases {
		var stdout, stderr bytes.Buffer
		code := cmdSession(append([]string{tc.verb}, remoteArgs(srv, tc.args...)...), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("session %s: code=%d stderr=%s", tc.verb, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), tc.expect) {
			t.Errorf("session %s stdout = %q, want to contain %q", tc.verb, stdout.String(), tc.expect)
		}
	}
}

// TestSessionListGetLogs_spec_24_17 exercises the three REST verbs the
// §24.17 table maps to the REST surface. F-24.17.2/.6.
func TestSessionListGetLogs_spec_24_17(t *testing.T) {
	srv := fakeGateway(t)

	var listOut, listErr bytes.Buffer
	if code := cmdSession(append([]string{"list"}, remoteArgs(srv)...), &listOut, &listErr); code != 0 {
		t.Fatalf("session list: code=%d stderr=%s", code, listErr.String())
	}
	if !strings.Contains(listOut.String(), "sess_1") || !strings.Contains(listOut.String(), "running") {
		t.Errorf("session list out = %q", listOut.String())
	}

	var getOut, getErr bytes.Buffer
	if code := cmdSession(append([]string{"get"}, remoteArgs(srv, "sess_1")...), &getOut, &getErr); code != 0 {
		t.Fatalf("session get: code=%d stderr=%s", code, getErr.String())
	}
	if !strings.Contains(getOut.String(), `"id": "sess_1"`) {
		t.Errorf("session get out = %q", getOut.String())
	}

	var logOut, logErr bytes.Buffer
	if code := cmdSession(append([]string{"logs"}, remoteArgs(srv, "sess_1")...), &logOut, &logErr); code != 0 {
		t.Fatalf("session logs: code=%d stderr=%s", code, logErr.String())
	}
	if !strings.Contains(logOut.String(), "log") || !strings.Contains(logOut.String(), "hi") {
		t.Errorf("session logs out = %q", logOut.String())
	}
}

// TestSessionListPassesBearer_spec_24_17_10 confirms --token is attached
// as the Authorization bearer the gateway sees. F-24.17.10.
func TestSessionListPassesBearer_spec_24_17_10(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessions":[]}`))
	}))
	t.Cleanup(srv.Close)

	var stdout, stderr bytes.Buffer
	code := cmdSession([]string{"list", "--api-url", srv.URL, "--token", "abc123"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("session list: code=%d stderr=%s", code, stderr.String())
	}
	if gotAuth != "Bearer abc123" {
		t.Errorf("Authorization = %q, want Bearer abc123", gotAuth)
	}
}

// TestSessionDiscoversAPIURLFromEnv_spec_24_17_9 confirms LENNY_API_URL /
// LENNY_API_TOKEN are honored when the flags are absent, so the CLI
// reaches a remote gateway without a running stack. F-24.17.9.
func TestSessionDiscoversAPIURLFromEnv_spec_24_17_9(t *testing.T) {
	srv := fakeGateway(t)
	t.Setenv("LENNY_API_URL", srv.URL)
	t.Setenv("LENNY_API_TOKEN", "env-bearer")

	var stdout, stderr bytes.Buffer
	code := cmdSession([]string{"list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("session list via env: code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "sess_1") {
		t.Errorf("session list out = %q", stdout.String())
	}
}

// TestSessionAttachNoGateway_spec_24_17_214 confirms the implemented
// attach verb surfaces a gateway-discovery error (exit 1) when no
// --api-url, LENNY_API_URL, or running stack resolves, rather than
// silently doing nothing. F-24.17.8.
func TestSessionAttachNoGateway_spec_24_17_214(t *testing.T) {
	t.Setenv("LENNY_API_URL", "")
	t.Setenv("LENNY_API_TOKEN", "")
	var stdout, stderr bytes.Buffer
	code := cmdSession([]string{"attach", "sess_1"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("attach exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no --api-url") {
		t.Errorf("attach should report discovery failure: %q", stderr.String())
	}
}

// TestSessionUnknownFlagRejected confirms a typo'd flag fails fast rather
// than being silently dropped.
func TestSessionUnknownFlagRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdSession([]string{"list", "--bogus", "x"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown flag exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown flag") {
		t.Errorf("expected unknown-flag diagnosis: %q", stderr.String())
	}
}
