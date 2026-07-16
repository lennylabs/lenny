// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// newFencedServer returns a fresh Server with sessionID = "s1" claimed
// so the CoordinatorFence and CheckpointBarrier handlers can run
// against a non-idle pod.
func newFencedServer(t *testing.T) *Server {
	t.Helper()
	s := New("test")
	if err := s.claimSession("s1"); err != nil {
		t.Fatalf("claim session: %v", err)
	}
	return s
}

// TestCoordinatorFenceRejectsMissingSessionID verifies that the
// adapter rejects a fence RPC missing a session id. spec: §4.7 line 632.
func TestCoordinatorFenceRejectsMissingSessionID(t *testing.T) {
	s := newFencedServer(t)
	_, err := s.CoordinatorFence(context.Background(), &adapterv1.CoordinatorFenceRequest{
		CoordinationGeneration: 1,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

// TestCoordinatorFenceRejectsZeroGeneration verifies that the adapter
// rejects a fence with a non-positive coordination_generation. spec:
// §10.1 line 33.
func TestCoordinatorFenceRejectsZeroGeneration(t *testing.T) {
	s := newFencedServer(t)
	_, err := s.CoordinatorFence(context.Background(), &adapterv1.CoordinatorFenceRequest{
		SessionId:              &adapterv1.SessionId{Value: "s1"},
		CoordinationGeneration: 0,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

// TestCoordinatorFenceFirstFenceNeverGap verifies the §10.1 line 36
// rule that the first fence on a pod's lifetime is recorded regardless
// of value and never treated as a gap.
func TestCoordinatorFenceFirstFenceNeverGap(t *testing.T) {
	s := newFencedServer(t)
	resp, err := s.CoordinatorFence(context.Background(), &adapterv1.CoordinatorFenceRequest{
		SessionId:              &adapterv1.SessionId{Value: "s1"},
		CoordinationGeneration: 42,
	})
	if err != nil {
		t.Fatalf("first fence: %v", err)
	}
	if !resp.GetAccepted() || resp.GetGapDetected() {
		t.Fatalf("first fence should be accepted without gap: %+v", resp)
	}
	if got := s.LastFencedGeneration(); got != 42 {
		t.Fatalf("last fenced generation: got %d want 42", got)
	}
}

// TestCoordinatorFenceMonotonicIncrement verifies the §10.1 line 33
// strict-monotonic rule and that the no-gap path doesn't set
// gap_detected.
func TestCoordinatorFenceMonotonicIncrement(t *testing.T) {
	s := newFencedServer(t)
	ctx := context.Background()
	if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 5,
	}); err != nil {
		t.Fatalf("first fence: %v", err)
	}
	resp, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 6,
	})
	if err != nil {
		t.Fatalf("second fence: %v", err)
	}
	if !resp.GetAccepted() || resp.GetGapDetected() {
		t.Fatalf("contiguous fence: %+v", resp)
	}
}

// TestCoordinatorFenceStaleGenerationRejected verifies that a fence
// carrying a generation not strictly greater than the last fenced
// value is rejected with FailedPrecondition. spec: §10.1 line 33.
func TestCoordinatorFenceStaleGenerationRejected(t *testing.T) {
	s := newFencedServer(t)
	ctx := context.Background()
	if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 7,
	}); err != nil {
		t.Fatalf("first fence: %v", err)
	}
	// Equal-or-lower: stale.
	for _, gen := range []int64{7, 6, 1} {
		_, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
			SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: gen,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("gen %d: expected FailedPrecondition, got %v", gen, err)
		}
		if !strings.Contains(err.Error(), "coordinator_handoff_stale") {
			t.Fatalf("gen %d: expected coordinator_handoff_stale detail, got %v", gen, err)
		}
	}
}

// TestCoordinatorFenceGapDetected verifies §10.1 line 36 gap detection:
// a generation that skips one or more values still logs
// `coordinator_generation_gap` and returns gap_detected=true after the
// dead last_tool_call_id reset was removed (proposal 0026), since gap
// detection has no dependence on last_tool_call_id. It also pins the
// proposal-0026 Pass-14 doc reconciliation: the gap path does not cancel
// in-flight RPCs (the §10.1 line 36 cancellation is an unimplemented
// requirement), so the fence's own context is left un-cancelled.
//
// spec: §10.1 lines 33-37 (CoordinatorFence gap), §4.2 (coordination_generation handoff).
func TestCoordinatorFenceGapDetected(t *testing.T) {
	// Redirect the default slog logger so the gap warning line is
	// observable; CoordinatorFence emits it via slog.WarnContext.
	logBuf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s := newFencedServer(t)
	if _, err := s.CoordinatorFence(context.Background(), &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 3,
	}); err != nil {
		t.Fatalf("first fence: %v", err)
	}
	// Pass the gap fence a cancellable context so the test can assert the
	// gap path does not cancel it. A pre-fix implementation matching the
	// old doc ("cancels any in-flight RPCs received under the missing
	// generation(s)") would have to cancel through this context, failing
	// the ctx.Err() check below.
	gapCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	resp, err := s.CoordinatorFence(gapCtx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 7,
	})
	if err != nil {
		t.Fatalf("gap fence: %v", err)
	}
	if !resp.GetAccepted() || !resp.GetGapDetected() {
		t.Fatalf("gap fence: expected accepted+gap_detected, got %+v", resp)
	}
	if !strings.Contains(logBuf.String(), "coordinator_generation_gap") {
		t.Fatalf("gap path should log coordinator_generation_gap, got %q", logBuf.String())
	}
	if gapCtx.Err() != nil {
		t.Fatalf("gap path must not cancel in-flight RPCs (unimplemented §10.1 line 36); ctx.Err()=%v", gapCtx.Err())
	}
}

// TestCheckpointBarrierRequiresSession verifies session validation on
// the barrier RPC.
func TestCheckpointBarrierRequiresSession(t *testing.T) {
	s := newFencedServer(t)
	_, err := s.CheckpointBarrier(context.Background(), &adapterv1.CheckpointBarrierRequest{
		BarrierId: "b1", CoordinationGeneration: 1,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for missing session id, got %v", err)
	}
}

// TestCheckpointBarrierRejectsWithoutFence verifies that the barrier
// path requires a prior CoordinatorFence; without one the gate is
// closed. spec: §10.1 line 34 — fence is a precondition for any
// subsequent operational RPC.
func TestCheckpointBarrierRejectsWithoutFence(t *testing.T) {
	s := newFencedServer(t)
	_, err := s.CheckpointBarrier(context.Background(), &adapterv1.CheckpointBarrierRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, BarrierId: "b1", CoordinationGeneration: 1,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition without fence, got %v", err)
	}
}

// TestCheckpointBarrierRejectsGenerationMismatch verifies that the
// barrier rejects when its coordination_generation does not match the
// last fenced value.
func TestCheckpointBarrierRejectsGenerationMismatch(t *testing.T) {
	s := newFencedServer(t)
	ctx := context.Background()
	if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 4,
	}); err != nil {
		t.Fatalf("fence: %v", err)
	}
	_, err := s.CheckpointBarrier(ctx, &adapterv1.CheckpointBarrierRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, BarrierId: "b1", CoordinationGeneration: 3,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition on stale barrier, got %v", err)
	}
}

// waitBarrierWaiting spins until the CheckpointBarrier RPC under test has
// opened its quiesce-and-hold gate, so the test can link a checkpoint id
// into it exactly as the gateway-driven Checkpoint stream would.
func waitBarrierWaiting(t *testing.T, s *Server) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		s.barrier.mu.Lock()
		waiting := s.barrier.waiting
		s.barrier.mu.Unlock()
		if waiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("CheckpointBarrier never opened its quiesce-and-hold gate")
}

// TestCheckpointBarrierAcksEchoedCheckpointID verifies the §10.1 lines
// 163-172 quiesce-and-hold contract: fence sets generation N, the barrier
// with N quiesces and holds, and it returns only after the gateway-driven
// Checkpoint stream terminates, echoing the checkpoint_id that stream
// carried on its CheckpointStart. A pre-fix barrier that drove its own
// in-adapter checkpoint would return an adapter-minted ref rather than the
// gateway's id, or return before the stream ran.
//
// spec: §4.7 line 660, §10.1 lines 163-172.
func TestCheckpointBarrierAcksEchoedCheckpointID(t *testing.T) {
	s := newFencedServer(t)
	ctx := context.Background()
	if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 9,
	}); err != nil {
		t.Fatalf("fence: %v", err)
	}

	// Attach a fake control-event sink so we can observe the ack emit
	// without standing up the gRPC LifecycleChannel stream.
	sink := make(chan controlEvent, 4)
	s.controlMu.Lock()
	s.controlSink = sink
	s.controlMu.Unlock()
	t.Cleanup(func() {
		s.controlMu.Lock()
		s.controlSink = nil
		s.controlMu.Unlock()
	})

	type barrierResult struct {
		resp *adapterv1.CheckpointBarrierResponse
		err  error
	}
	resultCh := make(chan barrierResult, 1)
	go func() {
		resp, err := s.CheckpointBarrier(ctx, &adapterv1.CheckpointBarrierRequest{
			SessionId: &adapterv1.SessionId{Value: "s1"}, BarrierId: "b1", CoordinationGeneration: 9,
		})
		resultCh <- barrierResult{resp, err}
	}()

	// The barrier holds quiescence; simulate the gateway-driven Checkpoint
	// stream linking its minted id and terminating.
	waitBarrierWaiting(t, s)
	if !s.isQuiescedForBarrier() {
		t.Fatal("barrier must hold quiescence while it waits for the stream")
	}
	if !s.barrier.link("gw-ckpt-1") {
		t.Fatal("Checkpoint stream could not link into the open barrier gate")
	}
	s.barrier.complete()

	got := <-resultCh
	if got.err != nil {
		t.Fatalf("barrier: %v", got.err)
	}
	if got.resp.GetBarrierId() != "b1" {
		t.Fatalf("barrier_id: got %q want b1", got.resp.GetBarrierId())
	}
	if got.resp.GetCheckpointRef() != "gw-ckpt-1" {
		t.Fatalf("checkpoint_ref: got %q want the echoed gateway checkpoint_id gw-ckpt-1", got.resp.GetCheckpointRef())
	}
	// Quiescence is released only after the RPC returns.
	if s.isQuiescedForBarrier() {
		t.Fatal("quiescence must be released after the barrier returns")
	}

	// Confirm the ack landed on the control stream too. Fields match the
	// synchronous return.
	select {
	case ev := <-sink:
		if ev.Type != eventCheckpointBarrierAck {
			t.Fatalf("control event type: got %q", ev.Type)
		}
		if ev.BarrierID != "b1" || ev.CheckpointRef != "gw-ckpt-1" {
			t.Fatalf("control event fields: %+v", ev)
		}
		// Round-trip JSON marshal so we exercise the wire encoding the
		// LifecycleChannel uses.
		buf, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal control event: %v", err)
		}
		if !strings.Contains(string(buf), `"type":"CheckpointBarrierAck"`) {
			t.Fatalf("expected CheckpointBarrierAck discriminator, got %s", buf)
		}
	default:
		t.Fatalf("expected CheckpointBarrierAck on the control stream")
	}
}

// TestCheckpointBarrierQuiescedMsIsTimeToQuiescence pins §10.1 line 167:
// quiesced_ms is the time to reach quiescence measured inside the ack window,
// not the full hold duration across the gateway-driven Checkpoint stream. The
// barrier holds quiescence open for a wall-clock span before the stream
// links and terminates; a pre-fix barrier measured time.Since(startedAt)
// after that hold and reported the whole window instead.
//
// spec: §10.1 line 167.
func TestCheckpointBarrierQuiescedMsIsTimeToQuiescence(t *testing.T) {
	s := newFencedServer(t)
	ctx := context.Background()
	if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 3,
	}); err != nil {
		t.Fatalf("fence: %v", err)
	}

	resultCh := make(chan *adapterv1.CheckpointBarrierResponse, 1)
	go func() {
		resp, _ := s.CheckpointBarrier(ctx, &adapterv1.CheckpointBarrierRequest{
			SessionId: &adapterv1.SessionId{Value: "s1"}, BarrierId: "b1", CoordinationGeneration: 3,
		})
		resultCh <- resp
	}()

	waitBarrierWaiting(t, s)
	// Hold the gateway-driven stream open well past any plausible
	// time-to-quiescence before linking its id and completing it.
	const hold = 200 * time.Millisecond
	time.Sleep(hold)
	s.barrier.link("gw-ckpt-1")
	s.barrier.complete()

	resp := <-resultCh
	if resp.GetQuiescedMs() >= hold.Milliseconds()/2 {
		t.Fatalf("quiesced_ms = %d, want the time-to-quiescence (well under the %d ms hold), not the whole held-stream window",
			resp.GetQuiescedMs(), hold.Milliseconds())
	}
}

// TestCheckpointBarrierEmptyCheckpointWhenNoStreamDriven verifies that a
// barrier whose wall-clock window expires without the gateway driving a
// Checkpoint stream returns an empty checkpoint_ref, so the gateway
// finalises a partial manifest rather than blocking the drain. spec:
// §10.1 lines 169-172 — partial-capture path.
func TestCheckpointBarrierEmptyCheckpointWhenNoStreamDriven(t *testing.T) {
	s := newFencedServer(t)
	if _, err := s.CoordinatorFence(context.Background(), &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 2,
	}); err != nil {
		t.Fatalf("fence: %v", err)
	}
	// A short-deadline context stands in for the barrier's wall-clock
	// window; no Checkpoint stream is driven against the pod.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	resp, err := s.CheckpointBarrier(ctx, &adapterv1.CheckpointBarrierRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, BarrierId: "b2", CoordinationGeneration: 2,
	})
	if err != nil {
		t.Fatalf("barrier with no gateway-driven stream should not error: %v", err)
	}
	if resp.GetCheckpointRef() != "" {
		t.Fatalf("expected empty checkpoint_ref when no stream was driven, got %q", resp.GetCheckpointRef())
	}
}

// TestCheckpointBarrierMissingBarrierID verifies barrier_id validation.
func TestCheckpointBarrierMissingBarrierID(t *testing.T) {
	s := newFencedServer(t)
	ctx := context.Background()
	if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 1,
	}); err != nil {
		t.Fatalf("fence: %v", err)
	}
	_, err := s.CheckpointBarrier(ctx, &adapterv1.CheckpointBarrierRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 1,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for missing barrier_id, got %v", err)
	}
}

// TestExtractToolCallID covers the tool_call frame parser used to stamp
// the §16.3 `session.tool_call` span with the invoked tool's call id.
func TestExtractToolCallID(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		want  string
	}{
		{"tool_call", `{"type":"tool_call","id":"tc-42","name":"foo"}`, "tc-42"},
		{"not_tool_call", `{"type":"response","id":"x"}`, ""},
		{"malformed", `{not-json`, ""},
		{"no_id", `{"type":"tool_call","name":"foo"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractToolCallID([]byte(tc.frame)); got != tc.want {
				t.Fatalf("extractToolCallID(%q): got %q want %q", tc.frame, got, tc.want)
			}
		})
	}
}
