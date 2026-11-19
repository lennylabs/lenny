// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/seam"

	"github.com/lennylabs/lenny/pkg/session/sessionstore"
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

// OwnedRouteSpy mounts spy at pattern behind the session check RegisterRoutes
// gives a route of wrapper ("readID" for a read, "manageID" for an action),
// inside the tenant check, through the API front's request pipeline, so an
// external test observes directly whether a request reaches the handler.
// It returns nil for any other wrapper name.
func (s *Server) OwnedRouteSpy(pattern, wrapper string, spy http.HandlerFunc) http.Handler {
	switch wrapper {
	case "readID":
		return s.spyPipeline(pattern, seam.AccessSessionRead, spy)
	case "manageID":
		return s.spyPipeline(pattern, seam.AccessSessionManage, spy)
	}
	return nil
}
