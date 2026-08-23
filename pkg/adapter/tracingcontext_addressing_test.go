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

// tracingFrame builds a set_tracing_context frame, tagging it with the
// session address when one is given. An untagged frame is what a runtime
// that does not populate the address writes; §28.5.3 requires the address
// on every session-scoped frame, on every pod.
func tracingFrame(sessionID string) []byte {
	if sessionID == "" {
		return []byte(`{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc"}}`)
	}
	return []byte(`{"type":"set_tracing_context","sessionId":"` + sessionID +
		`","context":{"langsmith_run_id":"run_abc"}}`)
}

// statusFrame builds a status frame the Attach loop relays as content. It
// is the synchronization point of every case below: the output relay
// handles one frame at a time, so receiving the status frame that follows
// a set_tracing_context frame proves the adapter finished handling that
// frame.
func statusFrame(sessionID string) []byte {
	if sessionID == "" {
		return []byte(`{"type":"status","state":"thinking"}`)
	}
	return []byte(`{"type":"status","sessionId":"` + sessionID + `","state":"thinking"}`)
}

// tracingDrops reads the §28.5.3 drop counter.
func tracingDrops() float64 {
	return testutil.ToFloat64(adapter.SetTracingContextDroppedCounter())
}

// unaddressedRejections reads the §28.5.3 unaddressed-frame counter for
// the set_tracing_context frame type.
func unaddressedRejections() float64 {
	return testutil.ToFloat64(adapter.UnaddressedFrameRejectedCounter("set_tracing_context"))
}

// openTracingAttach binds an Attach stream to sessionID and returns it.
// The session identifier is the stream's whole address.
func openTracingAttach(t *testing.T, client adapterv1.AdapterClient, sessionID string) adapterv1.Adapter_AttachClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach(%s): %v", sessionID, err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	}); err != nil {
		t.Fatalf("Send bind(%s): %v", sessionID, err)
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

// dropLog is one expected drop diagnostic: the session the frame was
// addressed to, and the (session, slot) address of the Attach stream that
// dropped it. A drop counted by an unlabelled counter is only attributable
// to a misaddressed frame, a wrong-session stamp, or a teardown-window
// arrival through this line, so the line must name all three fields in
// that order.
type dropLog struct {
	frameSession string
	session      string
	streamSlot   string
}

// matches reports whether line names the frame's session, then the
// stream's session, then the stream's slot.
func (d dropLog) matches(line string) bool {
	frame := strings.Index(line, fmt.Sprintf("for session %q", d.frameSession))
	session := strings.Index(line, "bound to session "+d.session)
	stream := strings.LastIndex(line, fmt.Sprintf("slot %q", d.streamSlot))
	return frame >= 0 && session > frame && stream > session
}

// rejectLog is one expected unaddressed-frame diagnostic, which names the
// session of the stream that rejected the frame. The frame carries no
// session of its own, so the stream's binding is the whole address the
// line can name.
type rejectLog struct {
	session string
}

// matches reports whether line is the unaddressed-frame rejection for this
// stream's session.
func (r rejectLog) matches(line string) bool {
	return strings.Contains(line, "carrying no session identifier") &&
		strings.Contains(line, "bound to session "+r.session)
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
			t.Errorf("%d protocol-error line(s) name frame session %q on stream (session %s, slot %q), want 1; got %q",
				n, w.frameSession, w.session, w.streamSlot, got)
		}
	}
}

// requireRejectLogs asserts the rejection path emitted exactly one
// protocol-error line per expected rejecting stream.
func requireRejectLogs(t *testing.T, logs func() []string, want ...rejectLog) {
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
			t.Errorf("%d protocol-error line(s) reject an unaddressed frame on the stream bound to session %s, want 1; got %q",
				n, w.session, got)
		}
	}
}

// requireUnaddressed asserts the unaddressed-frame counter moved by want
// since before.
func requireUnaddressed(t *testing.T, before, want float64) {
	t.Helper()
	if got := unaddressedRejections() - before; got != want {
		t.Errorf("unaddressed set_tracing_context frame counter moved by %v, want %v", got, want)
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
func concurrentTracingPod(t *testing.T, sessions ...string) (*adapter.Server, *fakeRuntime, *fakePlatformForwarder, adapterv1.AdapterClient) {
	t.Helper()
	s, rt := concurrentServer(t)
	rt.output = make(chan []byte, 16)
	fwd := &fakePlatformForwarder{result: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)}
	s.PlatformForwarder = fwd
	for _, sess := range sessions {
		if _, err := s.StartSession(context.Background(), slotStartReq(sess)); err != nil {
			t.Fatalf("StartSession(%s): %v", sess, err)
		}
	}
	client, _ := adapterClient(t, s)
	return s, rt, fwd, client
}

// spec: 28.5.3 (set_tracing_context addressing) — a frame carrying the
// stream's own session address satisfies both conditions, so the adapter
// registers it once, against that session, and counts no drop.
//
// diagnosis: a failure means the addressing rule rejects a correctly
// addressed frame, so a runtime's tracing identifiers never reach the
// gateway.
func TestSetTracingContextAddressedToOwnSessionRegistersOnce_spec_28_5_3(t *testing.T) {
	_, rt, fwd, client := concurrentTracingPod(t, "sess-a", "sess-b")
	streamA := openTracingAttach(t, client, "sess-a")
	openTracingAttach(t, client, "sess-b")
	rt.waitForSubscribers(t, 2)

	before := tracingDrops()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("sess-a")
	rt.output <- statusFrame("sess-a")
	awaitStatus(t, streamA)

	requireCalls(t, fwd, "sess-a")
	requireDrops(t, before, 0)
	requireDropLogs(t, logs)
}

// spec: 28.5.3 (set_tracing_context addressing), 4.6.1 (an absent address
// on a pod holding more than one slot) — a frame carrying no session
// identifier names nothing the pod can resolve when it holds more than one
// slot. Every stream receives it through the fan-out and every one of them
// rejects, counts, and logs it, so no session's tracing context is
// written. The rejection is counted on the unaddressed-frame series rather
// than on the misaddressed-frame drop series, because the two partition
// the rejections between them.
//
// diagnosis: a failure means an unaddressed frame is registering against
// some session again, which merges one runtime's tracing identifiers into
// a co-tenant's delegation lease, or that the rejection is attributed to
// the wrong counter.
func TestSetTracingContextUnaddressedOnAMultiSlotPodIsRejected_spec_28_5_3(t *testing.T) {
	_, rt, fwd, client := concurrentTracingPod(t, "sess-a", "sess-b")
	streamA := openTracingAttach(t, client, "sess-a")
	streamB := openTracingAttach(t, client, "sess-b")
	rt.waitForSubscribers(t, 2)

	beforeDrops := tracingDrops()
	beforeRejects := unaddressedRejections()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("")
	rt.output <- statusFrame("sess-a")
	rt.output <- statusFrame("sess-b")
	awaitStatus(t, streamA)
	awaitStatus(t, streamB)

	requireCalls(t, fwd)
	// Both streams see the unaddressed frame, so both reject it.
	requireUnaddressed(t, beforeRejects, 2)
	requireDrops(t, beforeDrops, 0)
	requireRejectLogs(t, logs, rejectLog{session: "sess-a"}, rejectLog{session: "sess-b"})
}

// spec: 28.5.3 (an absent address on a pod holding at most one slot
// resolves to the receiving stream's own binding), 28.5.3
// (set_tracing_context addressing) — a Basic-level runtime that echoes no
// identifier has the stdout frame as its only tracing path, and on a pod
// holding one slot the frame can address no session other than the one the
// receiving stream is bound to. It resolves rather than being rejected,
// and neither rejection counter moves.
//
// diagnosis: a failure means the adapter rejects every untagged
// set_tracing_context frame, which silently disables tracing for every
// Basic-level runtime, since the JSONL frame is the only tracing path such
// a runtime has.
func TestSetTracingContextUnaddressedOnASingleSlotPodResolvesToTheStream_spec_28_5_3(t *testing.T) {
	_, rt, fwd, client := concurrentTracingPod(t, "sess-a")
	streamA := openTracingAttach(t, client, "sess-a")
	rt.waitForSubscribers(t, 1)

	beforeDrops := tracingDrops()
	beforeRejects := unaddressedRejections()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("")
	rt.output <- statusFrame("sess-a")
	awaitStatus(t, streamA)

	requireCalls(t, fwd, "sess-a")
	requireUnaddressed(t, beforeRejects, 0)
	requireDrops(t, beforeDrops, 0)
	requireDropLogs(t, logs)
}

// spec: 28.5.3 (set_tracing_context addressing) — a frame addressed to a
// co-tenant never reaches this stream's handler: the demultiplexer drops
// it first, so the stream registers nothing and the addressing counter
// does not move.
//
// diagnosis: a failure means a session's Attach stream handled a frame
// addressed to a co-tenant.
func TestSetTracingContextAddressedToACoTenantNeverReachesStream_spec_28_5_3(t *testing.T) {
	_, rt, fwd, client := concurrentTracingPod(t, "sess-a", "sess-b")
	streamA := openTracingAttach(t, client, "sess-a")
	rt.waitForSubscribers(t, 1)

	before := tracingDrops()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("sess-b")
	rt.output <- statusFrame("sess-a")
	awaitStatus(t, streamA)

	requireCalls(t, fwd)
	requireDrops(t, before, 0)
	requireDropLogs(t, logs)
}

// spec: 28.5.3 (set_tracing_context addressing), 4.6.1 (an absent address
// on a pod holding more than one slot) — address resolution is exact
// string equality, with an absent or unreadable address counting as the
// empty string, and the adapter reads no other outcome off the frame. A
// frame whose address the adapter cannot read as a string therefore
// carries no address at all, and on a pod holding more than one slot it is
// rejected rather than resolved, which is the fail-closed direction.
//
// diagnosis: a failure means the adapter applies an addressing outcome the
// addressing rule does not define, accepting a frame the rule rejects
// because it decodes the address value's JSON type as a third answer.
func TestSetTracingContextUnreadableAddressIsTheEmptyAddress_spec_28_5_3(t *testing.T) {
	frames := map[string]string{
		"number": `{"type":"set_tracing_context","sessionId":1,"context":{"langsmith_run_id":"run_abc"}}`,
		"null":   `{"type":"set_tracing_context","sessionId":null,"context":{"langsmith_run_id":"run_abc"}}`,
		"object": `{"type":"set_tracing_context","sessionId":{"id":"sess-a"},"context":{"langsmith_run_id":"run_abc"}}`,
	}
	for name, frame := range frames {
		t.Run(name+" is rejected", func(t *testing.T) {
			// A pod holding a second slot is what makes an unreadable
			// address unresolvable: there is more than one session the
			// frame could name and nothing in it says which.
			_, rt, fwd, client := concurrentTracingPod(t, "sess-a", "sess-b")
			streamA := openTracingAttach(t, client, "sess-a")
			rt.waitForSubscribers(t, 1)

			beforeDrops := tracingDrops()
			beforeRejects := unaddressedRejections()
			logs := captureDropLogs(t)
			rt.output <- []byte(frame)
			rt.output <- statusFrame("sess-a")
			awaitStatus(t, streamA)

			requireCalls(t, fwd)
			requireUnaddressed(t, beforeRejects, 1)
			requireDrops(t, beforeDrops, 0)
			requireRejectLogs(t, logs, rejectLog{session: "sess-a"})
		})
	}
}

// spec: 28.5.3 (set_tracing_context addressing) — live-binding
// confirmation. The session's registry entry is deleted while its stream
// is still draining the runtime's output, so a correctly addressed frame
// arriving afterwards no longer names a live binding and is dropped.
//
// diagnosis: a failure means a frame arriving in the teardown window
// registers tracing identifiers against a released session.
func TestSetTracingContextAfterSessionReleaseIsDropped_spec_28_5_3(t *testing.T) {
	s, rt, fwd, client := concurrentTracingPod(t, "sess-a", "sess-b")
	streamA := openTracingAttach(t, client, "sess-a")
	rt.waitForSubscribers(t, 1)
	s.ReleaseSlotForTest("sess-a")

	before := tracingDrops()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("sess-a")
	rt.output <- statusFrame("sess-a")
	awaitStatus(t, streamA)

	requireCalls(t, fwd)
	requireDrops(t, before, 1)
	requireDropLogs(t, logs, dropLog{frameSession: "sess-a", session: "sess-a", streamSlot: "sess-a"})
}

// spec: 28.5.3 (the slot count that decides an absent address is every
// entry in the adapter's slot registry, bound or registered-but-unbound),
// 28.5.3 (set_tracing_context addressing) — the registry entry a
// workspace-prep RPC creates ahead of StartSession already counts, so a
// frame carrying no session identifier is rejected while a second
// session's workspace is being prepared rather than resolving to the one
// session that happens to be bound. Counting the bound entries alone
// would resolve it to the incumbent, which is the fail-open direction the
// counting basis exists to close.
//
// diagnosis: a failure means the slot count is taken over bound entries
// alone, so on a pod that is preparing a second session an unaddressed
// frame registers one runtime's tracing identifiers against the
// incumbent session instead of being rejected.
func TestSetTracingContextUnaddressedWhileASecondSlotIsUnboundIsRejected_spec_28_5_3(t *testing.T) {
	s, rt, fwd, client := concurrentTracingPod(t, "sess-a")
	streamA := openTracingAttach(t, client, "sess-a")
	rt.waitForSubscribers(t, 1)
	// The second session's workspace is being prepared: its slot entry
	// exists and its tree is created, but nothing has bound or started it.
	if err := s.RegisterUnboundSlotForTest("sess-b"); err != nil {
		t.Fatalf("register unbound slot for sess-b: %v", err)
	}

	beforeDrops := tracingDrops()
	beforeRejects := unaddressedRejections()
	logs := captureDropLogs(t)
	rt.output <- tracingFrame("")
	rt.output <- statusFrame("sess-a")
	awaitStatus(t, streamA)

	requireCalls(t, fwd)
	requireUnaddressed(t, beforeRejects, 1)
	requireDrops(t, beforeDrops, 0)
	requireRejectLogs(t, logs, rejectLog{session: "sess-a"})
}
