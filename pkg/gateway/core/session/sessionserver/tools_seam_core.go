// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// ToolsSeam is the session core's side of every operation the platform
// tools seam calls on the core: the terminal pipeline, which the orphan
// cleanup and the deadlock detector run after their own terminal write, and
// the tree node operations GWSEAM-RecoverTree drives, declared in this file;
// and the session lookup, forwarding, child session, message delivery,
// export, and client route operations declared in tools_seam.go and
// tools_routes_seam.go. Server returns it from ToolsSeam. It stays in
// sessionserver until the session core moves, as UploadSeam does.
type ToolsSeam struct {
	s *Server
}

// ToolsSeam returns the core's side of the operations the platform tools
// seam calls.
func (s *Server) ToolsSeam() ToolsSeam { return ToolsSeam{s: s} }

var (
	_ seam.Terminals = ToolsSeam{}
	_ seam.TreeNodes = ToolsSeam{}
)

// BeginTerminal opens the replica's terminal claim for a session.
//
// spec: GWSEAM-BeginTerminal
func (t ToolsSeam) BeginTerminal(sessionID string) func() {
	return t.s.BeginTerminal(sessionID)
}

// OnSessionTerminal runs the terminal pipeline for the session the
// caller's write moved to a terminal state. The pipeline runs on the row
// the record carries, or, for a record that carries none, on the row read
// back from the primary by the record's tenant and identifier.
//
// spec: GWSEAM-OnSessionTerminal
func (t ToolsSeam) OnSessionTerminal(ctx context.Context, from state.State, s seam.Session) {
	t.s.OnSessionTerminal(ctx, session.State(from), t.s.terminalRow(ctx, s))
}

// AfterTerminalWrite runs the core's post-terminal hook with the caller's
// own signals over the stored row, and returns the record of the row the
// hook left.
//
// spec: GWSEAM-AfterTerminalWrite
func (t ToolsSeam) AfterTerminalWrite(ctx context.Context, from state.State, s seam.Session, signals func(context.Context, seam.Session)) seam.Session {
	var rowSignals func(context.Context, sessionstore.Session)
	if signals != nil {
		rowSignals = func(ctx context.Context, row sessionstore.Session) { signals(ctx, seamSessionOf(row)) }
	}
	return seamSessionOf(t.s.AfterTerminalWrite(ctx, session.State(from), t.s.terminalRow(ctx, s), rowSignals))
}

// terminalRow returns the row a seam's terminal write produced: the row the
// session record carries, or, for a record that carries none, the row read
// back from the primary, because the terminal pipeline reads row fields the
// session record does not carry. A read failure is logged and the pipeline runs on
// the row the record carries, so the session's pod is still released.
//
// spec: GWSEAM-OnSessionTerminal
func (s *Server) terminalRow(ctx context.Context, rec seam.Session) sessionstore.Session {
	if row, ok := storedRow(rec); ok {
		return row
	}
	row, err := s.readPrimary(ctx, rec.TenantID, rec.ID)
	if err == nil {
		return row
	}
	slog.Warn("sessionserver: read the terminal row a seam wrote; the terminal pipeline runs on the session record",
		"session_id", rec.ID, "tenant_id", rec.TenantID, "error", err)
	return recordRow(rec)
}

// recordRow is the stored row for a session record, with the fields the
// record carries.
func recordRow(rec seam.Session) sessionstore.Session {
	row := sessionstore.Session{
		ID:              rec.ID,
		TenantID:        rec.TenantID,
		UserID:          rec.UserID,
		State:           session.State(rec.State),
		CreatedAt:       rec.CreatedAt,
		UpdatedAt:       rec.UpdatedAt,
		TerminatedAt:    rec.TerminatedAt,
		RuntimeRef:      rec.RuntimeRef,
		PoolRef:         rec.Pool,
		ParentSessionID: rec.ParentSessionID,
		RootSessionID:   rec.RootSessionID,
		PodAssignment:   rec.PodAssignment,
	}
	if rec.DelegationDepth > 0 {
		row.DelegationDepth = uint32(rec.DelegationDepth)
	}
	if rec.LastSeq > 0 {
		row.LastSeq = int64(rec.LastSeq)
	}
	return row
}

// ListTree returns the records of every node of the tree rooted at
// rootSessionID, each carrying its stored row, so the tree recovery walks
// the rows it walked before the seam split rather than rows rebuilt from
// the records' fields.
//
// spec: GWSEAM-ListTree
func (t ToolsSeam) ListTree(ctx context.Context, tenantID, rootSessionID string) ([]seam.Session, error) {
	rows, err := t.s.store.ListByRoot(ctx, tenantID, rootSessionID)
	if err != nil {
		return nil, err
	}
	out := make([]seam.Session, len(rows))
	for i, row := range rows {
		out[i] = storedSession(row)
	}
	return out, nil
}

// StartNode drives a node's restore through the resume driver, which reads
// the node's stored row. A node whose row cannot be read is logged and not
// started, so the recovery's level timeout fails it.
//
// spec: GWSEAM-StartNode
func (t ToolsSeam) StartNode(ctx context.Context, node seam.Session) {
	row, err := t.s.store.Get(ctx, node.TenantID, node.ID)
	if err != nil {
		slog.Warn("sessionserver: read a delegation node to start its recovery",
			"session_id", node.ID, "tenant_id", node.TenantID, "error", err)
		return
	}
	sessionNodeReattacher{s: t.s}.StartNode(ctx, row)
}

// ReattachNode waits for a node's restore.
//
// spec: GWSEAM-ReattachNode
func (t ToolsSeam) ReattachNode(ctx context.Context, node seam.Session) error {
	return sessionNodeReattacher{s: t.s}.ReattachNode(ctx, recordRow(node))
}

// FailNode moves a node the recovery could not restore to failed.
//
// spec: GWSEAM-FailNode
func (t ToolsSeam) FailNode(ctx context.Context, node seam.Session, reason string) {
	sessionTerminalMarker{s: t.s}.FailNode(ctx, recordRow(node), reason)
}

// AgentCallerType is the principal CallerType a platform tool call from a
// session's pod runs under. A replica that forwards such a call to the
// coordinating replica carries the calling session's identifier instead of
// a client credential, because the call reached it with none.
const AgentCallerType = "agent"

// SessionResolver resolves a session identifier to its row without a
// tenant, because a platform tool call from a session's pod and a
// forwarded tool call carry only the session's identifier.
// *sessionstore memory and Postgres stores satisfy it through GetByID.
type SessionResolver interface {
	GetByID(ctx context.Context, id string) (sessionstore.Session, error)
}

// WithSessionPrincipal resolves sessionID and returns ctx carrying the
// principal a platform tool call from that session runs under. It is the
// one place that principal is built: the LNK-GWCONTROL bridge reaches it
// through GWSEAM-AttachCallingSession for a call from a session's pod, and
// the forward listener calls it for a forwarded call that carries the
// calling session's identifier. An unknown session matches
// seam.ErrSessionNotFound.
//
// spec: GWSEAM-AttachCallingSession
func WithSessionPrincipal(ctx context.Context, sessions SessionResolver, sessionID string) (context.Context, error) {
	sess, err := sessions.GetByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return ctx, seam.ErrSessionNotFound
		}
		return ctx, fmt.Errorf("resolve session %s: %w", sessionID, err)
	}
	return authmw.WithPrincipal(ctx, authmw.Principal{
		Subject:    sess.UserID,
		TenantID:   sess.TenantID,
		SessionID:  sessionID,
		CallerType: AgentCallerType,
	}), nil
}

// CallingSessions serves GWSEAM-AttachCallingSession over a session
// resolver.
type CallingSessions struct {
	sessions SessionResolver
}

// NewCallingSessions returns the calling session operation over sessions.
func NewCallingSessions(sessions SessionResolver) CallingSessions {
	return CallingSessions{sessions: sessions}
}

var _ seam.CallingSessions = CallingSessions{}

// AttachCallingSession returns ctx carrying the principal of the agent
// caller of sessionID.
//
// spec: GWSEAM-AttachCallingSession
func (c CallingSessions) AttachCallingSession(ctx context.Context, sessionID string) (context.Context, error) {
	return WithSessionPrincipal(ctx, c.sessions, sessionID)
}
