// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"log/slog"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// elicitationExpiryTimeout bounds the delete of a session's raised
// elicitations. It runs on a context detached from its caller's
// cancellation, so a caller that ended does not leave the elicitations
// pending.
const elicitationExpiryTimeout = 5 * time.Second

// expireRaisedElicitations expires the pending lenny/request_elicitation
// elicitations the session's pod raised, when the session leaves its pod:
// at a failure edge into resume_pending or awaiting_client_action, at the
// suspension release, and as the post-terminal hook's last step. A respond
// or dismiss of an expired elicitation is then refused as not found, and the
// runtime waiting on it gets an error. It matches the raising session, so an
// elicitation a child raised that the chain holds for this session is left.
// An error is logged at WARN with the session ID; it never fails its caller.
//
// spec: §9.2 (Elicitation Chain), §7.3 (Retry and Resume)
func (s *Server) expireRaisedElicitations(ctx context.Context, row sessionstore.Session) {
	if s.interactions == nil {
		return
	}
	ectx, cancel := context.WithTimeout(context.WithoutCancel(ctx), elicitationExpiryTimeout)
	defer cancel()
	if _, err := s.interactions.ExpireRaisedBy(ectx, row.TenantID, row.ID); err != nil {
		slog.Warn("sessionserver: expire the elicitations a session raised", "session_id", row.ID, "error", err)
	}
}
