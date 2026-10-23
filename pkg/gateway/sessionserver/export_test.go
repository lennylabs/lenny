// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// DriveResumeAndWait drives one resume attempt for the row, as the watchdog's
// resume_pending sweep does, and waits until the attempt returns. It is the
// external tests' handle on the asynchronous resume driver.
func (s *Server) DriveResumeAndWait(ctx context.Context, row sessionstore.Session) {
	s.DriveResume(ctx, row)
	s.WaitResumeIdle(row.ID)
}

// WaitResumeIdle waits until no resume attempt for the session is in flight
// on this replica, or ten seconds pass.
func (s *Server) WaitResumeIdle(sessionID string) {
	deadline := time.Now().Add(10 * time.Second)
	for s.resumeAttempts.inFlight(sessionID) && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
}

// inFlight reports whether an attempt for the session is registered.
func (r *attemptRecord) inFlight(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.cancels[sessionID]
	return ok
}
