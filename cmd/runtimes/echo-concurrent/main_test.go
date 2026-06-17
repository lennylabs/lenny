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
// tests assert on: the discriminator, the slotId the front loop stamps,
// and the echoed text parts. The §15.4.1 outbound schema carries slotId
// alone for concurrent multiplexing, so the tests assert on slotId and
// never on a cwd wire field (the per-slot cwd is an internal derivation
// covered by TestSlotCwdDerivation).
type outFrame struct {
	Type   string `json:"type"`
	SlotID string `json:"slotId"`
	Output []struct {
		Inline string `json:"inline"`
	} `json:"output"`
}

// drive runs the slotId dispatch loop over input and returns the decoded
// outbound frames. A trailing shutdown is appended so the loop drains
// every slot deterministically before returning.
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

// message builds a `message` JSONL frame carrying an optional slotId and a
// single inline text part.
func message(slotID, text string) string {
	m := map[string]any{
		"type":  "message",
		"id":    "m_" + text,
		"input": []map[string]any{{"type": "text", "inline": text}},
	}
	if slotID != "" {
		m["slotId"] = slotID
	}
	b, _ := json.Marshal(m)
	return string(b) + "\n"
}

// TestDemultiplexesTwoSlotsWithIsolatedSequences asserts the front loop
// routes frames to per-slot echocore loops keyed on slotId, each with its
// own sequence counter, and stamps the originating slotId onto every
// response. This is the core §15.4.1 dispatch loop the concurrent-workspace
// pod depends on. slotId is the only field the §15.4.1 outbound schema tags
// for concurrent multiplexing; the per-slot cwd derivation is asserted
// directly in TestSlotCwdDerivation rather than read off the wire.
// spec: §5.2 line 509 (slotId multiplexing, dispatch loop keyed on slotId),
// §15.4.1 line 1459 (single stdin channel), §6.4 line 384 (per-slot cwd).
func TestDemultiplexesTwoSlotsWithIsolatedSequences(t *testing.T) {
	// Interleave two slots: slot-01 gets two messages, slot-02 one. Each
	// slot's sequence counter is independent, so slot-01's second response
	// is seq=2 while slot-02's only response is seq=1.
	in := message("slot-01", "a1") +
		message("slot-02", "b1") +
		message("slot-01", "a2")
	frames := responsesOnly(drive(t, in))

	bySlot := map[string][]outFrame{}
	for _, f := range frames {
		bySlot[f.SlotID] = append(bySlot[f.SlotID], f)
	}
	if len(bySlot["slot-01"]) != 2 {
		t.Fatalf("slot-01 got %d responses, want 2: %+v", len(bySlot["slot-01"]), bySlot["slot-01"])
	}
	if len(bySlot["slot-02"]) != 1 {
		t.Fatalf("slot-02 got %d responses, want 1: %+v", len(bySlot["slot-02"]), bySlot["slot-02"])
	}

	// Per-slot cwd derivation (§6.4 line 384) is an internal filesystem
	// derivation the runtime never emits on the wire; it is asserted
	// directly in TestSlotCwdDerivation. Here, confirm the dispatch tagged
	// each response with the slot whose cwd the runtime derives.
	for slotID := range bySlot {
		if slotID == "" {
			continue
		}
		if want := slotCwd(slotID); want != "/workspace/slots/"+slotID+"/current/" {
			t.Errorf("slot %q cwd = %q, want the per-slot path", slotID, want)
		}
	}

	// Independent per-slot sequence counters: slot-01's responses are
	// seq=1 then seq=2; slot-02's single response is seq=1, not seq=3,
	// proving the counters are not shared across slots.
	if got := inline(bySlot["slot-01"][0]); !strings.Contains(got, "[echo seq=1]") || !strings.Contains(got, "a1") {
		t.Errorf("slot-01 first response = %q, want seq=1 echo of a1", got)
	}
	if got := inline(bySlot["slot-01"][1]); !strings.Contains(got, "[echo seq=2]") || !strings.Contains(got, "a2") {
		t.Errorf("slot-01 second response = %q, want seq=2 echo of a2", got)
	}
	if got := inline(bySlot["slot-02"][0]); !strings.Contains(got, "[echo seq=1]") || !strings.Contains(got, "b1") {
		t.Errorf("slot-02 response = %q, want an independent seq=1 echo of b1", got)
	}
}

// TestNoSlotIDFrameServesWholePodSession asserts a frame without a slotId
// takes the single-session whole-pod path: the response carries no slotId,
// so echo-concurrent also serves a maxConcurrentSessions: 1 pod, where
// runtimes never see slotId.
// spec: §15.4.1 line 1459 (maxConcurrentSessions: 1 pods never carry slotId).
func TestNoSlotIDFrameServesWholePodSession(t *testing.T) {
	frames := responsesOnly(drive(t, message("", "solo")))
	if len(frames) != 1 {
		t.Fatalf("got %d responses, want 1: %+v", len(frames), frames)
	}
	if frames[0].SlotID != "" {
		t.Errorf("whole-pod response slotId = %q, want empty (no slotId on a single-session pod)", frames[0].SlotID)
	}
	if got := inline(frames[0]); !strings.Contains(got, "[echo seq=1]") || !strings.Contains(got, "solo") {
		t.Errorf("whole-pod response = %q, want a seq=1 echo of solo", got)
	}
}

// TestHeartbeatAckIsNotSlotStamped asserts a per-slot heartbeat is
// answered with a heartbeat_ack carrying no content payload: the front
// loop does not stamp slotId onto the protocol-level ack, matching the
// §15.4.1 heartbeat_ack schema.
// spec: §15.4.1 line 1453 (heartbeat_ack is protocol-level, no payload).
func TestHeartbeatAckIsNotSlotStamped(t *testing.T) {
	in := `{"type":"heartbeat","ts":1,"slotId":"slot-01"}` + "\n"
	frames := drive(t, in)
	var acks int
	for _, f := range frames {
		if f.Type != "heartbeat_ack" {
			continue
		}
		acks++
		if f.SlotID != "" {
			t.Errorf("heartbeat_ack carried slotId=%q, want none", f.SlotID)
		}
	}
	if acks != 1 {
		t.Fatalf("got %d heartbeat_ack frames, want 1: %+v", acks, frames)
	}
}

// TestShutdownEndsEverySlot asserts a pod-level shutdown frame ends the
// loop and is not echoed: a trailing message after shutdown produces no
// further output. spec: §15.4.1 line 1443 (shutdown is a graceful exit).
func TestShutdownEndsEverySlot(t *testing.T) {
	var out bytes.Buffer
	in := message("slot-01", "before") +
		`{"type":"shutdown","reason":"drain","deadline_ms":1}` + "\n" +
		message("slot-01", "after")
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
// with no error. spec: §15.4.1 (inbound EOF exits cleanly).
func TestEmptyInputExitsCleanly(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), strings.NewReader(""), &out, io.Discard); err != nil {
		t.Errorf("EOF on empty input must be a clean exit, got %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("empty input produced output %q, want none", out.String())
	}
}

// TestSlotCwdDerivation asserts the per-slot cwd derivation: a non-empty
// slotId yields /workspace/slots/{slotId}/current/, and the empty default
// session keeps the global /workspace/current.
// spec: §6.4 line 384 (per-slot cwd; no global /workspace/current when
// maxConcurrentSessions > 1).
func TestSlotCwdDerivation(t *testing.T) {
	if got := slotCwd("slot-7"); got != "/workspace/slots/slot-7/current/" {
		t.Errorf("slotCwd(slot-7) = %q, want the per-slot path", got)
	}
	if got := slotCwd(""); got != "/workspace/current" {
		t.Errorf("slotCwd(\"\") = %q, want the global /workspace/current", got)
	}
}

// TestPerSlotProtocolErrorFailsTheRuntime asserts a malformed message
// body on a slot fails the whole runtime rather than silently wedging the
// slot: the per-slot echocore loop returns a ProtocolError, the front loop
// surfaces it through the slot drain, and run returns a protocolError the
// entrypoint maps to exit code 2. Failing closed prevents a slot from
// hanging a concurrent pod.
// spec: §15.4 (protocol-error exit code 2), §5.2 line 509 (per-slot dispatch).
func TestPerSlotProtocolErrorFailsTheRuntime(t *testing.T) {
	// A frame whose `input` is a string, not an OutputPart array: the front
	// loop accepts it (it reads only type and slotId) but echocore's
	// handleMessage rejects the body.
	in := `{"type":"message","id":"m1","slotId":"slot-01","input":"not-an-array"}` + "\n"
	var out bytes.Buffer
	err := run(context.Background(), strings.NewReader(in), &out, io.Discard)
	if err == nil {
		t.Fatal("a malformed per-slot message body must fail the runtime")
	}
	if !strings.Contains(err.Error(), "slot-01") {
		t.Errorf("error %q must name the offending slot", err.Error())
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
	s := &slotWriter{slotID: "slot-01"}
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
	protoErr := slotError("slot-01", echocore.ProtocolError{Msg: "bad body"})
	var pe protocolError
	if !errors.As(protoErr, &pe) {
		t.Errorf("a per-slot ProtocolError must convert to protocolError (exit code 2), got %T", protoErr)
	}
	if !strings.Contains(protoErr.Error(), "slot-01") {
		t.Errorf("error %q must name the offending slot", protoErr.Error())
	}

	runErr := slotError("slot-02", io.ErrClosedPipe)
	if errors.As(runErr, &pe) {
		t.Error("a non-protocol per-slot failure must not convert to protocolError; it maps to exit code 1")
	}
	if !errors.Is(runErr, io.ErrClosedPipe) {
		t.Errorf("a non-protocol per-slot failure must preserve its wrapped chain, got %v", runErr)
	}
	if !strings.Contains(runErr.Error(), "slot-02") {
		t.Errorf("error %q must name the offending slot", runErr.Error())
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
