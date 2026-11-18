// SPDX-License-Identifier: MIT

package uploadhandler

import (
	"context"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// ErrIncomplete reports Options that lack Sessions, Identity, Tokens, or
// Quota.
var ErrIncomplete = errors.New("uploadhandler: options need Sessions, Identity, Tokens, and Quota")

// RuntimeRegistry reads the runtime a session is bound to, so the
// mid-session upload can check the runtime's capabilities.midSessionUpload
// flag.
type RuntimeRegistry interface {
	Get(ctx context.Context, name string) (runtimestore.Runtime, error)
}

// Options configures a Handler. Sessions, Identity, Tokens, and Quota are
// required, so a composition root that leaves out the token or the quota
// operation fails to build rather than admitting uploads unchecked. Every
// other dependency is optional, and a nil one leaves its work out, as the
// field says.
type Options struct {
	// Sessions looks up the session an upload names.
	Sessions seam.Sessions
	// Identity resolves the tenant a request runs as.
	Identity seam.RequestIdentity
	// Forwarding forwards a mid-session upload to the session's holder
	// replica. Nil serves every mid-session upload on this replica.
	Forwarding seam.RequestForwarding
	// Workspaces writes a mid-session upload into the session's workspace.
	// Nil answers every mid-session upload TARGET_NOT_READY.
	Workspaces seam.Workspaces
	// Tokens verifies the upload token of a plain or archive upload. The
	// session core's side admits every token when it has no verifier, as a
	// test or the minimal gateway does.
	Tokens seam.UploadTokens
	// Quota holds an upload against the tenant's storage quota. The session
	// core's side holds no reservation when it has no quota wired.
	Quota seam.StorageQuota
	// Audit records the session upload audit event. Nil records none.
	Audit seam.UploadAudit
	// Blobs stores staged uploads and serves the blob read. Nil answers
	// both BLOBSTORE_UNAVAILABLE.
	Blobs blobstore.Store
	// Runtimes reads the runtime a mid-session upload's session is bound
	// to. Nil leaves the mid-session capability off.
	Runtimes RuntimeRegistry
	// Subsystem is the §4.1 upload handler subsystem gate: its breaker and
	// concurrency limiter answer SUBSYSTEM_UNAVAILABLE while the stream
	// proxy and the platform tools seam keep serving. Nil runs uploads
	// without the gate.
	Subsystem *subsystem.Subsystem
	// Metrics receives the §16.1 upload handler observations. Nil drops
	// them.
	Metrics UploadHandlerMetrics
	// MaxConcurrentUploadsPerSession is the §11.1 per-session
	// concurrent-upload admission cap. Zero leaves it unlimited.
	MaxConcurrentUploadsPerSession int
	// MaxConcurrentUploadsGlobal is the §11.1 per-replica concurrent-upload
	// admission cap. Zero leaves it unlimited.
	MaxConcurrentUploadsGlobal int
	// MaxUploadBytesPerSession is the §11.1 per-session cumulative
	// upload-size cap. Zero leaves it unlimited.
	MaxUploadBytesPerSession int64
	// MidSessionUploadEnabled is the §7.4 deployer policy that admits
	// mid-session uploads when the bound runtime also declares
	// capabilities.midSessionUpload.
	MidSessionUploadEnabled bool
}

// Handler is the upload handler. It serves the session core
// seam.UploadWindows and registers its routes through seam.Routes.
type Handler struct {
	sessions   seam.Sessions
	identity   seam.RequestIdentity
	forwarding seam.RequestForwarding
	workspaces seam.Workspaces
	tokens     seam.UploadTokens
	quota      seam.StorageQuota
	audit      seam.UploadAudit
	blobs      blobstore.Store
	runtimes   RuntimeRegistry
	subsystem  *subsystem.Subsystem
	metrics    UploadHandlerMetrics

	// aborts is the §7.4 upload abort registry that CloseUploads fires.
	aborts *uploadAbortRegistry
	// limits enforces the §11.1 admission caps. Nil when none is set.
	limits *uploadLimiter

	midSessionUploadEnabled bool
}

// New returns a Handler, or ErrIncomplete when opts lacks Sessions,
// Identity, Tokens, or Quota.
func New(opts Options) (*Handler, error) {
	if opts.Sessions == nil || opts.Identity == nil || opts.Tokens == nil || opts.Quota == nil {
		return nil, ErrIncomplete
	}
	return &Handler{
		sessions:                opts.Sessions,
		identity:                opts.Identity,
		forwarding:              opts.Forwarding,
		workspaces:              opts.Workspaces,
		tokens:                  opts.Tokens,
		quota:                   opts.Quota,
		audit:                   opts.Audit,
		blobs:                   opts.Blobs,
		runtimes:                opts.Runtimes,
		subsystem:               opts.Subsystem,
		metrics:                 opts.Metrics,
		aborts:                  newUploadAbortRegistry(),
		limits:                  newUploadLimiter(opts.MaxConcurrentUploadsPerSession, opts.MaxConcurrentUploadsGlobal, opts.MaxUploadBytesPerSession),
		midSessionUploadEnabled: opts.MidSessionUploadEnabled,
	}, nil
}

// RegisterRoutes registers the upload handler's routes on the client
// listener. The three upload routes take the manage check on the path's
// session. The blob read takes only the tenant check, since handleBlob
// compares the blob URI's tenant with the caller's.
//
// spec: GWSEAM-RegisterRoutes
func (h *Handler) RegisterRoutes(routes seam.Routes) error {
	for _, r := range []seam.Route{
		{Method: http.MethodPost, Pattern: "/v1/sessions/{id}/upload", Access: seam.AccessSessionManage, Handler: http.HandlerFunc(h.handleUpload)},
		{Method: http.MethodPost, Pattern: "/v1/sessions/{id}/upload-archive", Access: seam.AccessSessionManage, Handler: http.HandlerFunc(h.handleUploadArchive)},
		{Method: http.MethodPost, Pattern: "/v1/sessions/{id}/upload-to-session", Access: seam.AccessSessionManage, Handler: http.HandlerFunc(h.handleUploadToSession)},
		{Method: http.MethodGet, Pattern: "/v1/blobs/{ref...}", Access: seam.AccessTenant, Handler: http.HandlerFunc(h.handleBlob)},
	} {
		r.Listener = seam.ListenerClient
		if err := routes.HandleRoute(r); err != nil {
			return err
		}
	}
	return nil
}

// CloseUploads ends a session's upload window: it fires the §7.4 abort
// signal of every in-flight upload on the session, so a later register gets
// an already-closed signal, and drops the session's §11.1 cumulative byte
// total. In-flight concurrency slots release themselves.
//
// spec: GWSEAM-CloseUploads
func (h *Handler) CloseUploads(sessionID string) {
	h.aborts.closeSession(sessionID)
	h.limits.closeSession(sessionID)
}

// RecordExtractionAbort counts one archive extraction abort by its §13.4
// error type in lenny_upload_extraction_aborted_total.
//
// spec: GWSEAM-RecordExtractionAbort
func (h *Handler) RecordExtractionAbort(errorType string) {
	if h.metrics == nil {
		return
	}
	h.metrics.AddExtractionAbort(errorType)
}

// writeError writes the client API error envelope.
func (h *Handler) writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	errorclassify.WriteError(w, status, code, message, details)
}

// writePreconditionError writes the envelope of a session precondition
// refusal.
func (h *Handler) writePreconditionError(w http.ResponseWriter, err error) {
	errorclassify.WritePreconditionError(w, err)
}
