// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
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
	// SetLegalHold flips the artifact's legal_hold flag. When hold is
	// true, setBy/setAt/note record the §15.1 line 865 provenance the
	// GET /v1/admin/legal-holds list reports.
	SetLegalHold(ctx context.Context, uri string, hold bool, setBy string, setAt time.Time, note string) error
	// IsLegalHeldAt reports whether any artifact scoped to
	// (tenant, session) carries legal_hold=true.
	IsLegalHeldAt(ctx context.Context, tenantID, sessionID string) (bool, error)
	// ListLegalHeld returns the tenant's artifact rows that carry an
	// active legal hold, with provenance, backing the artifact half of
	// the §15.1 line 865 list. spec: §15.1 lines 864-865.
	ListLegalHeld(ctx context.Context, tenantID string) ([]artifactcatalog.Record, error)
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
	// Note is the §15.1 line 864 justification for the hold. Required
	// when Hold is true; ignored on a clear. Recorded as the hold's
	// provenance and surfaced by GET /v1/admin/legal-holds.
	Note string `json:"note,omitempty"`
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
	// spec: §15.1 line 864 — `note` is required when setting a hold so the
	// preservation order has a recorded justification. It is ignored on a
	// clear.
	if body.Hold && strings.TrimSpace(body.Note) == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"note is required when setting a legal hold",
			map[string]any{"field": "note"})
		return
	}
	if body.ArtifactID != "" {
		r.setArtifactLegalHold(w, req, body)
		return
	}
	r.setSessionLegalHold(w, req, body)
}

// setSessionLegalHold flips the §12.8 session-level legal hold. Setting a
// hold records the §15.1 line 865 provenance (operator, instant, note);
// clearing blanks it so a released session reports no stale provenance.
func (r *Router) setSessionLegalHold(w http.ResponseWriter, req *http.Request, body LegalHoldRequest) {
	principal, _ := authmw.FromContext(req.Context())
	now := r.clock()
	updated, err := r.sessions.Update(req.Context(), body.TenantID, body.SessionID,
		func(s *sessionstore.Session) error {
			s.LegalHold = body.Hold
			if body.Hold {
				s.LegalHoldSetBy = principal.Subject
				s.LegalHoldSetAt = now
				s.LegalHoldNote = body.Note
			} else {
				s.LegalHoldSetBy = ""
				s.LegalHoldSetAt = time.Time{}
				s.LegalHoldNote = ""
			}
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
		"note":      body.Note,
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
	principal, _ := authmw.FromContext(req.Context())
	if err := r.artifactHolds.SetLegalHold(req.Context(), body.ArtifactID, body.Hold, principal.Subject, r.clock(), body.Note); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	r.emitLegalHold(req, body.Hold, "artifact", body.ArtifactID, map[string]any{
		"tenantId":   body.TenantID,
		"artifactId": body.ArtifactID,
		"sessionId":  rec.SessionID,
		"note":       body.Note,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"artifactId": body.ArtifactID,
		"tenantId":   body.TenantID,
		"legalHold":  body.Hold,
	})
}

// legalHoldEntry is one row of the §15.1 line 865
// GET /v1/admin/legal-holds list. The wire fields match the spec
// projection (resourceType, resourceId, setBy, setAt, note); tenantId is
// carried so a platform-admin listing across tenants can attribute each
// hold.
//
// spec: §15.1 line 865.
type legalHoldEntry struct {
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
	TenantID     string `json:"tenantId"`
	SetBy        string `json:"setBy"`
	SetAt        string `json:"setAt"`
	Note         string `json:"note"`
}

// handleListLegalHolds implements GET /v1/admin/legal-holds — the §15.1
// line 865 active-hold listing. It enumerates session-level and
// artifact-level holds from the resource rows' provenance, honouring the
// `?tenant_id`, `?resource_type`, and `?resource_id` filters and the
// canonical §15.1 cursor-paginated envelope. A tenant-admin caller is
// scoped to its own tenant regardless of the `tenant_id` param; a
// platform-admin omitting `tenant_id` lists across every tenant.
//
// spec: §15.1 line 865.
func (r *Router) handleListLegalHolds(w http.ResponseWriter, req *http.Request) {
	p, _ := authmw.FromContext(req.Context())
	q := req.URL.Query()
	resourceType := q.Get("resource_type")
	if resourceType != "" && resourceType != "session" && resourceType != "artifact" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"resource_type must be 'session' or 'artifact'",
			map[string]any{"field": "resource_type"})
		return
	}
	resourceID := q.Get("resource_id")

	// spec: §15.1 line 865 — a tenant-admin is automatically scoped to its
	// own tenant; the tenant_id param cannot widen that. A platform-admin
	// honours the param, and omitting it lists across every tenant.
	tenantFilter := q.Get("tenant_id")
	if !p.HasRole(auth.RolePlatformAdmin) {
		tenantFilter = p.TenantID
	}

	tenantIDs, err := r.legalHoldTenantScope(req.Context(), tenantFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	includeSessions := resourceType == "" || resourceType == "session"
	includeArtifacts := resourceType == "" || resourceType == "artifact"

	var entries []legalHoldEntry
	for _, tid := range tenantIDs {
		if includeSessions {
			rows, lerr := r.sessions.List(req.Context(), tid, sessionstore.ListFilter{})
			if lerr != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", lerr.Error(), nil)
				return
			}
			for _, s := range rows {
				if !s.LegalHold || (resourceID != "" && s.ID != resourceID) {
					continue
				}
				entries = append(entries, legalHoldEntry{
					ResourceType: "session",
					ResourceID:   s.ID,
					TenantID:     tid,
					SetBy:        s.LegalHoldSetBy,
					SetAt:        rfc3339Nano(s.LegalHoldSetAt),
					Note:         s.LegalHoldNote,
				})
			}
		}
		if includeArtifacts && r.artifactHolds != nil {
			recs, lerr := r.artifactHolds.ListLegalHeld(req.Context(), tid)
			if lerr != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", lerr.Error(), nil)
				return
			}
			for _, rec := range recs {
				if resourceID != "" && rec.URI != resourceID {
					continue
				}
				entries = append(entries, legalHoldEntry{
					ResourceType: "artifact",
					ResourceID:   rec.URI,
					TenantID:     rec.TenantID,
					SetBy:        rec.LegalHoldSetBy,
					SetAt:        rfc3339Nano(rec.LegalHoldSetAt),
					Note:         rec.LegalHoldNote,
				})
			}
		}
	}

	// spec: §15.1 lines 1228-1253 — canonical cursor-paginated envelope.
	// resourceId is the name sort field and the timestamp tiebreaker.
	writePaginatedList(w, req, r.clock(), entries, adminTimestampSortFields, adminListDefaultSort,
		func(e legalHoldEntry, s pagination.Sort) (string, string) {
			if s.Field == "name" {
				return e.ResourceID, e.ResourceID
			}
			return e.SetAt, e.ResourceID
		})
}

// legalHoldTenantScope resolves the set of tenant ids the legal-hold list
// scans. A non-empty filter scopes to that single tenant; an empty filter
// (platform-admin list-all) enumerates every tenant. When no tenant store
// is wired the list-all case yields an empty scope.
func (r *Router) legalHoldTenantScope(ctx context.Context, tenantFilter string) ([]string, error) {
	if tenantFilter != "" {
		return []string{tenantFilter}, nil
	}
	if r.tenants == nil {
		return nil, nil
	}
	tenants, err := r.tenants.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(tenants))
	for _, t := range tenants {
		ids = append(ids, t.ID)
	}
	return ids, nil
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
