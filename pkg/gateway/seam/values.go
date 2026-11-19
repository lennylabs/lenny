// SPDX-License-Identifier: MIT

package seam

import (
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/session/sessionrow"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// Session is the session record that crosses the seam interface: the
// fields of a stored session that a seam reads. A seam that needs more of
// the session asks the core for it through an operation rather than
// reading the session store of another part.
type Session struct {
	ID              string
	TenantID        string
	UserID          string
	ParentSessionID string
	RootSessionID   string
	RuntimeRef      string
	Pool            string
	State           state.State
	// LastSeq is the sequence number of the session's last event.
	LastSeq uint64
	// Stored is the row of the session store a seam read or wrote and
	// hands to the session core unchanged, so that an operation that runs
	// the core's logic on that row reads the row the caller acted on rather
	// than reading the session again. The core returns the row its logic
	// produced in the same field, and the seam reads that row back. A row
	// whose identifier or tenant differs from the record's is ignored. It is
	// nil when the caller holds no row. Each side sets it to a copy of its
	// own row and reads the row through it without writing through it.
	Stored *sessionrow.Session
	// DelegationDepth is the session's depth in its delegation tree, zero
	// for a tree root.
	DelegationDepth int
	// PodAssignment names the pod the session is bound to, empty when it
	// holds none.
	PodAssignment string
	// CreatedAt, UpdatedAt, and TerminatedAt are the times the session was
	// created, last written, and moved to a terminal state; TerminatedAt is
	// zero for a session that is not terminal.
	CreatedAt    time.Time
	UpdatedAt    time.Time
	TerminatedAt time.Time
}

// SessionAction is the access a caller asks for on a session.
type SessionAction int

// The session actions.
const (
	// SessionRead reads a session and its streams.
	SessionRead SessionAction = iota + 1
	// SessionManage changes a session: it sends to, uploads to, or ends it.
	SessionManage
)

// ForwardKind names the request a forward to the holder replica carries.
// The session core reads it to pick the states a forward applies to and to
// answer HoldsUndelivered.
type ForwardKind string

// ForwardUpload is the forward kind of a mid-session upload.
const ForwardUpload ForwardKind = "upload"

// ForwardOutcome is the result of a forward to the holder replica.
type ForwardOutcome int

// The forward outcomes.
const (
	// ForwardLocal means the request runs on this replica, which holds the
	// session or serves it without a holder.
	ForwardLocal ForwardOutcome = iota + 1
	// ForwardDelivered means the holder replica received the request and
	// its response has been relayed.
	ForwardDelivered
	// ForwardNotDelivered means the request reached no replica that acted
	// on it, so the caller may retry or hold it.
	ForwardNotDelivered
	// ForwardUnknown means the request left this replica and the reply was
	// lost, so the caller cannot tell whether the holder acted on it.
	ForwardUnknown
)

// The errors the operations return for a failure the caller branches on.
// An implementation wraps the sentinel so errors.Is matches it.
var (
	// ErrSessionNotFound reports a session that does not exist, or that the
	// caller may not see.
	ErrSessionNotFound = errors.New("seam: session not found")
	// ErrUnauthenticated reports a request that carries no caller identity.
	ErrUnauthenticated = errors.New("seam: caller not authenticated")
	// ErrForbidden reports a caller whose role does not grant the action.
	ErrForbidden = errors.New("seam: action not permitted")
	// ErrNoLiveBinding reports a session whose pod is not bound on this
	// replica.
	ErrNoLiveBinding = errors.New("seam: session has no live pod binding on this replica")
	// ErrSessionSealed reports a session sealed against further delivery.
	ErrSessionSealed = errors.New("seam: session sealed against delivery")
	// ErrNotBound reports a session that has no runtime to deliver to.
	ErrNotBound = errors.New("seam: session not bound")
	// ErrHeldDeliveryPreempted reports a delivery of held messages that
	// another delivery to the same session took over.
	ErrHeldDeliveryPreempted = errors.New("seam: held delivery preempted")
	// ErrChildCredentialUnavailable reports a delegation whose child would
	// find no credential available to it.
	ErrChildCredentialUnavailable = errors.New("seam: child credential unavailable")
	// ErrChildUserCredentialNotFound reports a delegation whose child needs
	// a user credential the user has not registered.
	ErrChildUserCredentialNotFound = errors.New("seam: child user credential not found")
	// ErrChildCredentialAssignment reports a child whose credential
	// assignment failed while it was materialized.
	ErrChildCredentialAssignment = errors.New("seam: child credential assignment failed")
	// ErrPoolWarming reports a child whose pool had no warm pod yet.
	ErrPoolWarming = errors.New("seam: pool warming")
	// ErrWorkspaceWrite reports a workspace write the pod's adapter failed.
	ErrWorkspaceWrite = errors.New("seam: workspace write failed")
	// ErrUploadTokenInvalid reports an upload token that is absent,
	// malformed, or not signed by a current key.
	ErrUploadTokenInvalid = errors.New("seam: upload token invalid")
	// ErrUploadTokenExpired reports an upload token past its expiry.
	ErrUploadTokenExpired = errors.New("seam: upload token expired")
	// ErrUploadTokenMismatch reports an upload token issued for another
	// session.
	ErrUploadTokenMismatch = errors.New("seam: upload token issued for another session")
	// ErrUploadTokenConsumed reports an upload token that a finalize
	// already consumed.
	ErrUploadTokenConsumed = errors.New("seam: upload token consumed")
	// ErrStorageQuotaExceeded reports an upload its tenant's storage quota
	// refuses.
	ErrStorageQuotaExceeded = errors.New("seam: storage quota exceeded")
	// ErrStorageQuotaUnavailable reports a storage quota that cannot be
	// read, so the upload is refused rather than admitted unchecked.
	ErrStorageQuotaUnavailable = errors.New("seam: storage quota unavailable")
	// ErrContentLengthRequired reports an upload that declares no size for
	// a tenant with a storage quota.
	ErrContentLengthRequired = errors.New("seam: upload declares no content length")
)

// SealedError reports how many messages a session accepted before it was
// sealed against delivery, with the response to them. It unwraps to
// ErrSessionSealed.
type SealedError struct {
	Delivered int
	Response  Response
}

func (e *SealedError) Error() string {
	return ErrSessionSealed.Error() + " after a partial delivery"
}

// Unwrap returns ErrSessionSealed.
func (e *SealedError) Unwrap() error { return ErrSessionSealed }

// Refusal is a refusal an operation reports with a client API error code,
// such as UNAUTHORIZED, a quota code, a policy code, or an environment
// access code, with its message and details. A refusal is not transient:
// the caller reports it and does not retry the operation.
type Refusal struct {
	Code    string
	Message string
	Details map[string]any
}

func (e *Refusal) Error() string { return "seam: refused: " + e.Code + ": " + e.Message }

// IsRefusal reports whether err is or wraps a *Refusal, and returns it.
func IsRefusal(err error) (*Refusal, bool) {
	var r *Refusal
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}

// CapRefusal reports a resume that a tenant limit refuses, with the message
// and the details the client error carries.
type CapRefusal struct {
	Message string
	Details map[string]any
}

func (e *CapRefusal) Error() string { return "seam: resume refused: " + e.Message }
