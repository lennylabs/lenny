// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
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
// binding was released.
func (s *Server) ReleaseSlotForTest(sessionID string) {
	s.noteRuntimeClosed(sessionID)
	s.releaseSessionSlot(sessionID)
}

// ClaimSessionForTest binds and marks started the session's slot the way
// the merged start claim does, so a test can put the adapter in the state
// a completed StartSession leaves without driving the whole RPC.
func (s *Server) ClaimSessionForTest(sessionID string) error {
	_, _, err := s.claimSessionSlot(sessionID, s.isSDKWarm(), false)
	if err != nil {
		return err
	}
	s.noteRuntimeStarted(sessionID)
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
	_, err := s.ensureSlotPaths(sessionID)
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
