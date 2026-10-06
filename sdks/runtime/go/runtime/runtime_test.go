// SPDX-License-Identifier: MIT

package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// echoHandler is the Basic-level test handler: it echoes the inbound
// parts and records every lifecycle callback the SDK made.
type echoHandler struct {
	mu          sync.Mutex
	created     int
	messages    int
	terminated  int
	lastTermin  TerminationReason
	replyParts  func(in []MessagePart) []MessagePart
	replyErr    error
	createErr   error
	onMessageFn func(ctx context.Context, m Message)
}

func (h *echoHandler) OnCreate(_ context.Context, _ CreateRequest) error {
	h.mu.Lock()
	h.created++
	h.mu.Unlock()
	return h.createErr
}

func (h *echoHandler) OnMessage(ctx context.Context, m Message) (Reply, error) {
	h.mu.Lock()
	h.messages++
	h.mu.Unlock()
	if h.onMessageFn != nil {
		h.onMessageFn(ctx, m)
	}
	if h.replyErr != nil {
		return Reply{}, h.replyErr
	}
	parts := m.Envelope.Input
	if h.replyParts != nil {
		parts = h.replyParts(m.Envelope.Input)
	}
	return Reply{Parts: parts, Final: true}, nil
}

func (h *echoHandler) OnTerminate(_ context.Context, _ string, r TerminationReason) error {
	h.mu.Lock()
	h.terminated++
	h.lastTermin = r
	h.mu.Unlock()
	return nil
}

// runSDK drives the SDK loop with the given inbound lines and returns
// the decoded outbound frames. The handler runs in-process over a pipe.
func runSDK(t *testing.T, h Handler, inbound []string, opts ...Option) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(inbound, "\n") + "\n")
	var out syncBuffer
	allOpts := append([]Option{WithStreams(in, &out), WithLogger(nil), WithSocketTransport(false)}, opts...)

	done := make(chan error, 1)
	go func() { done <- Run(h, allOpts...) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s")
	}
	return decodeFrames(t, out.String())
}

// decodeFrames parses newline-delimited JSON frames.
func decodeFrames(t *testing.T, s string) []map[string]any {
	t.Helper()
	var frames []map[string]any
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("frame not JSON: %v (line %q)", err, line)
		}
		frames = append(frames, m)
	}
	return frames
}

// syncBuffer is a goroutine-safe in-memory writer.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunNilHandler(t *testing.T) {
	if err := Run(nil); err == nil {
		t.Fatal("Run(nil) must return an error")
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start, Outbound:
// session_started), 15.7 (Runtime Author SDKs)
//
// A message frame for an open session produces a response frame
// carrying the echoed parts, after the session_started that answers the
// session's session_start. OnCreate and OnTerminate each run once for the
// session.
func TestMessageRoundTrip(t *testing.T) {
	h := &echoHandler{}
	frames := runSDK(t, h, []string{
		startFrame("sess_a", "st_1"),
		msgFrame("sess_a", "msg_1", "ping"),
	})
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want session_started and response: %v", len(frames), frames)
	}
	if frames[0]["type"] != "session_started" || frames[0]["sessionId"] != "sess_a" || frames[0]["startId"] != "st_1" {
		t.Fatalf("first frame = %v, want session_started for sess_a / st_1", frames[0])
	}
	if frames[1]["type"] != "response" || frames[1]["sessionId"] != "sess_a" {
		t.Fatalf("second frame = %v, want the session's response", frames[1])
	}
	out, _ := frames[1]["output"].([]any)
	if len(out) != 1 {
		t.Fatalf("output has %d parts, want 1", len(out))
	}
	part := out[0].(map[string]any)
	if part["inline"] != "ping" {
		t.Fatalf("echoed inline = %v, want ping", part["inline"])
	}
	// §28.5.3 producer obligation: schemaVersion is stamped.
	if part["schemaVersion"] != float64(1) {
		t.Fatalf("schemaVersion = %v, want 1", part["schemaVersion"])
	}
	if h.created != 1 {
		t.Fatalf("OnCreate called %d times, want 1", h.created)
	}
	if h.terminated != 1 {
		t.Fatalf("OnTerminate called %d times, want 1", h.terminated)
	}
}

// TestHeartbeatAck confirms a heartbeat frame is answered with a
// heartbeat_ack within the loop (§28.5.3 heartbeat).
func TestHeartbeatAck(t *testing.T) {
	frames := runSDK(t, &echoHandler{}, []string{`{"type":"heartbeat","ts":1717430400}`})
	if len(frames) != 1 || frames[0]["type"] != "heartbeat_ack" {
		t.Fatalf("got %v, want a single heartbeat_ack", frames)
	}
}

// TestUnknownTypeIgnored confirms an unknown frame type is dropped and
// the loop keeps running (§28.5.3 forward compatibility).
func TestUnknownTypeIgnored(t *testing.T) {
	frames := runSDK(t, &echoHandler{}, []string{
		`{"type":"some_future_frame","x":1}`,
		`{"type":"heartbeat","ts":1}`,
	})
	if len(frames) != 1 || frames[0]["type"] != "heartbeat_ack" {
		t.Fatalf("unknown type not dropped cleanly: %v", frames)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, shutdown), 15.7 (Runtime Author SDKs)
//
// A shutdown frame ends the loop and every open session's OnTerminate
// sees the shutdown reason. A process that holds no session runs no
// OnTerminate.
func TestShutdownInvokesTerminate(t *testing.T) {
	h := &echoHandler{}
	runSDK(t, h, []string{startFrame("sess_a", "st_1"), `{"type":"shutdown","reason":"drain","deadline_ms":5000}`})
	if h.terminated != 1 {
		t.Fatalf("OnTerminate called %d times, want 1", h.terminated)
	}
	if h.lastTermin.Reason != "drain" || h.lastTermin.DeadlineMS != 5000 {
		t.Fatalf("termination reason = %+v, want {drain 5000}", h.lastTermin)
	}

	idle := &echoHandler{}
	runSDK(t, idle, []string{`{"type":"shutdown","reason":"drain","deadline_ms":5000}`})
	if idle.terminated != 0 || idle.created != 0 {
		t.Fatalf("a process holding no session ran OnCreate %d and OnTerminate %d times, want 0 and 0", idle.created, idle.terminated)
	}
}

// spec: 15.7 (Runtime Author SDKs), 28.5.3 (CH-MSGSOCK)
//
// Multiple messages for one session each produce a response, the SDK
// assigns the session increasing sequence numbers starting at 1, and
// OnCreate runs once for the session regardless of message count.
func TestSequentialMessages(t *testing.T) {
	var seqs []uint64
	h := &echoHandler{onMessageFn: func(_ context.Context, m Message) {
		seqs = append(seqs, m.Sequence)
	}}
	frames := runSDK(t, h, []string{
		startFrame("sess_a", "st_1"),
		msgFrame("sess_a", "m1", "one"),
		msgFrame("sess_a", "m2", "two"),
		msgFrame("sess_a", "m3", "three"),
	})
	if len(frames) != 4 {
		t.Fatalf("got %d frames, want session_started and three responses", len(frames))
	}
	if len(seqs) != 3 || seqs[0] != 1 || seqs[1] != 2 || seqs[2] != 3 {
		t.Fatalf("sequence numbers = %v, want [1 2 3]", seqs)
	}
	if h.created != 1 {
		t.Fatalf("OnCreate called %d times across three messages, want 1", h.created)
	}
}

// TestHandlerErrorReportedAsResponseError confirms an OnMessage error
// becomes a structured response error (§28.5.3 error via response).
func TestHandlerErrorReportedAsResponseError(t *testing.T) {
	h := &echoHandler{replyErr: errors.New("boom")}
	frames := runSDK(t, h, []string{
		startFrame("sess_a", "st_1"),
		msgFrame("sess_a", "m1", "x"),
	})
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
	if code := errorCode(frames[1]); code != "RUNTIME_ERROR" {
		t.Fatalf("error code = %q, want RUNTIME_ERROR (frame %v)", code, frames[1])
	}
	if frames[1]["sessionId"] != "sess_a" {
		t.Fatalf("error response sessionId = %v, want sess_a", frames[1]["sessionId"])
	}
}

// TestMalformedFrameIsProtocolError confirms a malformed inbound frame
// makes Run return a ProtocolError.
func TestMalformedFrameIsProtocolError(t *testing.T) {
	in := strings.NewReader("{not json}\n")
	var out syncBuffer
	err := Run(&echoHandler{}, WithStreams(in, &out), WithLogger(nil), WithSocketTransport(false))
	if err == nil {
		t.Fatal("Run must return an error on a malformed frame")
	}
	if !ErrIsProtocol(err) {
		t.Fatalf("error %v is not classified as a protocol error", err)
	}
}

// TestEmptyInputExitsCleanly confirms an empty inbound stream ends the
// loop with no frames and no error.
func TestEmptyInputExitsCleanly(t *testing.T) {
	in := strings.NewReader("")
	var out syncBuffer
	if err := Run(&echoHandler{}, WithStreams(in, &out), WithLogger(nil), WithSocketTransport(false)); err != nil {
		t.Fatalf("Run on empty input returned %v", err)
	}
	if out.String() != "" {
		t.Fatalf("empty input produced output: %q", out.String())
	}
}

// TestShorthandPartsAreCanonical confirms Reply parts emitted via the
// Text helper carry the canonical field set.
func TestShorthandPartsAreCanonical(t *testing.T) {
	h := &echoHandler{replyParts: func([]MessagePart) []MessagePart {
		return []MessagePart{Text("hello")}
	}}
	frames := runSDK(t, h, []string{
		startFrame("sess_a", "st_1"),
		msgFrame("sess_a", "m1", "x"),
	})
	out := frames[1]["output"].([]any)
	part := out[0].(map[string]any)
	if part["type"] != "text" || part["inline"] != "hello" {
		t.Fatalf("text part = %v, want {text hello}", part)
	}
}

// TestMessageEnvelopeAnnotationsRoundTrip asserts the §28.5.3 wire
// MessageEnvelope carries the §15.5 degradation-annotation
// map verbatim. A runtime author reading
// `env.Annotations[degradation.AnnotationSchemaVersionAhead]` must
// see the producer's `{knownVersion, encounteredVersion}` body without
// custom decoding.
//
// spec: §15.5. F-15.5.5.
func TestMessageEnvelopeAnnotationsRoundTrip_spec_15_5_2461(t *testing.T) {
	wire := []byte(`{
		"type": "message",
		"id": "m1",
		"schemaVersion": 1,
		"input": [{"type": "text", "inline": "hi"}],
		"annotations": {
			"schema_version_ahead": {"knownVersion": 1, "encounteredVersion": 3}
		}
	}`)
	env, err := decodeMessage(wire)
	if err != nil {
		t.Fatalf("decodeMessage: %v", err)
	}
	if env.Annotations == nil {
		t.Fatal("Annotations is nil; spec §15.5 catalog dropped")
	}
	body, ok := env.Annotations["schema_version_ahead"].(map[string]any)
	if !ok {
		t.Fatalf("schema_version_ahead body = %T, want map[string]any", env.Annotations["schema_version_ahead"])
	}
	if v, _ := body["knownVersion"].(float64); v != 1 {
		t.Errorf("knownVersion = %v, want 1", body["knownVersion"])
	}
	if v, _ := body["encounteredVersion"].(float64); v != 3 {
		t.Errorf("encounteredVersion = %v, want 3", body["encounteredVersion"])
	}

	// Round-trip back to JSON so producers can re-emit the envelope
	// downstream without losing the annotation.
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "schema_version_ahead") {
		t.Errorf("annotation key dropped on re-marshal: %s", out)
	}
}

// TestMessageEnvelopeAnnotationsOmitEmpty asserts the annotation field
// is omitted on the wire when no annotations are present. The §15.5
// catalog is opt-in: an unannotated envelope must not appear with an
// empty `annotations: {}` object.
//
// spec: §28.5.3 / §15.5. F-15.5.5.
func TestMessageEnvelopeAnnotationsOmitEmpty_spec_15_5(t *testing.T) {
	env := MessageEnvelope{Type: "message", ID: "m1"}
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "annotations") {
		t.Errorf("annotations rendered when empty: %s", out)
	}
}

// spec: 15.7 (Runtime Author SDKs), 28.5.3 (CH-MSGSOCK, shutdown), 4.7.10
// (Runtime process lifetime)
//
// A shutdown frame drains every live session as EOF does: each session
// dispatches the messages it queued, including one queued behind a
// handler still running when the frame arrived, before its OnTerminate,
// which receives the shutdown frame's reason, and Run returns only after
// every session's release.
func TestShutdownDrainsEverySessionWithItsReason(t *testing.T) {
	g := newGate()
	h := &recorder{messageGate: func(_ context.Context, m Message) {
		if m.Envelope.ID == "a1" {
			g.wait("a1")
		}
	}}
	l := startLiveSDK(t, h)
	for _, id := range []string{"sess_a", "sess_b"} {
		l.send(startFrame(id, "st_"+id))
		_ = l.next(3 * time.Second)
	}
	l.send(msgFrame("sess_a", "a1", "a"))
	g.awaitEntry(t)
	l.send(msgFrame("sess_a", "a2", "a"))
	l.send(`{"type":"shutdown","reason":"drain","deadline_ms":5000}`)
	select {
	case <-l.done:
		t.Fatal("Run returned while a session handler was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(g.release)
	var answered []string
	for f := range l.frames {
		if f["type"] == "response" {
			answered = append(answered, firstOutput(f))
		}
	}
	<-l.done
	if l.err != nil {
		t.Fatalf("Run returned %v", l.err)
	}
	if len(answered) != 2 {
		t.Fatalf("responses after shutdown = %v, want the answers to a1 and a2", answered)
	}
	for _, id := range []string{"sess_a", "sess_b"} {
		if r := h.terminations(id); len(r) != 1 || r[0].Reason != "drain" {
			t.Fatalf("%s terminations = %+v, want one carrying the shutdown reason", id, r)
		}
	}
}
