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

// coordinationState tracks the §10.1 coordinator generation gate for one
// bound session. It lives on that session's slot registry entry, so the
// gateway's CoordinatorFence for one session records and compares nothing
// belonging to a co-tenant session on the same pod. The adapter rejects a
// later RPC for the session carrying a strictly older generation. The
// first fence within a session's binding on the pod is never a gap,
// regardless of value (a replacement pod, or a session rebound to this
// one, can be fenced into an existing generation).
//
// spec: §4.7, §10.1.2.
type coordinationState struct {
	mu sync.Mutex
	// lastFenced is the most recent generation the adapter accepted via
	// CoordinatorFence for this session. Zero when no fence has been
	// installed within this binding.
	lastFenced int64
	// initialized reports whether at least one fence has landed for this
	// session within this binding; that session's first fence is exempt
	// from both the stale rejection and gap detection. It moves with
	// lastFenced rather than staying pod-wide, because a pod-wide flag
	// would make every later co-tenant's first fence report a gap.
	initialized bool
	// quiesced bounces a §10.1 quiesce signal off the session's local
	// state so the adapter can refuse new operational RPCs while a
	// barrier is in flight. The check is currently advisory — the
	// quiesce-strict enforcement against StartSession/SendMessage/etc.
	// lives in the broader §10.1 horizontal-scaling phase (F-10.1.6).
	// The field's placement carries no claim about the unit of
	// quiescence; §10.1 states that separately.
	quiesced bool
}

// LastFencedGeneration returns the most recent generation the adapter
// accepted via CoordinatorFence for the named session, or zero for a
// session the pod holds no recorded value for (one no coordinator has
// fenced within its current binding, or one the registry does not hold at
// all). Exposed for tests. spec: §10.1.2.
func (s *Server) LastFencedGeneration(sessionID string) int64 {
	st := s.slotStateForSession(sessionID)
	if st == nil {
		return 0
	}
	return st.lastFencedGeneration()
}

// isQuiescedForBarrier reports whether the named session is holding the
// §10.1 barrier quiesced state. Exposed for tests.
func (s *Server) isQuiescedForBarrier(sessionID string) bool {
	st := s.slotStateForSession(sessionID)
	if st == nil {
		return false
	}
	st.coord.mu.Lock()
	defer st.coord.mu.Unlock()
	return st.coord.quiesced
}

// BarrierWaiting reports whether a CheckpointBarrier RPC for the named
// session is currently holding that session's quiescence open, waiting for
// the gateway-driven Checkpoint stream to link its checkpoint_id and
// terminate. A caller that observes true can drive the Checkpoint stream
// for that session against the held pod and know the session's barrier
// gate is open, so the stream's CheckpointStart links (§10.1.8). Exposed
// for tests.
func (s *Server) BarrierWaiting(sessionID string) bool {
	st := s.slotStateForSession(sessionID)
	if st == nil {
		return false
	}
	st.barrier.mu.Lock()
	defer st.barrier.mu.Unlock()
	return st.barrier.waiting
}

// CoordinatorFence records the new coordination generation for the
// session the request names per §4.7 / §10.1. The fence is the
// precondition the §10.1 handoff protocol uses to close the split-brain
// window: from this point the adapter rejects any RPC for that session
// carrying a strictly older generation with FailedPrecondition + a
// `coordinator_handoff_stale` detail. The first fence within a session's
// binding on the pod is recorded regardless of value (a replacement pod
// can be fenced into an existing session's generation); subsequent fences
// for that session must be strictly greater. Skipping one
// or more generations triggers the §10.1.2 gap-detection path:
// the adapter logs a `coordinator_generation_gap` event and returns
// GapDetected: true while still acknowledging the new generation. The
// §10.1.2 in-flight-RPC cancellation is an unimplemented spec
// requirement the adapter does not currently perform.
//
// spec: §4.7, §10.1.
func (s *Server) CoordinatorFence(ctx context.Context, req *adapterv1.CoordinatorFenceRequest) (*adapterv1.CoordinatorFenceResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "CoordinatorFence requires a session id")
	}
	// spec: §10.1.2 — the entry is resolved once, under the bound-session
	// guard, and held for the life of the call. A second lookup by session
	// identifier would race the deregistration paths, which delete the map
	// key while returning the pointer with no field zeroed.
	st, err := s.boundSlotState(sessionID)
	if err != nil {
		return nil, err
	}
	gen := req.GetCoordinationGeneration()
	if gen <= 0 {
		return nil, status.Error(codes.InvalidArgument, "CoordinatorFence requires a positive coordination_generation")
	}

	st.coord.mu.Lock()
	defer st.coord.mu.Unlock()
	if st.coord.initialized && gen <= st.coord.lastFenced {
		// Stale fence: the gateway must re-read Postgres and re-issue.
		// Not a gap — the new coordinator's value is older than ours.
		return &adapterv1.CoordinatorFenceResponse{
				Accepted:             false,
				LastFencedGeneration: st.coord.lastFenced,
			}, status.Errorf(codes.FailedPrecondition,
				"coordinator_handoff_stale: requested generation %d <= last fenced %d", gen, st.coord.lastFenced)
	}
	gap := st.coord.initialized && gen > st.coord.lastFenced+1
	if gap {
		// §10.1.2 — a skipped generation in this session's own lineage
		// logs `coordinator_generation_gap` and reports GapDetected so the
		// caller can react. The spec's in-flight-RPC cancellation is a
		// requirement the adapter does not currently implement.
		slog.WarnContext(ctx, "coordinator_generation_gap",
			"session_id", sessionID,
			"last_fenced_generation", st.coord.lastFenced,
			"new_generation", gen)
	}
	st.coord.lastFenced = gen
	st.coord.initialized = true

	// spec: §10.1.4 — a successful fence from a new coordinator is the
	// only way out of the pod-scoped hold state, and a fence for any bound
	// session on the pod is that exit. The lock order is the registry
	// lock, then the entry lock, then the hold lock: exitHoldState locks
	// only hold.mu, so calling it while this entry's coord.mu is held is
	// deadlock-free, and the hold timeout never reaches back into it.
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
// The gate lives on the session's slot registry entry, so two co-tenant
// sessions drained together each hold their own: the gateway opens the
// Checkpoint stream for each quiesced session concurrently with that
// session's own CheckpointBarrier RPC, and the ack echoes the checkpoint
// id that session's stream carried.
//
// spec: §10.1 — the adapter holds quiescence, the gateway
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

// CheckpointBarrier implements the §4.7 / §10.1 graceful-drain
// barrier RPC as a quiesce-and-hold barrier. The adapter validates the
// request's coordination generation against the last fenced value,
// quiesces tool-call dispatch, and holds the quiesced state open while the
// gateway drives the Checkpoint stream against the held pod. It returns
// the ack only after that stream terminates, echoing the gateway-minted
// checkpoint_id the stream carried and reporting the time-to-quiescence in
// quiesced_ms. The ack is mirrored onto the §4.7 control stream.
//
// spec: §4.7, §10.1.
func (s *Server) CheckpointBarrier(ctx context.Context, req *adapterv1.CheckpointBarrierRequest) (*adapterv1.CheckpointBarrierResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "CheckpointBarrier requires a session id")
	}
	// spec: §10.1.8 — one resolve, under the bound-session guard, held for
	// the life of the call including the deferred quiescence clear.
	st, err := s.boundSlotState(sessionID)
	if err != nil {
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

	// §10.1.2: the barrier shares the §10.1 generation gate the
	// CoordinatorFence installs for this session. Reject when the
	// gateway-supplied value does not match the generation the pod holds
	// for the named session; the gateway re-issues after the next fence.
	st.coord.mu.Lock()
	fenced := st.coord.lastFenced
	initialized := st.coord.initialized
	st.coord.mu.Unlock()
	if !initialized || gen != fenced {
		return nil, status.Errorf(codes.FailedPrecondition,
			"coordinator_handoff_stale: barrier generation %d does not match last fenced %d", gen, fenced)
	}

	// Quiesce and hold: the quiesced state stays open across the
	// gateway-driven Checkpoint stream and is released only on RPC return,
	// so no new tool-call dispatch runs while the gateway drives the
	// checkpoint (§10.1).
	startedAt := time.Now()
	st.coord.mu.Lock()
	st.coord.quiesced = true
	st.coord.mu.Unlock()
	// spec: §10.1.8 — quiesced_ms is the time-to-quiescence measured
	// inside the ack window, so it is captured the instant quiescence is
	// reached, not across the held gateway-driven upload that follows.
	quiescedMs := time.Since(startedAt).Milliseconds()
	defer func() {
		// The entry the call resolved, rather than a fresh lookup: a
		// Shutdown or a hold timeout can deregister the session while the
		// barrier is held, and the clear must still reach the entry this
		// call quiesced. spec: §10.1.8.
		st.coord.mu.Lock()
		st.coord.quiesced = false
		st.coord.mu.Unlock()
	}()

	// Hold quiescence until the gateway-driven Checkpoint stream terminates
	// or the barrier's wall-clock window (the RPC context deadline the
	// gateway sets from checkpointBarrierAckTimeoutSeconds) expires. An
	// empty checkpoint_ref means the gateway drove no stream against the
	// pod within the window; the gateway then finalises a partial manifest.
	done := st.barrier.open()
	select {
	case <-done:
	case <-ctx.Done():
	}
	checkpointRef := st.barrier.release()

	resp := &adapterv1.CheckpointBarrierResponse{
		BarrierId:     barrierID,
		CheckpointRef: checkpointRef,
		QuiescedMs:    quiescedMs,
	}

	// Mirror the response onto the §4.7 control stream so the
	// gateway's barrier-target reconciler sees the ack without holding
	// the synchronous RPC open. Drop-tolerant: if no control stream is
	// attached the synchronous return is the gateway's signal.
	s.EmitCheckpointBarrierAck(barrierID, checkpointRef, quiescedMs)

	return resp, nil
}

// EmitCheckpointBarrierAck queues the §4.7 CheckpointBarrierAck
// onto the active gateway control stream. Drop-tolerant: when no stream
// is attached the synchronous CheckpointBarrier return value is the
// gateway's signal.
//
// spec: §4.7.
func (s *Server) EmitCheckpointBarrierAck(barrierID, checkpointRef string, quiescedMs int64) {
	s.emitControlEvent(controlEvent{
		Type:          eventCheckpointBarrierAck,
		BarrierID:     barrierID,
		CheckpointRef: checkpointRef,
		QuiescedMs:    quiescedMs,
	})
}
