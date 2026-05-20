// SPDX-License-Identifier: MIT

package opsserver

import (
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// eventSubscriptionErrorMap maps each §25.4 canonical event-
// subscription error code to its documented HTTP status and §25.2
// category.
var eventSubscriptionErrorMap = map[string]struct {
	status   int
	category conventions.ErrorCategory
}{
	eventsubscription.ErrCodeNotFound:       {http.StatusNotFound, conventions.CategoryPermanent},
	eventsubscription.ErrCodeInvalid:        {http.StatusBadRequest, conventions.CategoryPermanent},
	eventsubscription.ErrCodeNoDurableStore: {http.StatusServiceUnavailable, conventions.CategoryTransient},
}

// writeEventSubscriptionError maps a §25.5 service error to the §25.2
// canonical error envelope and writes it.
func writeEventSubscriptionError(w http.ResponseWriter, err error) {
	code := eventsubscription.CodeOf(err)
	if mapping, ok := eventSubscriptionErrorMap[code]; ok {
		conventions.WriteError(w, mapping.status, code, mapping.category, err.Error())
		return
	}
	conventions.WriteError(w, http.StatusInternalServerError, "INTERNAL",
		conventions.CategoryTransient, err.Error())
}

// registerEventSubscriptionRoutes wires the §25.5
// /v1/admin/event-subscriptions endpoints onto the Server's mux. The
// routes are registered only when the Server was constructed with an
// EventSubscriptions service; the v1 lenny-ops binary wires an
// in-memory store by default.
func (s *Server) registerEventSubscriptionRoutes() {
	if s.eventSubscriptions == nil {
		return
	}
	s.mux.HandleFunc("POST /v1/admin/event-subscriptions", s.handleCreateEventSubscription)
	s.mux.HandleFunc("GET /v1/admin/event-subscriptions", s.handleListEventSubscriptions)
	s.mux.HandleFunc("GET /v1/admin/event-subscriptions/{id}", s.handleGetEventSubscription)
	s.mux.HandleFunc("DELETE /v1/admin/event-subscriptions/{id}", s.handleDeleteEventSubscription)
}

// handleCreateEventSubscription implements
// POST /v1/admin/event-subscriptions. The §25.5 surface accepts the
// callback URL, an optional event-type filter, and the HMAC secret;
// it returns the allocated subscription record.
func (s *Server) handleCreateEventSubscription(w http.ResponseWriter, r *http.Request) {
	var req eventsubscription.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, eventsubscription.ErrCodeInvalid,
			conventions.CategoryPermanent, "request body is not valid JSON: "+err.Error())
		return
	}
	sub, err := s.eventSubscriptions.Create(r.Context(), req)
	if err != nil {
		writeEventSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sub)
}

// handleListEventSubscriptions implements
// GET /v1/admin/event-subscriptions. Subscriptions are returned in
// id-sorted order so the response is stable for diff-based tooling.
func (s *Server) handleListEventSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := s.eventSubscriptions.List(r.Context())
	if err != nil {
		writeEventSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": subs})
}

// handleGetEventSubscription implements
// GET /v1/admin/event-subscriptions/{id}.
func (s *Server) handleGetEventSubscription(w http.ResponseWriter, r *http.Request) {
	sub, err := s.eventSubscriptions.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeEventSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

// handleDeleteEventSubscription implements
// DELETE /v1/admin/event-subscriptions/{id}.
func (s *Server) handleDeleteEventSubscription(w http.ResponseWriter, r *http.Request) {
	if err := s.eventSubscriptions.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeEventSubscriptionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
