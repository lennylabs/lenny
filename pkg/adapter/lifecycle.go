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

// Interrupt signals the pod's runtime to pause (§4.7). A clean
// interrupt sends SIGTERM so the runtime can pause and checkpoint; a
// hard interrupt sends SIGKILL. The request's deadline_ms grace window
// is observed by the runtime and tracked by the gateway; the adapter
// delivers the signal and reports acknowledgement.
func (s *Server) Interrupt(ctx context.Context, req *adapterv1.InterruptRequest) (*adapterv1.InterruptResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "Interrupt requires a session id")
	}
	mode := req.GetMode()
	if mode == adapterv1.InterruptRequest_MODE_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "Interrupt requires a mode")
	}
	if err := s.checkSession(sessionID); err != nil {
		return nil, err
	}

	// §4.7: Checkpoint and Interrupt are serialized per session. A
	// rejected interrupt returns a BUSY status the gateway retries.
	release, err := s.ops.Begin(ctx, opInterrupt)
	if err != nil {
		if errors.Is(err, errOpBusy) {
			return &adapterv1.InterruptResponse{Status: adapterv1.InterruptResponse_STATUS_BUSY}, nil
		}
		return nil, status.FromContextError(err).Err()
	}
	defer release()

	// §4.7: a clean interrupt of a Full-level runtime is delivered over
	// the lifecycle channel; the runtime acknowledges at a safe stop
	// point. A hard interrupt, or any runtime without the lifecycle
	// channel, uses the signal path.
	if mode == adapterv1.InterruptRequest_MODE_CLEAN && s.Lifecycle != nil && s.Lifecycle.Supports("interrupt") {
		return s.interruptViaLifecycle(ctx, req)
	}
	if err := s.Runtime.Interrupt(ctx, sessionID, mode == adapterv1.InterruptRequest_MODE_HARD); err != nil {
		return nil, status.Errorf(codes.Internal, "interrupt runtime: %v", err)
	}
	return &adapterv1.InterruptResponse{
		Acknowledged: true,
		Status:       adapterv1.InterruptResponse_STATUS_ACKNOWLEDGED,
	}, nil
}

// SignalDeadline forwards the §11.3 line 240 pre-expiry warning to the
// running session's runtime as a DEADLINE_APPROACHING frame over the
// lifecycle channel. The gateway watchdog fires it five minutes before
// `maxSessionAge` so the agent can checkpoint. A Basic/Standard runtime
// has no lifecycle channel (§15 line 2141: "no lifecycle channel to
// deliver DEADLINE_APPROACHING"); the adapter reports delivered=false
// rather than erroring so the watchdog's best-effort warning never fails.
// The signal is one-way — unlike Interrupt it does not take the per-session
// op lock or wait for an acknowledgement.
func (s *Server) SignalDeadline(_ context.Context, req *adapterv1.SignalDeadlineRequest) (*adapterv1.SignalDeadlineResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "SignalDeadline requires a session id")
	}
	if err := s.checkSession(sessionID); err != nil {
		return nil, err
	}
	if s.Lifecycle == nil || !s.Lifecycle.Supports("deadline_signal") {
		// spec: §15 line 2141 — without the lifecycle channel the runtime
		// receives only `shutdown` at expiry with no advance notice.
		return &adapterv1.SignalDeadlineResponse{Delivered: false}, nil
	}
	trigger := req.GetTrigger()
	if trigger == "" {
		trigger = "session_age"
	}
	if err := s.Lifecycle.SignalDeadlineApproaching(req.GetRemainingMs(), trigger); err != nil {
		return nil, status.Errorf(codes.Internal, "signal deadline: %v", err)
	}
	return &adapterv1.SignalDeadlineResponse{Delivered: true}, nil
}

// interruptViaLifecycle delivers a §4.7 clean interrupt over the
// lifecycle channel and waits for the runtime's acknowledgement,
// bounded by the request's deadline. A deadline elapsing with no
// acknowledgement is reported as INTERRUPT_TIMEOUT rather than an
// error: §4.7 has the gateway move the session to suspended regardless.
func (s *Server) interruptViaLifecycle(ctx context.Context, req *adapterv1.InterruptRequest) (*adapterv1.InterruptResponse, error) {
	deadlineMs := req.GetDeadlineMs()
	ictx := ctx
	if deadlineMs > 0 {
		var cancel context.CancelFunc
		ictx, cancel = context.WithTimeout(ctx, time.Duration(deadlineMs)*time.Millisecond)
		defer cancel()
	}
	err := s.Lifecycle.RequestInterrupt(ictx, newLifecycleID(), deadlineMs)
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
