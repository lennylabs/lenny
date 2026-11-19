// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"log/slog"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// recordPodRelease records on the session row that this replica released the
// session's pod or slot, which ends the session's §7.1 post-terminal window.
// It runs after the release returns, whatever the release's error, because a
// release that returned an error has still ended this replica's binding or
// claim attempt, and a retry would find nothing to release. The write is
// detached from ctx and bounded (sessionstore.RecordPodRelease); a missing
// row and a row that is not terminal or already records a release write
// nothing, and any other store error is logged at WARN and leaves the
// session in its window until the cap.
//
// The callers are the pre-running reclaim, the post-terminal hook's executor
// release when it found a binding, and the terminal binding release when it
// released or failed to release a current binding. No caller runs it for a
// release skipped at shutdown, a stale binding evicted with no binder call,
// or a session with no binding, because no release ran on this replica.
//
// spec: §7.1 (Post-terminal window)
func (s *Server) recordPodRelease(ctx context.Context, sess sessionstore.Session) {
	if err := sessionstore.RecordPodRelease(ctx, s.store, sess.TenantID, sess.ID, s.clock()); err != nil {
		slog.Warn("sessionserver: pod release not recorded; the session stays in its post-terminal window until the cap",
			"session_id", sess.ID, "error", err)
	}
}
