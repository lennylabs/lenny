// SPDX-License-Identifier: MIT

package seam

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// UploadWindows is served by the upload handler to the session core.
type UploadWindows interface {
	// CloseUploads ends a session's upload window: it aborts the session's
	// uploads in progress and releases its upload limits.
	CloseUploads(sessionID string)

	// RecordExtractionAbort is GWSEAM-RecordExtractionAbort, called by the
	// archive extraction that still runs in the session core.
	RecordExtractionAbort(errorType string)
}

// BudgetTracking is served by the metering proxy to the session core: the
// per-session budget state the proxy holds, which the core forgets for a
// settled session and sweeps for sessions that ended elsewhere.
type BudgetTracking interface {
	// ForgetBudget is GWSEAM-ForgetBudget.
	ForgetBudget(sessionID string)

	// TrackedBudgetSessions is GWSEAM-TrackedBudgetSessions.
	TrackedBudgetSessions() []string
}

// BudgetResolutions is served by the metering proxy to the platform tools
// seam: the resolution of a budget extension on the budget the proxy
// enforces.
type BudgetResolutions interface {
	// RaiseBudget is GWSEAM-RaiseBudget.
	RaiseBudget(sessionID string, delta int64)

	// TerminateForBudget is GWSEAM-TerminateForBudget.
	TerminateForBudget(sessionID string)
}

// BudgetExtensions is served by the platform tools seam to the metering
// proxy.
type BudgetExtensions interface {
	// ExtendForBudget is GWSEAM-ExtendForBudget. reqCtx is the proxied
	// request's context and waitCtx bounds the in-path wait.
	ExtendForBudget(reqCtx, waitCtx context.Context, tenantID, sessionID string) (BudgetExtension, error)
}

// BudgetExtension is the outcome of ExtendForBudget.
type BudgetExtension int

// The budget extension outcomes.
const (
	// BudgetGranted means the session's budget was raised within the wait.
	BudgetGranted BudgetExtension = iota + 1
	// BudgetPending means the wait ended while the extension is still
	// open; the session denies per request until it resolves.
	BudgetPending
	// BudgetTerminal means the extension was refused or failed, so the
	// proxy ends the session.
	BudgetTerminal
)

// DelegationAdmin is served by the platform tools seam to the API front.
type DelegationAdmin interface {
	// ClearSubtreeDenial is GWSEAM-ClearSubtreeDenial.
	ClearSubtreeDenial(ctx context.Context, tenantID, rootSessionID, sessionID string) error

	// ElicitationFloor is GWSEAM-ElicitationFloor.
	ElicitationFloor() string
}

// RequestIdentity is the request identity the API front answers for the
// seams that handle client requests.
type RequestIdentity interface {
	// RequestTenant is GWSEAM-RequestTenant. It returns the empty string
	// for a request whose tenant did not resolve, which a route behind the
	// API front's tenant check never receives.
	RequestTenant(r *http.Request) string
}

// BearerRevocations is served by the API front to the platform tools seam.
type BearerRevocations interface {
	// IsBearerRevoked is GWSEAM-IsBearerRevoked.
	IsBearerRevoked(ctx context.Context, tenantID, jti string) (bool, error)
}

// DelegationTree is served by the platform tools seam to the session core.
type DelegationTree interface {
	// ReturnBudget returns the budget a node held to its tree.
	ReturnBudget(ctx context.Context, r BudgetReturn) error

	// RearmBudget restores the expiry of the budget keys of a tree and of
	// its parent nodes.
	RearmBudget(ctx context.Context, rootSessionID string, parentSessionIDs []string) error

	// ObserveHighWatermark returns the highest count of parallel children
	// a tree reached, or found false.
	ObserveHighWatermark(ctx context.Context, rootSessionID string) (value int64, found bool, err error)

	// ArchiveNode records a settled node in the tree archive.
	ArchiveNode(ctx context.Context, n ArchivedNode) error

	// ArchivedNodes returns the archived nodes of a tree in settle order.
	ArchivedNodes(ctx context.Context, tenantID, rootSessionID string) ([]ArchivedNode, error)

	// ArchivedNodeFor returns the archived node of one session, or
	// ErrSessionNotFound.
	ArchivedNodeFor(ctx context.Context, tenantID, nodeSessionID string) (ArchivedNode, error)

	// NodeUsage returns the usage of one node.
	NodeUsage(ctx context.Context, node Session) *sessionrecord.Usage

	// TreeUsage returns the usage of a subtree rolled up from its live and
	// archived nodes, given the usage of its root.
	TreeUsage(ctx context.Context, node Session, root *sessionrecord.Usage) *sessionrecord.TreeUsage

	// SubtreeTokens returns the tokens a session's live and archived
	// descendants used.
	SubtreeTokens(ctx context.Context, node Session) (int64, error)

	// Orphaned reports whether a session's chain of ancestors, among the
	// records of tree keyed by session identifier, is terminal or broken.
	Orphaned(node Session, tree map[string]Session) bool

	// RecoverTree is GWSEAM-RecoverTree. nodes is the session core's side
	// of the recovery.
	RecoverTree(ctx context.Context, tenantID, rootSessionID string, nodes TreeNodes) (TreeRecoverySummary, error)
}

// BudgetReturn is the budget one node returns to its tree: each cap and
// the delta the node held against it.
type BudgetReturn struct {
	RootSessionID         string
	ParentSessionID       string
	TreeSizeCap           int64
	TreeSizeDelta         int64
	TreeMemoryCap         int64
	TreeMemoryDelta       int64
	ParallelChildrenCap   int64
	ParallelChildrenDelta int64
	ChildrenTotalCap      int64
	ChildrenTotalDelta    int64
	TokenCap              int64
	TokenDelta            int64
}

// ArchivedNode is a settled node of a delegation tree.
type ArchivedNode struct {
	TenantID        string
	RootSessionID   string
	NodeSessionID   string
	ParentSessionID string
	State           string
	Result          string
	SettledAt       time.Time
	ArchivedAt      time.Time
	CompletionSeq   int64
}

// TreeRecoverySummary reports a tree recovery: the nodes found
// recoverable, the nodes recovered, and the nodes failed.
type TreeRecoverySummary struct {
	Recoverable int
	Recovered   int
	Failed      int
}

// InputWaits is served by the platform tools seam to the session core: the
// registry of the input requests a runtime opens.
type InputWaits interface {
	// PendingInputWaits returns the identifiers of a session's open input
	// requests.
	PendingInputWaits(sessionID string) []string

	// ResolveInputWait answers one open input request.
	ResolveInputWait(sessionID, requestID, answer string) error

	// ForgetInputWaits discards every open input request of a session.
	ForgetInputWaits(sessionID string)
}

// ToolApprovals is served by the platform tools seam to the session core:
// the registry of the waits for the approval of a tool call.
type ToolApprovals interface {
	// RegisterApproval opens an approval wait and returns the channel its
	// decision arrives on.
	RegisterApproval(sessionID, toolCallID string) (<-chan ApprovalDecision, error)

	// CancelApproval withdraws an approval wait.
	CancelApproval(sessionID, toolCallID string)

	// ResolveApproval delivers a decision on an approval wait.
	ResolveApproval(sessionID, toolCallID string, d ApprovalDecision) error

	// AwaitApproval is GWSEAM-AwaitApproval. It blocks until the decision
	// arrives or ctx ends.
	AwaitApproval(ctx context.Context, tenantID, sessionID string, call PendingToolCall) (ApprovalDecision, error)
}

// PendingToolCall is a runtime's tool call that waits for approval.
type PendingToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ApprovalDecision is the decision on a tool call's approval.
type ApprovalDecision struct {
	Approved bool
	Reason   string
}

// CallingSessions is served by the platform tools seam to the session
// core's forward listener.
type CallingSessions interface {
	// AttachCallingSession is GWSEAM-AttachCallingSession. It returns
	// ErrSessionNotFound for an unknown calling session.
	AttachCallingSession(ctx context.Context, sessionID string) (context.Context, error)
}

// Events is served by the stream proxy to the session core and every seam.
// It is the only path to the session event bus.
type Events interface {
	// Publish publishes an event on a session's stream and returns it with
	// the sequence number it was given. An empty tenant publishes on the
	// session's stream without a tenant scope.
	Publish(tenantID, sessionID, eventType, data string, at time.Time) Event

	// Subscribe is GWSEAM-Subscribe. buffer sizes the channel, and the
	// caller calls the returned function to end the subscription.
	Subscribe(tenantID, sessionID string, afterSeq uint64, buffer int) (<-chan Event, func(), error)

	// OldestRetainedSeq returns the sequence number of the oldest event
	// retained for a session, or false.
	OldestRetainedSeq(sessionID string) (uint64, bool)

	// Broadcast is GWSEAM-Broadcast. It returns the count of streams the
	// event reached.
	Broadcast(eventType, data string, at time.Time) int

	// StreamStats is GWSEAM-StreamStats.
	StreamStats() StreamStats
}

// StreamStats is the result of Events.StreamStats. Utilization is a
// fraction of the replay buffer, for the fullest session.
type StreamStats struct {
	ActiveSubscribers          int
	MaxReplayBufferUtilization float64
}

// Event is one event on a session's stream.
type Event struct {
	Seq       uint64
	SessionID string
	Type      string
	Data      string
	Timestamp time.Time
}
