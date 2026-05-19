// SPDX-License-Identifier: MIT

package lenny

import "encoding/json"

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
}
