// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// SetTracingContextDroppedCounter exposes the §28.5.3 drop counter so the
// package's external tests can read it with testutil.ToFloat64 and assert
// that a dropped set_tracing_context frame is counted as well as logged.
func SetTracingContextDroppedCounter() prometheus.Counter {
	return setTracingContextDropped.WithLabelValues()
}

// UnaddressedFrameRejectedCounter exposes the §28.5.3 unaddressed-frame
// counter for the given frame type so the package's external tests can
// assert that a frame carrying no per-session identifier on a pod holding
// more than one slot is counted on its own series rather than on the
// misaddressed-frame drop counter.
func UnaddressedFrameRejectedCounter(frameType string) prometheus.Counter {
	return unaddressedFrameRejected.WithLabelValues(frameType)
}

// ReleaseSlotForTest runs both release steps for the session the way the
// rollback paths do, so an external test can drive the §28.5.3 teardown
// window in which an Attach stream is still draining output after its
// binding was released. ctx is the context the release acquires the
// slot's per-slot guard on, as the handler rollbacks pass their request
// context.
func (s *Server) ReleaseSlotForTest(ctx context.Context, sessionID string) {
	s.noteRuntimeClosed(sessionID)
	s.releaseSessionSlot(ctx, sessionID)
}

// ClaimSessionForTest binds and marks started the session's slot the way
// the merged start claim does, so a test can put the adapter in the state
// a completed StartSession leaves without driving the whole RPC.
func (s *Server) ClaimSessionForTest(sessionID string) error {
	claim, err := s.claimSessionSlot(sessionID, slotResolve{allowCreate: true}, s.isSDKWarm(), false)
	if err != nil {
		return err
	}
	// A completed start's open sequence has written the session's
	// session_start, which is what admits its message writes.
	s.markSessionStartWritten(claim.entry, true)
	_ = s.noteRuntimeStarted(sessionID, claim.attempt)
	return nil
}

// claimSessionForTest is the package-internal form of ClaimSessionForTest.
func (s *Server) claimSessionForTest(sessionID string) error {
	return s.ClaimSessionForTest(sessionID)
}

// RegisterUnboundSlotForTest creates the session's registry entry and its
// on-disk tree without binding or starting it, the way the §4.7
// workspace-prep RPCs (PrepareWorkspace, FinalizeWorkspace, RunSetup) do
// when they run ahead of StartSession. It lets an external test put the
// pod in the state the §28.5.3 slot count must fail closed on: one bound
// session serving traffic while a second session's workspace is still
// being prepared.
func (s *Server) RegisterUnboundSlotForTest(sessionID string) error {
	_, err := s.ensureSlotPaths(sessionID, slotResolve{allowCreate: true})
	return err
}

// BeginCheckpointOpForTest takes the §4.7 pod operation lock for a
// checkpoint addressed to sessionID, so an external test can put the lock
// in a known admission state before driving a Checkpoint RPC. It returns
// the release func the caller must invoke once it is done holding the
// lock.
func (s *Server) BeginCheckpointOpForTest(ctx context.Context, sessionID string) (func(), error) {
	return s.ops.Begin(ctx, opCheckpoint, sessionID)
}

// WaitPendingCheckpointForTest blocks until a checkpoint addressed to
// sessionID has entered the operation lock's pending set, or until the
// timeout elapses. It reports whether the checkpoint became pending, so an
// external test can build the pending set deterministically rather than
// sleeping.
func (s *Server) WaitPendingCheckpointForTest(sessionID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.ops.mu.Lock()
		_, ok := s.ops.checkpoints[sessionID]
		s.ops.mu.Unlock()
		if ok {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// ErrRuntimeConnectionEnded exposes the socket transport's sticky ended
// error so the package's external tests can assert that a Start or Output
// after the runtime's connection ended fails with it. spec: §4.7.10.
var ErrRuntimeConnectionEnded = errRuntimeConnectionEnded

// SetPeerUIDLookupForTest replaces the SO_PEERCRED lookup the CH-MSGSOCK
// listener reads a connecting peer's UID with, so an external test can
// present a peer UID other than its own without root. Call it before the
// first Start.
func (p *SocketRuntimeProcess) SetPeerUIDLookupForTest(lookup func(net.Conn) (uint32, error)) {
	p.peerUID = lookup
}

// SetLoggerForTest routes the CH-MSGSOCK listener's refusal records to l so
// an external test can assert on them. Call it before the first Start.
func (p *SocketRuntimeProcess) SetLoggerForTest(l *slog.Logger) {
	p.logger = l
}
