// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// ArtifactLegalHolder is the artifact-side half of the §12.8 legal-hold
// control. It flips the `artifacts.legal_hold` flag for a single
// artifact and reports whether any artifact under a session carries a
// hold so the §12.8 erasure preflight can block on artifact-scoped
// preservation orders. *artifactcatalog.PgStore satisfies it.
//
// spec: §12.8 line 735; line 794(b).
type ArtifactLegalHolder interface {
	// Get returns the catalog record for uri (ErrNotFound when absent),
	// used to confirm the artifact belongs to the request's tenant
	// before flipping the hold.
	Get(ctx context.Context, uri string) (artifactcatalog.Record, error)
	// SetLegalHold flips the artifact's legal_hold flag.
	SetLegalHold(ctx context.Context, uri string, hold bool) error
	// IsLegalHeldAt reports whether any artifact scoped to
	// (tenant, session) carries legal_hold=true.
	IsLegalHeldAt(ctx context.Context, tenantID, sessionID string) (bool, error)
}

// WithSessions wires the SessionStore onto the Router. It backs the
// §12.8 legal-hold endpoint and the GDPR-erasure legal-hold preflight.
func (r *Router) WithSessions(s sessionstore.Store) *Router {
	r.sessions = s
	return r
}

// WithArtifactLegalHold wires the §12.8 artifact-side legal-hold
// control onto the Router. With it set, POST /v1/admin/legal-hold
// accepts an `artifactId`, and the GDPR-erasure legal-hold preflight
// consults artifact-level holds in addition to session-level holds. A
// nil holder leaves the endpoint session-only and skips the artifact
// half of the preflight.
//
// spec: §12.8 line 735; line 794(b).
func (r *Router) WithArtifactLegalHold(h ArtifactLegalHolder) *Router {
	r.artifactHolds = h
	return r
}

// LegalHoldRequest is the POST /v1/admin/legal-hold body. It names
// either the session or the artifact to hold or release and the desired
// hold state. Exactly one of SessionID and ArtifactID must be set.
//
// spec: §12.8 line 735 — "accepts a session ID or artifact ID".
type LegalHoldRequest struct {
	TenantID string `json:"tenantId"`
	// SessionID holds or releases a whole session (and transitively its
	// artifacts under the §12.8 retention rules).
	SessionID string `json:"sessionId,omitempty"`
	// ArtifactID is the artifact URI to hold or release. Mutually
	// exclusive with SessionID.
	ArtifactID string `json:"artifactId,omitempty"`
	Hold       bool   `json:"hold"`
}

// handleSetLegalHold implements POST /v1/admin/legal-hold — the §12.8
// legal-hold control. Setting a hold suspends the artifact retention
// GC for the named resource and blocks a GDPR erasure of the owner;
// clearing it restores normal retention. The body names either a
// session (`sessionId`) or a single artifact (`artifactId`). The
// operation is platform-admin only: a legal hold is a spoliation
// control and a tenant-admin must not set or clear one.
//
// spec: §12.8 line 735.
func (r *Router) handleSetLegalHold(w http.ResponseWriter, req *http.Request) {
	var body LegalHoldRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if body.TenantID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tenantId is required",
			map[string]any{"field": "tenantId"})
		return
	}
	// Exactly one of sessionId / artifactId. The §12.8 endpoint holds a
	// session or an artifact, never both in one call.
	if (body.SessionID == "") == (body.ArtifactID == "") {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"exactly one of sessionId or artifactId is required",
			map[string]any{"fields": []string{"sessionId", "artifactId"}})
		return
	}
	if body.ArtifactID != "" {
		r.setArtifactLegalHold(w, req, body)
		return
	}
	r.setSessionLegalHold(w, req, body)
}

// setSessionLegalHold flips the §12.8 session-level legal hold.
func (r *Router) setSessionLegalHold(w http.ResponseWriter, req *http.Request, body LegalHoldRequest) {
	updated, err := r.sessions.Update(req.Context(), body.TenantID, body.SessionID,
		func(s *sessionstore.Session) error {
			s.LegalHold = body.Hold
			return nil
		})
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	r.emitLegalHold(req, body.Hold, "session", body.SessionID, map[string]any{
		"tenantId":  body.TenantID,
		"sessionId": body.SessionID,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessionId": body.SessionID,
		"tenantId":  body.TenantID,
		"legalHold": updated.LegalHold,
	})
}

// setArtifactLegalHold flips the §12.8 artifact-level legal hold. The
// artifact must exist and belong to the request's tenant; a cross-tenant
// or unknown URI reads as not-found so the catalog is not probed across
// the tenant boundary.
func (r *Router) setArtifactLegalHold(w http.ResponseWriter, req *http.Request, body LegalHoldRequest) {
	if r.artifactHolds == nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"artifact-scoped legal holds are not available on this deployment", nil)
		return
	}
	rec, err := r.artifactHolds.Get(req.Context(), body.ArtifactID)
	if err != nil || rec.TenantID != body.TenantID {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "artifact not found", nil)
		return
	}
	if err := r.artifactHolds.SetLegalHold(req.Context(), body.ArtifactID, body.Hold); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	r.emitLegalHold(req, body.Hold, "artifact", body.ArtifactID, map[string]any{
		"tenantId":   body.TenantID,
		"artifactId": body.ArtifactID,
		"sessionId":  rec.SessionID,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"artifactId": body.ArtifactID,
		"tenantId":   body.TenantID,
		"legalHold":  body.Hold,
	})
}

// emitLegalHold writes the §12.8 ledger event for a hold set or clear,
// stamping the resourceType so the erasure preflight ledger covers
// session and artifact scopes. spec: §12.8 line 794.
func (r *Router) emitLegalHold(req *http.Request, hold bool, resourceType, resourceID string, detail map[string]any) {
	principal, _ := authmw.FromContext(req.Context())
	event := "legal_hold.cleared"
	if hold {
		event = "legal_hold.set"
	}
	detail["resourceType"] = resourceType
	r.emit(req.Context(), principal, event, resourceID, detail)
}

// heldResource describes a single §12.8 legal-hold blocking the erasure
// of a user. spec: §12.8 line 794 — the blocked event carries
// {resourceType, resourceId} tuples for each hold.
type heldResource struct {
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
}

// heldSessions returns the ids of the user's sessions that carry a
// §12.8 session-level legal hold. An empty result means no session-level
// hold blocks the erasure. When no SessionStore is wired the preflight
// cannot run and the result is empty.
func (r *Router) heldSessions(ctx context.Context, tenantID, userID string) ([]string, error) {
	if r.sessions == nil {
		return nil, nil
	}
	rows, err := r.sessions.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	var held []string
	for _, s := range rows {
		if s.UserID == userID && s.LegalHold {
			held = append(held, s.ID)
		}
	}
	return held, nil
}

// heldResourcesForUser enumerates the §12.8 step-0 legal-hold preflight
// blocking set for userID: session-level holds (resourceType session)
// and artifact-level holds on any artifact owned by one of the user's
// sessions (resourceType artifact). The returned holds back the
// gdpr.erasure_blocked_by_hold event's resource tuples and the override
// receipt. spec: §12.8 line 794(a)(b).
func (r *Router) heldResourcesForUser(ctx context.Context, tenantID, userID string) ([]heldResource, error) {
	if r.sessions == nil {
		return nil, nil
	}
	rows, err := r.sessions.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	var holds []heldResource
	for _, s := range rows {
		if s.UserID != userID {
			continue
		}
		if s.LegalHold {
			holds = append(holds, heldResource{ResourceType: "session", ResourceID: s.ID})
		}
		// §12.8 line 794(b): an artifact under one of the user's sessions
		// that carries its own hold blocks the erasure even when the
		// session itself is not held.
		if r.artifactHolds != nil {
			held, herr := r.artifactHolds.IsLegalHeldAt(ctx, tenantID, s.ID)
			if herr != nil {
				return nil, herr
			}
			if held {
				holds = append(holds, heldResource{ResourceType: "artifact", ResourceID: s.ID})
			}
		}
	}
	return holds, nil
}
