// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"encoding/json"
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
	rt.output <- tracingFrame("slot-a")
	rt.output <- statusFrame("slot-a")
	awaitStatus(t, streamA)

	requireCalls(t, fwd, "sess-slot-a")
	requireDrops(t, before, 0)
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
	rt.output <- tracingFrame("")
	rt.output <- statusFrame("slot-a")
	rt.output <- statusFrame("slot-b")
	awaitStatus(t, streamA)
	awaitStatus(t, streamB)

	requireCalls(t, fwd)
	// Both slot streams see the untagged frame, so both reject it.
	requireDrops(t, before, 2)
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
	rt.output <- tracingFrame("slot-b")
	rt.output <- statusFrame("slot-a")
	awaitStatus(t, streamA)

	requireCalls(t, fwd)
	requireDrops(t, before, 0)
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
	rt.output <- tracingFrame("")
	rt.output <- statusFrame("")
	awaitStatus(t, stream)

	requireCalls(t, fwd, "sess-solo")
	requireDrops(t, before, 0)
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
	rt.output <- tracingFrame("slot-x")
	rt.output <- statusFrame("")
	awaitStatus(t, stream)

	requireCalls(t, fwd)
	requireDrops(t, before, 1)
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
	rt.output <- tracingFrame("slot-a")
	rt.output <- statusFrame("slot-a")
	awaitStatus(t, streamA)

	requireCalls(t, fwd)
	requireDrops(t, before, 1)
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
	rt.output <- tracingFrame("")
	rt.output <- statusFrame("")
	awaitStatus(t, stream)

	requireCalls(t, fwd)
	requireDrops(t, before, 1)
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
	rt.output <- tracingFrame("")
	rt.output <- statusFrame("")
	awaitStatus(t, stream)

	requireCalls(t, fwd)
	requireDrops(t, before, 1)
}
