// SPDX-License-Identifier: MIT

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
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
// TestAttachProjectsElicitationCreateAndToolApproval verifies the
// §15.2.1 per-kind projection over the real SSE attach transport: an
// elicitation_request bus event reaches the client as a native MCP
// elicitation/create request (so an MCP-only client can surface and
// resolve the §9.2 elicitation natively), and an approval-required
// tool_use_requested event likewise projects to elicitation/create —
// the §15.2.1 carve-out that makes the REST-only tool-use approval
// endpoints correct by design rather than by accident. spec: §15.2
// lines 1362-1363, 1404. F-15.2.13, F-15.2.14.
func TestAttachProjectsElicitationCreateAndToolApproval_spec_15_2_1362(t *testing.T) {
	bus := sessionevents.NewBus(256)
	bus.PublishForTenant("acme", "sess-1", "elicitation_request",
		`{"elicitationId":"el-7","message":"Confirm?","schema":{"type":"string"},"originPod":"pod-1","initiatorType":"agent","delegationDepth":1}`, streamTS())
	bus.PublishForTenant("acme", "sess-1", "tool_use_requested",
		`{"tool_call_id":"tc-9","tool":"shell","args":{"cmd":"ls"}}`, streamTS())

	srv := NewServer()
	srv.SetAttach(AttachConfig{Events: bus, TenantFromRequest: func(*http.Request) string { return "acme" }, Now: streamTS})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	frames, stop, _ := startAttachStream(t, ts.URL, "sess-1", 0)
	defer stop()

	f1 := nextFrame(t, frames)
	var elicit map[string]any
	if err := json.Unmarshal([]byte(f1.data), &elicit); err != nil {
		t.Fatalf("elicitation frame not JSON: %v", err)
	}
	if elicit["method"] != "elicitation/create" {
		t.Fatalf("elicitation frame method = %v, want elicitation/create", elicit["method"])
	}
	if elicit["id"] != "elicit:el-7" {
		t.Fatalf("elicitation frame id = %v, want elicit:el-7", elicit["id"])
	}
	if elicit["params"].(map[string]any)["requestedSchema"] == nil {
		t.Fatalf("elicitation/create missing requestedSchema: %v", elicit["params"])
	}

	f2 := nextFrame(t, frames)
	var approve map[string]any
	if err := json.Unmarshal([]byte(f2.data), &approve); err != nil {
		t.Fatalf("tool-approval frame not JSON: %v", err)
	}
	if approve["method"] != "elicitation/create" {
		t.Fatalf("approval-required tool_use frame method = %v, want elicitation/create", approve["method"])
	}
	if approve["id"] != "toolapprove:tc-9" {
		t.Fatalf("tool-approval frame id = %v, want toolapprove:tc-9", approve["id"])
	}
}

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
	// §15.2.1 per-kind projection: a status_change is an MCP task status
	// notification. F-15.2.13.
	if !strings.Contains(f1.data, "notifications/tasks/statusUpdate") {
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

// diagnosis: a failure here means the SSE attach transport no longer
// bounds a write to a stalled subscriber, so a single slow client can
// block the handler goroutine writing into a full socket buffer forever
// instead of the connection being dropped within the configured timeout.
// That is exactly the unbounded-memory-growth / resource-exhaustion
// scenario the §15 bounded-error policy exists to prevent.
//
// TestAttachStreamClosesStalledSubscriberWithinBoundedTimeout verifies the
// §15 OutboundChannel bounded-error policy for the attach_session SSE
// transport: when the subscriber's read loop is behind such that a write
// would block, the gateway closes the connection within the bounded send
// timeout rather than blocking indefinitely.
//
// The test publishes a backlog large enough to exceed any realistic TCP
// socket buffer, opens the real attach stream over a real connection, and
// then reads nothing at all for a stall period — long enough for a
// genuinely blocked write to hit the bounded send timeout several times
// over, but not long enough to depend on ever fully draining the backlog.
// Only after the stall does the test start draining, and it asserts that
// draining completes (a clean EOF, meaning the server already closed the
// connection during the stall) within a bound comfortably below the time
// a full, un-terminated backlog transfer would take. A server that never
// bounds its writes keeps the handler goroutine parked in the still-open
// connection's live-event loop indefinitely once the backlog eventually
// drains, so the drain in that case never reaches EOF within the bound.
// spec: §7.2 "SSE back-pressure policy" (spec/07_session-lifecycle.md);
// §15 "Normative back-pressure policy for OutboundChannel
// implementations", bounded-error policy (spec/15_external-api-surface.md).
func TestAttachStreamClosesStalledSubscriberWithinBoundedTimeout_spec_15(t *testing.T) {
	origTimeout := attachSendTimeout
	attachSendTimeout = 30 * time.Millisecond
	defer func() { attachSendTimeout = origTimeout }()

	bus := sessionevents.NewBus(30000)
	// A large enough backlog (well past any realistic kernel socket
	// buffer, including macOS/Linux TCP autotuning ceilings in the low
	// single-digit megabytes) so the replay loop's write genuinely blocks
	// once the subscriber below stalls.
	payload := `{"type":"text","text":"` + strings.Repeat("A", 3000) + `"}`
	for i := 0; i < 7000; i++ {
		bus.PublishForTenant("acme", "sess-1", "response", payload, streamTS())
	}

	srv := NewServer()
	srv.SetAttach(AttachConfig{Events: bus, TenantFromRequest: func(*http.Request) string { return "acme" }, Now: streamTS})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(attachBody(t, "sess-1", 0)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	// The stall: read nothing at all for several multiples of the bounded
	// send timeout, so a genuinely blocked write has every opportunity to
	// hit it. This is the stalled-subscriber scenario the bounded-error
	// policy guards against.
	time.Sleep(300 * time.Millisecond)

	// Only now start draining. If the server already closed the
	// connection during the stall, this drains a small residue and the
	// drain finishes almost immediately — the gateway's bounded-error
	// policy calls for dropping the connection outright on a stalled
	// write, so the chunked-encoding trailer never gets written and the
	// client observes an abrupt io.ErrUnexpectedEOF rather than a clean
	// io.EOF; either terminates the drain and is an acceptable outcome
	// here. If the server never bounds its writes, this unblocks the
	// still-parked handler, which then finishes flushing the entire
	// backlog and settles into its live-event loop on the still-open
	// connection — draining never completes in that case.
	drained := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(io.Discard, resp.Body)
		drained <- copyErr
	}()

	select {
	case <-drained:
		// Any outcome here (clean EOF or an abrupt-close error) means the
		// connection was terminated rather than left open indefinitely.
	case <-time.After(2 * time.Second):
		t.Fatal("server did not close the stalled subscriber's connection within the bounded send timeout; draining the connection is still blocked on the still-open live-event loop")
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
	if err := writeMCPSessionEvent(rec, sessionevents.Event{Seq: 7, SessionID: "s", Type: "response", Data: `{"x":1}`, Timestamp: streamTS()}); err != nil {
		t.Fatalf("writeMCPSessionEvent: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "id: 7\n") {
		t.Fatalf("frame missing id line: %q", out)
	}
	// §15.2.1 per-kind projection: a response is an MCP streaming task
	// content frame. The SSE id: line still carries the SeqNum verbatim.
	// F-15.2.13.
	if !strings.Contains(out, "notifications/tasks/statusUpdate") {
		t.Fatalf("frame missing notification method: %q", out)
	}
}

func TestWriteMCPGapDetectedHasNoIDLine(t *testing.T) {
	var sb strings.Builder
	rec := &flushRecorder{Builder: &sb}
	if err := writeMCPGapDetected(rec, 5, 9); err != nil {
		t.Fatalf("writeMCPGapDetected: %v", err)
	}
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

// spec: §15 OutboundChannel bounded-error policy — "the gateway closes
// the channel on non-nil error" applies to any write/flush failure, not
// only a deadline timeout. writeBoundedSSEFrame must propagate a Write
// error rather than swallow it.
// diagnosis: a nil return here means a write failure on the underlying
// connection is silently ignored instead of signaling the caller to
// close the connection.
func TestWriteBoundedSSEFramePropagatesWriteError(t *testing.T) {
	w := &erroringWriter{writeErr: errors.New("broken pipe")}
	err := writeBoundedSSEFrame(w, []byte("data: x\n\n"))
	if err == nil {
		t.Fatal("writeBoundedSSEFrame: got nil error, want the underlying Write error propagated")
	}
}

// spec: §15 OutboundChannel bounded-error policy, same rationale as
// TestWriteBoundedSSEFramePropagatesWriteError, for the flush leg.
// diagnosis: a nil return here means a flush failure (e.g. the
// connection dropped between Write and Flush) is silently ignored.
func TestFlushBoundedSSEPropagatesFlushError(t *testing.T) {
	w := &erroringWriter{flushErr: errors.New("connection reset")}
	err := flushBoundedSSE(w)
	if err == nil {
		t.Fatal("flushBoundedSSE: got nil error, want the underlying Flush error propagated")
	}
}

// erroringWriter is a minimal http.ResponseWriter whose Write and Flush
// can be made to fail, to exercise writeBoundedSSEFrame/flushBoundedSSE's
// error-return branches. It deliberately does not implement
// SetWriteDeadline, so http.ResponseController.SetWriteDeadline returns
// http.ErrNotSupported (the tolerated case both functions fall through
// on), letting these tests isolate the Write/Flush error paths.
type erroringWriter struct {
	hdr      http.Header
	writeErr error
	flushErr error
}

func (e *erroringWriter) Header() http.Header {
	if e.hdr == nil {
		e.hdr = http.Header{}
	}
	return e.hdr
}
func (e *erroringWriter) Write(p []byte) (int, error) {
	if e.writeErr != nil {
		return 0, e.writeErr
	}
	return len(p), nil
}
func (e *erroringWriter) WriteHeader(int)   {}
func (e *erroringWriter) FlushError() error { return e.flushErr }
func (f *flushRecorder) Flush()             {}
