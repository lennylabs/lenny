// SPDX-License-Identifier: MIT

package opsserver

import (
	"net/http"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
)

// escalationErrorMap maps each §25.4 canonical escalation error code to
// its documented HTTP status and §25.2 category.
var escalationErrorMap = map[string]struct {
	status   int
	category conventions.ErrorCategory
}{
	escalation.ErrCodeNotFound:       {http.StatusNotFound, conventions.CategoryPermanent},
	escalation.ErrCodeNoDurableStore: {http.StatusServiceUnavailable, conventions.CategoryTransient},
	escalation.ErrCodeInvalid:        {http.StatusBadRequest, conventions.CategoryPermanent},
}

// writeEscalationError maps a §25.4 escalation service error to the
// §25.2 canonical error envelope and writes it.
func writeEscalationError(w http.ResponseWriter, err error) {
	code := escalation.CodeOf(err)
	if mapping, ok := escalationErrorMap[code]; ok {
		conventions.WriteError(w, mapping.status, code, mapping.category, err.Error())
		return
	}
	conventions.WriteError(w, http.StatusInternalServerError, "INTERNAL",
		conventions.CategoryTransient, err.Error())
}

// registerEscalationRoutes wires the §25.4 escalation endpoints onto the
// Server's mux.
func (s *Server) registerEscalationRoutes() {
	s.mux.HandleFunc("POST /v1/admin/escalations", s.handleCreateEscalation)
	s.mux.HandleFunc("GET /v1/admin/escalations", s.handleListEscalations)
	s.mux.HandleFunc("PUT /v1/admin/escalations/{id}", s.handleUpdateEscalation)
}

// escalationsUnavailable reports the §25.4 escalation surface as
// unconfigured.
func (s *Server) escalationsUnavailable(w http.ResponseWriter) {
	conventions.WriteError(w, http.StatusServiceUnavailable, "ESCALATION_SERVICE_UNAVAILABLE",
		conventions.CategoryTransient, "the escalation subsystem is not configured")
}

// handleCreateEscalation serves POST /v1/admin/escalations: record a
// §25.4 escalation. Creation emits the escalation_created event. The
// HTTP status reflects the durability tier the record landed in — 201
// for a durable store, 202 for the in-memory Tier 3 buffer.
func (s *Server) handleCreateEscalation(w http.ResponseWriter, r *http.Request) {
	if s.escalations == nil {
		s.escalationsUnavailable(w)
		return
	}
	var body escalation.CreateRequest
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	body.Source = callerIdentity(r)
	esc, err := s.escalations.Create(r.Context(), body)
	if err != nil {
		writeEscalationError(w, err)
		return
	}
	// §25.4: a record in the in-memory Tier 3 buffer is a 202 with the
	// X-Lenny-Persistence header; a durable record is a 201.
	status := http.StatusCreated
	if esc.Persistence == escalation.PersistenceBufferedMemory {
		status = http.StatusAccepted
	}
	w.Header().Set("X-Lenny-Persistence", esc.Persistence)
	writeJSON(w, status, esc)
}

// handleListEscalations serves GET /v1/admin/escalations: list the
// §25.4 escalations matching the ?status=, ?severity=, ?since=, and
// ?limit= filters.
func (s *Server) handleListEscalations(w http.ResponseWriter, r *http.Request) {
	if s.escalations == nil {
		s.escalationsUnavailable(w)
		return
	}
	q := r.URL.Query()
	params, err := conventions.ParsePageParams(q, "desc")
	if err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST",
			conventions.CategoryPermanent, err.Error())
		return
	}
	escs, err := s.escalations.List(r.Context(), escalation.Filter{
		Status:   q.Get("status"),
		Severity: q.Get("severity"),
		Since:    params.Since,
	}, params.Limit)
	if err != nil {
		writeEscalationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": escs,
		"pagination": conventions.Pagination{
			HasMore: false,
			Limit:   params.Limit,
		},
	})
}

// handleUpdateEscalation serves PUT /v1/admin/escalations/{id}: move a
// §25.4 escalation to acknowledged or resolved.
func (s *Server) handleUpdateEscalation(w http.ResponseWriter, r *http.Request) {
	if s.escalations == nil {
		s.escalationsUnavailable(w)
		return
	}
	var body escalation.UpdateRequest
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	esc, err := s.escalations.Update(r.Context(), r.PathValue("id"), body)
	if err != nil {
		writeEscalationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, esc)
}
