// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/tenancy/tenantstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// UploadSeam is the session core's side of the seam operations the upload
// handler calls: the session lookup and authorization, the request
// forwarding, the workspace write, the upload token verification, the
// storage quota, and the upload audit event. The composition root hands it
// to the upload handler through the seam interfaces. The session lookup and
// the authorization run in the Server methods that StreamSeam shares.
type UploadSeam struct {
	s *Server
}

// UploadSeam returns the session core's side of the upload handler's seam
// operations.
func (s *Server) UploadSeam() *UploadSeam {
	return &UploadSeam{s: s}
}

// SetUploadWindows attaches the upload handler's side of
// GWSEAM-CloseUploads, which the finalize handler calls once a session's
// upload window closes. It is called before the server serves.
func (s *Server) SetUploadWindows(u seam.UploadWindows) {
	s.uploads = u
}

// LookupSession is GWSEAM-LookupSession for the upload handler.
func (u *UploadSeam) LookupSession(ctx context.Context, tenantID, sessionID string) (seam.Session, error) {
	return u.s.lookupSeamSession(ctx, tenantID, sessionID)
}

// AuthorizeSession is GWSEAM-AuthorizeSession for the upload handler.
func (u *UploadSeam) AuthorizeSession(ctx context.Context, tenantID, sessionID string, action seam.SessionAction) (seam.Session, error) {
	return u.s.authorizeSeamSession(ctx, tenantID, sessionID, action)
}

// forwardKinds maps the seam's forward kinds to the core's forwarding rows.
var forwardKinds = map[seam.ForwardKind]ForwardKind{
	seam.ForwardUpload: ForwardKindUpload,
}

// ForwardRequest forwards a request for a session to its coordinating
// replica under the forwarding row of kind, through the same path as the
// session routes, and returns the forward outcome. A kind the core has no
// row for is a wiring defect: it answers INTERNAL_ERROR and returns
// seam.ForwardNotDelivered without forwarding.
//
// spec: GWSEAM-ForwardRequest
func (u *UploadSeam) ForwardRequest(w http.ResponseWriter, r *http.Request, s seam.Session, kind seam.ForwardKind, body io.Reader) seam.ForwardOutcome {
	k, ok := forwardKinds[kind]
	if !ok {
		u.s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "unknown forward kind "+strconv.Quote(string(kind)), nil)
		return seam.ForwardNotDelivered
	}
	row := sessionstore.Session{
		ID:              s.ID,
		TenantID:        s.TenantID,
		UserID:          s.UserID,
		ParentSessionID: s.ParentSessionID,
		RootSessionID:   s.RootSessionID,
		RuntimeRef:      s.RuntimeRef,
		PoolRef:         s.Pool,
		State:           sessionStateOf(s.State),
	}
	return seamForwardOutcome(u.s.forwardToHolder(w, r, row, k, body))
}

// seamForwardOutcome maps the core's forward outcome to the seam's.
func seamForwardOutcome(o ForwardOutcome) seam.ForwardOutcome {
	switch o {
	case ForwardLocal:
		return seam.ForwardLocal
	case ForwardDelivered:
		return seam.ForwardDelivered
	case ForwardNotDelivered:
		return seam.ForwardNotDelivered
	default:
		return seam.ForwardUnknown
	}
}

// WriteWorkspace writes a mid-session upload through the adapter binding
// this replica holds for the session. PrepareWorkspace streams the
// files into the session's staging tree, and FinalizeWorkspace overlays
// them onto the session's current tree and signals the runtime once
// promotion completes. A failure of either leaves the live workspace
// untouched and returns a *seam.WorkspaceWriteError naming the phase.
//
// spec: GWSEAM-WriteWorkspace; §4.7.1 (role and gateway RPC contract); §7.4
// — the pair is the marked mid-session form: mid_session true on both
// requests and no bind attempt token, because the binding it writes into
// predates the request and may have been made by another replica. The live
// binding checked here is the admission that keeps this identity-free
// request away from an entry this replica does not own.
func (u *UploadSeam) WriteWorkspace(ctx context.Context, sessionID string, files []seam.WorkspaceFile) (int, error) {
	if u.s.podRegistry == nil {
		return 0, seam.ErrNoLiveBinding
	}
	bind, ok := u.s.podRegistry.Get(sessionID)
	if !ok || bind == nil || bind.Adapter == nil {
		return 0, seam.ErrNoLiveBinding
	}
	plan, uploads := midSessionPlan(files)
	if _, err := bind.Adapter.PrepareWorkspace(ctx, sessionID, uploads, "", true); err != nil {
		return 0, &seam.WorkspaceWriteError{Phase: seam.WorkspaceStaging, Err: err}
	}
	if _, err := bind.Adapter.FinalizeWorkspace(ctx, sessionID, plan, nil, nil, "", true); err != nil {
		return 0, &seam.WorkspaceWriteError{Phase: seam.WorkspacePromotion, Err: err}
	}
	return len(plan.GetSources()), nil
}

// midSessionPlan builds the adapter WorkspacePlan of uploadFile sources for
// a mid-session upload and the staged-content map keyed by each file's
// upload ref.
func midSessionPlan(files []seam.WorkspaceFile) (*adapterv1.WorkspacePlan, map[string][]byte) {
	plan := &adapterv1.WorkspacePlan{SchemaVersion: 1}
	uploads := make(map[string][]byte, len(files))
	for i, f := range files {
		ref := "midupload-" + strconv.Itoa(i)
		uploads[ref] = f.Content
		plan.Sources = append(plan.Sources, &adapterv1.WorkspaceSource{
			Type:      "uploadFile",
			Path:      f.Path,
			UploadRef: ref,
			Mode:      f.Mode,
		})
	}
	return plan, uploads
}

// VerifyUploadToken verifies a client's upload token against the session id
// with the server's verifier. A refusal matches the seam's upload token
// error for its kind and keeps the verifier's message. With no verifier
// wired, as in a test or the minimal gateway, every token passes.
//
// spec: GWSEAM-VerifyUploadToken; §7.1 (uploadToken format and security
// properties).
func (u *UploadSeam) VerifyUploadToken(_ context.Context, sessionID, token string) error {
	err := u.s.verifyUploadTokenValue(token, sessionID)
	if err == nil {
		return nil
	}
	kind := seam.ErrUploadTokenInvalid
	switch {
	case errors.Is(err, uploadtoken.ErrExpired):
		kind = seam.ErrUploadTokenExpired
	case errors.Is(err, uploadtoken.ErrSessionMismatch):
		kind = seam.ErrUploadTokenMismatch
	case errors.Is(err, uploadtoken.ErrConsumed):
		kind = seam.ErrUploadTokenConsumed
	}
	return &uploadTokenError{kind: kind, err: err}
}

// uploadTokenError carries a verifier refusal under the seam's error for
// its kind. Its message is the verifier's, which the client envelope of an
// invalid token carries.
type uploadTokenError struct {
	kind error
	err  error
}

func (e *uploadTokenError) Error() string { return e.err.Error() }

// Unwrap returns the seam's error and the verifier's.
func (e *uploadTokenError) Unwrap() []error { return []error{e.kind, e.err} }

// ReserveStorage reserves an upload's declared size against its tenant's
// §11.2 storage quota. A tenant the store does not hold, a tenant with no
// storage limit, and a server with no quota counter or tenant store hold no
// reservation. A tenant with a limit needs a declared size. A quota the
// dual-store outage leaves unreadable fails closed with
// seam.ErrStorageQuotaUnavailable.
//
// spec: GWSEAM-ReserveStorage; §11.2; §12.4.
func (u *UploadSeam) ReserveStorage(ctx context.Context, tenantID string, incoming int64) (seam.StorageReservation, error) {
	if u.s.storageQuota == nil || u.s.tenants == nil {
		return seam.StorageReservation{}, nil
	}
	tenant, err := u.s.tenants.Get(ctx, tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return seam.StorageReservation{}, nil
	}
	if err != nil {
		return seam.StorageReservation{}, fmt.Errorf("storage-quota check failed: %w", err)
	}
	if tenant.StorageQuotaBytes <= 0 {
		return seam.StorageReservation{}, nil
	}
	if incoming < 0 {
		return seam.StorageReservation{}, seam.ErrContentLengthRequired
	}
	priorUsed, err := u.s.storageQuota.Reserve(ctx, tenantID, incoming, tenant.StorageQuotaBytes)
	switch {
	case errors.Is(err, storagequota.ErrQuotaExceeded):
		return seam.StorageReservation{}, &seam.StorageQuotaError{CurrentBytes: priorUsed, LimitBytes: tenant.StorageQuotaBytes}
	case errors.Is(err, storagequota.ErrUnavailable):
		return seam.StorageReservation{}, fmt.Errorf("%w: %w", seam.ErrStorageQuotaUnavailable, err)
	case err != nil:
		return seam.StorageReservation{}, fmt.Errorf("storage-quota reservation failed: %w", err)
	}
	return seam.StorageReservation{Held: true, Reserved: incoming, Headroom: tenant.StorageQuotaBytes - priorUsed}, nil
}

// AdjustStorage adds delta to a tenant's storage usage. With no quota
// counter wired it does nothing.
//
// spec: GWSEAM-AdjustStorage
func (u *UploadSeam) AdjustStorage(ctx context.Context, tenantID string, delta int64) error {
	if u.s.storageQuota == nil {
		return nil
	}
	return u.s.storageQuota.Adjust(ctx, tenantID, delta)
}

// AuditUpload writes the §16.6 session.upload audit event for an upload,
// under the session's own tenant and stamped with the server's clock. With
// no lifecycle audit sink wired it records nothing.
//
// spec: GWSEAM-AuditUpload; §16.6; §11.7 — F-7.4.17, F-13.4.8.
func (u *UploadSeam) AuditUpload(ctx context.Context, ev seam.UploadAuditEvent) {
	if u.s.lifecycleAudit == nil {
		return
	}
	u.s.lifecycleAudit.EmitSessionLifecycle(ctx, SessionLifecycleEvent{
		EventType:  auditSessionUpload,
		TenantID:   ev.Session.TenantID,
		SessionID:  ev.Session.ID,
		UserID:     ev.Session.UserID,
		RuntimeRef: ev.Session.RuntimeRef,
		State:      string(ev.Session.State),
		Outcome:    ev.Outcome,
		Reason:     ev.Reason,
		Detail:     ev.Detail,
		At:         u.s.clock(),
	})
}

// verifyUploadToken verifies the `X-Lenny-Upload-Token` header of a request
// against the session id. It returns nil on success or one of
// uploadtoken.{ErrInvalid, ErrExpired, ErrSessionMismatch, ErrConsumed}.
func (s *Server) verifyUploadToken(r *http.Request, sessionID string) error {
	return s.verifyUploadTokenValue(r.Header.Get("X-Lenny-Upload-Token"), sessionID)
}

// verifyUploadTokenValue verifies an upload token against the session id. A
// server with no verifier, as in a test or the minimal gateway, admits
// every token; production always wires one. An empty token is
// uploadtoken.ErrInvalid.
func (s *Server) verifyUploadTokenValue(tok, sessionID string) error {
	if s.uploadVerifier == nil {
		return nil
	}
	if tok == "" {
		return uploadtoken.ErrInvalid
	}
	_, err := s.uploadVerifier.Verify(tok, sessionID)
	return err
}

// writeUploadTokenError writes the envelope of an upload token refusal.
func (s *Server) writeUploadTokenError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, uploadtoken.ErrExpired):
		s.writeError(w, http.StatusUnauthorized, "UPLOAD_TOKEN_EXPIRED", "upload token has expired", nil)
	case errors.Is(err, uploadtoken.ErrSessionMismatch):
		s.writeError(w, http.StatusForbidden, "UPLOAD_TOKEN_MISMATCH", "upload token does not match session id", nil)
	case errors.Is(err, uploadtoken.ErrConsumed):
		s.writeError(w, http.StatusGone, "UPLOAD_TOKEN_CONSUMED", "upload token already consumed", nil)
	default:
		s.writeError(w, http.StatusUnauthorized, "UPLOAD_TOKEN_INVALID", err.Error(), nil)
	}
}
