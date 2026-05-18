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

// DemoteSDK is the §4.7 SDK-warm demotion RPC. The gateway calls it on
// an SDK-warm pod to tear down a pre-connected SDK session before
// workspace materialization. It applies only to pods whose runtime
// declares capabilities.preConnect: true. This adapter serves pod-warm
// pods, which never pre-connect an SDK, so DemoteSDK is not applicable
// and returns Unimplemented as the §4.7 contract specifies for
// non-preConnect pods.
func (s *Server) DemoteSDK(_ context.Context, _ *adapterv1.DemoteSDKRequest) (*adapterv1.DemoteSDKResponse, error) {
	return nil, status.Error(codes.Unimplemented,
		"DemoteSDK applies only to SDK-warm pods that declare capabilities.preConnect: true; this is a pod-warm adapter")
}
