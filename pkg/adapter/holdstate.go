// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// defaultCoordinatorHoldTimeout is the §10.1 line 50
// coordinatorHoldTimeoutSeconds default: the window the adapter holds a
// session after losing its coordinator before it self-terminates.
const defaultCoordinatorHoldTimeout = 120 * time.Second

// reasonCoordinatorLost is the §10.1 line 50 AdapterTerminating /
// session.terminated reason the adapter reports when the hold times out
// without a new coordinator.
const reasonCoordinatorLost = "coordinator_lost"

// holdState tracks the §10.1 coordinator-loss hold the adapter enters
// when the coordinating gateway's control connection drops while a
// session is still live. While held, every operational RPC is rejected
// with UNAVAILABLE + a coordinator_hold detail; only a fresh
// CoordinatorFence from a new coordinator exits the hold. If no
// coordinator fences within coordinatorHoldTimeoutSeconds the adapter
// self-terminates the session.
//
// spec: §10.1 lines 47-52.
type holdState struct {
	mu      sync.Mutex
	active  bool
	timer   expiryTimerHandle
	session string
	gen     int64
}

// coordinatorHoldAllowedMethods is the §10.1 line 49 allowlist: the only
// inbound gRPC methods the adapter still serves while in hold state. A
// new coordinator negotiates, opens the control stream, and fences;
// health probes keep the pod live. Every other RPC is rejected with the
// coordinator_hold detail until the fence lands.
//
// spec: §10.1 line 49.
var coordinatorHoldAllowedMethods = map[string]bool{
	"/lenny.adapter.v1.Adapter/CoordinatorFence": true,
	"/lenny.adapter.v1.Adapter/NegotiateVersion": true,
	"/lenny.adapter.v1.Adapter/LifecycleChannel": true,
	"/grpc.health.v1.Health/Check":               true,
	"/grpc.health.v1.Health/Watch":               true,
}

// holdAfter schedules f to run after d through the injected test seam
// when set and time.AfterFunc otherwise.
func (s *Server) holdAfter(d time.Duration, f func()) expiryTimerHandle {
	if s.HoldAfterFunc != nil {
		return s.HoldAfterFunc(d, f)
	}
	return time.AfterFunc(d, f)
}

// coordinatorHoldTimeout returns the configured §10.1 line 50 hold
// timeout, or the 120s spec default.
func (s *Server) coordinatorHoldTimeout() time.Duration {
	if s.CoordinatorHoldTimeout > 0 {
		return s.CoordinatorHoldTimeout
	}
	return defaultCoordinatorHoldTimeout
}

// onCoordinatorChannelClosed is invoked when the gateway control stream
// (LifecycleChannel) ends. When the pod still holds an active session the
// closed stream is the §10.1 coordinator-loss signal: the coordinating
// gateway replica crashed or partitioned (the gRPC keepalive at
// 10s/5s — §11.3 lines 205-206 — surfaces the dead connection within one
// interval plus one timeout), so the adapter enters hold state and waits
// coordinatorHoldTimeoutSeconds for a new coordinator to fence. A closed
// stream on an idle pod, or one whose session a clean teardown already
// released, is not a loss.
//
// spec: §10.1 lines 47-52.
func (s *Server) onCoordinatorChannelClosed() {
	session := s.currentSession()
	if session == "" {
		return
	}
	s.enterHoldState(session)
}

// enterHoldState arms the §10.1 hold for session: it raises the
// coordinator-hold gauge, logs coordinator_connection_lost with the last
// known generation, and starts the hold-timeout timer. It is idempotent —
// a second close while already held is a no-op.
//
// spec: §10.1 lines 47-52.
func (s *Server) enterHoldState(session string) {
	// Read the generation through the accessor (which takes coord.mu)
	// before locking hold.mu so the two locks are never held together.
	gen := s.LastFencedGeneration()

	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	if s.hold.active {
		return
	}
	s.hold.active = true
	s.hold.session = session
	s.hold.gen = gen
	setCoordinatorHold(true)
	slog.Warn("coordinator_connection_lost",
		"session_id", session,
		"last_generation", gen)
	s.hold.timer = s.holdAfter(s.coordinatorHoldTimeout(), s.onHoldTimeout)
}

// exitHoldState clears the §10.1 hold after a new coordinator's
// CoordinatorFence lands: it stops the timeout timer and lowers the
// coordinator-hold gauge. It is a no-op when no hold is active.
//
// spec: §10.1 line 49 — a successful CoordinatorFence is the only way to
// exit hold state.
func (s *Server) exitHoldState() {
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	if !s.hold.active {
		return
	}
	s.hold.active = false
	if s.hold.timer != nil {
		s.hold.timer.Stop()
		s.hold.timer = nil
	}
	setCoordinatorHold(false)
	slog.Info("coordinator_hold_resolved", "session_id", s.hold.session)
}

// onHoldTimeout runs when no new coordinator fenced within
// coordinatorHoldTimeoutSeconds. It lowers the gauge, notifies the
// gateway via AdapterTerminating (which drops to the disk post-mortem
// when the control stream is gone, the usual case since the coordinator
// is exactly what was lost), and best-effort terminates the runtime so
// the agent process does not run unsupervised.
//
// spec: §10.1 line 50.
func (s *Server) onHoldTimeout() {
	s.hold.mu.Lock()
	if !s.hold.active {
		// A fence raced in and exited the hold; nothing to terminate.
		s.hold.mu.Unlock()
		return
	}
	s.hold.active = false
	s.hold.timer = nil
	session := s.hold.session
	gen := s.hold.gen
	setCoordinatorHold(false)
	s.hold.mu.Unlock()

	slog.Warn(reasonCoordinatorLost,
		"session_id", session,
		"last_generation", gen)

	// §10.1 line 50 — notify the gateway so it can transition the session
	// without waiting for the 60s orphan-session reconciler.
	s.EmitAdapterTerminating(reasonCoordinatorLost)
	s.writeHoldPostMortem(session, gen)

	// Best-effort graceful runtime termination.
	if s.Runtime != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = s.Runtime.Close(ctx, session)
		cancel()
	}
}

// inHoldState reports whether the adapter is currently in §10.1 hold
// state. The hold-state interceptors consult it on every inbound RPC.
func (s *Server) inHoldState() bool {
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	return s.hold.active
}

// writeHoldPostMortem records a §10.1 line 50 coordinator_lost
// post-mortem to PostMortemDir so the terminal cause survives even when
// no coordinator ever returns to receive the AdapterTerminating event.
// Best-effort: an empty dir skips the write, and an encode/write failure
// is logged rather than retried.
func (s *Server) writeHoldPostMortem(session string, gen int64) {
	if s.PostMortemDir == "" {
		return
	}
	rec := struct {
		SessionID      string `json:"sessionId"`
		Reason         string `json:"reason"`
		LastGeneration int64  `json:"lastGeneration"`
		TerminatedAt   string `json:"terminatedAt"`
	}{
		SessionID:      session,
		Reason:         reasonCoordinatorLost,
		LastGeneration: gen,
		TerminatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		slog.Error("coordinator_lost post-mortem encode failed",
			"session_id", session, "error", err)
		return
	}
	path := filepath.Join(s.PostMortemDir, "coordinator_lost-"+sanitizeSessionFilename(session)+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		slog.Error("coordinator_lost post-mortem write failed",
			"session_id", session, "path", path, "error", err)
	}
}

// sanitizeSessionFilename maps a session id to a filesystem-safe basename
// component so the post-mortem path cannot escape PostMortemDir.
func sanitizeSessionFilename(session string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, session)
}

// holdStateUnaryInterceptor rejects every non-allowlisted unary RPC with
// UNAVAILABLE + coordinator_hold while the adapter is in §10.1 hold
// state. CoordinatorFence, NegotiateVersion, and health checks pass
// through so a new coordinator can re-establish coordination.
//
// spec: §10.1 line 49.
func (s *Server) holdStateUnaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if s.inHoldState() && !coordinatorHoldAllowedMethods[info.FullMethod] {
		return nil, coordinatorHoldError(info.FullMethod)
	}
	return handler(ctx, req)
}

// holdStateStreamInterceptor is the streaming counterpart of
// holdStateUnaryInterceptor. The LifecycleChannel is allowlisted so a new
// coordinator can re-attach the control stream; Attach and
// PrepareWorkspace are rejected like any other operational RPC.
//
// spec: §10.1 line 49.
func (s *Server) holdStateStreamInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if s.inHoldState() && !coordinatorHoldAllowedMethods[info.FullMethod] {
		return coordinatorHoldError(info.FullMethod)
	}
	return handler(srv, ss)
}

// coordinatorHoldError builds the §10.1 line 49 rejection carrying the
// coordinator_hold detail in its message.
func coordinatorHoldError(fullMethod string) error {
	return status.Errorf(codes.Unavailable,
		"coordinator_hold: adapter is awaiting a new coordinator; %s rejected", fullMethod)
}
