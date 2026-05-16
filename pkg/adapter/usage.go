// SPDX-License-Identifier: MIT

package adapter

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// Usage is a session's token and wall-clock accounting.
type Usage struct {
	// InputTokens is the prompt-token count.
	InputTokens int64
	// OutputTokens is the completion-token count.
	OutputTokens int64
	// WallClockMS is the elapsed runtime wall-clock time in
	// milliseconds.
	WallClockMS int64
}

// UsageMeter reports a session's resource accounting. The §4.7
// ReportUsage RPC reads it so the gateway can enforce §11.2 budgets
// and bill the session.
type UsageMeter interface {
	// Usage returns the token and wall-clock accounting for sessionID
	// accumulated since the previous call. The §4.7 ReportUsage
	// contract is incremental: an implementation reports the delta
	// since it was last read.
	Usage(ctx context.Context, sessionID string) (Usage, error)
}

// ReportUsage implements the §4.7 ReportUsage RPC. The gateway calls it
// to pull a session's token and time accounting for §11.2 budget
// enforcement and billing. The accounting is incremental — each call
// reports usage accumulated since the previous one.
func (s *Server) ReportUsage(ctx context.Context, req *adapterv1.ReportUsageRequest) (*adapterv1.ReportUsageResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "ReportUsage requires a session id")
	}
	if err := s.checkSession(sessionID); err != nil {
		return nil, err
	}
	if s.Usage == nil {
		return nil, status.Error(codes.Unimplemented,
			"adapter is not configured with a usage meter")
	}
	u, err := s.Usage.Usage(ctx, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read usage: %v", err)
	}
	return &adapterv1.ReportUsageResponse{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		WallClockMs:  u.WallClockMS,
	}, nil
}
