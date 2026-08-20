// SPDX-License-Identifier: MIT

package adapter

import (
	"github.com/prometheus/client_golang/prometheus"
)

// SetTracingContextDroppedCounter exposes the §28.5.3 drop counter so the
// package's external tests can read it with testutil.ToFloat64 and assert
// that a dropped set_tracing_context frame is counted as well as logged.
func SetTracingContextDroppedCounter() prometheus.Counter {
	return setTracingContextDropped.WithLabelValues()
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
