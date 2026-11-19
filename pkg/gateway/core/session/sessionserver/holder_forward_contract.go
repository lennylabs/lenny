// SPDX-License-Identifier: MIT

//go:build contract

package sessionserver

import (
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// ForwardToHolder exposes forwardToHolder to the tier-3 CH-HOLDERFORWARD
// contract tests, which build with the contract tag, so they drive the REST
// forward of a session route over the production forward client and the
// production forward listener. It forwards r with its unread body, exactly
// as a forwarded route does. Production builds do not compile this file.
//
// spec: §29.3 (Interactive message send) Forwarding rule 2, §28.5.4
// (Inter-replica) CH-HOLDERFORWARD
func (s *Server) ForwardToHolder(w http.ResponseWriter, r *http.Request, row sessionstore.Session, kind ForwardKind) ForwardOutcome {
	return s.forwardToHolder(w, r, row, kind, r.Body)
}
