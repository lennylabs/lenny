// SPDX-License-Identifier: MIT

package adapter

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

// SetTracingContextDroppedCounter exposes the §28.5.3 drop counter so the
// package's external tests can read it with testutil.ToFloat64 and assert
// that a dropped set_tracing_context frame is counted as well as logged.
func SetTracingContextDroppedCounter() prometheus.Counter {
	return setTracingContextDropped.WithLabelValues()
}

// ReleaseSessionForTest clears the pod's session binding the way Shutdown
// does after the runtime close, so an external test can drive the §28.5.3
// teardown window in which an Attach stream is still draining output after
// its binding was released.
func (s *Server) ReleaseSessionForTest() { s.releaseSession() }

// ReleaseSlotForTest deregisters a §6.4 slot the way shutdownSlot does, so
// an external test can drive the §28.5.3 teardown window on the per-slot
// branch.
func (s *Server) ReleaseSlotForTest(ctx context.Context, slotID string) {
	s.releaseSlot(ctx, slotID)
}
