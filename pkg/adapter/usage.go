// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"sync"
	"time"

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
	// accumulated since the previous default read. The §4.7 ReportUsage
	// contract is incremental: an implementation reports the delta
	// since the last-read watermark and advances the watermark to the
	// value it returns.
	Usage(ctx context.Context, sessionID string) (Usage, error)
	// Cumulative returns the session's running cumulative total and
	// advances the last-read watermark to that total. spec: §11.2 —
	// a reconnected gateway replica pulls the cumulative total to seed
	// its restored counter via the crash-recovery MAX rule; advancing
	// the watermark to the returned total makes the replica's first
	// steady-state delta read return zero rather than re-adding the
	// already-recovered tokens (no double-count).
	Cumulative(ctx context.Context, sessionID string) (Usage, error)
}

// SessionUsageMeter is the concrete §4.7 UsageMeter the adapter wires in
// direct mode. It accumulates the per-session token counts the runtime
// reports on each §4.7 llm_request_completed lifecycle frame (§11.2
// direct-mode usage) into a running cumulative total, and serves both
// the incremental delta read the gateway polls steady-state and the
// cumulative read a reconnected gateway replica pulls for the §11.2:46
// crash-recovery MAX rule.
//
// The adapter (not the gateway) holds the last-read watermark across a
// gateway crash. A cumulative read advances the watermark to the total
// it returns, so recovery and steady-state polling are a single
// accounting mechanism: after a replica seeds its counter from the
// cumulative total, the next steady-state delta returns zero and does
// not double-count. The watermark is never reset on rebind; a rebind is
// an ordinary coordinator handoff, so an unreset watermark after a
// cumulative recovery read is the correct state (§11.2:46, §10.1).
type SessionUsageMeter struct {
	// now returns the current time. Injected so a unit test can drive
	// the wall-clock accounting deterministically; nil selects time.Now.
	now func() time.Time

	// mu guards sessions. It protects the per-session cumulative totals
	// and last-read watermarks: every read (Usage, Cumulative) and every
	// accumulation (Add) mutates the entry under mu, so a concurrent
	// llm_request_completed frame and a gateway ReportUsage pull cannot
	// race on the same session's counters or watermark.
	mu       sync.Mutex
	sessions map[string]*sessionUsage
}

// sessionUsage is the running cumulative total and last-read watermark
// for one session. The cumulative fields only ever increase; a read
// returns the difference between the cumulative fields and the watermark
// and advances the watermark, so a delta is always non-negative.
type sessionUsage struct {
	cumInput  int64
	cumOutput int64
	// firstSeen is the time the session's first token-bearing frame
	// arrived; the cumulative wall-clock is measured from it.
	firstSeen time.Time
	// markInput/markOutput/markWallMS is the watermark advanced past on
	// the previous read. A default read returns (cum - mark).
	markInput  int64
	markOutput int64
	markWallMS int64
}

// NewSessionUsageMeter returns an empty SessionUsageMeter. Pass nil for
// now to use time.Now; tests inject a clock for deterministic
// wall-clock accounting.
func NewSessionUsageMeter(now func() time.Time) *SessionUsageMeter {
	return &SessionUsageMeter{
		now:      now,
		sessions: map[string]*sessionUsage{},
	}
}

// clock returns the meter's time source, defaulting to time.Now.
func (m *SessionUsageMeter) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// Add folds one llm_request_completed frame's token counts into
// sessionID's running cumulative total. Negative counts are clamped to
// zero so a malformed frame cannot decrement a session's total. The
// first token-bearing frame stamps the session's wall-clock origin.
// spec: §4.7 (llm_request_completed token fields), §11.2 (direct-mode
// usage).
func (m *SessionUsageMeter) Add(sessionID string, inputTokens, outputTokens int64) {
	if sessionID == "" {
		return
	}
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	su := m.sessions[sessionID]
	if su == nil {
		su = &sessionUsage{firstSeen: m.clock()}
		m.sessions[sessionID] = su
	}
	su.cumInput += inputTokens
	su.cumOutput += outputTokens
}

// Usage returns sessionID's accounting accumulated since the previous
// read and advances the watermark to the current cumulative total, so
// the next read reports only newly-accumulated tokens. An unread or
// unknown session reports a zero delta. spec: §4.7 (ReportUsage
// incremental contract).
func (m *SessionUsageMeter) Usage(_ context.Context, sessionID string) (Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readLocked(sessionID, false), nil
}

// Cumulative returns sessionID's running cumulative total and advances
// the watermark to that total, so a reconnected gateway replica seeds
// its restored counter from the cumulative total (the §11.2:46
// crash-recovery MAX rule) and its first subsequent delta read returns
// zero rather than re-adding the recovered tokens. An unknown session
// reports zero. spec: §11.2 (pod-reported cumulative-usage re-report on
// reconnection).
func (m *SessionUsageMeter) Cumulative(_ context.Context, sessionID string) (Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readLocked(sessionID, true), nil
}

// readLocked computes a read for sessionID and advances the watermark.
// When cumulative is true it returns the running total; otherwise it
// returns the delta since the last watermark. Either way the watermark
// is advanced to the current cumulative total, so a cumulative read
// followed by a default read reports zero (no double-count). Callers
// hold m.mu.
func (m *SessionUsageMeter) readLocked(sessionID string, cumulative bool) Usage {
	su := m.sessions[sessionID]
	if su == nil {
		return Usage{}
	}
	wallMS := int64(0)
	if !su.firstSeen.IsZero() {
		wallMS = m.clock().Sub(su.firstSeen).Milliseconds()
		if wallMS < 0 {
			wallMS = 0
		}
	}
	var out Usage
	if cumulative {
		out = Usage{
			InputTokens:  su.cumInput,
			OutputTokens: su.cumOutput,
			WallClockMS:  wallMS,
		}
	} else {
		out = Usage{
			InputTokens:  su.cumInput - su.markInput,
			OutputTokens: su.cumOutput - su.markOutput,
			WallClockMS:  wallMS - su.markWallMS,
		}
	}
	su.markInput = su.cumInput
	su.markOutput = su.cumOutput
	su.markWallMS = wallMS
	return out
}

// sessionTokenSink bridges the lifecycle channel's session-less
// tokenSink to a per-session SessionUsageMeter. The lifecycle frame
// carries no session id (§6.1 one session per pod), so the sink resolves
// the pod's current session at fold time and keys the meter by it. A
// token frame that arrives while the pod is idle (no assigned session)
// is dropped, since it belongs to no session.
type sessionTokenSink struct {
	meter          *SessionUsageMeter
	currentSession func() string
}

// AddTokens folds the counts into the pod's current session's total.
func (s sessionTokenSink) AddTokens(inputTokens, outputTokens int64) {
	s.meter.Add(s.currentSession(), inputTokens, outputTokens)
}

// NewSessionTokenSink returns the lifecycle-channel token sink that
// folds llm_request_completed token counts into meter under the pod's
// current session id. cmd/lenny-adapter wires it via
// LifecycleChannel.SetUsageSink.
func NewSessionTokenSink(meter *SessionUsageMeter, currentSession func() string) tokenSink {
	return sessionTokenSink{meter: meter, currentSession: currentSession}
}

// ReportUsage implements the §4.7 ReportUsage RPC. The gateway calls it
// to pull a session's token and time accounting for §11.2 budget
// enforcement and billing. The default read is incremental — each call
// reports usage accumulated since the previous read. When the request
// sets cumulative, the read returns the session's running cumulative
// total instead, which a reconnected gateway replica uses to seed its
// restored counter for the §11.2:46 crash-recovery MAX rule.
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
	read := s.Usage.Usage
	if req.GetCumulative() {
		read = s.Usage.Cumulative
	}
	u, err := read(ctx, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read usage: %v", err)
	}
	return &adapterv1.ReportUsageResponse{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		WallClockMs:  u.WallClockMS,
	}, nil
}
