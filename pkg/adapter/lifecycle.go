// SPDX-License-Identifier: MIT

package adapter

import (
	"context"

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
	if err := s.Runtime.Interrupt(ctx, sessionID, mode == adapterv1.InterruptRequest_MODE_HARD); err != nil {
		return nil, status.Errorf(codes.Internal, "interrupt runtime: %v", err)
	}
	return &adapterv1.InterruptResponse{Acknowledged: true}, nil
}
