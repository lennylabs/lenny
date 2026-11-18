// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// StreamSeam is the session core's side of the seam operations the stream
// proxy calls: the session lookup and authorization, the frames a reader
// attaching after a coordinator handoff receives, and the request identity
// operation, which the session server's request plumbing holds until the
// API front holds it. The composition root hands it to the stream proxy
// through the seam interfaces. The operations every seam calls run in
// methods of Server that each seam's adapter shares (decision 41 of change
// 0117).
type StreamSeam struct {
	s *Server
}

// The stream proxy reaches the session core through these interfaces.
var (
	_ seam.Sessions        = (*StreamSeam)(nil)
	_ seam.StreamFrames    = (*StreamSeam)(nil)
	_ seam.RequestIdentity = (*StreamSeam)(nil)
)

// StreamSeam returns the session core's side of the stream proxy's seam
// operations.
func (s *Server) StreamSeam() *StreamSeam {
	return &StreamSeam{s: s}
}

// LookupSession is GWSEAM-LookupSession for the stream proxy.
func (c *StreamSeam) LookupSession(ctx context.Context, tenantID, sessionID string) (seam.Session, error) {
	return c.s.lookupSeamSession(ctx, tenantID, sessionID)
}

// AuthorizeSession is GWSEAM-AuthorizeSession for the stream proxy. The
// stream proxy's routes take the same checks from their route access, so
// the stream proxy does not call it today.
func (c *StreamSeam) AuthorizeSession(ctx context.Context, tenantID, sessionID string, action seam.SessionAction) (seam.Session, error) {
	return c.s.authorizeSeamSession(ctx, tenantID, sessionID, action)
}

// RequestTenant is GWSEAM-RequestTenant for the stream proxy.
func (c *StreamSeam) RequestTenant(r *http.Request) string {
	return c.s.requestSeamTenant(r)
}

// ResumedFrame returns the session.resumed frame a reader attaching after a
// coordinator handoff receives.
//
// spec: GWSEAM-ResumedFrame
func (c *StreamSeam) ResumedFrame() ([]byte, bool) {
	return c.s.handoffResumedFrame()
}

// ChildrenReattachedFrame returns the children_reattached frame a reader of
// a parent session attaching after afterSeq receives across a coordinator
// handoff, or false when the session has no missed child completion and no
// active child.
//
// spec: GWSEAM-ChildrenReattachedFrame
func (c *StreamSeam) ChildrenReattachedFrame(ctx context.Context, tenantID, parentID string, afterSeq uint64) ([]byte, bool) {
	return c.s.buildHandoffChildrenReattached(ctx, tenantID, parentID, afterSeq)
}

// seamSessionOf returns the session record that crosses the seam interface
// for a stored session. A negative stored sequence number, which no write
// produces, crosses as zero.
func seamSessionOf(row sessionstore.Session) seam.Session {
	var lastSeq uint64
	if row.LastSeq > 0 {
		lastSeq = uint64(row.LastSeq)
	}
	return seam.Session{
		ID:              row.ID,
		TenantID:        row.TenantID,
		UserID:          row.UserID,
		ParentSessionID: row.ParentSessionID,
		RootSessionID:   row.RootSessionID,
		RuntimeRef:      row.RuntimeRef,
		Pool:            row.PoolRef,
		State:           state.State(row.State),
		LastSeq:         lastSeq,
	}
}

// lookupSeamSession returns the session record of a session in a tenant, or
// an error that matches seam.ErrSessionNotFound for a session the store
// does not hold. Any other store error is returned as it came.
//
// spec: GWSEAM-LookupSession
func (s *Server) lookupSeamSession(ctx context.Context, tenantID, sessionID string) (seam.Session, error) {
	row, err := s.store.Get(ctx, tenantID, sessionID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return seam.Session{}, fmt.Errorf("session %s: %w", sessionID, seam.ErrSessionNotFound)
	}
	if err != nil {
		return seam.Session{}, err
	}
	return seamSessionOf(row), nil
}

// authorizeSeamSession applies the checks a session route's wrappers apply,
// in their order: the §10.2 permission gate for the action, which refuses
// with seam.ErrForbidden; the principal subject, whose absence is
// seam.ErrUnauthenticated; and the ownership rule, under which a session
// the caller may not read or act on answers seam.ErrSessionNotFound, as a
// missing session does.
//
// spec: GWSEAM-AuthorizeSession; §10.2 (Session ownership) rules 3, 5, 6,
// and 7.
func (s *Server) authorizeSeamSession(ctx context.Context, tenantID, sessionID string, action seam.SessionAction) (seam.Session, error) {
	perm, act := auth.PermReadOwnSessions, false
	switch action {
	case seam.SessionRead:
	case seam.SessionManage:
		perm, act = auth.PermManageOwnSessions, true
	default:
		return seam.Session{}, fmt.Errorf("session action %d: %w", action, seam.ErrForbidden)
	}
	if !s.permissionGranted(ctx, perm) {
		return seam.Session{}, fmt.Errorf("permission %s: %w", perm, seam.ErrForbidden)
	}
	if p, ok := authmw.FromContext(ctx); !ok || p.Subject == "" {
		return seam.Session{}, seam.ErrUnauthenticated
	}
	row, err := s.accessRow(ctx, tenantID, sessionID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return seam.Session{}, fmt.Errorf("session %s: %w", sessionID, seam.ErrSessionNotFound)
	}
	if err != nil {
		return seam.Session{}, err
	}
	if !s.SessionAccessible(ctx, row, act) {
		return seam.Session{}, fmt.Errorf("session %s: %w", sessionID, seam.ErrSessionNotFound)
	}
	return seamSessionOf(row), nil
}

// requestSeamTenant returns the tenant a request runs as, as the session
// routes resolve it.
//
// spec: GWSEAM-RequestTenant
func (s *Server) requestSeamTenant(r *http.Request) string {
	return s.resolveTenant(r)
}
