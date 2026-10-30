// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
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
	if err := s.claimSessionForTest("s1"); err != nil {
		t.Fatalf("claim session: %v", err)
	}
	return s
}

// TestCoordinatorFenceRejectsMissingSessionID verifies that the
// adapter rejects a fence RPC missing a session id. spec: §4.7.
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
// §10.1.
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

// TestCoordinatorFenceFirstFenceNeverGap verifies the §10.1.2
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
	if got := s.LastFencedGeneration("s1"); got != 42 {
		t.Fatalf("last fenced generation: got %d want 42", got)
	}
}

// TestCoordinatorFenceMonotonicIncrement verifies the §10.1.2
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
// value is rejected with FailedPrecondition. spec: §10.1.
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

// TestCoordinatorFenceGapDetected verifies §10.1.2 gap detection:
// a generation that skips one or more values still logs
// `coordinator_generation_gap` and returns gap_detected=true after the
// dead last_tool_call_id reset was removed (proposal 0026), since gap
// detection has no dependence on last_tool_call_id. It also pins the
// proposal-0026 Pass-14 doc reconciliation: the gap path does not cancel
// in-flight RPCs (the §10.1.2 cancellation is an unimplemented
// requirement), so the fence's own context is left un-cancelled.
//
// spec: §10.1, §4.2 (coordination_generation handoff).
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
		t.Fatalf("gap path must not cancel in-flight RPCs (unimplemented §10.1.2); ctx.Err()=%v", gapCtx.Err())
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

// TestCheckpointBarrierAcceptsBoundSessionWithNoFencedGeneration verifies
// that the generation gate refuses the barrier only when the pod holds a
// generation for the named session that the barrier does not carry. A
// session bound to the pod that no coordinator has ever fenced there
// carries no recorded value, so its drain barrier is accepted, quiesces
// the session, and records no fenced generation. Against the pre-fix gate
// the same call was refused with FailedPrecondition.
//
// spec: §10.1.2, §10.1.8.
func TestCheckpointBarrierAcceptsBoundSessionWithNoFencedGeneration(t *testing.T) {
	s := newFencedServer(t)
	ctx := context.Background()

	type barrierResult struct {
		resp *adapterv1.CheckpointBarrierResponse
		err  error
	}
	resultCh := make(chan barrierResult, 1)
	go func() {
		resp, err := s.CheckpointBarrier(ctx, &adapterv1.CheckpointBarrierRequest{
			SessionId: &adapterv1.SessionId{Value: "s1"}, BarrierId: "b1", CoordinationGeneration: 1,
		})
		resultCh <- barrierResult{resp, err}
	}()

	// The barrier passed the gate and holds quiescence; drive it to
	// completion the way the gateway-driven Checkpoint stream would.
	waitBarrierWaiting(t, s, "s1")
	gate := sessionGate(t, s, "s1")
	if !gate.link("gw-ckpt-unfenced") {
		t.Fatal("Checkpoint stream could not link into the open barrier gate")
	}
	gate.complete()

	got := <-resultCh
	if got.err != nil {
		t.Fatalf("barrier for an unfenced bound session must be accepted, got %v", got.err)
	}
	if got.resp.GetBarrierId() != "b1" {
		t.Fatalf("barrier_id: got %q want b1", got.resp.GetBarrierId())
	}
	if gen := s.LastFencedGeneration("s1"); gen != 0 {
		t.Fatalf("an accepted barrier must record no fenced generation, got %d", gen)
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

// waitBarrierWaiting spins until the CheckpointBarrier RPC for the named
// session has opened that session's quiesce-and-hold gate, so the test can
// link a checkpoint id into it exactly as the gateway-driven Checkpoint
// stream would.
func waitBarrierWaiting(t *testing.T, s *Server, sessionID string) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if s.BarrierWaiting(sessionID) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("CheckpointBarrier for %s never opened its quiesce-and-hold gate", sessionID)
}

// sessionGate returns the named session's barrier gate so a test can link
// and complete it the way that session's own Checkpoint stream does.
func sessionGate(t *testing.T, s *Server, sessionID string) *barrierGate {
	t.Helper()
	st := s.slotStateForSession(sessionID)
	if st == nil {
		t.Fatalf("session %s holds no slot registry entry", sessionID)
	}
	return &st.barrier
}

// TestCheckpointBarrierAcksEchoedCheckpointID verifies the §10.1 quiesce-and-hold contract: fence sets generation N, the barrier
// with N quiesces and holds, and it returns only after the gateway-driven
// Checkpoint stream terminates, echoing the checkpoint_id that stream
// carried on its CheckpointStart. A pre-fix barrier that drove its own
// in-adapter checkpoint would return an adapter-minted ref rather than the
// gateway's id, or return before the stream ran.
//
// spec: §4.7, §10.1.
func TestCheckpointBarrierAcksEchoedCheckpointID(t *testing.T) {
	s := newFencedServer(t)
	ctx := context.Background()
	if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 9,
	}); err != nil {
		t.Fatalf("fence: %v", err)
	}

	// Attach a fake control-event sink so we can observe the ack emit
	// without standing up the gRPC AdapterEvents stream.
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
	waitBarrierWaiting(t, s, "s1")
	if !s.isQuiescedForBarrier("s1") {
		t.Fatal("barrier must hold quiescence while it waits for the stream")
	}
	gate := sessionGate(t, s, "s1")
	if !gate.link("gw-ckpt-1") {
		t.Fatal("Checkpoint stream could not link into the open barrier gate")
	}
	gate.complete()

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
	if s.isQuiescedForBarrier("s1") {
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
		// AdapterEvents uses.
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

// TestCheckpointBarrierQuiescedMsIsTimeToQuiescence pins §10.1.8:
// quiesced_ms is the time to reach quiescence measured inside the ack window,
// not the full hold duration across the gateway-driven Checkpoint stream. The
// barrier holds quiescence open for a wall-clock span before the stream
// links and terminates; a pre-fix barrier measured time.Since(startedAt)
// after that hold and reported the whole window instead.
//
// spec: §10.1.
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

	waitBarrierWaiting(t, s, "s1")
	// Hold the gateway-driven stream open well past any plausible
	// time-to-quiescence before linking its id and completing it.
	const hold = 200 * time.Millisecond
	time.Sleep(hold)
	gate := sessionGate(t, s, "s1")
	gate.link("gw-ckpt-1")
	gate.complete()

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
// §10.1 — partial-capture path.
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

// newCoTenantServer returns a Server with both sessions bound and started,
// modelling a concurrency-enabled pod holding two co-tenant sessions. The
// per-slot trees are rooted under the test's own temp dir so the two
// entries own disjoint filesystem state.
func newCoTenantServer(t *testing.T, sessions ...string) *Server {
	t.Helper()
	base := t.TempDir()
	s := New("co-tenant")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	for _, id := range sessions {
		if err := s.claimSessionForTest(id); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
	}
	return s
}

// spec: §10.1.2 (the pod records and compares the coordination generation
// per bound session), §10.1.8 (the barrier gate reads that session's
// value) — a co-tenant session's fence at a generation below another
// session's is accepted on its own entry, its barrier at that generation
// is accepted, and the first session's recorded value is untouched.
//
// The pre-fix pod held one generation for the whole process, so the second
// session's fence at 2 was refused as coordinator_handoff_stale against
// the first session's 7 and its barrier at 2 was refused for not matching.
func TestCoTenantFenceRecordsPerSessionGeneration_spec_10_1_2(t *testing.T) {
	s := newCoTenantServer(t, "sess-a", "sess-b")
	ctx := context.Background()

	if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-a"}, CoordinationGeneration: 7,
	}); err != nil {
		t.Fatalf("fence sess-a to 7: %v", err)
	}
	resp, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-b"}, CoordinationGeneration: 2,
	})
	if err != nil {
		t.Fatalf("fence sess-b to 2 (a co-tenant's lower generation is not stale): %v", err)
	}
	if !resp.GetAccepted() || resp.GetGapDetected() {
		t.Fatalf("sess-b first fence = %+v, want accepted without a gap", resp)
	}
	if got := s.LastFencedGeneration("sess-b"); got != 2 {
		t.Fatalf("sess-b last fenced generation = %d, want 2", got)
	}
	if got := s.LastFencedGeneration("sess-a"); got != 7 {
		t.Fatalf("sess-a last fenced generation = %d, want 7 (a co-tenant's fence records nothing for it)", got)
	}

	// sess-b's barrier at its own generation is accepted. No Checkpoint
	// stream is driven, so the barrier returns an empty ref on its window.
	bctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := s.CheckpointBarrier(bctx, &adapterv1.CheckpointBarrierRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-b"}, BarrierId: "b-b", CoordinationGeneration: 2,
	}); err != nil {
		t.Fatalf("barrier for sess-b at its own generation 2: %v", err)
	}
	if got := s.LastFencedGeneration("sess-a"); got != 7 {
		t.Fatalf("sess-a last fenced generation after sess-b's barrier = %d, want 7", got)
	}
}

// spec: §10.1.2 (the first fence within a session's binding on the pod is
// exempt from gap detection), §10.1.8 — a co-tenant's first fence is not a
// gap however far it sits above another session's recorded value, because
// the exemption's unit is the session's binding rather than the pod's
// lifetime.
//
// The pre-fix pod set one initialized flag for the whole process, so the
// first fence anywhere on the pod made every later co-tenant's first fence
// report a gap.
func TestCoTenantFirstFenceIsNotAGap_spec_10_1_2(t *testing.T) {
	logBuf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s := newCoTenantServer(t, "sess-a", "sess-b")
	ctx := context.Background()
	if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-a"}, CoordinationGeneration: 7,
	}); err != nil {
		t.Fatalf("fence sess-a to 7: %v", err)
	}
	resp, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-b"}, CoordinationGeneration: 9,
	})
	if err != nil {
		t.Fatalf("fence sess-b to 9: %v", err)
	}
	if resp.GetGapDetected() {
		t.Fatalf("sess-b's first fence reported a gap: %+v", resp)
	}
	if strings.Contains(logBuf.String(), "coordinator_generation_gap") {
		t.Fatalf("sess-b's first fence logged coordinator_generation_gap: %s", logBuf.String())
	}
	// The gap predicate still holds inside one session's own lineage.
	resp, err = s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-b"}, CoordinationGeneration: 12,
	})
	if err != nil {
		t.Fatalf("second fence for sess-b: %v", err)
	}
	if !resp.GetGapDetected() {
		t.Fatalf("a skip within sess-b's own lineage must report a gap: %+v", resp)
	}
}

// spec: §10.1.8 (the barrier's session gate), §10.1.2 — a
// CheckpointBarrier naming a session the pod holds no slot binding for is
// refused with FailedPrecondition and the not-assigned detail, whatever
// generation it carries. The session's registry entry exists (the §4.7
// workspace-prep RPCs created it) but is unbound, so the bound-session
// guard runs ahead of the barrier_id check, the positive-generation check,
// and the generation gate.
//
// The guard is the barrier path's only refusal for a session the pod holds
// no fenced generation for, so it fails closed for an unbound session.
func TestCheckpointBarrierRefusesUnboundSession_spec_10_1_8(t *testing.T) {
	s := newCoTenantServer(t, "sess-a")
	if err := s.RegisterUnboundSlotForTest("sess-b"); err != nil {
		t.Fatalf("register unbound slot: %v", err)
	}
	for _, gen := range []int64{0, 1, 7} {
		_, err := s.CheckpointBarrier(context.Background(), &adapterv1.CheckpointBarrierRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-b"}, BarrierId: "b1", CoordinationGeneration: gen,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("gen %d: expected FailedPrecondition for an unbound session, got %v", gen, err)
		}
		if !strings.Contains(err.Error(), "session sess-b is not assigned to this pod") {
			t.Fatalf("gen %d: expected the not-assigned detail, got %v", gen, err)
		}
	}
}

// spec: §10.1.8 (each quiesced session's barrier echoes the checkpoint id
// its own Checkpoint stream carried), §10.1.2 — two co-tenant sessions
// drained together hold independent barrier gates: each link and complete
// reaches only the barrier of the session whose stream carried it, a
// session holding no open gate ignores the other session's link, and each
// ack carries its own stream's id.
//
// Against the pre-fix pod-wide gate the second open() replaced the first
// barrier's channel, checkpoint id, and signal, so the first barrier
// blocked to its ack deadline and returned an empty or cross-linked ref.
func TestCoTenantBarrierGatesAreIndependent_spec_10_1_8(t *testing.T) {
	s := newCoTenantServer(t, "sess-a", "sess-b")
	ctx := context.Background()
	for _, id := range []string{"sess-a", "sess-b"} {
		if _, err := s.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
			SessionId: &adapterv1.SessionId{Value: id}, CoordinationGeneration: 4,
		}); err != nil {
			t.Fatalf("fence %s: %v", id, err)
		}
	}

	type result struct {
		resp *adapterv1.CheckpointBarrierResponse
		err  error
	}
	results := map[string]chan result{
		"sess-a": make(chan result, 1),
		"sess-b": make(chan result, 1),
	}
	for _, id := range []string{"sess-a", "sess-b"} {
		go func(id string) {
			resp, err := s.CheckpointBarrier(ctx, &adapterv1.CheckpointBarrierRequest{
				SessionId: &adapterv1.SessionId{Value: id}, BarrierId: "b-" + id, CoordinationGeneration: 4,
			})
			results[id] <- result{resp, err}
		}(id)
	}
	waitBarrierWaiting(t, s, "sess-a")
	waitBarrierWaiting(t, s, "sess-b")

	// A session that holds no open gate ignores a link.
	if err := s.RegisterUnboundSlotForTest("sess-c"); err != nil {
		t.Fatalf("register unbound slot: %v", err)
	}
	if sessionGate(t, s, "sess-c").link("gw-ckpt-c") {
		t.Fatal("a session holding no open gate linked a checkpoint id")
	}

	// Each stream links and completes its own session's gate.
	for _, id := range []string{"sess-a", "sess-b"} {
		gate := sessionGate(t, s, id)
		if !gate.link("gw-ckpt-" + id) {
			t.Fatalf("%s: Checkpoint stream could not link into its own open gate", id)
		}
		gate.complete()
	}
	for _, id := range []string{"sess-a", "sess-b"} {
		got := <-results[id]
		if got.err != nil {
			t.Fatalf("%s barrier: %v", id, got.err)
		}
		if want := "gw-ckpt-" + id; got.resp.GetCheckpointRef() != want {
			t.Fatalf("%s checkpoint_ref = %q, want %q (its own stream's id)", id, got.resp.GetCheckpointRef(), want)
		}
		if got.resp.GetBarrierId() != "b-"+id {
			t.Fatalf("%s barrier_id = %q", id, got.resp.GetBarrierId())
		}
	}
}

// spec: §10.1.2 (the pod holds a fenced generation per bound session, and
// holds none for a session it has no recorded value for), §10.1.8 — the
// per-session reads report zero and false for a session the registry does
// not hold at all, so a caller reading a session that never bound to this
// pod fails closed rather than inheriting a co-tenant's state.
func TestPerSessionReadsAreEmptyForAnUnheldSession_spec_10_1_2(t *testing.T) {
	s := newCoTenantServer(t, "sess-a")
	if _, err := s.CoordinatorFence(context.Background(), &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-a"}, CoordinationGeneration: 7,
	}); err != nil {
		t.Fatalf("fence sess-a: %v", err)
	}
	if got := s.LastFencedGeneration("sess-unknown"); got != 0 {
		t.Errorf("last fenced generation for a session the pod does not hold = %d, want 0", got)
	}
	if s.isQuiescedForBarrier("sess-unknown") {
		t.Error("a session the pod does not hold reported as quiesced")
	}
	if s.BarrierWaiting("sess-unknown") {
		t.Error("a session the pod does not hold reported a waiting barrier")
	}
}
