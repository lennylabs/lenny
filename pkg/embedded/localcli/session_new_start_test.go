// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// startGateway is a fake gateway that keeps each session's state, so a
// test of `lenny session new` observes the order of create, start, and
// message calls and the state a message arrives in. Like the gateway, it
// refuses a message to a session that is not running with
// TARGET_NOT_READY. lenny/create_session leaves a session in created and
// lenny/create_and_start_session leaves it in running.
type startGateway struct {
	// failStart makes lenny/create_and_start_session report a tool
	// failure and create no session.
	failStart bool
	// failMessages makes every message request fail with a server error
	// after the session has started.
	failMessages bool

	mu       sync.Mutex
	calls    []string
	states   map[string]string
	messages []string
}

func newStartGateway(t *testing.T, failStart bool) (*httptest.Server, *startGateway) {
	t.Helper()
	g := &startGateway{failStart: failStart, states: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", g.serveMCP)
	mux.HandleFunc("POST /v1/sessions/{id}/messages", g.serveMessages)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, g
}

func (g *startGateway) serveMCP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	reply := func(result any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID, "result": result,
		})
	}
	text := func(s string, isErr bool) map[string]any {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": s}}, "isError": isErr}
	}
	if req.Method != "tools/call" {
		reply(map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}})
		return
	}
	g.record(req.Params.Name)
	switch req.Params.Name {
	case "lenny/create_session":
		g.setState("sess_created", "created")
		reply(text(`{"sessionId":"sess_created","state":"created","uploadToken":"up-tok"}`, false))
	case "lenny/create_and_start_session":
		if g.failStart {
			reply(text("no warm pod is available for runtime echo", true))
			return
		}
		g.setState("sess_started", "running")
		reply(text(`{"id":"sess_started","state":"running","uploadToken":"up-tok"}`, false))
	default:
		reply(text("unknown tool "+req.Params.Name, true))
	}
}

func (g *startGateway) serveMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g.record("messages")
	body, _ := io.ReadAll(r.Body)
	g.mu.Lock()
	state := g.states[id]
	g.messages = append(g.messages, string(body))
	g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if g.failMessages {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"VALIDATION_ERROR","category":"PERMANENT","message":"message content is not valid","retryable":false}}`))
		return
	}
	if state != "running" {
		// The gateway classes TARGET_NOT_READY as transient. The fake
		// marks it non-retryable so a regression fails at once rather
		// than after the SDK's retry backoff.
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"TARGET_NOT_READY","category":"TRANSIENT","message":"session has not yet entered running state; retry after start","retryable":false}}`))
		return
	}
	_, _ = w.Write([]byte(`{"deliveryReceipt":{"messageId":"msg_1","status":"delivered"}}`))
}

func (g *startGateway) record(name string) {
	g.mu.Lock()
	g.calls = append(g.calls, name)
	g.mu.Unlock()
}

func (g *startGateway) setState(id, state string) {
	g.mu.Lock()
	g.states[id] = state
	g.mu.Unlock()
}

func (g *startGateway) snapshot() (calls, messages []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.calls...), append([]string(nil), g.messages...)
}

// TestSessionNewWithPromptStartsTheSessionBeforeDeliveringIt checks that
// `lenny session new --runtime <name> "prompt"` without `--workspace`
// creates and starts the session in one MCP call and only then sends the
// prompt, so the prompt reaches a running session. The fake refuses a
// message to a session that has not started, as the gateway does.
//
// spec: §24.17 (Session Operations)
func TestSessionNewWithPromptStartsTheSessionBeforeDeliveringIt(t *testing.T) {
	srv, g := newStartGateway(t, false)
	var stdout, stderr bytes.Buffer
	code := cmdSession(append([]string{"new"}, remoteArgs(srv, "--runtime", "echo", "hello", "there")...), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("session new: code=%d stderr=%s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "sess_started" {
		t.Errorf("stdout = %q, want sess_started", got)
	}
	calls, messages := g.snapshot()
	want := []string{"lenny/create_and_start_session", "messages"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("call order = %v, want %v", calls, want)
	}
	if len(messages) != 1 || !strings.Contains(messages[0], `"hello there"`) {
		t.Errorf("message bodies = %v, want one carrying the joined prompt", messages)
	}
}

// TestSessionNewWithoutPromptStillStartsTheSession checks that the command
// starts the session when no prompt is given, so the session it prints is
// one that `lenny session send` can reach, and that it sends no message.
//
// spec: §24.17 (Session Operations)
func TestSessionNewWithoutPromptStillStartsTheSession(t *testing.T) {
	srv, g := newStartGateway(t, false)
	var stdout, stderr bytes.Buffer
	code := cmdSession(append([]string{"new"}, remoteArgs(srv, "--runtime", "echo")...), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("session new: code=%d stderr=%s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "sess_started" {
		t.Errorf("stdout = %q, want sess_started", got)
	}
	calls, messages := g.snapshot()
	if strings.Join(calls, ",") != "lenny/create_and_start_session" {
		t.Errorf("calls = %v, want only lenny/create_and_start_session", calls)
	}
	if len(messages) != 0 {
		t.Errorf("message bodies = %v, want none without a prompt", messages)
	}
}

// TestSessionNewReportsAFailedStartAndSendsNoPrompt checks that when the
// gateway refuses to create and start the session, the command exits with
// status 1, reports the gateway's reason, prints no session id, and never
// sends the prompt.
//
// spec: §24.17 (Session Operations)
func TestSessionNewReportsAFailedStartAndSendsNoPrompt(t *testing.T) {
	srv, g := newStartGateway(t, true)
	var stdout, stderr bytes.Buffer
	code := cmdSession(append([]string{"new"}, remoteArgs(srv, "--runtime", "echo", "hello")...), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("session new with a failed start: code=%d, want 1; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no warm pod is available for runtime echo") {
		t.Errorf("stderr = %q, want the gateway's failure reason", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want no session id after a failed start", stdout.String())
	}
	calls, messages := g.snapshot()
	if strings.Join(calls, ",") != "lenny/create_and_start_session" {
		t.Errorf("calls = %v, want only lenny/create_and_start_session", calls)
	}
	if len(messages) != 0 {
		t.Errorf("message bodies = %v, want no prompt after a failed start", messages)
	}
}

// TestSessionNewNamesTheStartedSessionWhenThePromptFails checks that when
// the session starts but the prompt cannot be delivered, the command exits
// with status 1 and names the running session on stderr, so the caller can
// resend the prompt to it or cancel it rather than leave it holding a pod.
//
// spec: §24.17 (Session Operations)
func TestSessionNewNamesTheStartedSessionWhenThePromptFails(t *testing.T) {
	srv, g := newStartGateway(t, false)
	g.failMessages = true
	var stdout, stderr bytes.Buffer
	code := cmdSession(append([]string{"new"}, remoteArgs(srv, "--runtime", "echo", "hello")...), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("session new with a failed prompt: code=%d, want 1; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "session sess_started started") {
		t.Errorf("stderr = %q, want it to name the started session", stderr.String())
	}
	if !strings.Contains(stderr.String(), "message content is not valid") {
		t.Errorf("stderr = %q, want the gateway's reason", stderr.String())
	}
	calls, _ := g.snapshot()
	want := []string{"lenny/create_and_start_session", "messages"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Errorf("call order = %v, want %v", calls, want)
	}
}
