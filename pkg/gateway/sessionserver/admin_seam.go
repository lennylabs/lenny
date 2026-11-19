// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// AdminSeam is the session core's side of the operations the API front's
// admin handlers call on the core: the terminal pipeline, which an
// operator's force-terminate and a lifecycle drain run after their own
// terminal write, and the session side of a user revocation. Server
// returns it from AdminSeam. It stays in sessionserver until the session
// core moves, as ToolsSeam does.
type AdminSeam struct {
	s    *Server
	pods UserBindingReleases
}

// UserBindingReleases releases the pod bindings of a revoked user's
// sessions on this replica and asks the peer replicas to release theirs.
// The composition root's binding-release fan-out satisfies it.
type UserBindingReleases interface {
	TerminateUserSessions(ctx context.Context, tenantID, userID string, sessionIDs []string) seam.UserTermination
}

// AdminSeam returns the core's side of the operations the API front
// calls. pods releases a revoked user's pod bindings; nil, as in a
// deployment without warm-pod placement, releases none.
func (s *Server) AdminSeam(pods UserBindingReleases) AdminSeam {
	return AdminSeam{s: s, pods: pods}
}

var (
	_ seam.Terminals   = AdminSeam{}
	_ seam.Revocations = AdminSeam{}
)

// BeginTerminal opens the replica's terminal claim for a session.
//
// spec: GWSEAM-BeginTerminal
func (a AdminSeam) BeginTerminal(sessionID string) func() {
	return a.s.BeginTerminal(sessionID)
}

// OnSessionTerminal runs the terminal pipeline for the session the API
// front's write moved to a terminal state, on the row the record carries.
//
// spec: GWSEAM-OnSessionTerminal
func (a AdminSeam) OnSessionTerminal(ctx context.Context, from state.State, s seam.Session) {
	a.s.ToolsSeam().OnSessionTerminal(ctx, from, s)
}

// AfterTerminalWrite runs the core's post-terminal hook with the caller's
// own signals over the row the record carries.
//
// spec: GWSEAM-AfterTerminalWrite
func (a AdminSeam) AfterTerminalWrite(ctx context.Context, from state.State, s seam.Session, signals func(context.Context, seam.Session)) seam.Session {
	return a.s.ToolsSeam().AfterTerminalWrite(ctx, from, s, signals)
}

// AfterRevokeWrite runs the full_revoke per-session hook on the row the
// revocation write produced.
//
// spec: GWSEAM-AfterRevokeWrite
func (a AdminSeam) AfterRevokeWrite(ctx context.Context, s seam.Session) {
	a.s.AfterRevokeWrite(ctx, revokedRow(s))
}

// ReclaimRevokedPreRunning reclaims the pod or slot of a session the
// revocation cancelled before it ran.
//
// spec: GWSEAM-ReclaimRevokedPreRunning
func (a AdminSeam) ReclaimRevokedPreRunning(ctx context.Context, from state.State, s seam.Session) {
	a.s.ReclaimRevokedPreRunning(ctx, session.State(from), revokedRow(s))
}

// TerminateUserSessions releases the pod bindings of a revoked user's
// sessions. With no binding releases wired it releases none.
//
// spec: GWSEAM-TerminateUserSessions
func (a AdminSeam) TerminateUserSessions(ctx context.Context, tenantID, userID string, sessionIDs []string) seam.UserTermination {
	if a.pods == nil {
		return seam.UserTermination{}
	}
	return a.pods.TerminateUserSessions(ctx, tenantID, userID, sessionIDs)
}

// revokedRow is the row a revocation write produced: the row the record
// carries, or the row the record's fields describe.
func revokedRow(rec seam.Session) sessionstore.Session {
	if row, ok := storedRow(rec); ok {
		return row
	}
	return recordRow(rec)
}
