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

// defaultCoordinatorHoldTimeout is the §10.1.4
// coordinatorHoldTimeoutSeconds default: the window the adapter holds a
// session after losing its coordinator before it self-terminates.
const defaultCoordinatorHoldTimeout = 120 * time.Second

// reasonCoordinatorLost is the §10.1.4 AdapterTerminating /
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
// spec: §10.1.
type holdState struct {
	mu     sync.Mutex
	active bool
	timer  expiryTimerHandle
	gen    int64
}

// coordinatorHoldAllowedMethods is the §10.1.4 allowlist: the only
// inbound gRPC methods the adapter still serves while in hold state. A
// new coordinator negotiates, opens the control stream, and fences;
// health probes keep the pod live. Every other RPC is rejected with the
// coordinator_hold detail until the fence lands.
//
// spec: §10.1.
var coordinatorHoldAllowedMethods = map[string]bool{
	"/lenny.adapter.v1.Adapter/CoordinatorFence": true,
	"/lenny.adapter.v1.Adapter/NegotiateVersion": true,
	"/lenny.adapter.v1.Adapter/AdapterEvents":    true,
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

// coordinatorHoldTimeout returns the configured §10.1.4 hold
// timeout, or the 120s spec default.
func (s *Server) coordinatorHoldTimeout() time.Duration {
	if s.CoordinatorHoldTimeout > 0 {
		return s.CoordinatorHoldTimeout
	}
	return defaultCoordinatorHoldTimeout
}

// onCoordinatorChannelClosed is invoked when the gateway control stream
// (AdapterEvents) ends. When the pod still holds an active session the
// closed stream is the §10.1 coordinator-loss signal: the coordinating
// gateway replica crashed or partitioned (the gRPC keepalive at
// 10s/5s — §11.3 — surfaces the dead connection within one
// interval plus one timeout), so the adapter enters hold state and waits
// coordinatorHoldTimeoutSeconds for a new coordinator to fence. A closed
// stream on an idle pod, or one whose session a clean teardown already
// released, is not a loss.
//
// spec: §10.1.
func (s *Server) onCoordinatorChannelClosed() {
	// spec: §10.1 — the hold arms on the sessions the adapter has
	// started on this pod, read from the slot registry. Every session is
	// bound to a slot on every pod, so a pod-global session field would
	// name no session on a pod whose sessions all take the slot path and
	// the hold would never arm.
	if !s.hasStartedSession() {
		return
	}
	s.enterHoldState()
}

// enterHoldState arms the §10.1 hold: it raises the coordinator-hold
// gauge, logs coordinator_connection_lost with the last known generation
// and the number of sessions the pod has started, and starts the
// hold-timeout timer. It is idempotent — a second close while already
// held is a no-op.
//
// The hold names no session. Its unit is the pod, and the set it
// terminates is read from the slot registry when the timeout fires rather
// than recorded here, because a session admitted between this arming and
// the timeout starts after the read and would be missing from a recorded
// set while still running when the timeout fires.
//
// spec: §10.1.
func (s *Server) enterHoldState() {
	// Read the generation and the started-session count through their
	// accessors (which take coord.mu and s.mu) before locking hold.mu so
	// no two locks are ever held together.
	gen := s.LastFencedGeneration()
	started := s.startedSessionCount()

	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	if s.hold.active {
		return
	}
	s.hold.active = true
	s.hold.gen = gen
	setCoordinatorHold(true)
	slog.Warn("coordinator_connection_lost",
		"started_sessions", started,
		"last_generation", gen)
	s.hold.timer = s.holdAfter(s.coordinatorHoldTimeout(), s.onHoldTimeout)
}

// exitHoldState clears the §10.1 hold after a new coordinator's
// CoordinatorFence lands: it stops the timeout timer and lowers the
// coordinator-hold gauge. It is a no-op when no hold is active.
//
// spec: §10.1.4 — a successful CoordinatorFence is the only way to
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
	slog.Info("coordinator_hold_resolved", "last_generation", s.hold.gen)
}

// onHoldTimeout runs when no new coordinator fenced within
// coordinatorHoldTimeoutSeconds. It lowers the gauge and self-terminates
// every session the adapter has started on this pod, so no agent process
// is left running with live provider credentials and no coordinator.
//
// The termination runs as two passes. Pass 1 is one critical section that
// deregisters every started entry, which is what makes the termination and
// a concurrent gateway Shutdown mutually exclusive: that handler decides
// on the outcome of its own locked cancel-deregister step, so its step and
// pass 1's are two acquisitions of one lock and one of them is first.
// Pass 2 then terminates each collected member in turn.
//
// The set is read here rather than recorded when the hold armed, because
// the hold-state interceptor gates admission rather than binding: a
// StartSession admitted before the arming can claim and start afterwards,
// and a recorded set would leave that session running unsupervised.
//
// spec: §10.1; §10.1.4; §4.7.
func (s *Server) onHoldTimeout() {
	s.hold.mu.Lock()
	if !s.hold.active {
		// A fence raced in and exited the hold; nothing to terminate.
		s.hold.mu.Unlock()
		return
	}
	s.hold.active = false
	s.hold.timer = nil
	gen := s.hold.gen
	setCoordinatorHold(false)
	s.hold.mu.Unlock()

	// Pass 1.
	members := s.deregisterStartedSessions()
	if len(members) == 0 {
		// Every started session was released between the arming and the
		// firing, so there is nothing to terminate and nothing to report.
		return
	}

	// Pass 2. One close context is shared by every member, which keeps the
	// bound the single-session timeout had: a runtime process serving more
	// than one session returns from a non-last close without touching the
	// child, so only the last member's close consumes the grace.
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, m := range members {
		s.terminateHeldSession(closeCtx, m, gen)
	}
}

// terminateHeldSession runs the §10.1.4 coordinator-lost termination for
// one member of the set the hold timeout deregistered.
//
// The final usage flush is here rather than left to the gateway's later
// terminal Shutdown, because pass 1 removed the entry that request's own
// locked step would have had to remove for its teardown to run, so that
// request skips the teardown that carries the flush.
//
// The loop sends no CH-RUNTIMEOPS drain signal and reports no §5.2 session
// scrub. The drain asks the runtime to finish its current exchange inside
// a grace window a coordinator collects, and this is the path on which no
// coordinator exists; the scrub report is the record of a scrub a Shutdown
// teardown performed, and this path performs none.
//
// spec: §10.1.4; §4.7; §6.4.
func (s *Server) terminateHeldSession(ctx context.Context, m heldSession, gen int64) {
	slog.Warn(reasonCoordinatorLost,
		"session_id", m.sessionID,
		"last_generation", gen)
	s.writeHoldPostMortem(m.sessionID, gen)
	// §4.7 — flush this member's final usage report so the gateway can run
	// budget_return.lua (§8.3) with its complete token totals.
	s.emitFinalUsage(ctx, m.sessionID)

	// Best-effort graceful runtime termination. The close takes the
	// session off the pod's shared runtime process, so the generation
	// state moves with it: a terminated session must not keep naming the
	// process's sole occupant, or the intra-pod MCP surface would keep
	// forwarding a tool call under a principal whose session has ended and
	// the pod surface could never be cancelled for the next claim.
	// The loop passes no teardown condition: the runtime's own active set
	// closes the shared process on the last member.
	// spec: §10.1; §15.4.3.
	if s.Runtime != nil {
		_ = s.Runtime.Close(ctx, m.sessionID)
	}
	s.noteRuntimeClosed(m.sessionID)
	// The second release step. It follows the close so the agent process is
	// not reading a credential file the teardown has already removed.
	_ = removeSlotTree(m.state)

	// §10.1.4 — notify the gateway so it can transition this session
	// without waiting for the 60s orphan-session reconciler. The event
	// names the member the loop is terminating: leaving the stamp to the
	// pod-global accessor would emit an empty session on exactly the
	// concurrent pods this path reaches.
	s.EmitAdapterTerminating(m.sessionID, reasonCoordinatorLost)
}

// inHoldState reports whether the adapter is currently in §10.1 hold
// state. The hold-state interceptors consult it on every inbound RPC.
func (s *Server) inHoldState() bool {
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	return s.hold.active
}

// writeHoldPostMortem records a §10.1.4 coordinator_lost
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
// spec: §10.1.
func (s *Server) holdStateUnaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if s.inHoldState() && !coordinatorHoldAllowedMethods[info.FullMethod] {
		return nil, coordinatorHoldError(info.FullMethod)
	}
	return handler(ctx, req)
}

// holdStateStreamInterceptor is the streaming counterpart of
// holdStateUnaryInterceptor. The AdapterEvents is allowlisted so a new
// coordinator can re-attach the control stream; Attach and
// PrepareWorkspace are rejected like any other operational RPC.
//
// spec: §10.1.
func (s *Server) holdStateStreamInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if s.inHoldState() && !coordinatorHoldAllowedMethods[info.FullMethod] {
		return coordinatorHoldError(info.FullMethod)
	}
	return handler(srv, ss)
}

// coordinatorHoldError builds the §10.1.4 rejection carrying the
// coordinator_hold detail in its message.
func coordinatorHoldError(fullMethod string) error {
	return status.Errorf(codes.Unavailable,
		"coordinator_hold: adapter is awaiting a new coordinator; %s rejected", fullMethod)
}
