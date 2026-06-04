// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// artifactPayload is the §15.1 GET /v1/sessions/{id}/artifacts wire row.
// It surfaces the §12.5 catalog fields a client needs to discover and
// dereference a session artifact (the `ref` is the `lenny-blob://` URI
// the §15.1 GET /v1/blobs/{ref} handler resolves). spec: §15.1; §12.5
// line 309. F-15.2.3 / F-15.1.3.
type artifactPayload struct {
	Ref       string `json:"ref"`
	PartID    string `json:"partId,omitempty"`
	Type      string `json:"type"`
	MimeType  string `json:"mimeType,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	LegalHold bool   `json:"legalHold,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// handleListArtifacts implements GET /v1/sessions/{id}/artifacts per
// §15.1: the live §12.5 artifact catalog rows for one session. The
// listing is scoped to the caller's resolved tenant; soft-deleted and
// tombstoned rows are excluded so a client sees only dereferenceable
// artifacts. A session with no catalog wired or no rows returns an empty
// `{items: []}` envelope rather than an error. spec: §15.1 line 598;
// §12.5. F-15.2.3 / F-15.1.3.
func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")

	// Confirm the session exists and is visible to the caller's tenant
	// before exposing its artifacts, mirroring the GET /v1/sessions/{id}
	// 404 contract so a cross-tenant probe cannot enumerate artifacts.
	if _, err := s.store.Get(r.Context(), tenantID, id); err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	items := []artifactPayload{}
	if s.artifacts != nil {
		rows, err := s.artifacts.ListBySession(r.Context(), tenantID, id)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		for _, row := range rows {
			if row.State != artifactcatalog.StateLive {
				continue
			}
			items = append(items, toArtifactPayload(row))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

// toArtifactPayload projects a §12.5 catalog row onto the wire row,
// defaulting an empty artifact-type tag to `workspace` (the migration
// 0063 default).
func toArtifactPayload(row artifactcatalog.Record) artifactPayload {
	kind := string(row.ArtifactType)
	if kind == "" {
		kind = string(artifactcatalog.ArtifactTypeWorkspace)
	}
	p := artifactPayload{
		Ref:       row.URI,
		PartID:    row.PartID,
		Type:      kind,
		MimeType:  row.MimeType,
		SizeBytes: row.SizeBytes,
		LegalHold: row.LegalHold,
	}
	if !row.CreatedAt.IsZero() {
		p.CreatedAt = row.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return p
}

// handleSessionUsage implements GET /v1/sessions/{id}/usage per §15.1:
// the reconciled per-session token + compute total drawn from the
// §11.2.1 billing ledger. It requires the §10.2 view_usage permission
// (the same gate the tenant /v1/usage and /v1/metering/events reports
// run). A session with no billing events, or a gateway with metering
// disabled, returns a zero-valued report rather than an error. spec:
// §15.1; §11.2.1; §10.2 view_usage. F-15.2.3 / F-15.1.3.
func (s *Server) handleSessionUsage(w http.ResponseWriter, r *http.Request) {
	principal, ok := getPrincipal(r)
	if ok && !pkgauth.RolesGrant(principal.Roles, pkgauth.PermViewUsage) {
		s.writeError(w, http.StatusForbidden, "FORBIDDEN",
			"session usage requires the view_usage permission", nil)
		return
	}

	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	if _, err := s.store.Get(r.Context(), tenantID, id); err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	usage := billingstore.SessionUsage{}
	if s.billing != nil {
		u, err := s.billing.SessionTotals(r.Context(), tenantID, id)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		usage = u
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessionId":    id,
		"tokensInput":  usage.TokensInput,
		"tokensOutput": usage.TokensOutput,
		"podMinutes":   usage.PodMinutes,
		"eventCount":   usage.EventCount,
	})
}
