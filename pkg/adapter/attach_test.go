// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func TestAttachRoundTripsEnvelopes(t *testing.T) {
	s, rt, _ := sessionServer(t)
	rt.output = make(chan []byte, 8)
	rt.echoInput = true
	if _, err := s.StartSession(context.Background(), startReq("sess-x")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// The first message binds the session and carries an envelope; the
	// fake runtime echoes each written envelope to its output stream,
	// so a successful Recv proves the input reached WriteEnvelope.
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-x"},
		EnvelopeJson: []byte(`{"type":"message","n":1}`),
	}); err != nil {
		t.Fatalf("Send first: %v", err)
	}
	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv first: %v", err)
	}
	if string(got.GetEnvelopeJson()) != `{"type":"message","n":1}` {
		t.Errorf("received %s, want the echoed first envelope", got.GetEnvelopeJson())
	}

	// A subsequent message round-trips the same way.
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-x"},
		EnvelopeJson: []byte(`{"type":"message","n":2}`),
	}); err != nil {
		t.Fatalf("Send second: %v", err)
	}
	got, err = stream.Recv()
	if err != nil {
		t.Fatalf("Recv second: %v", err)
	}
	if string(got.GetEnvelopeJson()) != `{"type":"message","n":2}` {
		t.Errorf("received %s, want the echoed second envelope", got.GetEnvelopeJson())
	}

	// Closing the runtime output ends the stream cleanly.
	close(rt.output)
	if _, err := stream.Recv(); err != io.EOF {
		t.Errorf("Recv after the runtime output closed = %v, want io.EOF", err)
	}
}

func TestAttachStreamsRuntimeOutput(t *testing.T) {
	s, rt, _ := sessionServer(t)
	rt.output = make(chan []byte, 4)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// Bind the session with an envelope-free first message.
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	rt.output <- []byte(`{"type":"response"}`)
	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(got.GetEnvelopeJson()) != `{"type":"response"}` {
		t.Errorf("received %s, want the runtime output frame", got.GetEnvelopeJson())
	}
}

func TestAttachInterceptsAdapterLocalToolCall(t *testing.T) {
	s, rt, root := sessionServer(t)
	rt.output = make(chan []byte, 4)
	rt.echoInput = true
	if err := os.WriteFile(filepath.Join(root, "data.txt"), []byte("file-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The adapter intercepts the read_file tool_call, runs it, and
	// writes the tool_result to the runtime's stdin; the fake runtime
	// echoes that write back to its output, where the adapter relays
	// it. The tool_call itself is never relayed to the gateway.
	rt.output <- toolCallFrame(t, "tc_read", "read_file", map[string]string{"path": "data.txt"})
	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	tr := decodeToolResult(t, got.GetEnvelopeJson())
	if tr.Type != "tool_result" || tr.ID != "tc_read" {
		t.Errorf("relayed frame = %q/%q, want a tool_result for tc_read", tr.Type, tr.ID)
	}
	if tr.IsError || len(tr.Content) != 1 || tr.Content[0].Inline != "file-bytes" {
		t.Errorf("tool_result = %+v, want inline file-bytes with no error", tr)
	}
}

func TestAttachRejectsMissingSessionID(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	stream, err := client.Attach(context.Background())
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = stream.CloseSend()
	if _, err := stream.Recv(); status.Code(err) != codes.InvalidArgument {
		t.Errorf("Recv error code = %v, want InvalidArgument for a missing session id", status.Code(err))
	}
}

func TestAttachRejectsUnassignedSession(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	stream, err := client.Attach(context.Background())
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-other"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = stream.CloseSend()
	if _, err := stream.Recv(); status.Code(err) != codes.NotFound {
		t.Errorf("Recv error code = %v, want NotFound for an unassigned session", status.Code(err))
	}
}

// frameSlotIDForTest reads the slotId off a JSONL frame so a test can
// assert demultiplexing routed the right slot's output to a stream.
func frameSlotIDForTest(t *testing.T, frame []byte) string {
	t.Helper()
	var probe struct {
		SlotID string `json:"slotId"`
	}
	if err := json.Unmarshal(frame, &probe); err != nil {
		t.Fatalf("decode frame slotId from %q: %v", frame, err)
	}
	return probe.SlotID
}

// spec: §6.4 lines 401-405; spec/05:509; spec/15:1459 — the single
// pod-global runtime serves two concurrent slots over one connection, and
// the adapter demultiplexes its interleaved output by slotId so each
// per-slot Attach stream receives only its slot's frames. A second
// concurrent slot is admitted rather than rejected with "pod is not idle".
//
// diagnosis: a failure means per-slot output demultiplexing over the
// single runtime connection regressed: a slot's Attach stream saw a
// sibling slot's output, or the second concurrent slot was rejected.
func TestAttachDemultiplexesConcurrentSlotsBySlotID_spec_6_4(t *testing.T) {
	s, rt := concurrentServer(t)
	rt.output = make(chan []byte, 16)
	ctx := context.Background()
	// Two concurrent slots land on one pod; the second is admitted, not
	// rejected — both share the single pod-global runtime.
	for _, slot := range []string{"slot-a", "slot-b"} {
		if _, err := s.StartSession(ctx, slotStartReq("sess-"+slot, slot)); err != nil {
			t.Fatalf("StartSession(%s): %v", slot, err)
		}
	}
	client, _ := adapterClient(t, s)

	open := func(slot string) adapterv1.Adapter_AttachClient {
		t.Helper()
		streamCtx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		stream, err := client.Attach(streamCtx)
		if err != nil {
			t.Fatalf("Attach(%s): %v", slot, err)
		}
		if err := stream.Send(&adapterv1.AttachRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-" + slot},
			SlotId:    &adapterv1.SlotId{Value: slot},
		}); err != nil {
			t.Fatalf("Send bind(%s): %v", slot, err)
		}
		return stream
	}
	streamA := open("slot-a")
	streamB := open("slot-b")

	// Both Attach handlers must have subscribed to the single fan-out before
	// any frame is written: the runtime delivers each frame only to the
	// subscribers present when it arrives, so a frame written before a slot
	// subscribes would never reach that slot's stream. The bind Send above is
	// asynchronous, so the server-side Output subscription may not have
	// happened yet when open() returns.
	rt.waitForSubscribers(t, 2)

	// The runtime interleaves slotId-tagged frames for both slots over the
	// one connection (each stamped by the runtime's dispatch loop). The
	// adapter must route each frame to the slot that owns it.
	rt.output <- []byte(`{"type":"response","slotId":"slot-a","n":1}`)
	rt.output <- []byte(`{"type":"response","slotId":"slot-b","n":1}`)

	// slot-a's stream receives only the slot-a frame.
	gotA, err := streamA.Recv()
	if err != nil {
		t.Fatalf("Recv(slot-a): %v", err)
	}
	if sid := frameSlotIDForTest(t, gotA.GetEnvelopeJson()); sid != "slot-a" {
		t.Errorf("slot-a stream received a frame for slot %q, want slot-a", sid)
	}
	// slot-b's stream receives only the slot-b frame.
	gotB, err := streamB.Recv()
	if err != nil {
		t.Fatalf("Recv(slot-b): %v", err)
	}
	if sid := frameSlotIDForTest(t, gotB.GetEnvelopeJson()); sid != "slot-b" {
		t.Errorf("slot-b stream received a frame for slot %q, want slot-b", sid)
	}

	// A second slot-a frame still routes only to slot-a, proving the
	// sibling slot-b frame above was demultiplexed away rather than
	// consumed by slot-a's stream.
	rt.output <- []byte(`{"type":"response","slotId":"slot-a","n":2}`)
	gotA2, err := streamA.Recv()
	if err != nil {
		t.Fatalf("Recv(slot-a #2): %v", err)
	}
	if sid := frameSlotIDForTest(t, gotA2.GetEnvelopeJson()); sid != "slot-a" {
		t.Errorf("slot-a stream second frame slot = %q, want slot-a", sid)
	}
}

// spec: §6.4; spec/15:1459 — a no-slotId Attach binds the whole-pod base
// path and reads the runtime's output unfiltered, so the single-session
// pool behaves exactly as before per-slot routing.
//
// diagnosis: a failure means the no-slotId base-path Attach regressed:
// a whole-pod frame no longer reaches the base stream.
func TestAttachNoSlotIDServesBasePath_spec_6_4(t *testing.T) {
	s, rt, _ := sessionServer(t)
	rt.output = make(chan []byte, 4)
	if _, err := s.StartSession(context.Background(), startReq("sess-base")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-base"},
	}); err != nil {
		t.Fatalf("Send bind: %v", err)
	}
	// A whole-pod frame carries no slotId; the base path relays it.
	rt.output <- []byte(`{"type":"response","text":"base"}`)
	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(got.GetEnvelopeJson()) != `{"type":"response","text":"base"}` {
		t.Errorf("base stream received %q, want the whole-pod frame", got.GetEnvelopeJson())
	}
}

// spec: §6.4 lines 401-405; spec/15:1459 — an inbound (client→agent)
// envelope on a per-slot Attach stream is stamped with the slot's slotId
// before it reaches the shared runtime, so the runtime's dispatch loop
// routes it to the slot's cwd. Driven under two concurrent slots so the
// race detector exercises the shared-connection write path.
//
// diagnosis: a failure means inbound slotId stamping over the single
// runtime connection regressed: an envelope reached the runtime without
// the addressed slot's slotId, which would misroute it on a concurrent pod.
func TestAttachStampsInboundSlotID_spec_6_4(t *testing.T) {
	s, rt := concurrentServer(t)
	rt.output = make(chan []byte, 16)
	ctx := context.Background()
	for _, slot := range []string{"slot-a", "slot-b"} {
		if _, err := s.StartSession(ctx, slotStartReq("sess-"+slot, slot)); err != nil {
			t.Fatalf("StartSession(%s): %v", slot, err)
		}
	}
	client, _ := adapterClient(t, s)

	send := func(wg *sync.WaitGroup, slot string) {
		defer wg.Done()
		// The stream stays open until the test ends (t.Cleanup cancels it),
		// so cancelling it does not race the server's delivery of the
		// first-message envelope to the shared runtime.
		streamCtx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		stream, err := client.Attach(streamCtx)
		if err != nil {
			t.Errorf("Attach(%s): %v", slot, err)
			return
		}
		// The first message binds the stream and carries an envelope with
		// no slotId; the adapter must stamp the slot's slotId onto it.
		if err := stream.Send(&adapterv1.AttachRequest{
			SessionId:    &adapterv1.SessionId{Value: "sess-" + slot},
			SlotId:       &adapterv1.SlotId{Value: slot},
			EnvelopeJson: []byte(`{"type":"message"}`),
		}); err != nil {
			t.Errorf("Send(%s): %v", slot, err)
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go send(&wg, "slot-a")
	go send(&wg, "slot-b")
	wg.Wait()

	// Both inbound envelopes reached the shared runtime, each stamped with
	// its slot's slotId. Wait briefly for the concurrent writes to land.
	deadline := time.Now().Add(2 * time.Second)
	var envs [][]byte
	for time.Now().Before(deadline) {
		envs = rt.envelopesSnapshot()
		if len(envs) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(envs) != 2 {
		t.Fatalf("runtime received %d envelopes, want 2 (one per slot)", len(envs))
	}
	seen := map[string]bool{}
	for _, env := range envs {
		seen[frameSlotIDForTest(t, env)] = true
	}
	if !seen["slot-a"] || !seen["slot-b"] {
		t.Errorf("stamped slotIds = %v, want both slot-a and slot-b", seen)
	}
}
