// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

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
// detection has no dependence on last_tool_call_id.
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
	ctx := context.Background()
	if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 3,
	}); err != nil {
		t.Fatalf("first fence: %v", err)
	}
	resp, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
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

// stubCheckpointSink is a deterministic CheckpointSink for tests.
type stubCheckpointSink struct {
	id  string
	err error
}

func (s stubCheckpointSink) SaveCheckpoint(_ context.Context, _ string, r io.Reader) (string, error) {
	if r != nil {
		_, _ = io.Copy(io.Discard, r)
	}
	return s.id, s.err
}

// TestCheckpointBarrierAcksMatchedGeneration verifies the happy path:
// fence sets generation N, barrier with N quiesces, emits the ack on
// the control stream, and returns the synchronous mirror. spec:
// §4.7 line 660, §10.1 lines 163-181.
func TestCheckpointBarrierAcksMatchedGeneration(t *testing.T) {
	s := newFencedServer(t)
	// Wire a stub checkpoint sink + workspace so flushBarrierCheckpoint
	// returns a non-empty checkpoint_ref. WorkspaceRoot just needs to be
	// non-empty for the gate to fire; the stub sink ignores the archive.
	s.WorkspaceRoot = t.TempDir()
	s.Checkpoints = stubCheckpointSink{id: "ck-1"}
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

	resp, err := s.CheckpointBarrier(ctx, &adapterv1.CheckpointBarrierRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, BarrierId: "b1", CoordinationGeneration: 9,
	})
	if err != nil {
		t.Fatalf("barrier: %v", err)
	}
	if resp.GetBarrierId() != "b1" {
		t.Fatalf("barrier_id: got %q want b1", resp.GetBarrierId())
	}
	if resp.GetCheckpointRef() != "ck-1" {
		t.Fatalf("checkpoint_ref: got %q want ck-1", resp.GetCheckpointRef())
	}

	// Confirm the ack landed on the control stream too. Fields match the
	// synchronous return.
	select {
	case ev := <-sink:
		if ev.Type != eventCheckpointBarrierAck {
			t.Fatalf("control event type: got %q", ev.Type)
		}
		if ev.BarrierID != "b1" || ev.CheckpointRef != "ck-1" {
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

// TestCheckpointBarrierEmptyCheckpointOnSinkError verifies that a
// checkpoint sink failure does not abort the barrier; the ack carries
// an empty checkpoint_ref so the gateway falls through to its existing
// resume path. spec: §10.1 lines 163-181 — best-effort flush.
func TestCheckpointBarrierEmptyCheckpointOnSinkError(t *testing.T) {
	s := newFencedServer(t)
	s.WorkspaceRoot = t.TempDir()
	s.Checkpoints = stubCheckpointSink{err: errors.New("sink down")}
	ctx := context.Background()
	if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 2,
	}); err != nil {
		t.Fatalf("fence: %v", err)
	}
	resp, err := s.CheckpointBarrier(ctx, &adapterv1.CheckpointBarrierRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, BarrierId: "b2", CoordinationGeneration: 2,
	})
	if err != nil {
		t.Fatalf("barrier with failing sink should not error: %v", err)
	}
	if resp.GetCheckpointRef() != "" {
		t.Fatalf("expected empty checkpoint_ref on sink failure, got %q", resp.GetCheckpointRef())
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
