// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	lenny "github.com/lennylabs/lenny/sdks/client/go/lenny"
)

// sseFrame writes one Server-Sent Events frame and flushes it so the SDK
// stream reader observes it before the connection is held open.
func sseFrame(t *testing.T, w http.ResponseWriter, fl http.Flusher, seq int, typ, data string) {
	t.Helper()
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", seq, typ, data)
	fl.Flush()
}

// attachGateway is a fake gateway exercising the §24.17 line 213 attach
// render loop. It serves the §15.1 SSE event stream and the GetSession
// fast path. sessionState controls what GET /v1/sessions/{id} reports;
// frames are the SSE frames the events endpoint emits in order. It
// records whether the events endpoint was reached.
type attachGateway struct {
	sessionState string
	frames       []struct {
		typ  string
		data string
	}
	eventsHit atomic.Bool
}

func (g *attachGateway) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		write := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": result,
			})
		}
		switch req.Method {
		case "initialize":
			write(map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "g", "version": "0"}})
		case "tools/call":
			write(map[string]any{"content": []map[string]any{{"type": "text", "text": `{"sessionId":"sess_att","state":"running"}`}}})
		default:
			write(map[string]any{})
		}
	})
	mux.HandleFunc("GET /v1/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","state":"` + g.sessionState + `","runtimeRef":"claude-code"}`))
	})
	mux.HandleFunc("GET /v1/sessions/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		g.eventsHit.Store(true)
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter is not a Flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i, f := range g.frames {
			sseFrame(t, w, fl, i+1, f.typ, f.data)
		}
		// Hold the connection open like the real gateway until the client
		// disconnects (the attach loop cancels its context on terminal).
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newAttachClient(t *testing.T, url string) *lenny.Client {
	t.Helper()
	c, err := lenny.New(url, lenny.WithAuth(lenny.BearerToken("test-bearer")))
	if err != nil {
		t.Fatalf("lenny.New: %v", err)
	}
	return c
}

// TestStreamSessionRendersUntilTerminal_spec_24_17_213 confirms the attach
// loop renders response output to stdout, lifecycle transitions to
// stderr, and exits 0 when the session reaches `completed`. F-24.17.8.
func TestStreamSessionRendersUntilTerminal_spec_24_17_213(t *testing.T) {
	g := &attachGateway{sessionState: "running", frames: []struct{ typ, data string }{
		{"status_change", `{"state":"running"}`},
		{"response", `{"text":"hello world"}`},
		{"status_change", `{"state":"completed"}`},
	}}
	srv := g.server(t)
	var stdout, stderr bytes.Buffer
	code := streamSession(context.Background(), newAttachClient(t, srv.URL), "sess_att", &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "hello world") {
		t.Errorf("stdout = %q, want agent output", stdout.String())
	}
	if !strings.Contains(stderr.String(), "session completed") {
		t.Errorf("stderr = %q, want terminal lifecycle line", stderr.String())
	}
}

// TestStreamSessionFailedExitsNonZero_spec_24_17_213 confirms a session
// that ends in `failed` returns a non-zero exit code so a script can
// branch on the disposition. F-24.17.8.
func TestStreamSessionFailedExitsNonZero_spec_24_17_213(t *testing.T) {
	g := &attachGateway{sessionState: "running", frames: []struct{ typ, data string }{
		{"status_change", `{"state":"failed"}`},
	}}
	srv := g.server(t)
	var stdout, stderr bytes.Buffer
	code := streamSession(context.Background(), newAttachClient(t, srv.URL), "sess_att", &stdout, &stderr)
	if code == 0 {
		t.Fatalf("code = 0, want non-zero on failed (stderr=%s)", stderr.String())
	}
}

// TestStreamSessionFastPathTerminal_spec_24_17_213 confirms attaching to a
// session that is already terminal reports it via the GetSession fast path
// without opening the event stream. F-24.17.8.
func TestStreamSessionFastPathTerminal_spec_24_17_213(t *testing.T) {
	g := &attachGateway{sessionState: "completed"}
	srv := g.server(t)
	var stdout, stderr bytes.Buffer
	code := streamSession(context.Background(), newAttachClient(t, srv.URL), "sess_att", &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (stderr=%s)", code, stderr.String())
	}
	if g.eventsHit.Load() {
		t.Error("event stream was opened for an already-terminal session; fast path missed")
	}
	if !strings.Contains(stderr.String(), "session completed") {
		t.Errorf("stderr = %q, want terminal lifecycle line", stderr.String())
	}
}

// TestStreamSessionRendersElicitation_spec_24_17_213 confirms an
// elicitation_request frame surfaces the §8.5 prompt parts inline.
// F-24.17.8.
func TestStreamSessionRendersElicitation_spec_24_17_213(t *testing.T) {
	g := &attachGateway{sessionState: "running", frames: []struct{ typ, data string }{
		{"elicitation_request", `{"requestId":"r1","parts":[{"type":"text","text":"What is your name?"}],"metadata":{"maxInputRounds":1}}`},
		{"status_change", `{"state":"completed"}`},
	}}
	srv := g.server(t)
	var stdout, stderr bytes.Buffer
	code := streamSession(context.Background(), newAttachClient(t, srv.URL), "sess_att", &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "What is your name?") {
		t.Errorf("stderr = %q, want elicitation prompt", stderr.String())
	}
	if !strings.Contains(stderr.String(), "maxInputRounds=1") {
		t.Errorf("stderr = %q, want one_shot annotation", stderr.String())
	}
}

// TestSessionNewAttachStreams_spec_24_17_4 confirms `session new --attach`
// creates the session, prints its id, then renders the stream inline
// until the session terminates. F-24.17.4 / F-24.17.8.
func TestSessionNewAttachStreams_spec_24_17_4(t *testing.T) {
	g := &attachGateway{sessionState: "running", frames: []struct{ typ, data string }{
		{"response", `{"text":"done"}`},
		{"status_change", `{"state":"completed"}`},
	}}
	srv := g.server(t)
	var stdout, stderr bytes.Buffer
	args := []string{"new", "--api-url", srv.URL, "--token", "test-bearer", "--runtime", "claude-code", "--attach"}
	code := cmdSession(args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "sess_att") {
		t.Errorf("stdout = %q, want created session id", stdout.String())
	}
	if !strings.Contains(stdout.String(), "done") {
		t.Errorf("stdout = %q, want streamed output", stdout.String())
	}
	if !g.eventsHit.Load() {
		t.Error("--attach did not open the event stream")
	}
}

// TestSessionAttachSubcommand_spec_24_17_214 confirms `session attach
// <id>` opens the stream for an existing session. F-24.17.8.
func TestSessionAttachSubcommand_spec_24_17_214(t *testing.T) {
	g := &attachGateway{sessionState: "running", frames: []struct{ typ, data string }{
		{"status_change", `{"state":"completed"}`},
	}}
	srv := g.server(t)
	var stdout, stderr bytes.Buffer
	args := []string{"attach", "--api-url", srv.URL, "--token", "test-bearer", "sess_att"}
	code := cmdSession(args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (stderr=%s)", code, stderr.String())
	}
	if !g.eventsHit.Load() {
		t.Error("attach did not open the event stream")
	}
}

// TestSessionAttachMissingID_spec_24_17_214 confirms the attach verb
// rejects a missing session id with a usage error. F-24.17.8.
func TestSessionAttachMissingID_spec_24_17_214(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdSession([]string{"attach", "--api-url", "http://example", "--token", "t"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage") {
		t.Errorf("stderr = %q, want usage error", stderr.String())
	}
}

// TestAttachWantedDefaultsOffForNonTTY_spec_24_17_213 confirms the
// §24.17 line 213 default: without --attach and with a non-interactive
// stdout (a buffer, the scripted/piped path), attach stays off. F-24.17.4.
func TestAttachWantedDefaultsOffForNonTTY_spec_24_17_213(t *testing.T) {
	if attachWanted(sessionFlags{}, &bytes.Buffer{}) {
		t.Error("attach defaulted on for a non-TTY stdout")
	}
	if !attachWanted(sessionFlags{attach: true}, &bytes.Buffer{}) {
		t.Error("explicit --attach did not force attach")
	}
}

// TestSessionNewNonAttachPrintsID_spec_24_17_4 confirms the
// non-interactive default still prints just the session id (the scripted
// path), unchanged by the attach work. F-24.17.4.
func TestSessionNewNonAttachPrintsID_spec_24_17_4(t *testing.T) {
	g := &attachGateway{sessionState: "running"}
	srv := g.server(t)
	var stdout, stderr bytes.Buffer
	args := []string{"new", "--api-url", srv.URL, "--token", "test-bearer", "--runtime", "claude-code"}
	code := cmdSession(args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (stderr=%s)", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "sess_att" {
		t.Errorf("stdout = %q, want bare session id", stdout.String())
	}
	if g.eventsHit.Load() {
		t.Error("non-attached session new opened the event stream")
	}
}
