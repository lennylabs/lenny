// SPDX-License-Identifier: MIT

package seam

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/lennylabs/lenny/pkg/session/state"
)

// Sessions is served by the session core to every seam.
type Sessions interface {
	// LookupSession returns the session record of a session in a tenant,
	// or ErrSessionNotFound.
	LookupSession(ctx context.Context, tenantID, sessionID string) (Session, error)

	// AuthorizeSession resolves whether the caller the context carries may
	// take an action on a session, and returns the session record when it
	// may. It returns ErrUnauthenticated, ErrForbidden, or
	// ErrSessionNotFound, which also stands for a session the caller may
	// not see.
	AuthorizeSession(ctx context.Context, tenantID, sessionID string, action SessionAction) (Session, error)
}

// RequestForwarding is served by the session core to the seams that serve
// HTTP requests: the delivery of a client request to the replica that holds
// the session's coordination lease.
type RequestForwarding interface {
	// ForwardRequest delivers an HTTP request for a session to its holder
	// replica and relays the response on w, or returns ForwardLocal when
	// the request runs on this replica. On ForwardNotDelivered and
	// ForwardUnknown it has written the refusal to w, and the caller
	// returns on any outcome other than ForwardLocal.
	ForwardRequest(w http.ResponseWriter, r *http.Request, s Session, kind ForwardKind, body io.Reader) ForwardOutcome
}

// Forwarding is served by the session core to the platform tools seam: the
// delivery of a platform tool call to the replica that holds the session's
// coordination lease.
type Forwarding interface {
	// ForwardToolCall delivers a platform tool call that addresses a
	// session to its holder replica and returns the tool result. The
	// holder's refusal envelope arrives as a *Refusal, which the caller
	// does not retry.
	ForwardToolCall(ctx context.Context, s Session, kind ForwardKind, tool string, arguments json.RawMessage) (ToolResult, ForwardOutcome, error)

	// Forwarded reports whether the request the context carries arrived
	// from another replica through the forward listener.
	Forwarded(ctx context.Context) bool

	// HoldsUndelivered reports whether a message whose forward was not
	// delivered is held for later delivery.
	HoldsUndelivered(s Session, kind ForwardKind) bool
}

// ToolResult is the result of a platform tool call, in the encoding the
// platform tools seam returns to the runtime.
type ToolResult struct {
	// Content is the encoded content blocks of the result.
	Content json.RawMessage
	// IsError marks a result that reports a tool error.
	IsError bool
}

// ChildSessions is served by the session core to the platform tools seam.
type ChildSessions interface {
	// CreateSession is GWSEAM-CreateSession. body is the JSON of a client
	// API create request. A refusal (authorization, quota, policy, or
	// environment access) comes back as a *Refusal with the client API
	// error code, which the caller does not retry.
	CreateSession(ctx context.Context, tenantID string, body json.RawMessage) (CreatedSession, error)

	// MaterializeChild is GWSEAM-MaterializeChild. It returns
	// ErrChildCredentialAssignment or ErrPoolWarming for the failures the
	// caller reports to the parent.
	MaterializeChild(ctx context.Context, tenantID, childID string) (state.State, error)

	// CheckChildCredentials checks, before a delegation, that a child of a
	// runtime would have the credentials it needs. It returns
	// ErrChildCredentialUnavailable or ErrChildUserCredentialNotFound.
	CheckChildCredentials(ctx context.Context, q ChildCredentialQuery) error

	// ApplyExperimentRouting is GWSEAM-ApplyExperimentRouting. It may
	// rewrite the runtime and pool of the child record it is given.
	ApplyExperimentRouting(ctx context.Context, child *Session) error

	// EffectiveMaxRetries returns the retry budget of a session.
	EffectiveMaxRetries(s Session) int
}

// Terminals is the terminal pipeline of the session core, which a seam or
// the API front runs after its own terminal write.
type Terminals interface {
	// BeginTerminal is GWSEAM-BeginTerminal. The caller calls the returned
	// function when its terminal work ends, so shutdown waits for it.
	BeginTerminal(sessionID string) (end func())

	// OnSessionTerminal is GWSEAM-OnSessionTerminal. from is the state the
	// caller's write left.
	OnSessionTerminal(ctx context.Context, from state.State, s Session)

	// AfterTerminalWrite is GWSEAM-AfterTerminalWrite. signals runs the
	// caller's own terminal signals inside the pipeline; the result is the
	// session record after the pipeline.
	AfterTerminalWrite(ctx context.Context, from state.State, s Session, signals func(context.Context, Session)) Session
}

// ForceTerminations is served by the session core to the API front: the
// operator's force-terminate of a session.
type ForceTerminations interface {
	// ForceTerminate is GWSEAM-ForceTerminate. It reports the session
	// after the write, the state it left, and whether this call moved it.
	ForceTerminate(ctx context.Context, tenantID, sessionID string) (s Session, previous state.State, transitioned bool, err error)
}

// CallingSessions is served by the session core to the platform tools
// seam's bridge of the platform tool calls a session's pod makes over
// LNK-GWCONTROL. The core's forward listener resolves a forwarded tool
// call's calling session the same way.
type CallingSessions interface {
	// AttachCallingSession is GWSEAM-AttachCallingSession. It returns
	// ErrSessionNotFound for an unknown calling session.
	AttachCallingSession(ctx context.Context, sessionID string) (context.Context, error)
}

// Revocations is served by the session core to the API front: the session
// side of a credential or user revocation.
type Revocations interface {
	// AfterRevokeWrite is GWSEAM-AfterRevokeWrite.
	AfterRevokeWrite(ctx context.Context, s Session)

	// ReclaimRevokedPreRunning is GWSEAM-ReclaimRevokedPreRunning.
	ReclaimRevokedPreRunning(ctx context.Context, from state.State, s Session)

	// TerminateUserSessions is GWSEAM-TerminateUserSessions.
	TerminateUserSessions(ctx context.Context, tenantID, userID string, sessionIDs []string) UserTermination
}

// UserTermination reports TerminateUserSessions: the pods released and the
// sessions whose release failed.
type UserTermination struct {
	PodsTerminated int
	FailedSessions []string
}

// Activity is served by the session core to the platform tools seam and the
// metering proxy.
type Activity interface {
	// StampActivity is GWSEAM-StampActivity.
	StampActivity(tenantID, sessionID string)
}

// SessionExpiry is served by the session core to the metering proxy.
type SessionExpiry interface {
	// ExpireSession is GWSEAM-ExpireSession. reason is the failure reason
	// the expired session records. It returns at once: the state write and
	// the terminal pipeline run on the session core's own context.
	ExpireSession(sessionID, reason string)
}

// CredentialRotations is served by the session core to the metering proxy.
type CredentialRotations interface {
	// RotateCredential is GWSEAM-RotateCredential. A failure to mint the
	// replacement or to push it to the pod is the core's to record; the
	// caller has no outcome to branch on.
	RotateCredential(ctx context.Context, r CredentialRotation)
}

// CredentialRotation names the faulted credential lease of a session and
// the pool its replacement comes from. Provider and Trigger carry the
// provider and rotation trigger strings of the credential lease contract.
type CredentialRotation struct {
	TenantID  string
	SessionID string
	Provider  string
	// CoordinationGeneration is the coordination generation the faulted
	// lease recorded, which the replacement records too.
	CoordinationGeneration int64
	NextPool               string
	Trigger                string
}

// CreatedSession is what CreateSession returns.
type CreatedSession struct {
	ID          string
	State       state.State
	UploadToken string
}

// ChildCredentialQuery names the delegation CheckChildCredentials checks.
type ChildCredentialQuery struct {
	TenantID        string
	UserID          string
	ChildRuntimeRef string
	// CredentialOriginSessionID is the session whose credentials the
	// child would inherit.
	CredentialOriginSessionID string
}

// ClientAPI is served by the API front to the platform tools seam.
type ClientAPI interface {
	// InvokeClientAPI runs one client API request in process.
	InvokeClientAPI(ctx context.Context, call ClientAPICall) (ClientAPIResult, error)
}

// ClientAPICall is one client API request run in process.
type ClientAPICall struct {
	TenantID    string
	Method      string
	Target      string
	Body        []byte
	ContentType string
	Headers     map[string]string
}

// ClientAPIResult is the response to a ClientAPICall: its status, body,
// and content type, and for a status outside 2xx the error code, message,
// and details of the refusal. A result whose status is outside 2xx always
// carries a non-empty ErrorCode.
type ClientAPIResult struct {
	Status       int
	Body         []byte
	ContentType  string
	ErrorCode    string
	ErrorMessage string
	ErrorDetails map[string]any
}

// MessageDelivery is served by the session core to the platform tools seam.
type MessageDelivery interface {
	// SendMessages delivers messages to a running session and returns its
	// response. It returns ErrNotBound, ErrSessionSealed, or a
	// *SealedError after a partial delivery.
	SendMessages(ctx context.Context, sessionID string, messages []Message) (Response, error)

	// EnqueueInbox is GWSEAM-EnqueueInbox. The returned message is the one
	// evicted for room, or nil.
	EnqueueInbox(ctx context.Context, tenantID, sessionID string, msg InboxMessage) (*InboxMessage, error)

	// LockDelivery is GWSEAM-LockDelivery. The caller defers the returned
	// unlock.
	LockDelivery(ctx context.Context, sessionID string) (func(), error)

	// HoldMessage holds a message for a session that cannot take it now.
	HoldMessage(ctx context.Context, tenantID, sessionID string, msg InboxMessage) (HoldOutcome, state.State, error)

	// HoldUndeliveredForward holds a message whose forward was not
	// delivered.
	HoldUndeliveredForward(ctx context.Context, s Session, msg InboxMessage) (HoldOutcome, state.State, error)

	// DeliverHeld delivers the messages held for a session, or returns
	// ErrHeldDeliveryPreempted.
	DeliverHeld(ctx context.Context, s Session) error

	// ResumeSuspended resumes a suspended session for a delivery.
	ResumeSuspended(ctx context.Context, s Session) error

	// AdmitResume checks a resume against the tenant's limits and returns
	// a *CapRefusal when a limit refuses it.
	AdmitResume(ctx context.Context, s Session) error
}

// Message is one message SendMessages delivers.
type Message struct {
	ID      string
	Role    string
	Content string
	// FromKind and FromID name the sender, such as a session.
	FromKind string
	FromID   string
}

// Response is a session's response to delivered messages.
type Response struct {
	Parts       []MessagePart
	Annotations map[string]any
}

// MessagePart is one part of a Response.
type MessagePart struct {
	Type          string
	Text          string
	Ref           string
	SchemaVersion int
	Annotations   map[string]any
}

// InboxMessage is a message held in a session's inbox or held for later
// delivery.
type InboxMessage struct {
	MessageID       string
	SenderSessionID string
	Payload         []byte
	EnqueuedAtUnix  int64
}

// HoldOutcome is the result of holding a message.
type HoldOutcome int

// The hold outcomes.
const (
	// HoldHeld means the message is held for later delivery.
	HoldHeld HoldOutcome = iota + 1
	// HoldTerminal means the session is terminal, so nothing is held.
	HoldTerminal
	// HoldDropped means the message was dropped.
	HoldDropped
)

// PodFiles is served by the session core to the platform tools seam.
type PodFiles interface {
	// ExportPaths is GWSEAM-ExportPaths. It returns ErrNoLiveBinding when
	// the session's pod is bound elsewhere or not at all.
	ExportPaths(ctx context.Context, sessionID string, specs []ExportSpec) ([]ExportedFile, error)
}

// ExportSpec selects files to export from a pod.
type ExportSpec struct {
	Source     string
	DestPrefix string
}

// ExportedFile is one file ExportPaths read.
type ExportedFile struct {
	Path    string
	Content []byte
	SHA256  string
	Size    int64
}

// PodRecycle is served by the session core to the scrub report service of
// the platform tools seam.
type PodRecycle interface {
	// IncrementSessionsServed increments a pod's count of sessions served.
	IncrementSessionsServed(ctx context.Context, podID string) (count int, found bool, err error)

	// IncrementScrubFailureCount increments a pod's count of failed scrubs.
	IncrementScrubFailureCount(ctx context.Context, podID string) (count int, found bool, err error)

	// RecycleCounters returns a pod's counts of sessions served and of
	// failed scrubs.
	RecycleCounters(ctx context.Context, podID string) (served, failures int, found bool, err error)

	// RecordLeak records that a slot of a pod leaked.
	RecordLeak(ctx context.Context, podID, slotID string) error

	// RetireOnSessionCount retires a pod that has served its maximum count
	// of sessions.
	RetireOnSessionCount(ctx context.Context, podID string, count int) error

	// InspectForRecycle returns a pod's recycle policy, or found false.
	InspectForRecycle(ctx context.Context, podID string) (policy PodRecyclePolicy, found bool, err error)

	// RecyclePod returns a pod to its pool after a successful scrub.
	RecyclePod(ctx context.Context, podID string, preConnect, scrubWarning bool) error

	// RetirePod retires a pod.
	RetirePod(ctx context.Context, podID string, r PodRetirement) error

	// RecordScrubFailure is GWSEAM-RecordScrubFailure. count is the pod's
	// cumulative count of failed scrubs after this one.
	RecordScrubFailure(podID, pool, runtimeClass string, count int)

	// RecordPodRetirement is GWSEAM-RecordPodRetirement. reason is a
	// retirement reason of the pod scrub contract.
	RecordPodRetirement(reason, pool, runtimeClass string)
}

// PodRecyclePolicy is what InspectForRecycle returns for one pod. The
// policy values are the strings the pod scrub contract defines.
type PodRecyclePolicy struct {
	PreConnect            bool
	OnScrubFailure        string
	MaxScrubFailures      int
	MaxSessionsPerPod     int
	MaxConcurrentSessions int
	MaxPodUptimeSeconds   int64
	VMRestart             bool
	HostSchedulable       bool
	PodUptimeSeconds      int64
	Pool                  string
	RuntimeClass          string
}

// PodRetirement describes the retirement of a pod.
type PodRetirement struct {
	Failed         bool
	ScrubWarning   bool
	Reason         string
	SessionsServed int
	UptimeSeconds  int64
	Detail         string
}

// Workspaces is served by the session core to the upload handler.
type Workspaces interface {
	// WriteWorkspace is GWSEAM-WriteWorkspace. It returns the count of
	// files written, ErrNoLiveBinding, or a *WorkspaceWriteError, which
	// matches ErrWorkspaceWrite, when the pod's adapter fails.
	WriteWorkspace(ctx context.Context, sessionID string, files []WorkspaceFile) (int, error)
}

// WorkspaceFile is one uploaded file, by its path relative to the
// workspace root.
type WorkspaceFile struct {
	Path    string
	Content []byte
	// Mode is the octal file mode, such as "0644". Empty leaves the mode to
	// the pod's adapter.
	Mode string
}

// WorkspacePhase names the step of a workspace write that failed.
type WorkspacePhase string

// The workspace write phases.
const (
	// WorkspaceStaging streams the files into the session's staging tree.
	WorkspaceStaging WorkspacePhase = "staging"
	// WorkspacePromotion overlays the staged files onto the session's
	// workspace and signals the runtime.
	WorkspacePromotion WorkspacePhase = "promotion"
)

// WorkspaceWriteError reports a workspace write that the pod's adapter
// failed, with the phase that failed and the adapter's error. Neither phase
// changes the live workspace when it fails. It matches ErrWorkspaceWrite
// and the adapter's error under errors.Is.
type WorkspaceWriteError struct {
	Phase WorkspacePhase
	Err   error
}

func (e *WorkspaceWriteError) Error() string {
	return "seam: workspace " + string(e.Phase) + " failed: " + e.Err.Error()
}

// Unwrap returns ErrWorkspaceWrite and the adapter's error.
func (e *WorkspaceWriteError) Unwrap() []error { return []error{ErrWorkspaceWrite, e.Err} }

// UploadTokens is served by the session core to the upload handler.
type UploadTokens interface {
	// VerifyUploadToken is GWSEAM-VerifyUploadToken. token is the value the
	// client presented, empty when it presented none. It returns nil, or
	// one of ErrUploadTokenInvalid, ErrUploadTokenExpired,
	// ErrUploadTokenMismatch, and ErrUploadTokenConsumed.
	VerifyUploadToken(ctx context.Context, sessionID, token string) error
}

// StorageQuota is served by the session core to the upload handler: the
// tenant storage quota an upload is held against.
type StorageQuota interface {
	// ReserveStorage is GWSEAM-ReserveStorage. incoming is the upload's
	// declared size, negative when the request declared none. It returns a
	// reservation whose Held is false when the tenant has no storage quota,
	// or one of a *StorageQuotaError, ErrStorageQuotaUnavailable, and
	// ErrContentLengthRequired.
	ReserveStorage(ctx context.Context, tenantID string, incoming int64) (StorageReservation, error)

	// AdjustStorage is GWSEAM-AdjustStorage. delta is negative to release
	// bytes.
	AdjustStorage(ctx context.Context, tenantID string, delta int64) error
}

// StorageReservation is a storage quota reservation that ReserveStorage
// returns.
type StorageReservation struct {
	// Held is false when the tenant has no storage quota, and the other
	// fields are then zero.
	Held bool
	// Reserved is the byte count reserved, the upload's declared size.
	Reserved int64
	// Headroom is the byte count the upload's stream may not exceed: the
	// tenant's quota less its usage before the reservation.
	Headroom int64
}

// StorageQuotaError reports an upload that would exceed its tenant's
// storage quota, with the tenant's usage and limit. It matches
// ErrStorageQuotaExceeded under errors.Is.
type StorageQuotaError struct {
	CurrentBytes int64
	LimitBytes   int64
}

func (e *StorageQuotaError) Error() string { return ErrStorageQuotaExceeded.Error() }

// Unwrap returns ErrStorageQuotaExceeded.
func (e *StorageQuotaError) Unwrap() error { return ErrStorageQuotaExceeded }

// UploadAudit is served by the session core to the upload handler.
type UploadAudit interface {
	// AuditUpload is GWSEAM-AuditUpload. It records the event without
	// waiting for its delivery.
	AuditUpload(ctx context.Context, ev UploadAuditEvent)
}

// UploadAuditEvent is one session upload audit event.
type UploadAuditEvent struct {
	// Session is the session the upload targeted.
	Session Session
	// Outcome is UploadAccepted or UploadRejected.
	Outcome string
	// Reason is the rejection sub-code of a rejected upload, such as
	// "size_limit". It is empty on an accepted upload.
	Reason string
	// Detail is an event-specific note, such as the reference of the
	// stored blob or the count of files written.
	Detail string
}

// The upload audit outcomes.
const (
	UploadAccepted = "accepted"
	UploadRejected = "rejected"
)

// StreamFrames is served by the session core to the stream proxy.
type StreamFrames interface {
	// ResumedFrame is GWSEAM-ResumedFrame. The bool is false when no
	// frame applies.
	ResumedFrame() ([]byte, bool)

	// ChildrenReattachedFrame is GWSEAM-ChildrenReattachedFrame. The bool
	// is false when no frame applies.
	ChildrenReattachedFrame(ctx context.Context, tenantID, parentID string, afterSeq uint64) ([]byte, bool)
}

// TreeNodes is served by the session core to RecoverTree.
type TreeNodes interface {
	// ListTree returns the session records of every node of a tree.
	ListTree(ctx context.Context, tenantID, rootSessionID string) ([]Session, error)

	// StartNode starts a node that had not started.
	StartNode(ctx context.Context, node Session)

	// ReattachNode reattaches a running node to this replica.
	ReattachNode(ctx context.Context, node Session) error

	// FailNode moves a node that cannot be recovered to a failed state.
	FailNode(ctx context.Context, node Session, reason string)
}
