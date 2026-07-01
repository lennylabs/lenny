// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// ReplayMode is the §15.1 replay mode enum.
type ReplayMode string

const (
	// ReplayPromptHistory replays the source transcript as the
	// initial message sequence into a fresh session.
	ReplayPromptHistory ReplayMode = "prompt_history"
	// ReplayWorkspaceDerive starts from the source workspace with no
	// pre-loaded prompt history — equivalent to derive with a
	// substituted runtime.
	ReplayWorkspaceDerive ReplayMode = "workspace_derive"
)

// ReplayRequest is the §15.1 POST /v1/sessions/{id}/replay body.
type ReplayRequest struct {
	ReplayMode              ReplayMode        `json:"replayMode,omitempty"`
	TargetRuntime           string            `json:"targetRuntime"`
	TargetPool              string            `json:"targetPool,omitempty"`
	TargetIsolationProfile  isolation.Profile `json:"targetIsolationProfile,omitempty"`
	AllowIsolationDowngrade bool              `json:"allowIsolationDowngrade,omitempty"`
	EvalRef                 string            `json:"evalRef,omitempty"`
	TicketID                string            `json:"ticketId,omitempty"`
}

// ReplayResponse is the §15.1 replay reply.
type ReplayResponse struct {
	SessionResponse

	ReplayMode      string `json:"replayMode"`
	ParentSessionID string `json:"parentSessionId"`
}

// handleReplay implements POST /v1/sessions/{id}/replay per §15.1.
//
// Replay creates a new session against `targetRuntime` from a
// terminal source session. The two §15.1 modes:
//
//   - prompt_history (default): the source workspace snapshot is
//     copied and the source transcript is replayed as the initial
//     message sequence.
//   - workspace_derive: the source workspace snapshot is copied with
//     no pre-loaded prompt history (derive with a substituted
//     runtime).
//
// The §15.1 SEC-001 monotonicity rule applies whenever the resolved
// target pool's isolation profile differs from the source session's
// — the platform-admin override mirrors the derive path.
func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
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

	var req ReplayRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if req.ReplayMode == "" {
		req.ReplayMode = ReplayPromptHistory
	}
	if req.ReplayMode != ReplayPromptHistory && req.ReplayMode != ReplayWorkspaceDerive {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"replayMode must be prompt_history or workspace_derive",
			map[string]any{"fields": []map[string]string{{"field": "replayMode"}}})
		return
	}
	if req.TargetRuntime == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "targetRuntime is required",
			map[string]any{"fields": []map[string]string{{"field": "targetRuntime"}}})
		return
	}

	// §15.1: replay requires a terminal source session with a
	// resolvable workspace snapshot.
	if !session.IsTerminal(source.State) {
		s.writeError(w, http.StatusConflict, "REPLAY_ON_LIVE_SESSION",
			"replay requires a terminal source session",
			map[string]any{"currentState": string(source.State)})
		return
	}
	if source.WorkspaceSnapshot == nil || source.WorkspaceSnapshot.Ref == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"source session has no resolvable workspace snapshot",
			map[string]any{"fields": []map[string]string{{"field": "sourceSessionId"}}})
		return
	}

	// §15.1 SEC-001 monotonicity — same rule as derive.
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
	principal, _ := getPrincipal(r)
	if !isolation.AtLeastAsRestrictive(target, source.IsolationProfile) {
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
				"allowIsolationDowngrade=true requires the platform-admin role", nil)
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

	now := s.clock()
	replayed := sessionstore.Session{
		ID:                 s.idFn(),
		TenantID:           tenantID,
		UserID:             source.UserID,
		State:              session.StateCreated,
		CreatedAt:          now,
		UpdatedAt:          now,
		RuntimeRef:         req.TargetRuntime,
		PoolRef:            req.TargetPool,
		IsolationProfile:   target,
		WorkspaceSnapshot:  copySnapshotRef(source.WorkspaceSnapshot, derivedSnapshotRef(tenantID, source.ID)),
		ParentSessionID:    source.ID,
		ParentWorkspaceRef: source.WorkspaceSnapshot.Ref,
	}
	if err := s.store.Create(r.Context(), replayed); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// prompt_history mode: copy the source transcript into the
	// replayed session so the new runtime sees the prior conversation.
	if req.ReplayMode == ReplayPromptHistory && s.transcripts != nil {
		if entries, err := s.transcripts.Get(r.Context(), tenantID, source.ID); err == nil {
			_ = s.transcripts.Append(r.Context(), tenantID, replayed.ID, entries...)
		}
	}

	resp := ReplayResponse{
		SessionResponse: toResponse(replayed),
		ReplayMode:      string(req.ReplayMode),
		ParentSessionID: replayed.ParentSessionID,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}
