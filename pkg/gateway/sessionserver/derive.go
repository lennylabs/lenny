// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// DeriveRequest is the §7.1 / §15.1 POST /v1/sessions/{id}/derive body.
type DeriveRequest struct {
	// RuntimeRef overrides the derived session's runtime. Optional —
	// when blank, the derived session inherits the source's
	// `RuntimeRef`. Production validates this against the
	// ExternalAdapterRegistry per §15.4.
	RuntimeRef string `json:"runtimeRef,omitempty"`

	// TargetPool identifies the `SandboxWarmPool` the derived session
	// runs against. Optional — when blank, the derived session
	// inherits the source's pool. Production resolves this through
	// the registry per §5.3.
	TargetPool string `json:"targetPool,omitempty"`

	// TargetIsolationProfile is the §5.3 isolation profile the derived
	// session is bound to. The minimal gateway accepts this directly
	// from the request body so monotonicity tests can drive the check
	// without a live pool registry; production resolves this from the
	// target pool's `sessionIsolationLevel.isolationProfile`.
	TargetIsolationProfile isolation.Profile `json:"targetIsolationProfile,omitempty"`

	// UserID overrides the derived session's user. Optional — when
	// blank, the derived session inherits the source's userId.
	UserID string `json:"userId,omitempty"`

	// AllowStale lifts the §7.1 derive rule 1 precondition for
	// non-terminal source sessions. Without this flag, derives from
	// `running`, `suspended`, `resume_pending`, or
	// `awaiting_client_action` are rejected with
	// `409 DERIVE_ON_LIVE_SESSION`.
	AllowStale bool `json:"allowStale,omitempty"`

	// AllowIsolationDowngrade overrides §7.1 derive rule 5 (SEC-001)
	// when the target pool's isolation profile is weaker than the
	// source session's. Requires the caller to hold the
	// `platform-admin` role; non-admin callers carrying this flag
	// receive `403 FORBIDDEN`.
	AllowIsolationDowngrade bool `json:"allowIsolationDowngrade,omitempty"`

	// TicketID is the optional free-text justification echoed into
	// the `derive.isolation_downgrade` audit event per §7.1 derive
	// rule 5.
	TicketID string `json:"ticketId,omitempty"`
}

// DeriveResponse is the §15.1 derive envelope. The caller-visible
// fields are the regular session envelope plus the
// `workspaceSnapshot*` echo per §7.1.
type DeriveResponse struct {
	SessionResponse

	// WorkspaceSnapshotSource echoes the §7.1 derive response field.
	WorkspaceSnapshotSource string `json:"workspaceSnapshotSource,omitempty"`

	// WorkspaceSnapshotTimestamp is the moment the snapshot was
	// originally committed to object storage, RFC3339Nano.
	WorkspaceSnapshotTimestamp string `json:"workspaceSnapshotTimestamp,omitempty"`

	// ParentSessionID echoes the source session id for client-side
	// audit / lineage display.
	ParentSessionID string `json:"parentSessionId,omitempty"`
}

// DeriveAuditSink is the §7.1 derive rule 5 audit hook. The gateway
// emits a `derive.isolation_downgrade` event when an
// `allowIsolationDowngrade: true` override is exercised; this
// interface lets the server compose with the audit subsystem without
// importing it directly.
type DeriveAuditSink interface {
	// EmitDeriveIsolationDowngrade records the per-§7.1 derive rule 5
	// admin override. The implementation must be non-blocking — the
	// derive handler does not wait for delivery.
	EmitDeriveIsolationDowngrade(ctx context.Context, event DeriveIsolationDowngradeEvent)
}

// DeriveIsolationDowngradeEvent is the §7.1 derive rule 5 audit
// payload. Field names match the spec for downstream OCSF mapping.
type DeriveIsolationDowngradeEvent struct {
	SourceSessionID        string
	SourceIsolationProfile isolation.Profile
	TargetPool             string
	TargetIsolationProfile isolation.Profile
	AuthorizingUserSubject string
	TicketID               string
	TenantID               string
}

// handleDerive implements POST /v1/sessions/{id}/derive per §7.1 and
// §15.1. The handler runs the source-session lookup, the precondition
// state check, the workspace-snapshot resolution, the SEC-001
// monotonicity check (with platform-admin override), the snapshot
// copy, and the atomic INSERT of the derived session row.
//
// The minimal gateway elides the §7.1 Redis advisory lock + MinIO
// copy + CAS-fenced coordination-generation write; the in-memory
// store mutex serialises concurrent derives synchronously, and the
// snapshot ref is reused in place since there is no separate object
// store to copy to. The derive_failure terminal-row path under
// `gateway.persistDeriveFailureRows: true` is also deferred to the
// phase that ships the Postgres-backed store.
func (s *Server) handleDerive(w http.ResponseWriter, r *http.Request) {
	if !s.requireActiveUser(w, r) {
		return
	}
	tenantID := s.resolveTenant(r)
	if !s.requireSessionQuota(w, r, tenantID) {
		return
	}
	if !s.requirePolicyChain(w, r, tenantID) {
		return
	}
	sourceID := r.PathValue("id")

	source, err := s.store.Get(r.Context(), tenantID, sourceID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	var req DeriveRequest
	if r.ContentLength > 0 {
		body := jsonReader(w, r)
		defer body.Close()
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
			return
		}
	}

	// §7.1 derive rule 1: non-terminal source requires `allowStale: true`.
	if !session.IsTerminal(source.State) && !req.AllowStale {
		s.writeError(w, http.StatusConflict, "DERIVE_ON_LIVE_SESSION",
			"source session is non-terminal; set allowStale: true to derive from the most recent checkpoint",
			map[string]any{"currentState": string(source.State)})
		return
	}

	// §7.1 derive rule 1: source must have a resolvable workspace snapshot.
	if source.WorkspaceSnapshot == nil || source.WorkspaceSnapshot.Ref == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"source session has no resolvable workspace snapshot",
			map[string]any{"fields": []map[string]string{{"field": "sourceSessionId"}}})
		return
	}

	// §7.1 derive rule 5 (SEC-001): isolation monotonicity. The
	// minimal gateway accepts the target profile from the request
	// body; production resolves it from the target pool.
	target := req.TargetIsolationProfile
	if target == "" {
		target = source.IsolationProfile
	}
	if !isolation.IsValid(target) {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			fmt.Sprintf("targetIsolationProfile %q is not a recognised §5.3 profile", target),
			map[string]any{"fields": []map[string]string{{"field": "targetIsolationProfile"}}})
		return
	}

	principal, _ := authmw.FromContext(r.Context())
	if !isolation.AtLeastAsRestrictive(target, source.IsolationProfile) {
		// Downgrade requested. Only platform-admin may override.
		if !req.AllowIsolationDowngrade {
			s.writeError(w, http.StatusUnprocessableEntity, "ISOLATION_MONOTONICITY_VIOLATED",
				"target pool's isolation profile is weaker than the source session's",
				map[string]any{
					"sourceIsolationProfile": string(source.IsolationProfile),
					"targetIsolationProfile": string(target),
					"targetPool":             req.TargetPool,
				})
			return
		}
		if !principal.HasRole(pkgauth.RolePlatformAdmin) {
			s.writeError(w, http.StatusForbidden, "FORBIDDEN",
				"allowIsolationDowngrade=true requires the platform-admin role",
				map[string]any{
					"sourceIsolationProfile": string(source.IsolationProfile),
					"targetIsolationProfile": string(target),
				})
			return
		}
		if s.deriveAuditSink != nil {
			s.deriveAuditSink.EmitDeriveIsolationDowngrade(r.Context(), DeriveIsolationDowngradeEvent{
				SourceSessionID:        source.ID,
				SourceIsolationProfile: source.IsolationProfile,
				TargetPool:             req.TargetPool,
				TargetIsolationProfile: target,
				AuthorizingUserSubject: principal.Subject,
				TicketID:               req.TicketID,
				TenantID:               tenantID,
			})
		}
	}

	// Resolve the derived session's runtime / userId. Inherit the
	// source's values when the request omits them — same default
	// shape as `replayMode: prompt_history`.
	runtimeRef := req.RuntimeRef
	if runtimeRef == "" {
		runtimeRef = source.RuntimeRef
	}
	userID := req.UserID
	if userID == "" {
		userID = source.UserID
	}
	pool := req.TargetPool
	if pool == "" {
		pool = source.PoolRef
	}

	now := s.clock()
	derived := sessionstore.Session{
		ID:                 s.idFn(),
		TenantID:           tenantID,
		UserID:             userID,
		State:              session.StateCreated,
		CreatedAt:          now,
		UpdatedAt:          now,
		RuntimeRef:         runtimeRef,
		PoolRef:            pool,
		IsolationProfile:   target,
		WorkspaceSnapshot:  copySnapshotRef(source.WorkspaceSnapshot, derivedSnapshotRef(tenantID, source.ID)),
		ParentSessionID:    source.ID,
		ParentWorkspaceRef: source.WorkspaceSnapshot.Ref,
	}
	if err := s.store.Create(r.Context(), derived); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	resp := DeriveResponse{
		SessionResponse:            toResponse(derived),
		WorkspaceSnapshotSource:    string(derived.WorkspaceSnapshot.Source),
		WorkspaceSnapshotTimestamp: derived.WorkspaceSnapshot.Timestamp.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		ParentSessionID:            derived.ParentSessionID,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// copySnapshotRef returns a §7.1 derived-snapshot reference at the
// derived session's path. The minimal gateway elides the MinIO copy
// and simply rewrites the ref; production swaps in a real `Copier`.
func copySnapshotRef(src *sessionstore.WorkspaceSnapshot, newRef string) *sessionstore.WorkspaceSnapshot {
	if src == nil {
		return nil
	}
	return &sessionstore.WorkspaceSnapshot{
		Ref:       newRef,
		Source:    src.Source,
		Timestamp: src.Timestamp,
	}
}

// derivedSnapshotRef computes the §4.5 MinIO path for a derived
// snapshot under the derived session's own object prefix.
func derivedSnapshotRef(tenantID, sourceSessionID string) string {
	return fmt.Sprintf("/%s/workspace/derived-from-%s", tenantID, sourceSessionID)
}
