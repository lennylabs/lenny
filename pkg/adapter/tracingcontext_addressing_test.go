// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// tracingFrame builds a set_tracing_context frame, tagging it with slotID
// when slotID is non-empty. An untagged frame is what a runtime on a
// single-session pod writes; a tagged one is what the dispatch loop on a
// concurrent pod stamps.
func tracingFrame(slotID string) []byte {
	if slotID == "" {
		return []byte(`{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc"}}`)
	}
	return []byte(`{"type":"set_tracing_context","slotId":"` + slotID +
		`","context":{"langsmith_run_id":"run_abc"}}`)
}

// statusFrame builds a status frame the Attach loop relays as content. It
// is the synchronization point of every case below: the output relay
// handles one frame at a time, so receiving the status frame that follows
// a set_tracing_context frame proves the adapter finished handling that
// frame.
func statusFrame(slotID string) []byte {
	if slotID == "" {
		return []byte(`{"type":"status","state":"thinking"}`)
	}
	return []byte(`{"type":"status","slotId":"` + slotID + `","state":"thinking"}`)
}

// tracingDrops reads the §28.5.3 drop counter.
func tracingDrops() float64 {
	return testutil.ToFloat64(adapter.SetTracingContextDroppedCounter())
}

// openTracingAttach binds an Attach stream to (sessionID, slotID) and
// returns it. An empty slotID binds the pod-global base path.
func openTracingAttach(t *testing.T, client adapterv1.AdapterClient, sessionID, slotID string) adapterv1.Adapter_AttachClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach(%s/%s): %v", sessionID, slotID, err)
	}
	req := &adapterv1.AttachRequest{SessionId: &adapterv1.SessionId{Value: sessionID}}
	if slotID != "" {
		req.SlotId = &adapterv1.SlotId{Value: slotID}
	}
	if err := stream.Send(req); err != nil {
		t.Fatalf("Send bind(%s/%s): %v", sessionID, slotID, err)
	}
	return stream
}

// awaitStatus receives the next relayed frame and requires it to be the
// status frame, which the adapter relays only after it has handled every
// frame written before it.
func awaitStatus(t *testing.T, stream adapterv1.Adapter_AttachClient) {
	t.Helper()
	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if ty := jsonType(t, got.GetEnvelopeJson()); ty != "status" {
		t.Fatalf("relayed frame type = %q, want status (set_tracing_context must never be relayed)", ty)
	}
}

// requireCalls asserts the forwarder saw exactly the expected sessions, in
// order, and nothing else.
func requireCalls(t *testing.T, fwd *fakePlatformForwarder, want ...string) {
	t.Helper()
	n, sessions := fwd.calls()
	if n != len(want) {
		t.Fatalf("CallPlatformTool count = %d (sessions %v), want %d (%v)", n, sessions, len(want), want)
	}
	for i, s := range sessions {
		if s != want[i] {
			t.Errorf("CallPlatformTool[%d] session = %q, want %q", i, s, want[i])
		}
	}
}

// syncBuffer collects log output written from the goroutines that serve
// the pod's Attach streams, which write the drop log concurrently when a
// fanned-out frame is rejected on more than one stream.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
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

// captureDropLogs redirects the standard logger, which the adapter writes
// the drop diagnostic through, to a buffer for the duration of the case.
// The returned accessor yields the protocol-error lines the
// set_tracing_context drop path emitted, ignoring any other line the pod
// wrote while the case ran.
func captureDropLogs(t *testing.T) func() []string {
	t.Helper()
	prev := log.Writer()
	buf := &syncBuffer{}
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return func() []string {
		var lines []string
		for _, line := range strings.Split(buf.String(), "\n") {
			if strings.Contains(line, "protocol error") && strings.Contains(line, "set_tracing_context") {
				lines = append(lines, line)
			}
		}
		return lines
	}
}

// dropLog is one expected drop diagnostic: the slot the frame was tagged
// with, and the (session, slot) address of the Attach stream that dropped
// it. A drop counted by an unlabelled counter is only attributable to a
// misaddressed frame, a wrong-slot stamp, or a teardown-window arrival
// through this line, so the line must name all three fields in that order.
type dropLog struct {
	frameSlot  string
	session    string
	streamSlot string
}

// matches reports whether line names the frame's slot, then the stream's
// session, then the stream's slot.
func (d dropLog) matches(line string) bool {
	frame := strings.Index(line, fmt.Sprintf("slot %q", d.frameSlot))
	session := strings.Index(line, "session "+d.session)
	stream := strings.LastIndex(line, fmt.Sprintf("slot %q", d.streamSlot))
	return frame >= 0 && session > frame && stream > session
}

// requireDropLogs asserts the drop path emitted exactly one protocol-error
// line per expected drop and that each names its frame slot and its
// stream's address.
func requireDropLogs(t *testing.T, logs func() []string, want ...dropLog) {
	t.Helper()
	got := logs()
	if len(got) != len(want) {
		t.Fatalf("set_tracing_context protocol-error log lines = %d %q, want %d", len(got), got, len(want))
	}
	for _, w := range want {
		n := 0
		for _, line := range got {
			if w.matches(line) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%d protocol-error line(s) name frame slot %q on stream (session %s, slot %q), want 1; got %q",
				n, w.frameSlot, w.session, w.streamSlot, got)
		}
	}
}

// requireDrops asserts the drop counter moved by want since before.
func requireDrops(t *testing.T, before, want float64) {
	t.Helper()
	if got := tracingDrops() - before; got != want {
		t.Errorf("set_tracing_context drop counter moved by %v, want %v", got, want)
	}
}

// concurrentTracingPod starts a two-slot concurrent pod with a platform
// forwarder wired and returns the server, its runtime, the forwarder, and
// an adapter client.
func concurrentTracingPod(t *testing.T, slots ...string) (*adapter.Server, *fakeRuntime, *fakePlatformForwarder, adapterv1.AdapterClient) {
	t.Helper()
	s, rt := concurrentServer(t)
	rt.output = make(chan []byte, 16)
	fwd := &fakePlatformForwarder{result: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)}
	s.PlatformForwarder = fwd
	for _, slot := range slots {
		if _, err := s.StartSession(context.Background(), slotStartReq("sess-"+slot, slot)); err != nil {
			t.Fatalf("StartSession(%s): %v", slot, err)
		}
	}
	client, _ := adapterClient(t, s)
	return s, rt, fwd, client
}

// singleSessionTracingPod starts a single-session pod with a platform
// forwarder wired and returns the server, its runtime, the forwarder, and
// an adapter client.
func singleSessionTracingPod(t *testing.T, sessionID string) (*adapter.Server, *fakeRuntime, *fakePlatformForwarder, adapterv1.AdapterClient) {
	t.Helper()
	s, rt, _ := sessionServer(t)
	rt.output = make(chan []byte, 8)
	fwd := &fakePlatformForwarder{result: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)}
	s.PlatformForwarder = fwd
	if _, err := s.StartSession(context.Background(), startReq(sessionID)); err != nil {
		t.Fatalf("StartSession(%s): %v", sessionID, err)
	}
	client, _ := adapterClient(t, s)
	return s, rt, fwd, client
}

// spec: 28.5.3 (set_tracing_context addressing) — a frame tagged with the
// stream's own slot satisfies both conditions, so the adapter registers it
// once, against that slot's session, and counts no drop.
//
// diagnosis: a failure means the addressing rule rejects a correctly
// addressed frame, so a concurrent runtime's tracing identifiers never
// reach the gateway.
func TestSetTracingContextTaggedForOwnSlotRegistersOnce_spec_28_5_3(t *testing.T) {
	_, rt, fwd, client := concurrentTracingPod(t, "slot-a", "slot-b")
	streamA := openTracingAttach(t, client, "sess-slot-a", "slot-a")
	openTracingAttach(t, client, "sess-slot-b", "slot-b")
	rt.waitForSubscribers(t, 2)

	before := tracingDrops()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("slot-a")
	rt.output <- statusFrame("slot-a")
	awaitStatus(t, streamA)

	requireCalls(t, fwd, "sess-slot-a")
	requireDrops(t, before, 0)
	requireDropLogs(t, logs)
}

// spec: 28.5.3 (set_tracing_context addressing) — an untagged frame on a
// concurrent pod addresses no slot-bound stream. Every slot's stream
// receives it through the fan-out and every one of them drops, counts, and
// logs it, so no session's tracing context is written.
//
// diagnosis: a failure means the untagged frame is registering against a
// slot's session again, which merges one runtime's tracing identifiers
// into every sibling slot's delegation lease.
func TestSetTracingContextUntaggedOnConcurrentPodIsDropped_spec_28_5_3(t *testing.T) {
	_, rt, fwd, client := concurrentTracingPod(t, "slot-a", "slot-b")
	streamA := openTracingAttach(t, client, "sess-slot-a", "slot-a")
	streamB := openTracingAttach(t, client, "sess-slot-b", "slot-b")
	rt.waitForSubscribers(t, 2)

	before := tracingDrops()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("")
	rt.output <- statusFrame("slot-a")
	rt.output <- statusFrame("slot-b")
	awaitStatus(t, streamA)
	awaitStatus(t, streamB)

	requireCalls(t, fwd)
	// Both slot streams see the untagged frame, so both reject it.
	requireDrops(t, before, 2)
	requireDropLogs(t, logs,
		dropLog{frameSlot: "", session: "sess-slot-a", streamSlot: "slot-a"},
		dropLog{frameSlot: "", session: "sess-slot-b", streamSlot: "slot-b"})
}

// spec: 28.5.3 (set_tracing_context addressing) — a frame tagged for a
// sibling slot never reaches this stream's handler: the per-slot
// demultiplexer drops it first, so the stream registers nothing and the
// addressing counter does not move.
//
// diagnosis: a failure means a slot's Attach stream handled a frame
// addressed to a sibling slot.
func TestSetTracingContextTaggedForSiblingSlotNeverReachesStream_spec_28_5_3(t *testing.T) {
	_, rt, fwd, client := concurrentTracingPod(t, "slot-a", "slot-b")
	streamA := openTracingAttach(t, client, "sess-slot-a", "slot-a")
	rt.waitForSubscribers(t, 1)

	before := tracingDrops()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("slot-b")
	rt.output <- statusFrame("slot-a")
	awaitStatus(t, streamA)

	requireCalls(t, fwd)
	requireDrops(t, before, 0)
	requireDropLogs(t, logs)
}

// spec: 28.5.3 (set_tracing_context addressing) — on a single-session pod
// the stream carries no slot, so an untagged frame satisfies address
// equality, the pod's session is still the stream's session, and the slot
// registry is empty. The adapter registers it once.
//
// diagnosis: a failure means the addressing rule rejects the frame a
// conforming single-session runtime writes.
func TestSetTracingContextUntaggedOnSingleSessionPodRegisters_spec_28_5_3(t *testing.T) {
	_, rt, fwd, client := singleSessionTracingPod(t, "sess-solo")
	stream := openTracingAttach(t, client, "sess-solo", "")
	rt.waitForSubscribers(t, 1)

	before := tracingDrops()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("")
	rt.output <- statusFrame("")
	awaitStatus(t, stream)

	requireCalls(t, fwd, "sess-solo")
	requireDrops(t, before, 0)
	requireDropLogs(t, logs)
}

// spec: 28.5.3 (set_tracing_context addressing) — no session on a
// single-session pod holds a slot id and the adapter stamps one only on
// concurrent slots, so a frame carrying any slotId there fails address
// equality and is dropped, counted, and logged.
//
// diagnosis: a failure means the adapter accepts a slot-tagged frame on a
// stream that holds no slot, which is the case address equality exists to
// reject.
func TestSetTracingContextTaggedOnSingleSessionPodIsDropped_spec_28_5_3(t *testing.T) {
	_, rt, fwd, client := singleSessionTracingPod(t, "sess-solo")
	stream := openTracingAttach(t, client, "sess-solo", "")
	rt.waitForSubscribers(t, 1)

	before := tracingDrops()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("slot-x")
	rt.output <- statusFrame("")
	awaitStatus(t, stream)

	requireCalls(t, fwd)
	requireDrops(t, before, 1)
	requireDropLogs(t, logs, dropLog{frameSlot: "slot-x", session: "sess-solo", streamSlot: ""})
}

// spec: 28.5.3 (set_tracing_context addressing) — the frame's slotId is
// compared as a string, so a frame whose slotId is present but is not a
// JSON string carries no address. The published JSONL schema rejects that
// value, and the adapter drops, counts, and logs it rather than reading it
// as an untagged frame and registering it against the stream's session.
//
// diagnosis: a failure means a malformed address fails open. The slotless
// stream on a single-session pod treats an unreadable slotId as an absent
// one, so a frame the contract declares invalid registers tracing
// identifiers instead of being dropped.
func TestSetTracingContextWithNonStringSlotIDIsDropped_spec_28_5_3(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame string
	}{
		{"number", `{"type":"set_tracing_context","slotId":1,"context":{"langsmith_run_id":"run_abc"}}`},
		{"null", `{"type":"set_tracing_context","slotId":null,"context":{"langsmith_run_id":"run_abc"}}`},
		{"object", `{"type":"set_tracing_context","slotId":{"id":"slot-x"},"context":{"langsmith_run_id":"run_abc"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, rt, fwd, client := singleSessionTracingPod(t, "sess-solo")
			stream := openTracingAttach(t, client, "sess-solo", "")
			rt.waitForSubscribers(t, 1)

			before := tracingDrops()
			logs := captureDropLogs(t)
			rt.output <- []byte(tc.frame)
			rt.output <- statusFrame("")
			awaitStatus(t, stream)

			requireCalls(t, fwd)
			requireDrops(t, before, 1)
			requireMalformedSlotIDLog(t, logs, "sess-solo", "")
		})
	}
}

// requireMalformedSlotIDLog asserts the drop path emitted exactly one
// protocol-error line naming a non-string slotId and the address of the
// stream that dropped the frame. The line must be distinguishable from the
// untagged-frame drop, because a runtime that stamps a non-string slotId
// has a different defect from one that omits the field.
func requireMalformedSlotIDLog(t *testing.T, logs func() []string, session, streamSlot string) {
	t.Helper()
	got := logs()
	if len(got) != 1 {
		t.Fatalf("set_tracing_context protocol-error log lines = %d %q, want 1", len(got), got)
	}
	line := got[0]
	if !strings.Contains(line, "non-string slotId") {
		t.Errorf("protocol-error line %q does not report a non-string slotId", line)
	}
	if !strings.Contains(line, "session "+session) || !strings.Contains(line, fmt.Sprintf("slot %q", streamSlot)) {
		t.Errorf("protocol-error line %q does not name the stream (session %s, slot %q)", line, session, streamSlot)
	}
}

// spec: 28.5.3 (set_tracing_context addressing) — live-binding
// confirmation on the per-slot branch. The slot entry is deleted while the
// stream is still draining the runtime's output, so a correctly tagged
// frame arriving afterwards no longer names a live binding and is dropped.
//
// diagnosis: a failure means a frame arriving in the slot's teardown
// window registers tracing identifiers against a released session.
func TestSetTracingContextAfterSlotReleaseIsDropped_spec_28_5_3(t *testing.T) {
	s, rt, fwd, client := concurrentTracingPod(t, "slot-a", "slot-b")
	streamA := openTracingAttach(t, client, "sess-slot-a", "slot-a")
	rt.waitForSubscribers(t, 1)
	s.ReleaseSlotForTest(context.Background(), "slot-a")

	before := tracingDrops()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("slot-a")
	rt.output <- statusFrame("slot-a")
	awaitStatus(t, streamA)

	requireCalls(t, fwd)
	requireDrops(t, before, 1)
	requireDropLogs(t, logs, dropLog{frameSlot: "slot-a", session: "sess-slot-a", streamSlot: "slot-a"})
}

// spec: 28.5.3 (set_tracing_context addressing) — live-binding
// confirmation on the pod-global branch, ambiguity term. The pod holds an
// occupied slot and a pod-global session at once, which the adapter does
// not prevent, so an untagged frame on the slotless stream names no single
// session. The empty-registry term is the only one that fails, and it
// rejects the frame fail-closed.
//
// diagnosis: a failure means an untagged frame on a pod that has taken the
// per-slot path registers against the pod-global session, which is the
// ambiguity the empty-registry term rejects.
func TestSetTracingContextPodGlobalStreamWithOccupiedSlotIsDropped_spec_28_5_3(t *testing.T) {
	s, rt, fwd, client := concurrentTracingPod(t, "slot-a")
	if _, err := s.StartSession(context.Background(), startReq("sess-pod")); err != nil {
		t.Fatalf("StartSession(pod-global): %v", err)
	}
	stream := openTracingAttach(t, client, "sess-pod", "")
	rt.waitForSubscribers(t, 1)

	before := tracingDrops()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("")
	rt.output <- statusFrame("")
	awaitStatus(t, stream)

	requireCalls(t, fwd)
	requireDrops(t, before, 1)
	requireDropLogs(t, logs, dropLog{frameSlot: "", session: "sess-pod", streamSlot: ""})
}

// spec: 28.5.3 (set_tracing_context addressing) — live-binding
// confirmation on the pod-global branch, session term. The pod's session
// is released while the stream is still draining the runtime's output,
// mirroring the grace-deadline overrun in Shutdown, so an untagged frame
// arriving afterwards is dropped rather than registered against the
// just-released session.
//
// diagnosis: a failure means the teardown window on the pod-global branch
// is open again: a frame arriving after the release registers tracing
// identifiers against a session the pod no longer holds.
func TestSetTracingContextAfterSessionReleaseIsDropped_spec_28_5_3(t *testing.T) {
	s, rt, fwd, client := singleSessionTracingPod(t, "sess-solo")
	stream := openTracingAttach(t, client, "sess-solo", "")
	// The bind is validated before the stream subscribes to the runtime's
	// output, so waiting for the subscriber keeps the release after the
	// bind: the stream is open and draining when its session goes away.
	rt.waitForSubscribers(t, 1)
	s.ReleaseSessionForTest()

	before := tracingDrops()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("")
	rt.output <- statusFrame("")
	awaitStatus(t, stream)

	requireCalls(t, fwd)
	requireDrops(t, before, 1)
	requireDropLogs(t, logs, dropLog{frameSlot: "", session: "sess-solo", streamSlot: ""})
}
