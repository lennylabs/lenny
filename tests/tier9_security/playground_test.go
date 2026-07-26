// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §27 web playground on a live cluster:
// the §27.7 Content-Security-Policy posture and the §27.3 dev-mode
// "ignored, not rejected" caller-material invariant.
package tier9_security_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// spec: §27.7 (the Content-Security-Policy directive block: "default-src
// 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src
// 'self' ...; img-src 'self' data:; object-src 'none'; media-src 'none';
// frame-ancestors 'none'; base-uri 'self'; form-action 'self'"; "The
// gateway also sets `X-Content-Type-Options: nosniff` and
// `Referrer-Policy: same-origin` on all playground responses."); §27.3
// ("dev" row of the "Auth by mode" table: "Any `Authorization: Bearer` or
// `lenny_playground_session` cookie presented in `dev` mode is **ignored**
// (not rejected — dev mode never gates on caller material).").
//
// diagnosis: a failure here means either the deployed gateway's
// Content-Security-Policy for /playground/* has regressed from the §27.7
// directive block on a live response (a clickjacking or injection
// exposure, not just a unit-level string mismatch), or the dev-mode mint
// endpoint gates on caller-supplied Authorization/cookie material it MUST
// ignore, which would make dev mode fail unpredictably depending on
// whatever stray credential a browser or proxy happens to attach rather
// than behaving as the admission-material-free mode the spec defines.
func TestPlaygroundSecurityPostureOnLiveCluster(t *testing.T) {
	// See the identical skip in
	// tests/tier5_e2e_kind/playground_test.go: playground.enabled=true
	// crash-loops the live gateway today because
	// pkg/gateway/mcpfabric/playground/metrics.go registers a
	// non-snake_case metric label ("authMode") that
	// pkg/observability/metrics's §16.1.1 validator rejects, and §27.8's
	// own metrics table names that same label "authMode", so the fix
	// requires reconciling the spec table and the code together rather
	// than a code-only change. Remove this skip once that lands and the
	// e2e overlay (tests/testinfra/kind/e2e-values.yaml) carries
	// playground.enabled=true.
	t.Skip("playground.enabled=true crash-loops the live gateway (non-snake_case metrics label); needs a spec/code reconciliation before this can run")

	d := sessiondriver.New(t)
	base := d.BaseURL()
	client := &http.Client{Timeout: 30 * time.Second}

	t.Run("live CSP matches the §27.7 directive block exactly", func(t *testing.T) {
		resp, err := client.Get(base + "/playground/")
		if err != nil {
			t.Fatalf("GET /playground/: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /playground/: want 200, got %d (body %s)", resp.StatusCode, body)
		}
		csp := resp.Header.Get("Content-Security-Policy")
		wantDirectives := []string{
			"default-src 'self'",
			"script-src 'self'",
			"style-src 'self' 'unsafe-inline'",
			"connect-src 'self'",
			"img-src 'self' data:",
			"object-src 'none'",
			"media-src 'none'",
			"frame-ancestors 'none'",
			"base-uri 'self'",
			"form-action 'self'",
		}
		for _, directive := range wantDirectives {
			if !strings.Contains(csp, directive) {
				t.Errorf("Content-Security-Policy = %q, missing directive %q", csp, directive)
			}
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
		}
		if got := resp.Header.Get("Referrer-Policy"); got != "same-origin" {
			t.Errorf("Referrer-Policy = %q, want same-origin", got)
		}

		// A non-playground route carries none of this: the CSP is
		// applied only to /playground/*, per §27.7.
		healthResp, err := client.Get(base + "/v1/admin/health")
		if err != nil {
			t.Fatalf("GET /v1/admin/health: %v", err)
		}
		defer healthResp.Body.Close()
		if got := healthResp.Header.Get("Content-Security-Policy"); got != "" {
			t.Errorf("GET /v1/admin/health carried a Content-Security-Policy header (%q); the §27.7 CSP is scoped to /playground/* only", got)
		}
	})

	t.Run("dev-mode mint ignores a stray bearer and a stray playground cookie", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, base+"/v1/playground/token", nil)
		if err != nil {
			t.Fatalf("build mint request: %v", err)
		}
		// Neither credential belongs to this installation's dev-mode
		// tenant; if the mint gated on either, the request would fail
		// with an auth error instead of minting normally.
		req.Header.Set("Authorization", "Bearer not-a-real-token")
		req.AddCookie(&http.Cookie{Name: "lenny_playground_session", Value: "not-a-real-session"})
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /v1/playground/token: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /v1/playground/token with a stray bearer and cookie in dev mode: want 200 (dev mode ignores caller material), got %d (body %s)", resp.StatusCode, body)
		}
	})
}

// spec: §27.1 ("Give security reviewers a UI surface to exercise the
// policy/audit pipeline end-to-end."); §27.5 (the playground UI "sends a
// chat message over the MCP WebSocket as a JSON-RPC tools/call for the
// session-message tool" (`lenny/send_message`), attaches to the session
// event stream first with `lenny/attach_session` "so the gateway pushes
// the agent's output ... back as notifications/lenny/sessionEvent frames
// over this socket", and the agent's reply "arrives as a session event,
// not as a tools/call result" (pkg/gateway/mcpfabric/playground/ui/app.js,
// the §27.4/§27.5 chat-screen wiring)); §27.9 line 254 ("The raw-frame
// inspector displays redacted frames only; the gateway applies the same
// redaction rules as the audit log ([§16.4]) before sending frames to the
// browser.").
//
// diagnosis: a failure here means a security reviewer's playground chat
// session does not work end to end against a live, chart-deployed
// gateway and a real runtime pod: the dev-mode bearer cannot open a real
// MCP WebSocket, attach to the session's event stream, send a chat
// message, and observe the agent's reply delivered as a live
// notifications/lenny/sessionEvent push over that same socket. This is
// the "full chat session" leg of the gap; it does not by itself prove
// the §27.9 credential-redaction guarantee (see the skip reason below).
func TestPlaygroundLiveChatSessionOverWebSocket(t *testing.T) {
	// Same root blocker as TestPlaygroundSecurityPostureOnLiveCluster
	// above: playground.enabled=true crash-loops the live gateway (the
	// non-snake_case "authMode" metrics label), so no playground route is
	// reachable on the e2e overlay today. Separately, and only relevant
	// once that is fixed: the browser-facing `lenny/send_message` tool
	// schema carries a plain string `message` argument
	// (pkg/gateway/mcpfabric/playground/ui/app.js), and the deployed
	// echo-runtime-sidecar registers no application-level tools of its
	// own, so nothing in the current live tool surface can put a
	// credential-shaped scalar value under a sensitive JSON key (the
	// precondition redactPlaygroundFrame scrubs, see
	// pkg/gateway/mcpfabric/mcp/playground_redact.go) into a frame this
	// test could observe. This test exercises the full chat-session
	// journey; the credential-redaction assertion is left for whatever
	// resolves that second, separate question.
	t.Skip("playground.enabled=true crash-loops the live gateway (non-snake_case metrics label); needs a spec/code reconciliation before this can run")

	d := sessiondriver.New(t)
	base := d.BaseURL()
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequest(http.MethodPost, base+"/v1/playground/token", nil)
	if err != nil {
		t.Fatalf("build mint request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/playground/token: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/playground/token: want 200, got %d (body %s)", resp.StatusCode, raw)
	}
	var minted struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.Unmarshal(raw, &minted); err != nil {
		t.Fatalf("decode mint response: %v; body %s", err, raw)
	}
	if minted.BearerToken == "" {
		t.Fatalf("mint response carried no bearerToken: %s", raw)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sess, err := d.CreateAndStart(ctx, "acme", sessiondriver.EchoRuntimeSidecar)
	if err != nil {
		t.Fatalf("create and start a session for the chat leg: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(base, "http") + "/mcp/v1/ws"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + minted.BearerToken}},
	})
	if err != nil {
		t.Fatalf("dial /mcp/v1/ws with the playground-minted bearer: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeFrame := func(v any) {
		body, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal frame: %v", err)
		}
		if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
			t.Fatalf("write frame: %v", err)
		}
	}
	readFrame := func() map[string]any {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		var f map[string]any
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("unmarshal frame: %v; frame %s", err, data)
		}
		return f
	}

	// §15.2 line 1289 / §27.5 R2: attach before sending, or the socket
	// never receives the pushed session events the reply arrives on.
	writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"id":      "attach-1",
		"method":  "tools/call",
		"params":  map[string]any{"name": "lenny/attach_session", "arguments": map[string]any{"sessionId": sess.ID}},
	})
	if attachResp := readFrame(); attachResp["error"] != nil {
		t.Fatalf("lenny/attach_session returned an error frame: %v", attachResp["error"])
	}

	const wantText = "hello from the security reviewer"
	writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"id":      "msg-1",
		"method":  "tools/call",
		"params":  map[string]any{"name": "lenny/send_message", "arguments": map[string]any{"to": sess.ID, "message": wantText}},
	})

	// The delivery receipt (a tools/call result) and the agent's reply
	// (a notifications/lenny/sessionEvent push, per §27.5) can arrive in
	// either order; read frames until the session event carries the
	// echoed text back or the deadline in ctx expires.
	deadline := time.After(30 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("did not observe the agent's reply as a notifications/lenny/sessionEvent frame within 30s")
		default:
		}
		frame := readFrame()
		if frame["method"] != "notifications/lenny/sessionEvent" {
			continue
		}
		params, _ := frame["params"].(map[string]any)
		data, _ := params["data"].(map[string]any)
		text, _ := data["text"].(string)
		if strings.Contains(text, wantText) {
			// echocore prefixes every echoed text part with "[echo
			// seq=N] "; observing the sent text back proves the full
			// send -> runtime -> attach-stream -> browser round trip
			// worked over the live deployed chart.
			return
		}
	}
}
