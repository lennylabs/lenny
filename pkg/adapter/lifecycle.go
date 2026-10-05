// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// Interrupt asks the pod's runtime to pause the session (§4.7). A clean
// interrupt goes over CH-RUNTIMEOPS when the runtime declared it, and the
// runtime acknowledges at a safe stop point. Otherwise, and for a hard
// interrupt, the handler calls Runtime.Interrupt, whose effect is the
// transport's: the developer-loop executor signals its process, and the
// sidecar transport sends no signal because the runtime process lives as
// long as the pod. The request's deadline_ms grace window is tracked by
// the gateway. spec: §4.7; §15.4.3; §4.7.10 (Runtime process lifetime).
func (s *Server) Interrupt(ctx context.Context, req *adapterv1.InterruptRequest) (*adapterv1.InterruptResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "Interrupt requires a session id")
	}
	mode := req.GetMode()
	if mode == adapterv1.InterruptRequest_MODE_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "Interrupt requires a mode")
	}
	st, err := s.boundSlotState(sessionID)
	if err != nil {
		return nil, err
	}

	// §4.7: a clean interrupt of a Full-level runtime is delivered over
	// CH-RUNTIMEOPS; the runtime acknowledges at a safe stop point. A
	// hard interrupt, or any runtime without CH-RUNTIMEOPS, calls
	// Runtime.Interrupt, and the sidecar transport sends no signal. The
	// branch is decided before the op lock, because the CH-RUNTIMEOPS
	// branch first waits for the session's session_started, and a wait
	// under s.ops would block every co-tenant's checkpoint and interrupt.
	// spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
	viaRuntimeOps := mode == adapterv1.InterruptRequest_MODE_CLEAN && s.Lifecycle != nil && s.Lifecycle.Supports("interrupt")
	if viaRuntimeOps {
		if resp, gerr := s.awaitInterruptGate(ctx, st, req.GetDeadlineMs()); resp != nil || gerr != nil {
			return resp, gerr
		}
	}

	// §4.7: Checkpoint and Interrupt are serialized pod-wide. An interrupt
	// is pod-scoped, so it is recorded in the lock's pending-interrupt
	// state rather than as a key of the pending checkpoint set, and it
	// holds the whole-pod queue while it waits. It is never admitted
	// while a checkpoint is pending, so it never coexists with pending
	// per-session checkpoints. A rejected interrupt returns a BUSY
	// status the gateway retries.
	release, err := s.ops.Begin(ctx, opInterrupt, "")
	if err != nil {
		if errors.Is(err, errOpBusy) {
			return &adapterv1.InterruptResponse{Status: adapterv1.InterruptResponse_STATUS_BUSY}, nil
		}
		return nil, status.FromContextError(err).Err()
	}
	defer release()

	if viaRuntimeOps {
		// The gate is read again without waiting once the op lock is held:
		// the interrupt can queue behind a co-tenant's whole upload, and the
		// session can end in that interval.
		if gerr := s.sessionStartedNow(st); gerr != nil {
			return interruptGateRefused(ctx)
		}
		return s.interruptViaLifecycle(ctx, sessionID, req)
	}
	if err := s.Runtime.Interrupt(ctx, sessionID, mode == adapterv1.InterruptRequest_MODE_HARD); err != nil {
		return nil, status.Errorf(codes.Internal, "interrupt runtime: %v", err)
	}
	return &adapterv1.InterruptResponse{
		Acknowledged: true,
		Status:       adapterv1.InterruptResponse_STATUS_ACKNOWLEDGED,
	}, nil
}

// SignalDeadline forwards the §11.3 pre-expiry warning to the
// running session's runtime as a DEADLINE_APPROACHING frame over the
// CH-RUNTIMEOPS. The gateway watchdog fires it five minutes before
// `maxSessionAge` so the agent can checkpoint. A Basic/Standard runtime
// has no CH-RUNTIMEOPS (§15: "no CH-RUNTIMEOPS to
// deliver DEADLINE_APPROACHING"); the adapter reports delivered=false
// rather than erroring so the watchdog's best-effort warning never fails.
// The signal is one-way — unlike Interrupt it does not take the per-session
// op lock or wait for an acknowledgement.
//
// The frame is written only after the adapter has read the session's
// session_started. The wait is bounded by the frame's remainingMs, or by
// SessionStartAckTimeout when remainingMs is not positive, and holds no
// lock; a gate that does not admit the frame reports delivered=false.
// spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
func (s *Server) SignalDeadline(ctx context.Context, req *adapterv1.SignalDeadlineRequest) (*adapterv1.SignalDeadlineResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "SignalDeadline requires a session id")
	}
	st, err := s.boundSlotState(sessionID)
	if err != nil {
		return nil, err
	}
	if s.Lifecycle == nil || !s.Lifecycle.Supports("deadline_signal") {
		// spec: §15.4.3 — without CH-RUNTIMEOPS the runtime receives no
		// advance notice of the session's expiry.
		return &adapterv1.SignalDeadlineResponse{Delivered: false}, nil
	}
	wctx, cancel := s.gateBound(ctx, time.Duration(req.GetRemainingMs())*time.Millisecond)
	defer cancel()
	if err := s.awaitSessionStarted(wctx, st); err != nil {
		return &adapterv1.SignalDeadlineResponse{Delivered: false}, nil
	}
	trigger := req.GetTrigger()
	if trigger == "" {
		trigger = "session_age"
	}
	// spec: §28.5.3 (CH-RUNTIMEOPS, Messages) — the frame names the
	// session the warning concerns.
	if err := s.Lifecycle.SignalDeadlineApproaching(sessionID, req.GetRemainingMs(), trigger); err != nil {
		return nil, status.Errorf(codes.Internal, "signal deadline: %v", err)
	}
	return &adapterv1.SignalDeadlineResponse{Delivered: true}, nil
}

// awaitInterruptGate waits, before the op lock, for the session_started that
// orders the interrupt_request frame. The wait is bounded by the request's
// deadline_ms, as interruptViaLifecycle bounds the acknowledgement, or by
// SessionStartAckTimeout when deadline_ms is zero. It returns a nil response
// and a nil error when the gate admits the frame, and otherwise the answer
// interruptGateRefused gives.
// spec: §28.5.3 (CH-RUNTIMEOPS, Messages); §28.5.3 (CH-MSGSOCK, Outbound:
// session_started).
func (s *Server) awaitInterruptGate(ctx context.Context, st *slotState, deadlineMs int32) (*adapterv1.InterruptResponse, error) {
	wctx, cancel := s.gateBound(ctx, time.Duration(deadlineMs)*time.Millisecond)
	defer cancel()
	if err := s.awaitSessionStarted(wctx, st); err != nil {
		return interruptGateRefused(ctx)
	}
	return nil, nil
}

// interruptGateRefused is the answer to a clean interrupt whose
// interrupt_request the gate did not admit. The adapter writes no frame and
// answers as it answers an interrupt the runtime did not acknowledge within
// its deadline, INTERRUPT_TIMEOUT, so the gateway moves the session to
// suspended; a cancelled caller context is surfaced as a gRPC status, as
// interruptViaLifecycle surfaces it. spec: §4.7; §28.5.3 (CH-RUNTIMEOPS,
// Messages).
func interruptGateRefused(ctx context.Context) (*adapterv1.InterruptResponse, error) {
	if ctx.Err() != nil {
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return &adapterv1.InterruptResponse{Status: adapterv1.InterruptResponse_STATUS_INTERRUPT_TIMEOUT}, nil
}

// interruptViaLifecycle delivers a §4.7 clean interrupt over the
// CH-RUNTIMEOPS and waits for the runtime's acknowledgement,
// bounded by the request's deadline. A deadline elapsing with no
// acknowledgement is reported as INTERRUPT_TIMEOUT rather than an
// error: §4.7 has the gateway move the session to suspended regardless.
// The interrupt_request names sessionID, the session to stop.
// spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
func (s *Server) interruptViaLifecycle(ctx context.Context, sessionID string, req *adapterv1.InterruptRequest) (*adapterv1.InterruptResponse, error) {
	deadlineMs := req.GetDeadlineMs()
	ictx := ctx
	if deadlineMs > 0 {
		var cancel context.CancelFunc
		ictx, cancel = context.WithTimeout(ctx, time.Duration(deadlineMs)*time.Millisecond)
		defer cancel()
	}
	err := s.Lifecycle.RequestInterrupt(ictx, sessionID, newLifecycleID(), deadlineMs)
	if err == nil {
		return &adapterv1.InterruptResponse{
			Acknowledged: true,
			Status:       adapterv1.InterruptResponse_STATUS_ACKNOWLEDGED,
		}, nil
	}
	// A cancelled caller context means the gateway gave up; surface it
	// as a gRPC status rather than an interrupt outcome.
	if ctx.Err() != nil {
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &adapterv1.InterruptResponse{Status: adapterv1.InterruptResponse_STATUS_INTERRUPT_TIMEOUT}, nil
	}
	return nil, status.Errorf(codes.Internal, "lifecycle interrupt: %v", err)
}
