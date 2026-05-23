// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/opsevents"
)

// BreakerPayload is the §15.1 admin-circuit-breaker wire shape.
type BreakerPayload struct {
	Name             string       `json:"name"`
	State            string       `json:"state,omitempty"`
	Reason           string       `json:"reason,omitempty"`
	LimitTier        string       `json:"limit_tier,omitempty"`
	Scope            ScopePayload `json:"scope,omitempty"`
	OpenedAt         string       `json:"opened_at,omitempty"`
	OpenedBySub      string       `json:"opened_by_sub,omitempty"`
	OpenedByTenantID string       `json:"opened_by_tenant_id,omitempty"`
}

// ScopePayload is the per-tier scope object.
type ScopePayload struct {
	Runtime       string `json:"runtime,omitempty"`
	Pool          string `json:"pool,omitempty"`
	Connector     string `json:"connector,omitempty"`
	OperationType string `json:"operation_type,omitempty"`
}

// OpenBreakerRequest is the §15.1 POST /open body.
type OpenBreakerRequest struct {
	Reason    string       `json:"reason"`
	LimitTier string       `json:"limit_tier"`
	Scope     ScopePayload `json:"scope"`
}

func fromBreaker(b circuitbreaker.Breaker) BreakerPayload {
	return BreakerPayload{
		Name:      b.Name,
		State:     string(b.State),
		Reason:    b.Reason,
		LimitTier: string(b.LimitTier),
		Scope: ScopePayload{
			Runtime:       b.Scope.Runtime,
			Pool:          b.Scope.Pool,
			Connector:     b.Scope.Connector,
			OperationType: string(b.Scope.OperationType),
		},
		OpenedAt:         rfc3339Nano(b.OpenedAt),
		OpenedBySub:      b.OpenedBySub,
		OpenedByTenantID: b.OpenedByTenantID,
	}
}

// WithBreakers wires the §15.1 admin breaker handlers onto the Router.
func (r *Router) WithBreakers(s breakerstore.Store) *Router {
	r.breakers = s
	return r
}

func (r *Router) handleListBreakers(w http.ResponseWriter, req *http.Request) {
	rows, err := r.breakers.List(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]BreakerPayload, 0, len(rows))
	for _, b := range rows {
		out = append(out, fromBreaker(b))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"circuit_breakers": out})
}

func (r *Router) handleOpenBreaker(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body OpenBreakerRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	b := circuitbreaker.Breaker{
		Name:      name,
		State:     circuitbreaker.StateOpen,
		Reason:    body.Reason,
		LimitTier: circuitbreaker.LimitTier(body.LimitTier),
		Scope: circuitbreaker.Scope{
			Runtime:       body.Scope.Runtime,
			Pool:          body.Scope.Pool,
			Connector:     body.Scope.Connector,
			OperationType: circuitbreaker.OperationType(body.Scope.OperationType),
		},
		OpenedAt:         r.clock(),
		OpenedBySub:      principal.Subject,
		OpenedByTenantID: principal.TenantID,
	}
	stored, err := r.breakers.Open(req.Context(), b)
	if err != nil {
		if errors.Is(err, breakerstore.ErrScopeImmutable) {
			writeError(w, http.StatusUnprocessableEntity, "INVALID_BREAKER_SCOPE",
				err.Error(), nil)
			return
		}
		var se *circuitbreaker.ScopeError
		if errors.As(err, &se) {
			writeError(w, http.StatusUnprocessableEntity, "INVALID_BREAKER_SCOPE",
				err.Error(), map[string]any{"field": se.Field, "reason": se.Reason})
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	r.emit(req.Context(), principal, "circuit_breaker.opened", name, map[string]any{
		"reason":     body.Reason,
		"limit_tier": body.LimitTier,
		"scope":      body.Scope,
	})
	// §25.3: surface the breaker transition as an operational event so
	// ops agents observe it through the event buffer.
	r.emitOpsEvent(req.Context(), opsevents.EventCircuitBreakerOpened, "warning", map[string]any{
		"name": name, "reason": body.Reason, "openedBy": principal.Subject,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromBreaker(stored))
}

func (r *Router) handleCloseBreaker(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	closed, err := r.breakers.Close(req.Context(), name)
	if err != nil {
		if errors.Is(err, breakerstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "breaker not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "circuit_breaker.closed", name, nil)
	r.emitOpsEvent(req.Context(), opsevents.EventCircuitBreakerClosed, "info", map[string]any{
		"name": name, "closedBy": principal.Subject,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromBreaker(closed))
}

func (r *Router) handleGetBreaker(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	row, err := r.breakers.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, breakerstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "breaker not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromBreaker(row))
}
