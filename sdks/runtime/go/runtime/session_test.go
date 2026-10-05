// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// gate is a release latch a handler blocks on.
type gate struct {
	entered chan string
	release chan struct{}
}

func newGate() *gate {
	return &gate{entered: make(chan string, 16), release: make(chan struct{})}
}

// wait records that a handler entered the gate and blocks until release.
func (g *gate) wait(tag string) {
	g.entered <- tag
	<-g.release
}

// awaitEntry waits for a handler to enter the gate and returns its tag.
func (g *gate) awaitEntry(t *testing.T) string {
	t.Helper()
	select {
	case tag := <-g.entered:
		return tag
	case <-time.After(3 * time.Second):
		t.Fatal("no handler entered the gate")
		return ""
	}
}

// spec: 15.7 (Runtime Author SDKs), 4.7.10 (Runtime process lifetime)
//
// The frame loop never blocks on one session's handler: while session A's
// handler is blocked, session B's message is answered and a heartbeat is
// acknowledged. A's response follows once its handler returns.
func TestFrameLoopDoesNotBlockOnOneSessionsHandler(t *testing.T) {
	g := newGate()
	h := &recorder{messageGate: func(_ context.Context, m Message) {
		if m.SessionID == "sess_a" {
			g.wait(m.Envelope.ID)
		}
	}}
	l := startLiveSDK(t, h)
	l.send(startFrame("sess_a", "st_a"))
	l.send(startFrame("sess_b", "st_b"))
	_, _ = l.next(3*time.Second), l.next(3*time.Second)

	l.send(msgFrame("sess_a", "m_a", "a"))
	g.awaitEntry(t)
	l.send(msgFrame("sess_b", "m_b", "b"))
	if f := l.next(3 * time.Second); f["sessionId"] != "sess_b" || firstOutput(f) != "b" {
		t.Fatalf("frame while sess_a's handler is blocked = %v, want sess_b's response", f)
	}
	if extra := l.heartbeatBarrier(); len(extra) != 0 {
		t.Fatalf("frames before heartbeat_ack = %v, want none", extra)
	}
	close(g.release)
	if f := l.next(3 * time.Second); f["sessionId"] != "sess_a" || firstOutput(f) != "a" {
		t.Fatalf("frame after release = %v, want sess_a's response", f)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start), 15.7 (Runtime Author
// SDKs)
//
// The SDK decodes the session_start context members into the
// CreateRequest. A frame that carries both objects fills
// ExperimentContext, TracingContext, and LLM, including llm.headers, and
// a frame that carries null for them leaves each nil.
func TestSessionStartDecodesContextIntoCreateRequest(t *testing.T) {
	t.Run("objects", func(t *testing.T) {
		h := &recorder{}
		runSDK(t, h, []string{`{"type":"session_start","sessionId":"sess_a","startId":"st_1",` +
			`"experimentContext":{"experimentId":"exp_1","variantId":"treatment","inherited":true},` +
			`"tracingContext":{"otel_trace_id":"0af7"},` +
			`"llm":{"deliveryMode":"proxy","dialect":"anthropic","apiKeyEnv":"ANTHROPIC_API_KEY","headers":{"anthropic-version":"2023-06-01"}}}`})
		reqs := h.createRequests()
		if len(reqs) != 1 {
			t.Fatalf("OnCreate ran %d times, want 1", len(reqs))
		}
		r := reqs[0]
		if r.SessionID != "sess_a" || r.TaskID != "sess_a" {
			t.Fatalf("ids = %q/%q, want sess_a/sess_a", r.SessionID, r.TaskID)
		}
		if r.ExperimentContext == nil || *r.ExperimentContext != (ExperimentContext{ExperimentID: "exp_1", VariantID: "treatment", Inherited: true}) {
			t.Fatalf("ExperimentContext = %+v", r.ExperimentContext)
		}
		if r.TracingContext["otel_trace_id"] != "0af7" {
			t.Fatalf("TracingContext = %v", r.TracingContext)
		}
		if r.LLM == nil || r.LLM.DeliveryMode != "proxy" || r.LLM.Dialect != "anthropic" ||
			r.LLM.APIKeyEnv != "ANTHROPIC_API_KEY" || r.LLM.Headers["anthropic-version"] != "2023-06-01" {
			t.Fatalf("LLM = %+v", r.LLM)
		}
		if r.Credentials != nil {
			t.Fatalf("Credentials = %+v, want nil for a frame naming no credentialsPath", r.Credentials)
		}
	})
	t.Run("nulls", func(t *testing.T) {
		h := &recorder{}
		runSDK(t, h, []string{`{"type":"session_start","sessionId":"sess_a","startId":"st_1","experimentContext":null,"tracingContext":null,"llm":null}`})
		r := h.createRequests()[0]
		if r.ExperimentContext != nil || r.TracingContext != nil || r.LLM != nil {
			t.Fatalf("CreateRequest = %+v, want nil ExperimentContext, TracingContext, and LLM", r)
		}
	})
}

// spec: 28.5.3 (CH-MSGSOCK, Outbound: session_started)
//
// The session_started answering a session_start is written only after
// OnCreate returns, and echoes the frame's sessionId and startId. The
// frame loop keeps answering heartbeats while the creation runs.
func TestSessionStartedFollowsOnCreate(t *testing.T) {
	g := newGate()
	h := &recorder{createGate: func(context.Context, CreateRequest) error {
		g.wait("create")
		return nil
	}}
	l := startLiveSDK(t, h)
	l.send(startFrame("sess_a", "st_9"))
	g.awaitEntry(t)
	if before := l.heartbeatBarrier(); len(before) != 0 {
		t.Fatalf("frames while OnCreate is blocked = %v, want none", before)
	}
	close(g.release)
	f := l.next(3 * time.Second)
	if f["type"] != "session_started" || f["sessionId"] != "sess_a" || f["startId"] != "st_9" || f["error"] != nil {
		t.Fatalf("frame after OnCreate returned = %v, want session_started sess_a / st_9 without error", f)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Session errors, Outbound: session_started)
//
// A session whose OnCreate fails is answered with a session_started
// carrying a RUNTIME_ERROR error, its messages are answered with a
// RUNTIME_ERROR response for its sessionId without reaching OnMessage, and
// the process keeps serving its other sessions.
func TestFailedCreateAnswersWithErrorsAndKeepsServing(t *testing.T) {
	h := &recorder{createGate: func(_ context.Context, req CreateRequest) error {
		if req.SessionID == "sess_bad" {
			return errors.New("model unavailable")
		}
		return nil
	}}
	l := startLiveSDK(t, h)
	l.send(startFrame("sess_bad", "st_1"))
	f := l.next(3 * time.Second)
	if f["type"] != "session_started" || errorCode(f) != "RUNTIME_ERROR" || f["startId"] != "st_1" {
		t.Fatalf("frame = %v, want session_started for st_1 carrying RUNTIME_ERROR", f)
	}
	l.send(msgFrame("sess_bad", "m1", "x"))
	if f := l.next(3 * time.Second); f["type"] != "response" || errorCode(f) != "RUNTIME_ERROR" || f["sessionId"] != "sess_bad" {
		t.Fatalf("response for the failed session = %v, want RUNTIME_ERROR for sess_bad", f)
	}
	l.send(startFrame("sess_ok", "st_2"))
	_ = l.next(3 * time.Second)
	l.send(msgFrame("sess_ok", "m2", "fine"))
	if f := l.next(3 * time.Second); f["sessionId"] != "sess_ok" || firstOutput(f) != "fine" || f["error"] != nil {
		t.Fatalf("response for the healthy session = %v, want its echo", f)
	}
	if i := indexOf(h.eventLog(), "message:sess_bad:m1"); i >= 0 {
		t.Fatal("OnMessage ran for a session whose creation failed")
	}
	// OnTerminate pairs with the OnCreate that ran, failed or not.
	if _, err := l.closeAndWait(); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if n := len(h.terminations("sess_bad")); n != 1 {
		t.Fatalf("OnTerminate ran %d times for the failed session, want 1", n)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start credentialsPath,
// Session errors), 4.7.11 (item 4)
//
// A credentialsPath naming a file the runtime cannot read fails the
// session's creation: session_started carries the error, OnCreate does
// not run, and the process keeps running.
func TestUnreadableCredentialFileFailsTheSessionCreation(t *testing.T) {
	h := &recorder{}
	missing := filepath.Join(t.TempDir(), "slots", "sess_a", "credentials.json")
	frames := runSDK(t, h, []string{
		fmt.Sprintf(`{"type":"session_start","sessionId":"sess_a","startId":"st_1","credentialsPath":%q}`, missing),
		`{"type":"heartbeat","ts":1}`,
	})
	byType := map[string]map[string]any{}
	for _, f := range frames {
		byType[f["type"].(string)] = f
	}
	started := byType["session_started"]
	if len(frames) != 2 || started == nil || errorCode(started) != "RUNTIME_ERROR" || byType["heartbeat_ack"] == nil {
		t.Fatalf("frames = %v, want session_started carrying RUNTIME_ERROR and a heartbeat_ack", frames)
	}
	if msg, _ := started["error"].(map[string]any)["message"].(string); !strings.Contains(msg, missing) {
		t.Fatalf("error message %q does not name the credential file", msg)
	}
	if len(h.createRequests()) != 0 || len(h.terminations("sess_a")) != 0 {
		t.Fatal("OnCreate or OnTerminate ran for a session whose credential file could not be read")
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start rule 3, Outbound:
// session_started rule 1)
//
// A duplicate session_start for a held session creates nothing and is
// answered with session_started carrying the duplicate's own startId. A
// duplicate read while the creation is running is answered after the
// first start's session_started, and a duplicate for a failed session
// carries the creation error.
func TestDuplicateSessionStartIsAnsweredAgain(t *testing.T) {
	g := newGate()
	h := &recorder{createGate: func(_ context.Context, req CreateRequest) error {
		if req.SessionID == "sess_bad" {
			return errors.New("broken")
		}
		g.wait(req.SessionID)
		return nil
	}}
	l := startLiveSDK(t, h)
	l.send(startFrame("sess_a", "st_1"))
	g.awaitEntry(t)
	l.send(startFrame("sess_a", "st_2"))
	if before := l.heartbeatBarrier(); len(before) != 0 {
		t.Fatalf("frames while the creation runs = %v, want none", before)
	}
	close(g.release)
	for _, want := range []string{"st_1", "st_2"} {
		if f := l.next(3 * time.Second); f["type"] != "session_started" || f["startId"] != want {
			t.Fatalf("frame = %v, want session_started for %s", f, want)
		}
	}
	l.send(startFrame("sess_a", "st_3"))
	if f := l.next(3 * time.Second); f["type"] != "session_started" || f["startId"] != "st_3" || f["error"] != nil {
		t.Fatalf("frame = %v, want session_started for st_3 without error", f)
	}
	l.send(startFrame("sess_bad", "st_4"))
	_ = l.next(3 * time.Second)
	l.send(startFrame("sess_bad", "st_5"))
	if f := l.next(3 * time.Second); f["startId"] != "st_5" || errorCode(f) != "RUNTIME_ERROR" {
		t.Fatalf("duplicate for the failed session = %v, want session_started st_5 carrying RUNTIME_ERROR", f)
	}
	creates := 0
	for _, e := range h.eventLog() {
		if e == "create:sess_a" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("OnCreate ran %d times for sess_a across three session_start frames, want 1", creates)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Session errors)
//
// A message for a session the runtime does not hold, including one that
// names no session, is answered with a RUNTIME_ERROR response for that
// sessionId and never reaches OnMessage; the process keeps running.
func TestMessageForUnknownSessionIsAnsweredWithRuntimeError(t *testing.T) {
	h := &recorder{}
	frames := runSDK(t, h, []string{
		msgFrame("sess_x", "m1", "x"),
		`{"type":"message","id":"m2","input":[{"type":"text","inline":"y"}]}`,
		`{"type":"heartbeat","ts":1}`,
	})
	if len(frames) != 3 {
		t.Fatalf("frames = %v, want two error responses and a heartbeat_ack", frames)
	}
	if frames[0]["sessionId"] != "sess_x" || errorCode(frames[0]) != "RUNTIME_ERROR" {
		t.Fatalf("first frame = %v, want RUNTIME_ERROR for sess_x", frames[0])
	}
	if errorCode(frames[1]) != "RUNTIME_ERROR" {
		t.Fatalf("second frame = %v, want RUNTIME_ERROR", frames[1])
	}
	if frames[2]["type"] != "heartbeat_ack" {
		t.Fatalf("third frame = %v, want heartbeat_ack", frames[2])
	}
	if len(h.eventLog()) != 0 {
		t.Fatalf("handler calls = %v, want none", h.eventLog())
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_end rule 2, Session errors)
//
// A message read after its session's session_end is dropped without a
// response, both while the ended state is still being released and after
// the release finished. A RUNTIME_ERROR response addressed to the ended
// session could otherwise be read by the adapter as the answer to a later
// start of the same session, since a response carries no startId.
func TestMessageAfterSessionEndWritesNoResponse(t *testing.T) {
	g := newGate()
	h := &recorder{messageGate: func(_ context.Context, m Message) {
		if m.Envelope.ID == "m1" {
			g.wait("m1")
		}
	}}
	l := startLiveSDK(t, h)
	l.send(startFrame("sess_a", "st_1"))
	_ = l.next(3 * time.Second)
	l.send(msgFrame("sess_a", "m1", "one"))
	g.awaitEntry(t)
	l.send(endFrame("sess_a"))
	l.send(msgFrame("sess_a", "m2", "during release"))
	if extra := l.heartbeatBarrier(); len(extra) != 0 {
		t.Fatalf("frames while the ended state was releasing = %v, want none", extra)
	}
	close(g.release)
	if !waitUntil(3*time.Second, func() bool { return len(h.terminations("sess_a")) == 1 }) {
		t.Fatal("OnTerminate did not run for the ended session")
	}
	l.send(msgFrame("sess_a", "m3", "after release"))
	if extra := l.heartbeatBarrier(); len(extra) != 0 {
		t.Fatalf("frames after the ended state was released = %v, want none", extra)
	}
	ev := h.eventLog()
	if indexOf(ev, "message:sess_a:m2") >= 0 || indexOf(ev, "message:sess_a:m3") >= 0 {
		t.Fatalf("messages read after session_end were dispatched: %v", ev)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_end rule 2), 15.7 (Runtime
// Author SDKs)
//
// On session_end the SDK cancels the session context, drops the queued
// messages without dispatching them, waits for the in-flight handler,
// drops that handler's response, and then runs OnTerminate with the
// session_end reason.
func TestSessionEndDiscardsQueueAndTerminatesAfterInFlightHandler(t *testing.T) {
	g := newGate()
	cancelled := make(chan struct{})
	h := &recorder{messageGate: func(ctx context.Context, m Message) {
		if m.Envelope.ID != "m1" {
			return
		}
		g.entered <- m.Envelope.ID
		<-ctx.Done()
		close(cancelled)
		<-g.release
	}}
	l := startLiveSDK(t, h)
	l.send(startFrame("sess_a", "st_1"))
	_ = l.next(3 * time.Second)
	l.send(msgFrame("sess_a", "m1", "one"))
	g.awaitEntry(t)
	l.send(msgFrame("sess_a", "m2", "two"))
	l.send(msgFrame("sess_a", "m3", "three"))
	l.send(endFrame("sess_a"))
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("the in-flight handler's context was not cancelled on session_end")
	}
	if n := len(h.terminations("sess_a")); n != 0 {
		t.Fatal("OnTerminate ran while the session's handler was still in flight")
	}
	close(g.release)
	if !waitUntil(3*time.Second, func() bool { return len(h.terminations("sess_a")) == 1 }) {
		t.Fatal("OnTerminate did not run after the in-flight handler returned")
	}
	if r := h.terminations("sess_a")[0]; r.Reason != "session_end" {
		t.Fatalf("termination reason = %+v, want session_end", r)
	}
	if extra := l.heartbeatBarrier(); len(extra) != 0 {
		t.Fatalf("frames after session_end = %v, want none: the in-flight response or a queued message was written", extra)
	}
	ev := h.eventLog()
	if indexOf(ev, "message:sess_a:m2") >= 0 || indexOf(ev, "message:sess_a:m3") >= 0 {
		t.Fatalf("queued messages were dispatched after session_end: %v", ev)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_end rule 2, Inbound:
// session_start rule 3), 4.7.10 (Runtime process lifetime)
//
// A session_start for a sessionId whose earlier state is still being
// released creates a new state whose OnCreate runs after the earlier
// OnTerminate returns. A message sent meanwhile queues and is answered by
// the new state rather than with RUNTIME_ERROR.
func TestSessionStartAfterSessionEndWaitsForTheRelease(t *testing.T) {
	g := newGate()
	h := &recorder{messageGate: func(_ context.Context, m Message) {
		if m.Envelope.ID == "m1" {
			g.wait("m1")
		}
	}}
	l := startLiveSDK(t, h)
	l.send(startFrame("sess_a", "st_1"))
	_ = l.next(3 * time.Second)
	l.send(msgFrame("sess_a", "m1", "one"))
	g.awaitEntry(t)
	l.send(endFrame("sess_a"))
	l.send(startFrame("sess_a", "st_2"))
	l.send(msgFrame("sess_a", "m2", "two"))
	if before := l.heartbeatBarrier(); len(before) != 0 {
		t.Fatalf("frames before the earlier state was released = %v, want none", before)
	}
	close(g.release)
	if f := l.next(3 * time.Second); f["type"] != "session_started" || f["startId"] != "st_2" {
		t.Fatalf("frame = %v, want session_started for st_2", f)
	}
	if f := l.next(3 * time.Second); f["type"] != "response" || firstOutput(f) != "two" || f["error"] != nil {
		t.Fatalf("frame = %v, want the new state's answer to m2", f)
	}
	ev := h.eventLog()
	term, second := indexOf(ev, "terminate:sess_a"), -1
	for i, e := range ev {
		if e == "create:sess_a" && i > 0 {
			second = i
		}
	}
	if term < 0 || second < term {
		t.Fatalf("event order = %v, want the second OnCreate after the first OnTerminate", ev)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_end rule 2), 15.7 (Runtime
// Author SDKs)
//
// Two overlapping context creations for one session are each released
// exactly once. Creation 1, which a session_end ended while it ran, is
// released when it completes and serves no message; creation 2 runs
// after that release and serves the session until a final session_end.
func TestOverlappingCreationsAreEachReleasedOnce(t *testing.T) {
	g := newGate()
	var mu sync.Mutex
	calls := 0
	served := map[string]int{}
	h := &recorder{}
	h.createGate = func(_ context.Context, req CreateRequest) error {
		mu.Lock()
		calls++
		n := calls
		served[req.SessionID] = n
		mu.Unlock()
		g.wait(fmt.Sprintf("create-%d", n))
		return nil
	}
	reply := &creationEcho{recorder: h, served: func(id string) int {
		mu.Lock()
		defer mu.Unlock()
		return served[id]
	}}
	l := startLiveSDK(t, reply)
	l.send(startFrame("sess_a", "st_1"))
	if tag := g.awaitEntry(t); tag != "create-1" {
		t.Fatalf("first creation tag = %s", tag)
	}
	l.send(endFrame("sess_a"))
	l.send(startFrame("sess_a", "st_2"))
	g.release <- struct{}{}
	if f := l.next(3 * time.Second); f["type"] != "session_started" || f["startId"] != "st_1" {
		t.Fatalf("frame = %v, want the session_started that answers st_1", f)
	}
	if !waitUntil(3*time.Second, func() bool { return len(h.terminations("sess_a")) == 1 }) {
		t.Fatal("creation 1 was not released once it completed")
	}
	if tag := g.awaitEntry(t); tag != "create-2" {
		t.Fatalf("second creation tag = %s", tag)
	}
	g.release <- struct{}{}
	if f := l.next(3 * time.Second); f["type"] != "session_started" || f["startId"] != "st_2" {
		t.Fatalf("frame = %v, want session_started for st_2", f)
	}
	l.send(msgFrame("sess_a", "m1", "x"))
	if f := l.next(3 * time.Second); firstOutput(f) != "creation 2" {
		t.Fatalf("message answered by %q, want creation 2", firstOutput(f))
	}
	l.send(endFrame("sess_a"))
	if !waitUntil(3*time.Second, func() bool { return len(h.terminations("sess_a")) == 2 }) {
		t.Fatal("creation 2 was not released by the final session_end")
	}
	if extra := l.heartbeatBarrier(); len(extra) != 0 {
		t.Fatalf("frames after the final session_end = %v, want none", extra)
	}
	if _, err := l.closeAndWait(); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if n := len(h.terminations("sess_a")); n != 2 {
		t.Fatalf("OnTerminate ran %d times, want once per creation", n)
	}
}

// creationEcho answers a message with the creation call that built the
// session's serving context.
type creationEcho struct {
	*recorder
	served func(sessionID string) int
}

func (c *creationEcho) OnMessage(_ context.Context, m Message) (Reply, error) {
	return TextReply(fmt.Sprintf("creation %d", c.served(m.SessionID))), nil
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_end rule 2)
//
// A tool_call pending when the session ends returns an error at once, and
// a tool_call issued with context.Background() from a goroutine the
// handler left running, after OnTerminate returned, writes no frame and
// returns an error well inside its timeout.
func TestToolCallsAfterSessionEndWriteNothingAndFailAtOnce(t *testing.T) {
	toolsCh := make(chan *AdapterTools, 1)
	pendingErr := make(chan error, 1)
	h := &recorder{messageGate: func(ctx context.Context, _ Message) {
		at := AdapterToolsFrom(ctx)
		toolsCh <- at
		go func() {
			_, err := at.ToolCall(context.Background(), "read_file", map[string]any{"path": "a"})
			pendingErr <- err
		}()
	}}
	l := startLiveSDK(t, h)
	l.send(startFrame("sess_a", "st_1"))
	_ = l.next(3 * time.Second)
	l.send(msgFrame("sess_a", "m1", "x"))
	tools := <-toolsCh
	seen := map[string]bool{}
	for len(seen) < 2 {
		f := l.next(3 * time.Second)
		seen[f["type"].(string)] = true
	}
	if !seen["tool_call"] || !seen["response"] {
		t.Fatalf("frames = %v, want the tool_call and the response", seen)
	}

	start := time.Now()
	l.send(endFrame("sess_a"))
	select {
	case err := <-pendingErr:
		if !errors.Is(err, errSessionEnded) {
			t.Fatalf("pending ToolCall error = %v, want errSessionEnded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the pending ToolCall did not return on session_end")
	}
	if !waitUntil(3*time.Second, func() bool { return len(h.terminations("sess_a")) == 1 }) {
		t.Fatal("OnTerminate did not run")
	}
	_, err := tools.ToolCall(context.Background(), "read_file", map[string]any{"path": "b"})
	if !errors.Is(err, errSessionEnded) || time.Since(start) > 2*time.Second {
		t.Fatalf("leaked ToolCall returned %v after %s, want errSessionEnded at once", err, time.Since(start))
	}
	if extra := l.heartbeatBarrier(); len(extra) != 0 {
		t.Fatalf("frames after session_end = %v, want none", extra)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_end rule 3)
//
// A session_end for a session the runtime does not hold is ignored, and
// a held session is unaffected.
func TestSessionEndForUnknownSessionIsIgnored(t *testing.T) {
	h := &recorder{}
	frames := runSDK(t, h, []string{
		startFrame("sess_a", "st_1"),
		endFrame("sess_x"),
		msgFrame("sess_a", "m1", "still here"),
	})
	if len(frames) != 2 || firstOutput(frames[1]) != "still here" {
		t.Fatalf("frames = %v, want session_started and sess_a's answer", frames)
	}
	if len(h.terminations("sess_x")) != 0 {
		t.Fatal("OnTerminate ran for a session the runtime never held")
	}
}

// spec: 15.7 (Runtime Author SDKs), 4.7.10 (Runtime process lifetime)
//
// At EOF every live session dispatches its queued messages before its
// OnTerminate, which receives stdin_closed, and Run returns only after
// every session goroutine returned, including a state a session_end
// removed whose handler is still running.
func TestEOFDrainsEverySessionBeforeRunReturns(t *testing.T) {
	g := newGate()
	h := &recorder{messageGate: func(_ context.Context, m Message) {
		if m.Envelope.ID == "a1" || m.Envelope.ID == "c1" {
			g.wait(m.Envelope.ID)
		}
	}}
	l := startLiveSDK(t, h)
	for _, id := range []string{"sess_a", "sess_b", "sess_c"} {
		l.send(startFrame(id, "st_"+id))
		_ = l.next(3 * time.Second)
	}
	l.send(msgFrame("sess_c", "c1", "c"))
	g.awaitEntry(t)
	l.send(endFrame("sess_c"))
	l.send(msgFrame("sess_a", "a1", "a"))
	g.awaitEntry(t)
	l.send(msgFrame("sess_a", "a2", "a"))
	l.send(msgFrame("sess_b", "b1", "b"))
	_ = l.next(3 * time.Second) // b1's response
	_ = l.in.Close()

	select {
	case <-l.done:
		t.Fatal("Run returned while session handlers were still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(g.release)
	var rest []map[string]any
	for f := range l.frames {
		rest = append(rest, f)
	}
	<-l.done
	if l.err != nil {
		t.Fatalf("Run returned %v", l.err)
	}
	if len(rest) != 2 {
		t.Fatalf("frames after EOF = %v, want the answers to a1 and a2 only", rest)
	}
	for _, id := range []string{"sess_a", "sess_b"} {
		if r := h.terminations(id); len(r) != 1 || r[0].Reason != "stdin_closed" {
			t.Fatalf("%s terminations = %+v, want one stdin_closed", id, r)
		}
	}
	if r := h.terminations("sess_c"); len(r) != 1 || r[0].Reason != "session_end" {
		t.Fatalf("sess_c terminations = %+v, want one session_end", r)
	}
	ev := h.eventLog()
	if indexOf(ev, "message:sess_a:a2") > indexOf(ev, "terminate:sess_a") {
		t.Fatalf("event order = %v, want a2 dispatched before sess_a's OnTerminate", ev)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start field table)
//
// A session_start without a sessionId or a startId, or with a member of
// the wrong type, is a protocol error.
func TestMalformedSessionStartIsProtocolError(t *testing.T) {
	for name, line := range map[string]string{
		"no sessionId":   `{"type":"session_start","startId":"st_1"}`,
		"no startId":     `{"type":"session_start","sessionId":"sess_a"}`,
		"tracing number": `{"type":"session_start","sessionId":"sess_a","startId":"st_1","tracingContext":{"k":1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var out syncBuffer
			err := Run(&recorder{}, WithStreams(strings.NewReader(line+"\n"), &out), WithLogger(nil), WithSocketTransport(false))
			if !ErrIsProtocol(err) {
				t.Fatalf("Run error = %v, want a protocol error", err)
			}
		})
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start credentialsPath),
// 4.7.10 (Runtime process lifetime)
//
// Each session's handler context carries that session's own credential
// bundle; a session whose session_start names no credentialsPath has
// none.
func TestEachSessionSeesItsOwnCredentials(t *testing.T) {
	dir := t.TempDir()
	pathA := writeProviderBundle(t, dir, "a.json", "anthropic")
	frames := runSDK(t, &credentialEcho{}, []string{
		fmt.Sprintf(`{"type":"session_start","sessionId":"sess_a","startId":"st_a","credentialsPath":%q}`, pathA),
		startFrame("sess_b", "st_b"),
		msgFrame("sess_a", "m_a", "x"),
		msgFrame("sess_b", "m_b", "x"),
	})
	got := map[string]string{}
	for _, f := range frames {
		if f["type"] == "response" {
			got[f["sessionId"].(string)] = firstOutput(f)
		}
	}
	if got["sess_a"] != "anthropic" || got["sess_b"] != "none" {
		t.Fatalf("providers seen = %v, want sess_a=anthropic and sess_b=none", got)
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS, credentials_rotated), 4.7.11 (item 4)
//
// A rotation naming a file the runtime cannot read, or naming no file,
// is reported and leaves the session's bundle in place; a readable file
// replaces it.
func TestRotationReloadFailuresKeepTheHeldBundle(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var logs []string
	cfg := defaultConfig()
	cfg.logger = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	p := newProcess(&recorder{}, cfg)
	st := &sessionState{id: "sess_a"}
	st.setCredentials(&CredentialBundle{Providers: []ProviderCredential{{Provider: "anthropic"}}})

	absent := filepath.Join(dir, "absent.json")
	if got := providerOf(p.reloadCredentials(st, absent)); got != "anthropic" {
		t.Fatalf("bundle after an unreadable rotation path = %s, want the held bundle", got)
	}
	if got := providerOf(p.reloadCredentials(st, "")); got != "anthropic" {
		t.Fatalf("bundle after a pathless rotation = %s, want the held bundle", got)
	}
	mu.Lock()
	joined := strings.Join(logs, "\n")
	mu.Unlock()
	if !strings.Contains(joined, absent) || !strings.Contains(joined, "no credentialsPath") {
		t.Fatalf("diagnostics = %q, want the unreadable path and the missing credentialsPath reported", joined)
	}
	rotated := writeProviderBundle(t, dir, "rotated.json", "rotated")
	if got := providerOf(p.reloadCredentials(st, rotated)); got != "rotated" || providerOf(st.Credentials()) != "rotated" {
		t.Fatalf("bundle after a readable rotation = %s, want rotated", got)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, tool_call and tool_result)
//
// An adapter-local tool call made inside a session's turn carries the
// session's identifier, and the tool_result correlated by id completes
// the call.
func TestToolCallCarriesTheSessionAndCompletesOnItsResult(t *testing.T) {
	got := make(chan string, 1)
	h := &recorder{messageGate: func(ctx context.Context, _ Message) {
		content, err := AdapterToolsFrom(ctx).ReadFile(ctx, "notes.txt")
		if err != nil {
			content = "error: " + err.Error()
		}
		got <- content
	}}
	l := startLiveSDK(t, h)
	l.send(startFrame("sess_a", "st_1"))
	_ = l.next(3 * time.Second)
	l.send(msgFrame("sess_a", "m1", "x"))
	call := l.next(3 * time.Second)
	if call["type"] != "tool_call" || call["sessionId"] != "sess_a" || call["name"] != "read_file" {
		t.Fatalf("frame = %v, want a read_file tool_call for sess_a", call)
	}
	l.send(fmt.Sprintf(`{"type":"tool_result","id":%q,"sessionId":"sess_a","content":[{"type":"text","inline":"hello"}]}`, call["id"]))
	select {
	case c := <-got:
		if c != "hello" {
			t.Fatalf("ReadFile returned %q, want hello", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ReadFile did not complete on its tool_result")
	}
	// A tool_result with no pending call is dropped.
	l.send(`{"type":"tool_result","id":"tc_unknown","content":[]}`)
	if f := l.next(3 * time.Second); f["type"] != "response" {
		t.Fatalf("frame = %v, want the turn's response", f)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: tool_result)
//
// A tool_result is routed by its sessionId. A result whose id matches
// session A's pending call but whose sessionId names session B, or a
// session the runtime does not hold, does not complete A's call, and the
// call stays pending until a result addressed to A arrives.
func TestToolResultForAnotherSessionDoesNotCompleteThePendingCall(t *testing.T) {
	got := make(chan string, 1)
	h := &recorder{messageGate: func(ctx context.Context, m Message) {
		if m.SessionID != "sess_a" {
			return
		}
		content, err := AdapterToolsFrom(ctx).ReadFile(ctx, "notes.txt")
		if err != nil {
			content = "error: " + err.Error()
		}
		got <- content
	}}
	l := startLiveSDK(t, h)
	l.send(startFrame("sess_a", "st_1"))
	_ = l.next(3 * time.Second)
	l.send(startFrame("sess_b", "st_2"))
	_ = l.next(3 * time.Second)
	l.send(msgFrame("sess_a", "m1", "x"))
	call := l.next(3 * time.Second)
	if call["type"] != "tool_call" || call["sessionId"] != "sess_a" {
		t.Fatalf("frame = %v, want a tool_call for sess_a", call)
	}

	for _, sid := range []string{"sess_b", "sess_unknown"} {
		l.send(fmt.Sprintf(`{"type":"tool_result","id":%q,"sessionId":%q,"content":[{"type":"text","inline":"wrong"}]}`, call["id"], sid))
	}
	if extra := l.heartbeatBarrier(); len(extra) != 0 {
		t.Fatalf("frames after misaddressed tool_results = %v, want none", extra)
	}
	select {
	case c := <-got:
		t.Fatalf("ReadFile completed with %q from a tool_result addressed to another session", c)
	case <-time.After(200 * time.Millisecond):
	}

	l.send(fmt.Sprintf(`{"type":"tool_result","id":%q,"sessionId":"sess_a","content":[{"type":"text","inline":"right"}]}`, call["id"]))
	select {
	case c := <-got:
		if c != "right" {
			t.Fatalf("ReadFile returned %q, want right", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ReadFile did not complete on the tool_result addressed to its session")
	}
}
