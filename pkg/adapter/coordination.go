// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// coordinationState tracks the adapter-side §10.1 coordinator generation
// gate. The gateway installs a new generation via CoordinatorFence on
// every handoff; the adapter rejects any later RPC carrying a strictly
// older generation. The first fence on a pod's lifetime is never a gap,
// regardless of value (a replacement pod can be fenced into an existing
// session's generation).
//
// spec: §4.7 line 632, §10.1 lines 33-37, 50-52.
type coordinationState struct {
	mu sync.Mutex
	// lastFenced is the most recent generation the adapter accepted via
	// CoordinatorFence. Zero when no fence has been installed.
	lastFenced int64
	// initialized reports whether at least one fence has landed; the
	// first fence is exempt from gap detection.
	initialized bool
	// quiesced bounces a §10.1 line 631 quiesce signal off the local
	// state so the adapter can refuse new operational RPCs while a
	// barrier is in flight. The check is currently advisory — the
	// quiesce-strict enforcement against StartSession/SendMessage/etc.
	// lives in the broader §10.1 horizontal-scaling phase (F-10.1.6).
	quiesced bool
}

// LastFencedGeneration returns the most recent generation the adapter
// accepted via CoordinatorFence, or zero before the first fence lands.
// Exposed for tests.
func (s *Server) LastFencedGeneration() int64 {
	s.coord.mu.Lock()
	defer s.coord.mu.Unlock()
	return s.coord.lastFenced
}

// isQuiesced reports whether the adapter is holding the §10.1 barrier
// quiesced state. Exposed for tests.
func (s *Server) isQuiescedForBarrier() bool {
	s.coord.mu.Lock()
	defer s.coord.mu.Unlock()
	return s.coord.quiesced
}

// BarrierWaiting reports whether a CheckpointBarrier RPC is currently
// holding quiescence open, waiting for the gateway-driven Checkpoint
// stream to link its checkpoint_id and terminate. A caller that observes
// true can drive the Checkpoint stream against the held pod and know the
// barrier's gate is open, so the stream's CheckpointStart links (§10.1
// lines 163-172). Exposed for tests.
func (s *Server) BarrierWaiting() bool {
	s.barrier.mu.Lock()
	defer s.barrier.mu.Unlock()
	return s.barrier.waiting
}

// CoordinatorFence records the new coordination generation on the pod
// per §4.7 line 632 / §10.1 lines 33-37. The fence is the precondition
// the §10.1 handoff protocol uses to close the split-brain window: from
// this point the adapter rejects any RPC carrying a strictly older
// generation with FailedPrecondition + a `coordinator_handoff_stale`
// detail. The first fence on a pod's lifetime is recorded regardless of
// value (a replacement pod can be fenced into an existing session's
// generation); subsequent fences must be strictly greater. Skipping one
// or more generations triggers the §10.1 line 36 gap-detection path:
// the adapter logs a `coordinator_generation_gap` event and returns
// GapDetected: true while still acknowledging the new generation. The
// §10.1 line 36 in-flight-RPC cancellation is an unimplemented spec
// requirement the adapter does not currently perform.
//
// spec: §4.7 line 632, §10.1 lines 33-37.
func (s *Server) CoordinatorFence(ctx context.Context, req *adapterv1.CoordinatorFenceRequest) (*adapterv1.CoordinatorFenceResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "CoordinatorFence requires a session id")
	}
	if err := s.checkSession(sessionID); err != nil {
		return nil, err
	}
	gen := req.GetCoordinationGeneration()
	if gen <= 0 {
		return nil, status.Error(codes.InvalidArgument, "CoordinatorFence requires a positive coordination_generation")
	}

	s.coord.mu.Lock()
	defer s.coord.mu.Unlock()
	if s.coord.initialized && gen <= s.coord.lastFenced {
		// Stale fence: the gateway must re-read Postgres and re-issue.
		// Not a gap — the new coordinator's value is older than ours.
		return &adapterv1.CoordinatorFenceResponse{
				Accepted:             false,
				LastFencedGeneration: s.coord.lastFenced,
			}, status.Errorf(codes.FailedPrecondition,
				"coordinator_handoff_stale: requested generation %d <= last fenced %d", gen, s.coord.lastFenced)
	}
	gap := s.coord.initialized && gen > s.coord.lastFenced+1
	if gap {
		// §10.1 line 36 — a skipped generation logs
		// `coordinator_generation_gap` and reports GapDetected so the
		// caller can react. The spec's in-flight-RPC cancellation is a
		// requirement the adapter does not currently implement.
		slog.WarnContext(ctx, "coordinator_generation_gap",
			"session_id", sessionID,
			"last_fenced_generation", s.coord.lastFenced,
			"new_generation", gen)
	}
	prev := s.coord.lastFenced
	s.coord.lastFenced = gen
	s.coord.initialized = true
	_ = prev

	// spec: §10.1 line 49 — a successful fence from a new coordinator is
	// the only way out of hold state. exitHoldState locks only hold.mu, so
	// calling it while coord.mu is held is deadlock-free (enterHoldState
	// reads the generation through the accessor before taking hold.mu, and
	// the hold timeout never reaches back into coord.mu).
	s.exitHoldState()

	return &adapterv1.CoordinatorFenceResponse{
		Accepted:             true,
		LastFencedGeneration: gen,
		GapDetected:          gap,
	}, nil
}

// barrierGate coordinates the §10.1 quiesce-and-hold CheckpointBarrier RPC
// with the gateway-driven Checkpoint stream. CheckpointBarrier opens the
// gate, holds quiescence, and blocks on the gate's done channel; the
// Checkpoint stream handler links its gateway-minted checkpoint_id into an
// open gate on CheckpointStart and signals the gate when the stream
// terminates. The barrier then returns the ack echoing that checkpoint_id.
//
// spec: §10.1 lines 163-172 — the adapter holds quiescence, the gateway
// drives the Checkpoint stream against the held pod, and the ack the
// adapter returns after that stream terminates is the completion signal.
type barrierGate struct {
	mu           sync.Mutex
	waiting      bool
	checkpointID string
	done         chan struct{}
	signaled     bool
}

// open marks a barrier waiting and returns the channel the linked stream
// closes on termination.
func (g *barrierGate) open() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.waiting = true
	g.checkpointID = ""
	g.signaled = false
	g.done = make(chan struct{})
	return g.done
}

// release clears the waiting flag and returns the checkpoint_id the linked
// stream recorded (empty when no stream linked before the barrier window
// closed).
func (g *barrierGate) release() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.waiting = false
	return g.checkpointID
}

// link records id into an open gate and reports whether it linked (a
// barrier is waiting). The Checkpoint stream calls it on CheckpointStart.
func (g *barrierGate) link(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.waiting {
		return false
	}
	g.checkpointID = id
	return true
}

// complete signals the gate that the linked stream terminated, waking a
// waiting barrier. It is idempotent and a no-op once the gate is released.
func (g *barrierGate) complete() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.waiting && !g.signaled {
		g.signaled = true
		close(g.done)
	}
}

// CheckpointBarrier implements the §4.7 line 631 / §10.1 graceful-drain
// barrier RPC as a quiesce-and-hold barrier. The adapter validates the
// request's coordination generation against the last fenced value,
// quiesces tool-call dispatch, and holds the quiesced state open while the
// gateway drives the Checkpoint stream against the held pod. It returns
// the ack only after that stream terminates, echoing the gateway-minted
// checkpoint_id the stream carried and reporting the time-to-quiescence in
// quiesced_ms. The ack is mirrored onto the §4.7 line 660 control stream.
//
// spec: §4.7 line 631, §10.1 lines 163-172.
func (s *Server) CheckpointBarrier(ctx context.Context, req *adapterv1.CheckpointBarrierRequest) (*adapterv1.CheckpointBarrierResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "CheckpointBarrier requires a session id")
	}
	if err := s.checkSession(sessionID); err != nil {
		return nil, err
	}
	barrierID := req.GetBarrierId()
	if barrierID == "" {
		return nil, status.Error(codes.InvalidArgument, "CheckpointBarrier requires a barrier_id")
	}
	gen := req.GetCoordinationGeneration()
	if gen <= 0 {
		return nil, status.Error(codes.InvalidArgument, "CheckpointBarrier requires a positive coordination_generation")
	}

	// §10.1: the barrier shares the §10.1 line 34 generation gate the
	// CoordinatorFence installs. Reject when the gateway-supplied value
	// does not match the last fenced generation; the gateway re-issues
	// after the next fence.
	s.coord.mu.Lock()
	fenced := s.coord.lastFenced
	initialized := s.coord.initialized
	s.coord.mu.Unlock()
	if !initialized || gen != fenced {
		return nil, status.Errorf(codes.FailedPrecondition,
			"coordinator_handoff_stale: barrier generation %d does not match last fenced %d", gen, fenced)
	}

	// Quiesce and hold: the quiesced state stays open across the
	// gateway-driven Checkpoint stream and is released only on RPC return,
	// so no new tool-call dispatch runs while the gateway drives the
	// checkpoint (§10.1 line 163-166).
	startedAt := time.Now()
	s.coord.mu.Lock()
	s.coord.quiesced = true
	s.coord.mu.Unlock()
	defer func() {
		s.coord.mu.Lock()
		s.coord.quiesced = false
		s.coord.mu.Unlock()
	}()

	// Hold quiescence until the gateway-driven Checkpoint stream terminates
	// or the barrier's wall-clock window (the RPC context deadline the
	// gateway sets from checkpointBarrierAckTimeoutSeconds) expires. An
	// empty checkpoint_ref means the gateway drove no stream against the
	// pod within the window; the gateway then finalises a partial manifest.
	done := s.barrier.open()
	select {
	case <-done:
	case <-ctx.Done():
	}
	checkpointRef := s.barrier.release()
	elapsed := time.Since(startedAt).Milliseconds()

	resp := &adapterv1.CheckpointBarrierResponse{
		BarrierId:     barrierID,
		CheckpointRef: checkpointRef,
		QuiescedMs:    elapsed,
	}

	// Mirror the response onto the §4.7 line 660 control stream so the
	// gateway's barrier-target reconciler sees the ack without holding
	// the synchronous RPC open. Drop-tolerant: if no control stream is
	// attached the synchronous return is the gateway's signal.
	s.EmitCheckpointBarrierAck(barrierID, checkpointRef, elapsed)

	return resp, nil
}

// EmitCheckpointBarrierAck queues the §4.7 line 660 CheckpointBarrierAck
// onto the active gateway control stream. Drop-tolerant: when no stream
// is attached the synchronous CheckpointBarrier return value is the
// gateway's signal.
//
// spec: §4.7 line 660.
func (s *Server) EmitCheckpointBarrierAck(barrierID, checkpointRef string, quiescedMs int64) {
	s.emitControlEvent(controlEvent{
		Type:          eventCheckpointBarrierAck,
		BarrierID:     barrierID,
		CheckpointRef: checkpointRef,
		QuiescedMs:    quiescedMs,
	})
}
