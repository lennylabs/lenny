// SPDX-License-Identifier: MIT

package platformtools

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
)

// MemoryRequest is the §9.4 POST /v1/sessions/{id}/memory body. The
// REST surface mirrors the `lenny/memory_write` MCP tool: every
// record carries the content and optional metadata. The owning
// (tenant, user, session) scope is derived from the URL session id
// and the request's tenant header so a malicious caller cannot
// inject records under another scope.
type MemoryRequest struct {
	Memories []MemoryItem `json:"memories"`
}

// MemoryItem is one §9.4 memory record on the wire.
type MemoryItem struct {
	ID        string         `json:"id,omitempty"`
	AgentType string         `json:"agentType,omitempty"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// MemoryResponse is the wire shape of one stored §9.4 memory.
type MemoryResponse struct {
	ID        string         `json:"id"`
	TenantID  string         `json:"tenantId"`
	UserID    string         `json:"userId"`
	AgentType string         `json:"agentType,omitempty"`
	SessionID string         `json:"sessionId"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt string         `json:"createdAt"`
}

// MemoryQueryResponse wraps a §9.4 query result.
type MemoryQueryResponse struct {
	Memories []MemoryResponse `json:"memories"`
}

// handleMemoryWrite implements POST /v1/sessions/{id}/memory — the
// REST counterpart to the §9.4 `lenny/memory_write` MCP tool.
func (h *Handler) handleMemoryWrite(w http.ResponseWriter, r *http.Request) {
	if h.memory == nil {
		h.writeError(w, http.StatusServiceUnavailable, "MEMORY_UNAVAILABLE",
			"the §9.4 memory store is not configured", nil)
		return
	}
	tenantID := h.identity.RequestTenant(r)
	id := r.PathValue("id")
	sess, ok := h.loadSessionForMemory(w, r, tenantID, id)
	if !ok {
		return
	}

	var req MemoryRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if len(req.Memories) == 0 {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"memories must contain at least one record",
			map[string]any{"field": "memories"})
		return
	}
	for i, m := range req.Memories {
		if m.Content == "" {
			h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"every memory record requires a non-empty content field",
				map[string]any{"field": "memories", "index": i})
			return
		}
	}

	scope := memorystore.MemoryScope{
		TenantID:  tenantID,
		UserID:    sess.UserID,
		SessionID: id,
	}
	if scope.UserID == "" {
		h.writeError(w, http.StatusUnprocessableEntity, "SESSION_NOT_USER_SCOPED",
			"the session has no user id; memory writes require a user-scoped session", nil)
		return
	}
	records := make([]memorystore.Memory, len(req.Memories))
	for i, m := range req.Memories {
		records[i] = memorystore.Memory{
			ID:        m.ID,
			AgentType: m.AgentType,
			Content:   m.Content,
			Metadata:  m.Metadata,
		}
	}
	if err := h.memory.Write(r.Context(), scope, records); err != nil {
		h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// Read back the freshest records for the response. The §9.4 store
	// returns them newest-first; the §9.4 limit cap is the number of
	// records the caller just wrote.
	stored, err := h.memory.List(r.Context(), scope, memorystore.MemoryFilter{Limit: len(records)})
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(MemoryQueryResponse{Memories: toMemoryResponses(stored)})
}

// handleMemoryQuery implements GET /v1/sessions/{id}/memory — the
// REST counterpart to the §9.4 `lenny/memory_query` MCP tool. The
// `q` query string filters by content substring, `limit` caps the
// row count, and the response sorts newest-first.
func (h *Handler) handleMemoryQuery(w http.ResponseWriter, r *http.Request) {
	if h.memory == nil {
		h.writeError(w, http.StatusServiceUnavailable, "MEMORY_UNAVAILABLE",
			"the §9.4 memory store is not configured", nil)
		return
	}
	tenantID := h.identity.RequestTenant(r)
	id := r.PathValue("id")
	sess, ok := h.loadSessionForMemory(w, r, tenantID, id)
	if !ok {
		return
	}
	scope := memorystore.MemoryScope{
		TenantID:  tenantID,
		UserID:    sess.UserID,
		SessionID: id,
	}
	if scope.UserID == "" {
		h.writeError(w, http.StatusUnprocessableEntity, "SESSION_NOT_USER_SCOPED",
			"the session has no user id; memory reads require a user-scoped session", nil)
		return
	}
	query := r.URL.Query().Get("q")
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"limit must be a non-negative integer",
				map[string]any{"field": "limit"})
			return
		}
		limit = v
	}
	matches, err := h.memory.Query(r.Context(), scope, query, limit)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(MemoryQueryResponse{Memories: toMemoryResponses(matches)})
}

// handleMemoryDelete implements DELETE /v1/sessions/{id}/memory/{memoryId}.
func (h *Handler) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	if h.memory == nil {
		h.writeError(w, http.StatusServiceUnavailable, "MEMORY_UNAVAILABLE",
			"the §9.4 memory store is not configured", nil)
		return
	}
	tenantID := h.identity.RequestTenant(r)
	id := r.PathValue("id")
	memoryID := r.PathValue("memoryId")
	if memoryID == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "memoryId is required", nil)
		return
	}
	sess, ok := h.loadSessionForMemory(w, r, tenantID, id)
	if !ok {
		return
	}
	scope := memorystore.MemoryScope{
		TenantID:  tenantID,
		UserID:    sess.UserID,
		SessionID: id,
	}
	if scope.UserID == "" {
		h.writeError(w, http.StatusUnprocessableEntity, "SESSION_NOT_USER_SCOPED",
			"the session has no user id; memory deletes require a user-scoped session", nil)
		return
	}
	if err := h.memory.Delete(r.Context(), scope, []string{memoryID}); err != nil {
		h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loadSessionForMemory resolves the URL session id under the request's
// tenant through GWSEAM-LookupSession and writes the RESOURCE_NOT_FOUND
// envelope when the session does not exist. Returns ok=false when an error
// was already written to w.
func (h *Handler) loadSessionForMemory(w http.ResponseWriter, r *http.Request, tenantID, id string) (seam.Session, bool) {
	sess, err := h.sessions.LookupSession(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, seam.ErrSessionNotFound) {
			h.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return seam.Session{}, false
		}
		h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return seam.Session{}, false
	}
	return sess, true
}

// toMemoryResponses projects stored memorystore.Memory values onto
// the wire shape.
func toMemoryResponses(in []memorystore.Memory) []MemoryResponse {
	out := make([]MemoryResponse, len(in))
	for i, m := range in {
		out[i] = MemoryResponse{
			ID:        m.ID,
			TenantID:  m.TenantID,
			UserID:    m.UserID,
			AgentType: m.AgentType,
			SessionID: m.SessionID,
			Content:   m.Content,
			Metadata:  m.Metadata,
			CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
	}
	return out
}
