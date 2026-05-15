// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// ExtendRetentionMaxSeconds is the §7.1 hard upper bound the
// minimal gateway accepts on `ttlSeconds`. Production deployers can
// lower this via tenant policy; raising past this ceiling is
// prohibited so a client cannot pin GC indefinitely. The value here
// (90 days) is a reasonable default that exceeds the §7.1 7-day
// retention default by an order of magnitude while staying within
// audit-recoverability windows.
const ExtendRetentionMaxSeconds = int64(90 * 24 * 60 * 60)

// ExtendRetentionRequest is the §15.1
// POST /v1/sessions/{id}/extend-retention body.
type ExtendRetentionRequest struct {
	// TTLSeconds is the number of seconds, measured from "now" (the
	// gateway clock), the session's artifacts should remain eligible
	// for retrieval before the GC job becomes eligible to delete
	// them. Must be > 0 and <= ExtendRetentionMaxSeconds.
	// Spec: §7.1 artifact retention.
	TTLSeconds int64 `json:"ttlSeconds"`
}

// ExtendRetentionResponse echoes the updated retention deadline.
type ExtendRetentionResponse struct {
	SessionResponse

	// RetentionExpiresAt is the absolute RFC3339Nano timestamp at
	// which the GC job becomes eligible to delete this session's
	// artifacts.
	RetentionExpiresAt string `json:"retentionExpiresAt"`
}

// handleExtendRetention implements POST /v1/sessions/{id}/extend-retention.
// The handler validates the TTL, sets the new RetentionExpiresAt on
// the session row, and returns the updated session envelope.
//
// §7.1 specifies the field but not a hard upper bound on TTL;
// production deployments cap it via tenant policy (deferred to the
// admin API phase). The in-memory gateway accepts any positive value.
//
// Per §15.1 the spec does not name a precondition state — retention
// extension is valid for any session row that exists (including
// terminal rows, which is the typical case since clients ask to
// retain a completed session's artifacts beyond the default 7 days).
func (s *Server) handleExtendRetention(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")

	var req ExtendRetentionRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if req.TTLSeconds <= 0 {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"ttlSeconds must be > 0",
			map[string]any{"fields": []map[string]string{{"field": "ttlSeconds"}}})
		return
	}
	if req.TTLSeconds > ExtendRetentionMaxSeconds {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"ttlSeconds exceeds the platform retention ceiling",
			map[string]any{
				"fields":     []map[string]string{{"field": "ttlSeconds"}},
				"maxSeconds": ExtendRetentionMaxSeconds,
			})
		return
	}

	now := s.clock()
	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		row.RetentionExpiresAt = now.Add(time.Duration(req.TTLSeconds) * time.Second)
		return nil
	})
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	resp := ExtendRetentionResponse{
		SessionResponse:    toResponse(updated),
		RetentionExpiresAt: updated.RetentionExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
