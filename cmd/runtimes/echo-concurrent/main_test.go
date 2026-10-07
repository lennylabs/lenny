// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

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
	StartID   string `json:"startId"`
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
	in := sessionStart("sess-01", "st_1") + sessionStart("sess-02", "st_2") +
		message("sess-01", "a1") +
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
	in := sessionStart("sess-01", "st_1") + message("sess-01", "before") +
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
	in := sessionStart("sess-01", "st_1") +
		`{"type":"message","id":"m1","sessionId":"sess-01","input":"not-an-array"}` + "\n"
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
	s := &slotWriter{worker: &slotWorker{sessionID: "sess-01"}}
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

// sessionStart builds a session_start frame for sessionID with startID.
func sessionStart(sessionID, startID string) string {
	b, _ := json.Marshal(map[string]any{"type": "session_start", "sessionId": sessionID, "startId": startID})
	return string(b) + "\n"
}

// sessionEnd builds a session_end frame for sessionID.
func sessionEnd(sessionID string) string {
	b, _ := json.Marshal(map[string]any{"type": "session_end", "sessionId": sessionID})
	return string(b) + "\n"
}

// acks returns the session_started frames among frames, in order.
func acks(frames []outFrame) []outFrame {
	var out []outFrame
	for _, f := range frames {
		if f.Type == "session_started" {
			out = append(out, f)
		}
	}
	return out
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_start, Outbound: session_started)
//
// Each session_start is answered with a session_started echoing its
// sessionId and startId, including a repeated start for a session the
// runtime already holds. The repeated start keeps the session's worker,
// so the session's sequence counter continues across it.
func TestSessionStartIsAcknowledgedAndKeepsTheWorker_spec_28_5_3(t *testing.T) {
	frames := drive(t, sessionStart("sess-01", "st_1")+
		message("sess-01", "a1")+
		sessionStart("sess-01", "st_2")+
		message("sess-01", "a2")+
		sessionStart("sess-02", "st_3"))

	got := acks(frames)
	want := []outFrame{{Type: "session_started", SessionID: "sess-01", StartID: "st_1"}, {Type: "session_started", SessionID: "sess-01", StartID: "st_2"}, {Type: "session_started", SessionID: "sess-02", StartID: "st_3"}}
	if len(got) != len(want) {
		t.Fatalf("session_started frames = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].SessionID != want[i].SessionID || got[i].StartID != want[i].StartID {
			t.Fatalf("session_started[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	responses := responsesOnly(frames)
	if len(responses) != 2 || !strings.Contains(inline(responses[1]), "[echo seq=2]") {
		t.Fatalf("responses = %+v, want the repeated start to keep sess-01's worker (second response seq=2)", responses)
	}
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_end), 4.7.10 (Runtime process
// lifetime)
//
// session_end releases the session's worker, so a later session_start for
// the same session builds a fresh one whose sequence restarts at 1, and a
// session_end for a session the runtime does not hold is ignored. The
// process keeps serving: a later heartbeat is still answered.
func TestSessionEndReleasesTheWorker_spec_28_5_3(t *testing.T) {
	frames := drive(t, sessionStart("sess-01", "st_1")+
		message("sess-01", "a1")+
		sessionEnd("sess-01")+
		sessionEnd("sess-unknown")+
		sessionStart("sess-01", "st_2")+
		message("sess-01", "a2")+
		`{"type":"heartbeat","ts":1}`+"\n")

	// The a1 response may be dropped: session_end follows a1 with no wait,
	// and a response the worker produces after session_end was dispatched
	// is never written. The a2 response is always written.
	responses := responsesOnly(frames)
	if len(responses) == 0 {
		t.Fatal("no responses, want at least the a2 echo")
	}
	if got := inline(responses[len(responses)-1]); !strings.Contains(got, "[echo seq=1]") || !strings.Contains(got, "a2") {
		t.Fatalf("response after session_end and a new session_start = %q, want a fresh worker's seq=1 echo of a2", got)
	}
	var sawAck bool
	for _, f := range frames {
		sawAck = sawAck || f.Type == "heartbeat_ack"
	}
	if !sawAck {
		t.Fatal("no heartbeat_ack after session_end: the process stopped serving")
	}
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_start)
//
// A session boundary frame carrying no sessionId names no session, so it
// is a protocol error like any other unaddressed session-scoped frame, and
// a malformed session_start body is one too.
func TestUnaddressedOrMalformedSessionFrameIsAProtocolError_spec_28_5_3(t *testing.T) {
	for _, in := range []string{
		`{"type":"session_start","startId":"st_1"}` + "\n",
		`{"type":"session_end"}` + "\n",
		`{"type":"session_start","sessionId":"sess-01","startId":7}` + "\n",
	} {
		var out bytes.Buffer
		err := run(context.Background(), strings.NewReader(in), &out, io.Discard)
		var pe protocolError
		if !errors.As(err, &pe) {
			t.Fatalf("run(%q) err = %v, want a protocol error", in, err)
		}
	}
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_end)
//
// After the runtime reads a session's session_end it writes no frame
// addressed to that session. A message delivered immediately before
// session_end can still be in the session's echocore buffer when the
// front loop dispatches session_end, so its response is produced while
// the worker drains. The write gate holds every frame the worker produces
// until session_end has been dispatched (the session is gone from the
// worker map), which is the late-write ordering; the response must then be
// dropped. A second session served after the end confirms the runtime
// keeps writing for the sessions it still holds.
func TestNoFrameForASessionAfterItsSessionEnd_spec_28_5_3(t *testing.T) {
	var out lockedBuffer
	d := newDemux(context.Background(), &out, io.Discard)
	d.sessionWriteGate = func(sessionID string) {
		if sessionID != "sess-01" {
			return
		}
		waitSessionReleased(t, d, sessionID)
	}

	for _, f := range []string{
		sessionStart("sess-01", "st_1"),
		message("sess-01", "a1"),
		sessionEnd("sess-01"),
		sessionStart("sess-02", "st_2"),
		message("sess-02", "b1"),
	} {
		var env struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
		}
		line := []byte(strings.TrimSuffix(f, "\n"))
		if err := json.Unmarshal(line, &env); err != nil {
			t.Fatalf("decode %q: %v", f, err)
		}
		if err := d.dispatch(env.Type, env.SessionID, line); err != nil {
			t.Fatalf("dispatch %s: %v", env.Type, err)
		}
	}
	if err := d.closeAll(); err != nil {
		t.Fatalf("closeAll: %v", err)
	}

	frames := decodeFrames(t, out.String())
	var sawB1 bool
	for _, f := range frames {
		if f.SessionID == "sess-01" && f.Type != "session_started" {
			t.Fatalf("frame %+v addressed to sess-01 was written after its session_end", f)
		}
		sawB1 = sawB1 || (f.SessionID == "sess-02" && strings.Contains(inline(f), "b1"))
	}
	if !sawB1 {
		t.Fatalf("frames = %+v, want sess-02's b1 echo after sess-01 ended", frames)
	}
}

// waitSessionReleased blocks until endSession has removed sessionID from
// the worker map, or fails the test after a bound.
func waitSessionReleased(t *testing.T, d *demux, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		_, held := d.slots[sessionID]
		d.mu.Unlock()
		if !held {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("session %q was never released by session_end", sessionID)
}

// lockedBuffer is a bytes.Buffer safe for the concurrent reads the test
// makes while workers write.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// outError is the error field of an outbound response frame.
type outError struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Error     *struct {
		Code string `json:"code"`
	} `json:"error"`
}

// driveRaw runs the dispatch loop over input, followed by a heartbeat and
// a shutdown, and returns the raw outbound JSONL and the diagnostics the
// runtime wrote to stderr.
func driveRaw(t *testing.T, input string) (out, stderr string) {
	t.Helper()
	var o, e bytes.Buffer
	in := input + `{"type":"heartbeat","ts":1}` + "\n" + `{"type":"shutdown","reason":"drain","deadline_ms":1}` + "\n"
	if err := run(context.Background(), strings.NewReader(in), &o, &e); err != nil {
		t.Fatalf("run: %v", err)
	}
	return o.String(), e.String()
}

// spec: 28.5.3 (CH-MSGSOCK Session errors)
//
// A message for a session whose session_start the runtime never read is
// answered with a response carrying error for that sessionId, and the
// runtime keeps serving. The message must not create a session context, so
// it gets no echo, and a later message for the same session is rejected
// the same way.
func TestMessageWithoutSessionStartIsAnsweredWithAnError_spec_28_5_3(t *testing.T) {
	out, _ := driveRaw(t, message("sess-09", "x1")+message("sess-09", "x2"))
	var errs int
	var sawAck bool
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var f outError
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		switch f.Type {
		case "heartbeat_ack":
			sawAck = true
		case "response":
			if f.SessionID != "sess-09" || f.Error == nil || f.Error.Code != "RUNTIME_ERROR" {
				t.Fatalf("response %q, want a RUNTIME_ERROR response for sess-09", line)
			}
			errs++
		default:
			t.Fatalf("unexpected frame %q for a session that was never started", line)
		}
	}
	if errs != 2 {
		t.Fatalf("got %d error responses, want one per message (2): %s", errs, out)
	}
	if !sawAck {
		t.Fatal("no heartbeat_ack after the rejected messages: the runtime stopped serving")
	}
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_end rule 2, Session errors)
//
// After session_end released a session, a message or a tool_result for it
// does not recreate the session's context and is not answered, because
// after session_end the runtime writes no frame addressed to the session.
// The runtime keeps serving and records a diagnostic. A session_start for
// the same session reopens it, and a message after that gets a fresh
// worker's echo.
func TestMessageAfterSessionEndDoesNotReopenTheSession_spec_28_5_3(t *testing.T) {
	out, stderr := driveRaw(t, sessionStart("sess-01", "st_1")+
		sessionEnd("sess-01")+
		message("sess-01", "late")+
		`{"type":"tool_result","sessionId":"sess-01","id":"tc_1","content":[]}`+"\n")
	for _, f := range decodeFrames(t, out) {
		if f.SessionID == "sess-01" && f.Type != "session_started" {
			t.Fatalf("frame %+v addressed to sess-01 after its session_end: the late frame reopened the session", f)
		}
	}
	if !strings.Contains(stderr, "already ended") {
		t.Fatalf("stderr %q, want a diagnostic for the frame dropped after session_end", stderr)
	}

	frames := drive(t, sessionStart("sess-01", "st_1")+
		sessionEnd("sess-01")+
		message("sess-01", "late")+
		sessionStart("sess-01", "st_2")+
		message("sess-01", "again"))
	responses := responsesOnly(frames)
	if len(responses) != 1 || !strings.Contains(inline(responses[0]), "[echo seq=1]") || !strings.Contains(inline(responses[0]), "again") {
		t.Fatalf("responses = %+v, want only the reopened session's seq=1 echo of again", responses)
	}
}

// spec: 28.5.3 (CH-MSGSOCK Session errors)
//
// A tool_result for a session the runtime never started has no response
// to carry an error, so it is dropped with a diagnostic and the runtime
// keeps serving.
func TestToolResultForAnUnheldSessionIsDropped_spec_28_5_3(t *testing.T) {
	out, stderr := driveRaw(t, `{"type":"tool_result","sessionId":"sess-09","id":"tc_1","content":[]}`+"\n")
	frames := decodeFrames(t, out)
	if len(frames) != 1 || frames[0].Type != "heartbeat_ack" {
		t.Fatalf("frames = %+v, want only the heartbeat_ack", frames)
	}
	if !strings.Contains(stderr, "does not hold") {
		t.Fatalf("stderr %q, want a diagnostic for the dropped tool_result", stderr)
	}
}

// TestHeartbeatSilenceDirectiveStopsThePodsAcks asserts that once any
// session sends the echocore silence directive, the pod answers no further
// heartbeat. Heartbeats are pod-global, so every slot's stream reaches the
// adapter's ack deadline together, as when the pod's one runtime hangs.
// spec: §28.5.3 (CH-MSGSOCK Timing.); §5.2.
func TestHeartbeatSilenceDirectiveStopsThePodsAcks(t *testing.T) {
	in := `{"type":"heartbeat","ts":1}` + "\n" +
		`{"type":"session_start","sessionId":"sess-01","startId":"st-1"}` + "\n" +
		message("sess-01", echocore.HeartbeatSilenceDirective) +
		`{"type":"heartbeat","ts":2}` + "\n" +
		`{"type":"heartbeat","ts":3}` + "\n"
	acks := 0
	for _, f := range drive(t, in) {
		if f.Type == "heartbeat_ack" {
			acks++
		}
	}
	if acks != 1 {
		t.Fatalf("got %d heartbeat_ack frames, want only the one before the directive", acks)
	}
}
