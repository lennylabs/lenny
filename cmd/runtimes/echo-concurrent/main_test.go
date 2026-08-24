// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
)

// outFrame is the subset of an outbound JSONL frame the echo-concurrent
// tests assert on: the discriminator, the sessionId the front loop
// stamps, and the echoed text parts. The §28.5.3 outbound schema carries
// sessionId alone for multiplexing, so the tests assert on sessionId and
// never on a cwd wire field (the per-session cwd is an internal
// derivation covered by TestSessionCwdDerivation).
type outFrame struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Output    []struct {
		Inline string `json:"inline"`
	} `json:"output"`
}

// drive runs the sessionId dispatch loop over input and returns the
// decoded outbound frames. A trailing shutdown is appended so the loop
// drains every session deterministically before returning.
func drive(t *testing.T, input string) []outFrame {
	t.Helper()
	var out bytes.Buffer
	in := input + `{"type":"shutdown","reason":"session_complete","deadline_ms":1}` + "\n"
	if err := run(context.Background(), strings.NewReader(in), &out, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	return decodeFrames(t, out.String())
}

func decodeFrames(t *testing.T, raw string) []outFrame {
	t.Helper()
	var frames []outFrame
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if line == "" {
			continue
		}
		var f outFrame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("decode frame %q: %v", line, err)
		}
		frames = append(frames, f)
	}
	return frames
}

// message builds a `message` JSONL frame carrying the session address the
// adapter stamps and a single inline text part. An empty sessionID builds
// the unaddressed frame §28.5.3 makes a protocol error on this leg.
func message(sessionID, text string) string {
	m := map[string]any{
		"type":  "message",
		"id":    "m_" + text,
		"input": []map[string]any{{"type": "text", "inline": text}},
	}
	if sessionID != "" {
		m["sessionId"] = sessionID
	}
	b, _ := json.Marshal(m)
	return string(b) + "\n"
}

// TestDemultiplexesTwoSessionsWithIsolatedSequences asserts the front loop
// routes frames to per-session echocore loops keyed on sessionId, each
// with its own sequence counter, and stamps the originating sessionId onto
// every response. This is the core §28.5.3 dispatch loop the
// concurrent-workspace pod depends on. sessionId is the only field the
// §28.5.3 outbound schema adds for multiplexing; the per-session cwd
// derivation is asserted directly in TestSessionCwdDerivation rather than
// read off the wire.
// spec: §5.2; §6.4; §28.5.3.
func TestDemultiplexesTwoSessionsWithIsolatedSequences(t *testing.T) {
	// Interleave two sessions: sess-01 gets two messages, sess-02 one.
	// Each session's sequence counter is independent, so sess-01's second
	// response is seq=2 while sess-02's only response is seq=1.
	in := message("sess-01", "a1") +
		message("sess-02", "b1") +
		message("sess-01", "a2")
	frames := responsesOnly(drive(t, in))

	bySession := map[string][]outFrame{}
	for _, f := range frames {
		bySession[f.SessionID] = append(bySession[f.SessionID], f)
	}
	if len(bySession["sess-01"]) != 2 {
		t.Fatalf("sess-01 got %d responses, want 2: %+v", len(bySession["sess-01"]), bySession["sess-01"])
	}
	if len(bySession["sess-02"]) != 1 {
		t.Fatalf("sess-02 got %d responses, want 1: %+v", len(bySession["sess-02"]), bySession["sess-02"])
	}

	// Per-session cwd derivation (§6.4) is an internal filesystem
	// derivation the runtime never emits on the wire; it is asserted
	// directly in TestSessionCwdDerivation. Here, confirm the dispatch
	// tagged each response with the session whose cwd the runtime derives.
	for sessionID := range bySession {
		if want := slotCwd(sessionID); want != "/workspace/slots/"+sessionID+"/current/" {
			t.Errorf("session %q cwd = %q, want the per-slot path", sessionID, want)
		}
	}

	// Independent per-session sequence counters: sess-01's responses are
	// seq=1 then seq=2; sess-02's single response is seq=1, not seq=3,
	// proving the counters are not shared across sessions.
	if got := inline(bySession["sess-01"][0]); !strings.Contains(got, "[echo seq=1]") || !strings.Contains(got, "a1") {
		t.Errorf("sess-01 first response = %q, want seq=1 echo of a1", got)
	}
	if got := inline(bySession["sess-01"][1]); !strings.Contains(got, "[echo seq=2]") || !strings.Contains(got, "a2") {
		t.Errorf("sess-01 second response = %q, want seq=2 echo of a2", got)
	}
	if got := inline(bySession["sess-02"][0]); !strings.Contains(got, "[echo seq=1]") || !strings.Contains(got, "b1") {
		t.Errorf("sess-02 response = %q, want an independent seq=1 echo of b1", got)
	}
}

// TestUnaddressedSessionScopedFrameIsAProtocolError asserts a
// session-scoped frame carrying no sessionId names no session the runtime
// may act for. The adapter populates the identifier on every pod, so the
// front loop fails closed with the §15.4 protocol-error rather than
// routing the frame to a pod-global default session, which is the path
// §28.5.3 retires.
// spec: §15.4; §28.5.3.
func TestUnaddressedSessionScopedFrameIsAProtocolError(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), strings.NewReader(message("", "solo")), &out, io.Discard)
	if err == nil {
		t.Fatal("a session-scoped frame carrying no sessionId must fail the runtime")
	}
	var pe protocolError
	if !errors.As(err, &pe) {
		t.Errorf("error %T must convert to protocolError so the entrypoint exits with code 2", err)
	}
	if !strings.Contains(err.Error(), "sessionId") {
		t.Errorf("error %q must name the missing address", err.Error())
	}
	for _, f := range responsesOnly(decodeFrames(t, out.String())) {
		t.Errorf("an unaddressed frame produced a response %+v; it must be answered by no session", f)
	}
}

// TestUnaddressedFrameOutsideTheSessionScopedSetIsTolerated asserts the
// addressing rule is scoped to the session-scoped inbound set. A frame of
// an unknown type carries no per-session identifier and names no session,
// but it sits outside that set, so §15.4's unknown-type tolerance governs
// it: the runtime drops it with a diagnostic and keeps serving. Failing
// the runtime on it would make every forward-compatible frame type fatal
// and would leave the following heartbeat unanswered.
// spec: §15.4; §28.5.3.
func TestUnaddressedFrameOutsideTheSessionScopedSetIsTolerated(t *testing.T) {
	var stderr bytes.Buffer
	var out bytes.Buffer
	in := `{"type":"this_is_a_future_message_type","x":1}` + "\n" +
		`{"type":"heartbeat","ts":2}` + "\n" +
		`{"type":"shutdown","reason":"session_complete","deadline_ms":1}` + "\n"
	if err := run(context.Background(), strings.NewReader(in), &out, &stderr); err != nil {
		t.Fatalf("an unaddressed frame outside the session-scoped set must not fail the runtime: %v", err)
	}
	frames := decodeFrames(t, out.String())
	var acks int
	for _, f := range frames {
		if f.Type == "heartbeat_ack" {
			acks++
		}
		if f.Type == "response" {
			t.Errorf("an unknown frame type produced a response %+v; it must be answered by no session", f)
		}
	}
	if acks != 1 {
		t.Fatalf("got %d heartbeat_ack frames after the unknown type, want 1: %+v", acks, frames)
	}
	if !strings.Contains(stderr.String(), "this_is_a_future_message_type") {
		t.Errorf("stderr %q must name the dropped frame type", stderr.String())
	}
}

// TestHeartbeatAckIsPodGlobalAndUnaddressed asserts an unaddressed
// heartbeat is answered once, with a heartbeat_ack carrying no per-session
// identifier. heartbeat and heartbeat_ack are protocol-level and sit
// outside the addressing rule, so the pod answers a heartbeat that names
// no session rather than failing closed on it.
// spec: §28.5.3.
func TestHeartbeatAckIsPodGlobalAndUnaddressed(t *testing.T) {
	in := `{"type":"heartbeat","ts":1}` + "\n"
	frames := drive(t, in)
	var acks int
	for _, f := range frames {
		if f.Type != "heartbeat_ack" {
			continue
		}
		acks++
		if f.SessionID != "" {
			t.Errorf("heartbeat_ack carried sessionId=%q, want none", f.SessionID)
		}
	}
	if acks != 1 {
		t.Fatalf("got %d heartbeat_ack frames, want 1: %+v", acks, frames)
	}
}

// TestShutdownEndsEverySlot asserts a pod-level shutdown frame ends the
// loop and is not echoed: a trailing message after shutdown produces no
// further output. spec: §28.5.3.
func TestShutdownEndsEverySlot(t *testing.T) {
	var out bytes.Buffer
	in := message("sess-01", "before") +
		`{"type":"shutdown","reason":"drain","deadline_ms":1}` + "\n" +
		message("sess-01", "after")
	if err := run(context.Background(), strings.NewReader(in), &out, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, f := range responsesOnly(decodeFrames(t, out.String())) {
		if strings.Contains(inline(f), "after") {
			t.Errorf("shutdown must end the loop; got a post-shutdown response %q", inline(f))
		}
	}
}

// TestMalformedFrameIsAProtocolError asserts a malformed inbound frame on
// the front loop is a protocol error the entrypoint maps to exit code 2.
// spec: §15.4 (protocol-error exit code 2 for unrecoverable inbound JSONL).
func TestMalformedFrameIsAProtocolError(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), strings.NewReader("not json\n"), &out, io.Discard)
	if err == nil {
		t.Fatal("malformed input must be a protocol error")
	}
	var pe protocolError
	if !errors.As(err, &pe) {
		t.Errorf("error %v must be a protocolError so the entrypoint sets exit code 2", err)
	}
}

// TestEmptyInputExitsCleanly asserts EOF on empty input is a clean exit
// with no error. spec: §28.5.3 (inbound EOF exits cleanly).
func TestEmptyInputExitsCleanly(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), strings.NewReader(""), &out, io.Discard); err != nil {
		t.Errorf("EOF on empty input must be a clean exit, got %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("empty input produced output %q, want none", out.String())
	}
}

// TestSessionCwdDerivation asserts the per-session cwd derivation: the
// session identifier yields /workspace/slots/{sessionId}/current/. Every
// session is bound to a slot on every pod, so there is one layout and no
// pod-global /workspace/current alternative.
// spec: §6.4; §28.5.3.
func TestSessionCwdDerivation(t *testing.T) {
	if got := slotCwd("sess-7"); got != "/workspace/slots/sess-7/current/" {
		t.Errorf("slotCwd(sess-7) = %q, want the per-slot path", got)
	}
}

// TestPerSlotProtocolErrorFailsTheRuntime asserts a malformed message
// body on a slot fails the whole runtime rather than silently wedging the
// slot: the per-slot echocore loop returns a ProtocolError, the front loop
// surfaces it through the slot drain, and run returns a protocolError the
// entrypoint maps to exit code 2. Failing closed prevents a slot from
// hanging a concurrent pod.
// spec: §15.4 (protocol-error exit code 2), §5.2.
func TestPerSlotProtocolErrorFailsTheRuntime(t *testing.T) {
	// A frame whose `input` is a string, not a MessagePart array: the front
	// loop accepts it (it reads only type and sessionId) but echocore's
	// handleMessage rejects the body.
	in := `{"type":"message","id":"m1","sessionId":"sess-01","input":"not-an-array"}` + "\n"
	var out bytes.Buffer
	err := run(context.Background(), strings.NewReader(in), &out, io.Discard)
	if err == nil {
		t.Fatal("a malformed per-slot message body must fail the runtime")
	}
	if !strings.Contains(err.Error(), "sess-01") {
		t.Errorf("error %q must name the offending session", err.Error())
	}
	// The per-slot ProtocolError must surface as the package-local
	// protocolError so the entrypoint maps it to the §15.4 protocol-error
	// exit code (2). Asserting errors.As here pins the exit-code-2 contract:
	// a chain that wrapped only echocore.ProtocolError would fail this match
	// and the runtime would exit with the runtime-error code (1) instead.
	var pe protocolError
	if !errors.As(err, &pe) {
		t.Errorf("error %T must convert to protocolError so the entrypoint exits with code 2", err)
	}
}

// TestProtocolErrorMessage asserts the front-loop protocolError renders
// the §15.4 protocol-error prefix so a diagnosis line is legible.
// spec: §15.4 (protocol error).
func TestProtocolErrorMessage(t *testing.T) {
	e := protocolError{msg: "bad frame"}
	if got := e.Error(); got != "protocol error: bad frame" {
		t.Errorf("Error() = %q, want the protocol-error prefix", got)
	}
}

// TestStampLeavesNonObjectFrameUnchanged asserts the slot writer forwards
// a non-object outbound frame verbatim, so a future non-object frame on a
// slot is not dropped or corrupted by the stamping path.
func TestStampLeavesNonObjectFrameUnchanged(t *testing.T) {
	s := &slotWriter{sessionID: "sess-01"}
	got, err := s.stamp([]byte("[]"))
	if err != nil {
		t.Fatalf("stamp non-object frame: %v", err)
	}
	if string(got) != "[]" {
		t.Errorf("non-object frame = %q, want it forwarded unchanged", got)
	}
}

// TestWriteFrameAppendsMissingNewline asserts writeFrame terminates a
// frame that lacks a trailing newline, so per-slot frames stay
// newline-delimited on the shared transport regardless of how a worker
// produced them.
func TestWriteFrameAppendsMissingNewline(t *testing.T) {
	var out bytes.Buffer
	d := newDemux(context.Background(), &out, io.Discard)
	if err := d.writeFrame([]byte(`{"type":"response"}`)); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	if got := out.String(); got != `{"type":"response"}`+"\n" {
		t.Errorf("writeFrame output = %q, want a trailing newline appended", got)
	}
}

// TestWriteFrameSurfacesTransportError asserts a transport write error on
// a slot's outbound frame is surfaced rather than swallowed, so a broken
// connection fails the slot's echocore loop and ultimately the runtime.
func TestWriteFrameSurfacesTransportError(t *testing.T) {
	d := newDemux(context.Background(), errWriter{}, io.Discard)
	if err := d.writeFrame([]byte(`{"type":"response"}`)); err == nil {
		t.Fatal("a transport write error must surface from writeFrame")
	}
}

// TestSlotErrorMapsExitCodes asserts slotError routes a per-slot failure to
// the right §15.4 exit code: a per-slot echocore.ProtocolError converts to
// the package-local protocolError (exit code 2) while any other per-slot
// failure keeps a plain wrapped chain (exit code 1). The entrypoint selects
// the exit code with errors.As(err, &protocolError{}), so both branches are
// pinned through that match. Failing closed on a malformed slot frame keeps
// a single slot from wedging a concurrent pod.
// spec: §15.4 (protocol-error exit code 2 vs runtime-error exit code 1).
func TestSlotErrorMapsExitCodes(t *testing.T) {
	protoErr := slotError("sess-01", echocore.ProtocolError{Msg: "bad body"})
	var pe protocolError
	if !errors.As(protoErr, &pe) {
		t.Errorf("a per-slot ProtocolError must convert to protocolError (exit code 2), got %T", protoErr)
	}
	if !strings.Contains(protoErr.Error(), "sess-01") {
		t.Errorf("error %q must name the offending session", protoErr.Error())
	}

	runErr := slotError("sess-02", io.ErrClosedPipe)
	if errors.As(runErr, &pe) {
		t.Error("a non-protocol per-slot failure must not convert to protocolError; it maps to exit code 1")
	}
	if !errors.Is(runErr, io.ErrClosedPipe) {
		t.Errorf("a non-protocol per-slot failure must preserve its wrapped chain, got %v", runErr)
	}
	if !strings.Contains(runErr.Error(), "sess-02") {
		t.Errorf("error %q must name the offending session", runErr.Error())
	}
}

// errWriter fails every write, standing in for a broken transport.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// responsesOnly keeps only `response` frames, dropping heartbeat_ack and
// any control frames the loop emits.
func responsesOnly(frames []outFrame) []outFrame {
	var out []outFrame
	for _, f := range frames {
		if f.Type == "response" {
			out = append(out, f)
		}
	}
	return out
}

func inline(f outFrame) string {
	var b strings.Builder
	for _, p := range f.Output {
		b.WriteString(p.Inline)
	}
	return b.String()
}
