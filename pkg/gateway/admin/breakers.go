// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/observability/audit"
)

// scopeDetail returns the §16.7 line 673 tier-specific scope object the
// `circuit_breaker.state_changed` audit row carries. The shape mirrors
// the admin API body and the persisted `cb:{name}` value: a single
// key keyed by `limit_tier` (`runtime`, `pool`, `connector`, or
// `operation_type`). F-16.7.4.
func scopeDetail(s ScopePayload) map[string]any {
	switch {
	case s.Runtime != "":
		return map[string]any{"runtime": s.Runtime}
	case s.Pool != "":
		return map[string]any{"pool": s.Pool}
	case s.Connector != "":
		return map[string]any{"connector": s.Connector}
	case s.OperationType != "":
		return map[string]any{"operation_type": s.OperationType}
	}
	return map[string]any{}
}

// scopeDetailFromStored returns the spec-mandated scope object from a
// stored Breaker — used on Close where the admin request carries an
// empty body but the persisted `cb:{name}` value supplies the tier and
// scope SIEM consumers join on. F-16.7.4.
func scopeDetailFromStored(b circuitbreaker.Breaker) map[string]any {
	switch {
	case b.Scope.Runtime != "":
		return map[string]any{"runtime": b.Scope.Runtime}
	case b.Scope.Pool != "":
		return map[string]any{"pool": b.Scope.Pool}
	case b.Scope.Connector != "":
		return map[string]any{"connector": b.Scope.Connector}
	case b.Scope.OperationType != "":
		return map[string]any{"operation_type": string(b.Scope.OperationType)}
	}
	return map[string]any{}
}

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
	// spec: §16.7 line 673 — the operator-managed circuit-breaker
	// lifecycle event is `circuit_breaker.state_changed` with
	// old_state/new_state fields. The `circuit_breaker.opened` /
	// `circuit_breaker.closed` strings the gateway used to emit are
	// not in the §16.7 catalog and have no OCSF mapping; rows would
	// dead-letter at translation. F-16.7.4.
	r.emit(req.Context(), principal, audit.EventCircuitBreakerStateChanged.String(), name, map[string]any{
		"circuit_name":       name,
		"old_state":          "closed",
		"new_state":          "open",
		"reason":             body.Reason,
		"limit_tier":         body.LimitTier,
		"scope":              scopeDetail(body.Scope),
		"operator_sub":       principal.Subject,
		"operator_tenant_id": principal.TenantID,
		"timestamp":          rfc3339Nano(stored.OpenedAt),
	})
	// §25.3: surface the breaker transition as an operational event so
	// ops agents observe it through the event buffer.
	r.emitOpsEvent(req.Context(), events.EventCircuitBreakerOpened, "warning", map[string]any{
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
	// spec: §16.7 line 673 — close path emits the same
	// `circuit_breaker.state_changed` event with old_state=open,
	// new_state=closed and a platform-generated reason ("operator
	// close"). The persisted tier/scope are echoed so a SIEM joining
	// on `limit_tier` finds both transitions for the same breaker.
	// F-16.7.4.
	r.emit(req.Context(), principal, audit.EventCircuitBreakerStateChanged.String(), name, map[string]any{
		"circuit_name":       name,
		"old_state":          "open",
		"new_state":          "closed",
		"reason":             "operator close",
		"limit_tier":         string(closed.LimitTier),
		"scope":              scopeDetailFromStored(closed),
		"operator_sub":       principal.Subject,
		"operator_tenant_id": principal.TenantID,
		"timestamp":          rfc3339Nano(r.clock()),
	})
	r.emitOpsEvent(req.Context(), events.EventCircuitBreakerClosed, "info", map[string]any{
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
