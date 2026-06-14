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
	replyParts  func(in []OutputPart) []OutputPart
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

func (h *echoHandler) OnTerminate(_ context.Context, r TerminationReason) error {
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

// TestMessageRoundTrip confirms a message frame produces a response
// frame carrying the echoed parts (§15.4.1 message/response).
func TestMessageRoundTrip(t *testing.T) {
	h := &echoHandler{}
	frames := runSDK(t, h, []string{
		`{"type":"message","id":"msg_1","input":[{"type":"text","inline":"ping"}]}`,
	})
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if frames[0]["type"] != "response" {
		t.Fatalf("frame type = %v, want response", frames[0]["type"])
	}
	out, _ := frames[0]["output"].([]any)
	if len(out) != 1 {
		t.Fatalf("output has %d parts, want 1", len(out))
	}
	part := out[0].(map[string]any)
	if part["inline"] != "ping" {
		t.Fatalf("echoed inline = %v, want ping", part["inline"])
	}
	// §15.4.1 producer obligation: schemaVersion is stamped.
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
// heartbeat_ack within the loop (§15.4.1 heartbeat).
func TestHeartbeatAck(t *testing.T) {
	frames := runSDK(t, &echoHandler{}, []string{`{"type":"heartbeat","ts":1717430400}`})
	if len(frames) != 1 || frames[0]["type"] != "heartbeat_ack" {
		t.Fatalf("got %v, want a single heartbeat_ack", frames)
	}
}

// TestUnknownTypeIgnored confirms an unknown frame type is dropped and
// the loop keeps running (§15.4.1 forward compatibility).
func TestUnknownTypeIgnored(t *testing.T) {
	frames := runSDK(t, &echoHandler{}, []string{
		`{"type":"some_future_frame","x":1}`,
		`{"type":"heartbeat","ts":1}`,
	})
	if len(frames) != 1 || frames[0]["type"] != "heartbeat_ack" {
		t.Fatalf("unknown type not dropped cleanly: %v", frames)
	}
}

// TestShutdownInvokesTerminate confirms a shutdown frame ends the loop
// and OnTerminate sees the shutdown reason (§15.4.1 shutdown).
func TestShutdownInvokesTerminate(t *testing.T) {
	h := &echoHandler{}
	runSDK(t, h, []string{`{"type":"shutdown","reason":"drain","deadline_ms":5000}`})
	if h.terminated != 1 {
		t.Fatalf("OnTerminate called %d times, want 1", h.terminated)
	}
	if h.lastTermin.Reason != "drain" || h.lastTermin.DeadlineMS != 5000 {
		t.Fatalf("termination reason = %+v, want {drain 5000}", h.lastTermin)
	}
}

// TestSequentialMessages confirms multiple messages each produce a
// response, the SDK assigns increasing sequence numbers, and OnCreate is
// invoked once for the whole session regardless of message count. The
// session has exactly one execution, so the SDK does not re-invoke
// OnCreate between messages.
// spec: 6.71 (TaskID frozen to a single OnCreate)
func TestSequentialMessages(t *testing.T) {
	var seqs []uint64
	h := &echoHandler{onMessageFn: func(_ context.Context, m Message) {
		seqs = append(seqs, m.Sequence)
	}}
	frames := runSDK(t, h, []string{
		`{"type":"message","id":"m1","input":[{"type":"text","inline":"one"}]}`,
		`{"type":"message","id":"m2","input":[{"type":"text","inline":"two"}]}`,
		`{"type":"message","id":"m3","input":[{"type":"text","inline":"three"}]}`,
	})
	if len(frames) != 3 {
		t.Fatalf("got %d response frames, want 3", len(frames))
	}
	if len(seqs) != 3 || seqs[0] == 0 {
		t.Fatalf("sequence numbers = %v, want three increasing non-zero values", seqs)
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("sequence not increasing: %v", seqs)
		}
	}
	// §6.71: the session has one execution, so OnCreate fires once for
	// the whole session regardless of how many messages arrive.
	if h.created != 1 {
		t.Fatalf("OnCreate called %d times across three messages, want 1", h.created)
	}
}

// TestHandlerErrorReportedAsResponseError confirms an OnMessage error
// becomes a structured response error (§15.4.1 error via response).
func TestHandlerErrorReportedAsResponseError(t *testing.T) {
	h := &echoHandler{replyErr: errors.New("boom")}
	frames := runSDK(t, h, []string{
		`{"type":"message","id":"m1","input":[{"type":"text","inline":"x"}]}`,
	})
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	errObj, ok := frames[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("response carries no error object: %v", frames[0])
	}
	if errObj["code"] != "RUNTIME_ERROR" {
		t.Fatalf("error code = %v, want RUNTIME_ERROR", errObj["code"])
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
	h := &echoHandler{replyParts: func([]OutputPart) []OutputPart {
		return []OutputPart{Text("hello")}
	}}
	frames := runSDK(t, h, []string{
		`{"type":"message","id":"m1","input":[{"type":"text","inline":"x"}]}`,
	})
	out := frames[0]["output"].([]any)
	part := out[0].(map[string]any)
	if part["type"] != "text" || part["inline"] != "hello" {
		t.Fatalf("text part = %v, want {text hello}", part)
	}
}

// TestMessageEnvelopeAnnotationsRoundTrip asserts the §15.4.1 wire
// MessageEnvelope carries the §15.5 line 2461 degradation-annotation
// map verbatim. A runtime author reading
// `env.Annotations[degradation.AnnotationSchemaVersionAhead]` must
// see the producer's `{knownVersion, encounteredVersion}` body without
// custom decoding.
//
// spec: §15.5 line 2461. F-15.5.5.
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
		t.Fatal("Annotations is nil; spec §15.5 line 2461 catalog dropped")
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
// spec: §15.4.1 / §15.5 line 2461. F-15.5.5.
func TestMessageEnvelopeAnnotationsOmitEmpty_spec_15_4_1(t *testing.T) {
	env := MessageEnvelope{Type: "message", ID: "m1"}
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "annotations") {
		t.Errorf("annotations rendered when empty: %s", out)
	}
}
