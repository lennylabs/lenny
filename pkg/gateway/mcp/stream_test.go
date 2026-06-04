// SPDX-License-Identifier: MIT

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
)

func streamTS() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// attachBody builds the JSON-RPC tools/call request body for attach_session.
func attachBody(t *testing.T, sessionID string, resumeFromSeq uint64) string {
	t.Helper()
	args := map[string]any{"sessionId": sessionID}
	if resumeFromSeq > 0 {
		args["resumeFromSeq"] = resumeFromSeq
	}
	argsJSON, _ := json.Marshal(args)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": AttachToolName, "arguments": json.RawMessage(argsJSON)},
	})
	return string(body)
}

type sseFrame struct {
	id      string
	data    string
	comment string
}

// scanSSE reads completed SSE frames from r and pushes each onto out. A
// frame ends at a blank line. It runs until r is closed (EOF) or errors.
func scanSSE(r io.Reader, out chan<- sseFrame) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var cur sseFrame
	has := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if has {
				out <- cur
				cur = sseFrame{}
				has = false
			}
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
			has = true
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
			has = true
		case strings.HasPrefix(line, ":"):
			cur.comment = line
			has = true
		}
	}
}

func nextFrame(t *testing.T, frames <-chan sseFrame) sseFrame {
	t.Helper()
	select {
	case f := <-frames:
		return f
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SSE frame")
		return sseFrame{}
	}
}

// startAttachStream opens the SSE channel against ts and returns a frame
// channel plus a cancel func.
func startAttachStream(t *testing.T, serverURL, sessionID string, resumeFromSeq uint64) (<-chan sseFrame, func(), *http.Response) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/mcp",
		strings.NewReader(attachBody(t, sessionID, resumeFromSeq)))
	if err != nil {
		cancel()
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("do request: %v", err)
	}
	frames := make(chan sseFrame, 16)
	go func() {
		scanSSE(resp.Body, frames)
		close(frames)
	}()
	return frames, func() { cancel(); resp.Body.Close() }, resp
}

// TestAttachStreamsBacklogThenLive verifies the §15.2 line 1331 transport:
// retained backlog replays first (each frame carrying its SeqNum on the
// SSE id: line), then live events tail the stream.
func TestAttachStreamsBacklogThenLive_spec_15_2(t *testing.T) {
	bus := sessionevents.NewBus(256)
	bus.PublishForTenant("acme", "sess-1", "status_change", `{"state":"running"}`, streamTS())
	bus.PublishForTenant("acme", "sess-1", "response", `{"text":"hi"}`, streamTS())

	srv := NewServer()
	srv.SetAttach(AttachConfig{
		Events:            bus,
		TenantFromRequest: func(*http.Request) string { return "acme" },
		Now:               streamTS,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	frames, stop, resp := startAttachStream(t, ts.URL, "sess-1", 0)
	defer stop()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	f1 := nextFrame(t, frames)
	if f1.id != "1" {
		t.Fatalf("backlog frame 1 id = %q, want 1", f1.id)
	}
	if !strings.Contains(f1.data, "notifications/lenny/sessionEvent") || !strings.Contains(f1.data, "status_change") {
		t.Fatalf("backlog frame 1 data = %q", f1.data)
	}
	f2 := nextFrame(t, frames)
	if f2.id != "2" {
		t.Fatalf("backlog frame 2 id = %q, want 2", f2.id)
	}

	// A live event after subscribe tails the same stream.
	bus.PublishForTenant("acme", "sess-1", "response", `{"text":"bye"}`, streamTS())
	f3 := nextFrame(t, frames)
	if f3.id != "3" {
		t.Fatalf("live frame id = %q, want 3", f3.id)
	}
	if !strings.Contains(f3.data, "bye") {
		t.Fatalf("live frame data = %q", f3.data)
	}
}

// TestAttachResumeFromSeqReplaysOnlyNewer verifies resumeFromSeq replays
// only events with SeqNum greater than the cursor. spec: §15.2 line 1331.
func TestAttachResumeFromSeqReplaysOnlyNewer_spec_15_2(t *testing.T) {
	bus := sessionevents.NewBus(256)
	for i := 0; i < 3; i++ {
		bus.PublishForTenant("acme", "sess-1", "response", `{}`, streamTS())
	}
	srv := NewServer()
	srv.SetAttach(AttachConfig{Events: bus, TenantFromRequest: func(*http.Request) string { return "acme" }, Now: streamTS})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	frames, stop, _ := startAttachStream(t, ts.URL, "sess-1", 2)
	defer stop()

	f := nextFrame(t, frames)
	if f.id != "3" {
		t.Fatalf("first replayed frame id = %q, want 3 (events 1-2 below cursor)", f.id)
	}
}

// TestAttachLastEventIDResume verifies the SSE Last-Event-ID header acts
// as an implicit resumeFromSeq on plain reconnects. spec: §15.2 line 1331.
func TestAttachLastEventIDResume_spec_15_2(t *testing.T) {
	bus := sessionevents.NewBus(256)
	for i := 0; i < 3; i++ {
		bus.PublishForTenant("acme", "sess-1", "response", `{}`, streamTS())
	}
	srv := NewServer()
	srv.SetAttach(AttachConfig{Events: bus, TenantFromRequest: func(*http.Request) string { return "acme" }, Now: streamTS})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/mcp", strings.NewReader(attachBody(t, "sess-1", 0)))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Last-Event-ID", "2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	frames := make(chan sseFrame, 8)
	go func() { scanSSE(resp.Body, frames); close(frames) }()

	f := nextFrame(t, frames)
	if f.id != "3" {
		t.Fatalf("Last-Event-ID resume first frame id = %q, want 3", f.id)
	}
}

// TestAttachGapDetected verifies the §15.2 line 1331 gap_detected frame is
// emitted (with no id: line) when the cursor sits below the oldest
// retained sequence.
func TestAttachGapDetected_spec_15_2(t *testing.T) {
	bus := sessionevents.NewBus(2) // retains the last 2 events
	for i := 0; i < 4; i++ {
		bus.PublishForTenant("acme", "sess-1", "response", `{}`, streamTS())
	}
	// history now holds seq 3,4; oldest retained = 3.
	srv := NewServer()
	srv.SetAttach(AttachConfig{Events: bus, TenantFromRequest: func(*http.Request) string { return "acme" }, Now: streamTS})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	frames, stop, _ := startAttachStream(t, ts.URL, "sess-1", 1)
	defer stop()

	gap := nextFrame(t, frames)
	if gap.id != "" {
		t.Fatalf("gap_detected frame carried an id: %q (must be a stream-control frame)", gap.id)
	}
	if !strings.Contains(gap.data, "notifications/lenny/gapDetected") {
		t.Fatalf("expected gap_detected frame, got %q", gap.data)
	}
	if !strings.Contains(gap.data, `"lastSeenSeq":1`) || !strings.Contains(gap.data, `"nextSeq":3`) {
		t.Fatalf("gap_detected payload = %q, want lastSeenSeq=1 nextSeq=3", gap.data)
	}
	// The backlog (seq 3,4) follows the gap marker.
	if f := nextFrame(t, frames); f.id != "3" {
		t.Fatalf("frame after gap id = %q, want 3", f.id)
	}
}

// TestAttachKeepalive verifies the §15.2 line 1333 `:keepalive` comment
// line is written after the idle interval with no SessionEvent frame.
func TestAttachKeepalive_spec_15_2(t *testing.T) {
	orig := attachKeepAliveInterval
	attachKeepAliveInterval = 30 * time.Millisecond
	defer func() { attachKeepAliveInterval = orig }()

	bus := sessionevents.NewBus(256)
	bus.PublishForTenant("acme", "sess-1", "response", `{}`, streamTS())
	srv := NewServer()
	srv.SetAttach(AttachConfig{Events: bus, TenantFromRequest: func(*http.Request) string { return "acme" }, Now: streamTS})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	frames, stop, _ := startAttachStream(t, ts.URL, "sess-1", 0)
	defer stop()

	_ = nextFrame(t, frames) // the backlog event
	ka := nextFrame(t, frames)
	if !strings.HasPrefix(ka.comment, ":keepalive") {
		t.Fatalf("expected :keepalive comment frame, got id=%q data=%q comment=%q", ka.id, ka.data, ka.comment)
	}
}

// TestAttachMissingSessionID returns VALIDATION_ERROR before opening the
// stream. spec: §15.2.1 rule 3.
func TestAttachMissingSessionID_spec_15_2(t *testing.T) {
	bus := sessionevents.NewBus(256)
	srv := NewServer()
	srv.SetAttach(AttachConfig{Events: bus, TenantFromRequest: func(*http.Request) string { return "acme" }})

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + AttachToolName + `","arguments":{}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var resp jsonRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if resp.Error == nil {
		t.Fatal("expected a JSON-RPC error for missing sessionId")
	}
	if !strings.Contains(rec.Body.String(), "VALIDATION_ERROR") {
		t.Fatalf("expected VALIDATION_ERROR, got %s", rec.Body.String())
	}
}

// TestAttachAuthorizeRejection surfaces the Authorize gate's ToolError as
// the JSON-RPC error before any SSE byte. spec: §7.2 tenant isolation.
func TestAttachAuthorizeRejection_spec_15_2(t *testing.T) {
	bus := sessionevents.NewBus(256)
	srv := NewServer()
	srv.SetAttach(AttachConfig{
		Events:            bus,
		TenantFromRequest: func(*http.Request) string { return "acme" },
		Authorize: func(_ context.Context, _, _ string) error {
			return NewToolError("RESOURCE_NOT_FOUND", "session not found", nil)
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(attachBody(t, "ghost", 0)))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("error path Content-Type = %q, want application/json (no SSE upgrade)", ct)
	}
	if !strings.Contains(rec.Body.String(), "RESOURCE_NOT_FOUND") {
		t.Fatalf("expected RESOURCE_NOT_FOUND, got %s", rec.Body.String())
	}
}

// TestAttachNonStreamFallsThroughToHandler verifies a tools/call without
// Accept: text/event-stream runs the registered snapshot handler instead
// of upgrading to SSE. spec: §15.2 line 1289.
func TestAttachNonStreamFallsThroughToHandler_spec_15_2(t *testing.T) {
	bus := sessionevents.NewBus(256)
	called := false
	srv := NewServer()
	srv.RegisterTool(Tool{Name: AttachToolName, Description: "snapshot"},
		func(context.Context, json.RawMessage) (ToolResult, error) {
			called = true
			return ToolResult{Content: []ToolContent{{Type: "text", Text: `{"state":"running"}`}}}, nil
		})
	srv.SetAttach(AttachConfig{Events: bus, TenantFromRequest: func(*http.Request) string { return "acme" }})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(attachBody(t, "sess-1", 0)))
	// No Accept: text/event-stream — the snapshot handler must run.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if !called {
		t.Fatal("non-SSE attach_session call did not reach the registered handler")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("snapshot Content-Type = %q, want application/json", ct)
	}
}

func TestWantsEventStream(t *testing.T) {
	cases := map[string]bool{
		"text/event-stream":                   true,
		"application/json, text/event-stream": true,
		"TEXT/EVENT-STREAM":                   true,
		"application/json":                    false,
		"":                                    false,
	}
	for accept, want := range cases {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		if got := wantsEventStream(r); got != want {
			t.Errorf("wantsEventStream(%q) = %v, want %v", accept, got, want)
		}
	}
}

func TestLastEventID(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if got := lastEventID(r); got != 0 {
		t.Fatalf("no header → %d, want 0", got)
	}
	r.Header.Set("Last-Event-ID", "42")
	if got := lastEventID(r); got != 42 {
		t.Fatalf("Last-Event-ID 42 → %d", got)
	}
	r.Header.Set("Last-Event-ID", "not-a-number")
	if got := lastEventID(r); got != 0 {
		t.Fatalf("non-numeric Last-Event-ID → %d, want 0", got)
	}
}

func TestWriteMCPSessionEventCarriesSeqOnIDLine(t *testing.T) {
	var sb strings.Builder
	rec := &flushRecorder{Builder: &sb}
	writeMCPSessionEvent(rec, sessionevents.Event{Seq: 7, SessionID: "s", Type: "response", Data: `{"x":1}`, Timestamp: streamTS()})
	out := sb.String()
	if !strings.Contains(out, "id: 7\n") {
		t.Fatalf("frame missing id line: %q", out)
	}
	if !strings.Contains(out, "notifications/lenny/sessionEvent") {
		t.Fatalf("frame missing notification method: %q", out)
	}
}

func TestWriteMCPGapDetectedHasNoIDLine(t *testing.T) {
	var sb strings.Builder
	rec := &flushRecorder{Builder: &sb}
	writeMCPGapDetected(rec, 5, 9)
	out := sb.String()
	if strings.Contains(out, "id:") {
		t.Fatalf("gap_detected must not carry an id line: %q", out)
	}
	if !strings.Contains(out, `"lastSeenSeq":5`) || !strings.Contains(out, `"nextSeq":9`) {
		t.Fatalf("gap_detected payload = %q", out)
	}
}

// flushRecorder is a minimal http.ResponseWriter for the frame-writer unit
// tests (they call the writers directly, not through a server).
type flushRecorder struct {
	*strings.Builder
	hdr http.Header
}

func (f *flushRecorder) Header() http.Header {
	if f.hdr == nil {
		f.hdr = http.Header{}
	}
	return f.hdr
}
func (f *flushRecorder) Write(p []byte) (int, error) { return f.Builder.Write(p) }
func (f *flushRecorder) WriteHeader(int)             {}
func (f *flushRecorder) Flush()                      {}
