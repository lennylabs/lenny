// SPDX-License-Identifier: MIT

package lenny

import (
	"encoding/json"
	"time"
)

// State is a §7.1 session lifecycle state. The gateway returns it as
// the `state` field of every session envelope.
type State string

// The §7.1 session states the REST surface exposes.
const (
	StateCreated   State = "created"
	StateReady     State = "ready"
	StateRunning   State = "running"
	StateSuspended State = "suspended"
	StateCompleted State = "completed"
	StateCancelled State = "cancelled"
	StateFailed    State = "failed"
	StateExpired   State = "expired"
)

// CreateSessionRequest is the body of POST /v1/sessions. RuntimeRef
// is required; the remaining fields are optional.
type CreateSessionRequest struct {
	// RuntimeRef identifies the runtime the session targets. Required.
	RuntimeRef string `json:"runtimeRef"`

	// UserID is the §10.2 user the session is created for. Optional.
	UserID string `json:"userId,omitempty"`

	// WorkspacePlan is the §14 workspace plan submitted at creation.
	// Optional; an absent plan starts the session with an empty
	// workspace.
	WorkspacePlan json.RawMessage `json:"workspacePlan,omitempty"`

	// Environment is the §10.6 environment the session is created in.
	// Optional.
	Environment string `json:"environment,omitempty"`

	// IsolationProfile pins the session to a §5.3 isolation profile.
	// Optional; the gateway resolves a default when this is empty.
	IsolationProfile string `json:"isolationProfile,omitempty"`
}

// Session is the §15.1 session envelope returned by GET, the
// transition endpoints, and DELETE.
type Session struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenantId"`
	UserID      string `json:"userId,omitempty"`
	RuntimeRef  string `json:"runtimeRef,omitempty"`
	Environment string `json:"environment,omitempty"`
	State       State  `json:"state"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`

	// FailureClass is populated when State is StateFailed.
	FailureClass string `json:"failureClass,omitempty"`

	// WorkspacePlan echoes the §14 plan stored at creation. Absent
	// when the session was created without a plan.
	WorkspacePlan json.RawMessage `json:"workspacePlan,omitempty"`
}

// IsolationLevel mirrors the §7.1 sessionIsolationLevel object on the
// create response.
type IsolationLevel struct {
	ExecutionMode        string `json:"executionMode"`
	IsolationProfile     string `json:"isolationProfile"`
	PodReuse             bool   `json:"podReuse"`
	ScrubPolicy          string `json:"scrubPolicy,omitempty"`
	ResidualStateWarning bool   `json:"residualStateWarning"`
}

// CreateSessionResult is the §15.1 POST /v1/sessions response. It
// embeds the session envelope and adds the §7.1 uploadToken and
// isolation-level fields.
type CreateSessionResult struct {
	Session

	// UploadToken is the §7.1 single-use upload token. Treat it as a
	// secret.
	UploadToken string `json:"uploadToken"`

	// IsolationLevel echoes the §7.1 sessionIsolationLevel object.
	IsolationLevel IsolationLevel `json:"sessionIsolationLevel"`
}

// ListOptions narrows GET /v1/sessions. An empty field applies no
// filter for that dimension.
type ListOptions struct {
	// State filters by session state.
	State State

	// Runtime filters by runtimeRef.
	Runtime string

	// Cursor carries the pagination cursor from a prior page's
	// NextCursor. An empty cursor requests the first page.
	Cursor string

	// Limit caps the page size. Zero leaves the gateway default in
	// place.
	Limit int
}

// SessionPage is one page of a GET /v1/sessions listing. The gateway
// returns the session array; the pagination fields are populated
// from the §25.2 pagination envelope when the gateway supplies it.
type SessionPage struct {
	// Sessions is the page of session envelopes.
	Sessions []Session `json:"sessions"`

	// NextCursor is the cursor for the following page. It is empty
	// when HasMore is false.
	NextCursor string `json:"-"`

	// HasMore reports whether more pages follow this one.
	HasMore bool `json:"-"`

	// Total is the §15.1 line 1252 total match count across all pages.
	// It is nil when the gateway omits it (the count would require a
	// full table scan); callers must not rely on its presence.
	Total *int64 `json:"-"`
}

// DeliveryReceipt mirrors the §15.4 lines 1725-1737 delivery_receipt
// envelope returned by POST /v1/sessions/{id}/messages. The closed
// `status` enum lets the SDK consumer branch on delivered vs queued vs
// dropped vs expired vs rate_limited vs error.
//
// spec: §15.4 lines 1725-1737; §7.2 line 345.
type DeliveryReceipt struct {
	// MessageID is the gateway-stamped message id. The sender may
	// supply one; when absent the gateway assigns `msg_<random>`.
	MessageID string `json:"messageId"`

	// Status is one of `delivered` | `queued` | `dropped` | `expired`
	// | `rate_limited` | `error`.
	Status string `json:"status"`

	// DeliveredAt is the RFC 3339 timestamp the gateway accepted the
	// message for delivery. Empty when Status is not `delivered`.
	DeliveredAt string `json:"deliveredAt,omitempty"`

	// Reason carries an optional disposition reason for a non-`delivered`
	// status (e.g. `target_terminated`, `dlq_overflow`).
	Reason string `json:"reason,omitempty"`
}

// MessagePayload is one §15.4 inbound message envelope on the
// POST /v1/sessions/{id}/messages request batch. The wire field names
// mirror the spec verbatim. spec: §15.4 lines 1672-1721.
type MessagePayload struct {
	// ID is an optional client-supplied message id. When absent the
	// gateway stamps one of the form `msg_<random>`.
	ID string `json:"id,omitempty"`

	// Role is the message role (`user`, `assistant`, etc.) — the
	// runtime interprets it.
	Role string `json:"role,omitempty"`

	// Content is the message body delivered to the runtime.
	Content string `json:"content"`

	// InReplyTo, when set, names a pending `lenny/request_input`
	// request the gateway resolves directly (§7.2 path 1) instead of
	// delivering the message to the executor.
	InReplyTo string `json:"inReplyTo,omitempty"`

	// Delivery is the §15.4 closed enum: `queued` (default) or
	// `immediate`. Unknown values reject with
	// `400 INVALID_DELIVERY_VALUE`.
	Delivery string `json:"delivery,omitempty"`

	// SlotID is the §5.2 concurrent-workspace slot identifier.
	SlotID string `json:"slotId,omitempty"`
}

// SendMessagesRequest is the body of POST /v1/sessions/{id}/messages.
// spec: §15.1 messages endpoint; §15.4 MessageEnvelope.
type SendMessagesRequest struct {
	// Messages is the inbound batch. At least one entry is required.
	Messages []MessagePayload `json:"messages"`
}

// OutputPart mirrors the §8.5 OutputPart shape the executor returns
// alongside the delivery receipt. The SDK exposes it as the
// caller-facing structured output of a synchronous message injection.
type OutputPart struct {
	Type string          `json:"type"`
	Text string          `json:"text,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// SendMessagesResponse is the POST /v1/sessions/{id}/messages response.
// spec: §15.1; §15.4 delivery_receipt; §7.2 line 345.
type SendMessagesResponse struct {
	// DeliveryReceipt is the §15.4 receipt envelope.
	DeliveryReceipt DeliveryReceipt `json:"deliveryReceipt"`

	// Output is the executor's synchronous response. Empty when the
	// executor delivered the message but produced no immediate output.
	Output []OutputPart `json:"output,omitempty"`
}

// TranscriptEntry mirrors one row of the §15.1 transcript page.
type TranscriptEntry struct {
	Seq       uint64          `json:"seq"`
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	CreatedAt string          `json:"createdAt,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

// TranscriptResponse is the §15.1 GET /v1/sessions/{id}/transcript
// envelope.
type TranscriptResponse struct {
	SessionID string            `json:"sessionId"`
	Entries   []TranscriptEntry `json:"entries"`
}

// TranscriptOptions narrows GET /v1/sessions/{id}/transcript.
type TranscriptOptions struct {
	// AfterSeq returns only entries with Seq > AfterSeq. Use the last
	// seq from the previous page for incremental fetches.
	AfterSeq uint64
	// Limit caps the page size. Zero leaves the gateway default in
	// place.
	Limit int
}

// LogEntry is one entry of the §15.1 line 673 GET /v1/sessions/{id}/logs
// page. It mirrors the event-store envelope items the gateway returns:
// Data is the already-marshalled log payload kept as a RawMessage.
type LogEntry struct {
	Seq       uint64          `json:"seq"`
	SessionID string          `json:"sessionId"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	Timestamp string          `json:"timestamp"`
}

// LogsPage is one page of GET /v1/sessions/{id}/logs in the §15.1 line
// 1228 canonical `{items, cursor, hasMore}` envelope.
type LogsPage struct {
	Items   []LogEntry `json:"items"`
	Cursor  string     `json:"cursor"`
	HasMore bool       `json:"hasMore"`
}

// LogsOptions narrows GET /v1/sessions/{id}/logs.
type LogsOptions struct {
	// Since returns only entries at or after this time — the §24.17
	// `--since` flag. The zero value applies no time filter.
	Since time.Time

	// Cursor carries the pagination cursor from a prior page's Cursor.
	// An empty cursor requests the first page.
	Cursor string

	// Limit caps the page size. Zero leaves the gateway default in place.
	Limit int
}

// InteractionResolution is the response returned by the four §15.1
// interaction-resolution endpoints (tool-use approve/deny, elicitation
// respond/dismiss). spec: §7.2 lines 124-127; §15.1.
type InteractionResolution struct {
	// ID is the interaction id (tool_call_id or elicitation_id).
	ID string `json:"id"`
	// Phase is the new interaction phase (`approved`, `denied`,
	// `responded`, `dismissed`).
	Phase string `json:"phase"`
	// ResolvedAt is the RFC 3339 timestamp the gateway recorded.
	ResolvedAt string `json:"resolvedAt"`
}
