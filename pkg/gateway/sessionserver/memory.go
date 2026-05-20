// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
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
func (s *Server) handleMemoryWrite(w http.ResponseWriter, r *http.Request) {
	if s.memory == nil {
		s.writeError(w, http.StatusServiceUnavailable, "MEMORY_UNAVAILABLE",
			"the §9.4 memory store is not configured", nil)
		return
	}
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	sess, ok := s.loadSessionForMemory(w, r, tenantID, id)
	if !ok {
		return
	}

	var req MemoryRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if len(req.Memories) == 0 {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"memories must contain at least one record",
			map[string]any{"field": "memories"})
		return
	}
	for i, m := range req.Memories {
		if m.Content == "" {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
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
		s.writeError(w, http.StatusUnprocessableEntity, "SESSION_NOT_USER_SCOPED",
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
	if err := s.memory.Write(r.Context(), scope, records); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// Read back the freshest records for the response. The §9.4 store
	// returns them newest-first; the §9.4 limit cap is the number of
	// records the caller just wrote.
	stored, err := s.memory.List(r.Context(), scope, memorystore.MemoryFilter{Limit: len(records)})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
func (s *Server) handleMemoryQuery(w http.ResponseWriter, r *http.Request) {
	if s.memory == nil {
		s.writeError(w, http.StatusServiceUnavailable, "MEMORY_UNAVAILABLE",
			"the §9.4 memory store is not configured", nil)
		return
	}
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	sess, ok := s.loadSessionForMemory(w, r, tenantID, id)
	if !ok {
		return
	}
	scope := memorystore.MemoryScope{
		TenantID:  tenantID,
		UserID:    sess.UserID,
		SessionID: id,
	}
	if scope.UserID == "" {
		s.writeError(w, http.StatusUnprocessableEntity, "SESSION_NOT_USER_SCOPED",
			"the session has no user id; memory reads require a user-scoped session", nil)
		return
	}
	query := r.URL.Query().Get("q")
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"limit must be a non-negative integer",
				map[string]any{"field": "limit"})
			return
		}
		limit = v
	}
	matches, err := s.memory.Query(r.Context(), scope, query, limit)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(MemoryQueryResponse{Memories: toMemoryResponses(matches)})
}

// handleMemoryDelete implements DELETE /v1/sessions/{id}/memory/{memoryId}.
func (s *Server) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	if s.memory == nil {
		s.writeError(w, http.StatusServiceUnavailable, "MEMORY_UNAVAILABLE",
			"the §9.4 memory store is not configured", nil)
		return
	}
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	memoryID := r.PathValue("memoryId")
	if memoryID == "" {
		s.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "memoryId is required", nil)
		return
	}
	sess, ok := s.loadSessionForMemory(w, r, tenantID, id)
	if !ok {
		return
	}
	scope := memorystore.MemoryScope{
		TenantID:  tenantID,
		UserID:    sess.UserID,
		SessionID: id,
	}
	if scope.UserID == "" {
		s.writeError(w, http.StatusUnprocessableEntity, "SESSION_NOT_USER_SCOPED",
			"the session has no user id; memory deletes require a user-scoped session", nil)
		return
	}
	if err := s.memory.Delete(r.Context(), scope, []string{memoryID}); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loadSessionForMemory resolves the URL session id under the
// request's tenant and writes the §15.1 RESOURCE_NOT_FOUND envelope
// when the session does not exist. Returns ok=false when an error
// was already written to w.
func (s *Server) loadSessionForMemory(w http.ResponseWriter, r *http.Request, tenantID, id string) (sessionstore.Session, bool) {
	sess, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return sessionstore.Session{}, false
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return sessionstore.Session{}, false
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
