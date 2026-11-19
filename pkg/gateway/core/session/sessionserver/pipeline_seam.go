// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"fmt"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// PipelineSeam is the session core's side of the seam operations the API
// front's request pipeline calls before a route's handler runs: the session
// lookup and authorization of every seam, and the permission gate. The
// composition root hands it to the pipeline through the seam interfaces.
type PipelineSeam struct {
	s *Server
}

// The API front's request pipeline reaches the session core through these
// interfaces.
var (
	_ seam.Sessions    = (*PipelineSeam)(nil)
	_ seam.Permissions = (*PipelineSeam)(nil)
)

// RequestChecks returns the session core's side of the request pipeline's
// seam operations, as the session checks and the permission gate the
// pipeline holds.
func (s *Server) RequestChecks() (seam.Sessions, seam.Permissions) {
	p := &PipelineSeam{s: s}
	return p, p
}

// DevTenantHeader reports whether the server resolves the tenant of a
// request with no principal tenant from the dev header, as
// Options.DevTenantHeader set, so the API front's request pipeline built
// for it resolves the same tenant.
func (s *Server) DevTenantHeader() bool {
	return s.devTenantHeader
}

// LookupSession is GWSEAM-LookupSession for the request pipeline.
func (p *PipelineSeam) LookupSession(ctx context.Context, tenantID, sessionID string) (seam.Session, error) {
	return p.s.lookupSeamSession(ctx, tenantID, sessionID)
}

// AuthorizeSession is GWSEAM-AuthorizeSession for the request pipeline,
// which applies it as the read and manage checks of the routes on
// /v1/sessions/{id}.
func (p *PipelineSeam) AuthorizeSession(ctx context.Context, tenantID, sessionID string, action seam.SessionAction) (seam.Session, error) {
	return p.s.authorizeSeamSession(ctx, tenantID, sessionID, action)
}

// AuthorizePermission is GWSEAM-AuthorizePermission: the §10.2 permission
// gate for perm, for the principal ctx carries. It admits a request with no
// principal, and a principal with no roles in a single-tenant deployment,
// as permissionGranted does, because the session check that follows on a
// named session, and the owner binding on create and list, refuse a request
// with no principal subject.
//
// spec: GWSEAM-AuthorizePermission
func (p *PipelineSeam) AuthorizePermission(ctx context.Context, perm auth.Permission) error {
	if !p.s.permissionGranted(ctx, perm) {
		return fmt.Errorf("permission %s: %w", perm, seam.ErrForbidden)
	}
	return nil
}
