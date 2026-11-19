// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// SessionResponse is the §15.1 GET /v1/sessions/{id} envelope.
type SessionResponse struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenantId"`
	UserID      string `json:"userId,omitempty"`
	RuntimeRef  string `json:"runtimeRef,omitempty"`
	Environment string `json:"environment,omitempty"`
	State       string `json:"state"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`

	FailureClass string `json:"failureClass,omitempty"`

	// WorkspacePlan is the §14 WorkspacePlan stored at session
	// creation, echoed per §15.1. Absent when the session was created
	// without a plan.
	WorkspacePlan json.RawMessage `json:"workspacePlan,omitempty"`

	// Cwd is the §4.2 session working directory. Empty until the
	// runtime adapter materialises the workspace.
	// spec: §4.2.
	Cwd string `json:"cwd,omitempty"`

	// PodAssignment is the §4.2 pod-to-session binding the session is
	// currently bound to. Empty when the session has no live pod.
	// spec: §4.2.
	PodAssignment string `json:"podAssignment,omitempty"`

	// RecoveryGeneration is the §4.2 pod-recovery counter, visible to
	// clients per §4.2. Starts at zero and increments by one
	// on each pod recovery.
	// spec: §4.2 — "incremented on each pod recovery (visible
	// to clients via the session API ...)".
	RecoveryGeneration int64 `json:"recoveryGeneration"`

	// SchemaVersion is the §4.2 session-row schema version. v1
	// sessions report schema_version=1.
	// spec: §4.2.
	SchemaVersion int32 `json:"schemaVersion"`

	// RetryCount is the §4.2 retry counter the Session
	// Manager tracks across this logical session's lifetime.
	// spec: §4.2 — "Retry counters and policy enforcement".
	RetryCount int64 `json:"retryCount"`

	// PolicyEnforcementState is the §4.2 schemaless
	// policy-enforcement payload. Omitted from the JSON envelope
	// when empty.
	// spec: §4.2.
	PolicyEnforcementState json.RawMessage `json:"policyEnforcementState,omitempty"`

	// ResumeEligibleUntil is the §4.2 resume-window
	// deadline as RFC 3339 nanos. Empty when the session has no
	// resume budget.
	// spec: §4.2 — "Resume eligibility and window".
	ResumeEligibleUntil string `json:"resumeEligibleUntil,omitempty"`

	// SessionIsolationLevel echoes the §7.1 sessionIsolationLevel object
	// so a client that lost the create response can inspect the session's
	// isolation posture through GET /v1/sessions/{id} and the list. The
	// field is populated from the persisted §5.3 isolation profile and is
	// stable for the lifetime of the session (the profile never changes
	// after creation).
	// spec: §7.1 — "GET /v1/sessions/{id} also returns
	// sessionIsolationLevel in the session metadata ... does not change
	// for the lifetime of the session".
	SessionIsolationLevel SessionIsolationLevel `json:"sessionIsolationLevel"`

	// Metadata echoes the §7.1 client-supplied metadata payload
	// the session was created with. Omitted from the envelope when the
	// client submitted no metadata. F-7.3.20.
	// spec: §7.1.
	Metadata map[string]string `json:"metadata,omitempty"`

	// Labels echoes the §14 client-supplied session labels the
	// session was created with. Omitted when the client submitted none.
	// These are the values the `GET /v1/sessions?label=k=v` filter matches
	// against. spec: §14; §15.1. F-15.1.15.
	Labels map[string]string `json:"labels,omitempty"`

	// RetryPolicy echoes the §7.3 effective retry policy resolved at
	// session creation (the client-supplied object after clamp). Omitted
	// when the session was created with no override. F-7.3.1.
	// spec: §7.3.
	RetryPolicy *session.RetryPolicy `json:"retryPolicy,omitempty"`

	// Env echoes the §14 client-supplied env map (which passed the
	// deployer blocklist at admission). Omitted when the client supplied
	// none. spec: §14. F-14.1.12.
	Env map[string]string `json:"env,omitempty"`

	// Pool echoes the §14 / §14.1 client-requested target pool. Omitted
	// when the request named no pool. spec: §14.1. F-14.1.14.
	Pool string `json:"pool,omitempty"`

	// Origin echoes the §27.3 origin label recorded on the session row.
	// It is "playground" for a /playground/*-originated session and
	// omitted otherwise, so a §25.9 audit-log query and the §27.8
	// dashboards can slice on origin. spec: §27.6. F-27.6.8.
	Origin string `json:"origin,omitempty"`

	// Timeouts echoes the §14 per-session timeout overrides. Omitted when
	// the client supplied none. spec: §14. F-14.1.14.
	Timeouts *sessionstore.SessionTimeouts `json:"timeouts,omitempty"`

	// CredentialPolicy echoes the §14 per-session credentialPolicy
	// override. Omitted when the client supplied none. spec: §14
	// credentialPolicy. F-14.1.14.
	CredentialPolicy *sessionstore.CredentialPolicyOverride `json:"credentialPolicy,omitempty"`

	// DelegationLease echoes the §14 client-requested delegation lease
	// bounds. Omitted when the client supplied none. spec: §14. F-14.1.14.
	DelegationLease *sessionstore.DelegationLeaseRequest `json:"delegationLease,omitempty"`

	// RuntimeOptions echoes the §14 per-runtime options blob the session
	// was created with. Omitted when the client supplied none. spec: §14. F-14.1.14.
	RuntimeOptions json.RawMessage `json:"runtimeOptions,omitempty"`

	// SetupOutput is the §7.5 captured per-command output the
	// adapter returned at setup time, plus any §7.5 synthetic
	// rejection-reason entries the gateway recorded when it rejected a
	// command at admission. Omitted when no setup commands ran and the
	// gateway never rejected one. F-7.5.4 / F-7.5.11.
	// spec: §7.5.
	SetupOutput []SetupOutputEntry `json:"setupOutput,omitempty"`

	// TaskRecord is the §8.8 TaskRecord envelope projected from the
	// session row plus its transcript: the durable, protocol-bridging
	// task-level record (schemaVersion, taskId, sessionId, state, the
	// caller/agent messages array, usage, treeUsage). Populated only on
	// the single-session read (GET /v1/sessions/{id}); the list endpoint
	// omits it to avoid a transcript fetch per row. Absent when the
	// gateway has no transcript store wired. F-8.8.1.
	// spec: §8.8 (TaskRecord).
	TaskRecord *sessionrecord.Record `json:"taskRecord,omitempty"`
}

// SetupOutputEntry is one §7.5 setup-command record on the §15.1 session
// envelope. spec: §7.5 — F-7.5.4 / F-7.5.11.
type SetupOutputEntry struct {
	Cmd             string `json:"cmd"`
	ExitCode        int32  `json:"exitCode"`
	Stdout          string `json:"stdout,omitempty"`
	Stderr          string `json:"stderr,omitempty"`
	DurationMs      int64  `json:"durationMs,omitempty"`
	Truncated       bool   `json:"truncated,omitempty"`
	Rejected        bool   `json:"rejected,omitempty"`
	RejectionReason string `json:"rejectionReason,omitempty"`
}

// CreateSessionResponse is the §15.1 POST /v1/sessions response
// envelope. Carries the regular session fields plus the §7.1
// uploadToken and the §7.1 sessionIsolationLevel.
type CreateSessionResponse struct {
	SessionResponse

	// UploadToken is the §7.1 single-use HMAC uploadToken the client
	// supplies on every `POST /v1/sessions/{id}/upload` and
	// `POST /v1/sessions/{id}/upload-archive` until the session is
	// finalized. Treat as a secret per §7.1.
	UploadToken string `json:"uploadToken"`

	// WorkspacePlanWarnings echoes any §14 consumer-advisory
	// warnings (unknown source type, path collisions) the parser
	// raised. Empty when the plan is omitted or pristine.
	WorkspacePlanWarnings []workspaceplan.Warning `json:"workspacePlanWarnings,omitempty"`
}

// writeSession serialises a Session row as the §15.1 envelope and
// writes it with the supplied status code.
func (s *Server) writeSession(w http.ResponseWriter, code int, row sessionstore.Session) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(toResponse(row))
}

// toResponse converts a Session row into the §15.1 wire envelope.
// spec: §4.2.
func toResponse(row sessionstore.Session) SessionResponse {
	schemaVersion := row.SchemaVersion
	if schemaVersion == 0 {
		schemaVersion = 1
	}
	out := SessionResponse{
		ID:                 row.ID,
		TenantID:           row.TenantID,
		UserID:             row.UserID,
		RuntimeRef:         row.RuntimeRef,
		Environment:        row.Environment,
		State:              string(row.State),
		CreatedAt:          row.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:          row.UpdatedAt.UTC().Format(time.RFC3339Nano),
		Cwd:                row.Cwd,
		PodAssignment:      row.PodAssignment,
		RecoveryGeneration: row.RecoveryGeneration,
		SchemaVersion:      schemaVersion,
		RetryCount:         row.RetryCount,
		// spec: §7.1 — surface the isolation level on every read.
		// The execution-mode + scrub-policy halves are persisted on the
		// row at create time (migration 0084) so a client that lost the
		// create response, or a GET issued against a coordinator-handed-
		// off replica, returns the same rich envelope. Rows persisted
		// before migration 0084 (or by a code path that never resolved a
		// pool) carry empty ExecutionMode — they fall back to the
		// session-mode default so the field never understates the
		// isolation posture.
		SessionIsolationLevel: persistedIsolationLevel(row),
	}
	if row.FailureClass != "" {
		out.FailureClass = string(row.FailureClass)
	}
	if len(row.WorkspacePlan) > 0 {
		out.WorkspacePlan = row.WorkspacePlan
	}
	// spec: §4.2 — only surface the policy enforcement
	// state when the gateway has written something other than the
	// migration default `{}`. The omitempty json tag suppresses the
	// payload when nil; an explicit `{}` is preserved as-is.
	if len(row.PolicyEnforcementState) > 0 {
		out.PolicyEnforcementState = row.PolicyEnforcementState
	}
	// spec: §4.2 — emit the resume window only when set.
	if !row.ResumeEligibleUntil.IsZero() {
		out.ResumeEligibleUntil = row.ResumeEligibleUntil.UTC().Format(time.RFC3339Nano)
	}
	// spec: §7.1 — echo the client metadata so a client that
	// lost the create response can recover its own annotations.
	// F-7.3.20.
	if len(row.Metadata) > 0 {
		out.Metadata = cloneMetadata(row.Metadata)
	}
	// spec: §14 / §15.1 — echo the client labels so a
	// caller can confirm the filterable selector set on the row. F-15.1.15.
	if len(row.Labels) > 0 {
		out.Labels = cloneMetadata(row.Labels)
	}
	// spec: §7.3 — echo the effective retry policy so a
	// client can confirm what was clamped. F-7.3.1.
	if row.RetryPolicy != nil {
		out.RetryPolicy = cloneRetryPolicy(row.RetryPolicy)
	}
	// spec: §14 — echo the request envelope so a client that lost the
	// create response can recover its own env / pool / timeouts /
	// credentialPolicy / delegationLease / runtimeOptions. The env map
	// is the gateway-accepted set (every key passed the blocklist).
	// F-14.1.12 / F-14.1.14.
	if len(row.Env) > 0 {
		out.Env = cloneMetadata(row.Env)
	}
	out.Pool = row.Pool
	// spec: §27.6 — surface the origin=playground label on every
	// read so §25.9 audit queries and §27.8 dashboards can slice on it.
	// F-27.6.8.
	out.Origin = row.Origin
	if row.Timeouts != nil {
		t := *row.Timeouts
		out.Timeouts = &t
	}
	if row.CredentialPolicyOverride != nil {
		c := *row.CredentialPolicyOverride
		out.CredentialPolicy = &c
	}
	if row.DelegationLeaseRequest != nil {
		out.DelegationLease = cloneDelegationLeaseRequest(row.DelegationLeaseRequest)
	}
	if len(row.RuntimeOptions) > 0 {
		out.RuntimeOptions = append(json.RawMessage(nil), row.RuntimeOptions...)
	}
	// spec: §7.5 — echo the captured / rejected setup
	// outputs. F-7.5.4 / F-7.5.11.
	if len(row.SetupOutput) > 0 {
		out.SetupOutput = make([]SetupOutputEntry, 0, len(row.SetupOutput))
		for _, e := range row.SetupOutput {
			out.SetupOutput = append(out.SetupOutput, SetupOutputEntry{
				Cmd:             e.Cmd,
				ExitCode:        e.ExitCode,
				Stdout:          e.Stdout,
				Stderr:          e.Stderr,
				DurationMs:      e.DurationMs,
				Truncated:       e.Truncated,
				Rejected:        e.Rejected,
				RejectionReason: e.RejectionReason,
			})
		}
	}
	return out
}

// cloneMetadata returns a defensive copy of the §7.1 metadata
// payload so mutations in the request or row never leak across the
// gateway/store boundary. A nil input maps to nil so the wire envelope
// honours `omitempty`. F-7.3.20.
func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// cloneRetryPolicy returns a defensive copy of the §7.3 RetryPolicy so
// the wire envelope cannot mutate the persisted row or the in-flight
// request through a shared pointer. A nil input maps to nil so the
// envelope honours `omitempty`. F-7.3.1.
func cloneRetryPolicy(in *session.RetryPolicy) *session.RetryPolicy {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.RetryableFailures) > 0 {
		out.RetryableFailures = append([]string(nil), in.RetryableFailures...)
	}
	if len(in.NonRetryableFailures) > 0 {
		out.NonRetryableFailures = append([]string(nil), in.NonRetryableFailures...)
	}
	return &out
}

// cloneDelegationLeaseRequest returns a defensive copy of the §14
// delegation-lease request so the wire envelope cannot mutate the
// persisted row through a shared pointer. A nil input maps to nil so the
// envelope honours `omitempty`. F-14.1.14.
func cloneDelegationLeaseRequest(in *sessionstore.DelegationLeaseRequest) *sessionstore.DelegationLeaseRequest {
	if in == nil {
		return nil
	}
	out := *in
	if in.MaxDepth != nil {
		v := *in.MaxDepth
		out.MaxDepth = &v
	}
	if in.MaxChildrenTotal != nil {
		v := *in.MaxChildrenTotal
		out.MaxChildrenTotal = &v
	}
	return &out
}
