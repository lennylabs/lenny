// SPDX-License-Identifier: MIT

package opsserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// CacheInvalidator forces an immediate refresh of the §25.5 subscription
// delivery cache. The opsservice.SubscriptionCache satisfies it. spec:
// §25.5 line 2751.
type CacheInvalidator interface {
	Invalidate(ctx context.Context) error
}

// eventSubscriptionErrorMap maps each §25.5 canonical event-subscription
// error code to its documented HTTP status and §25.2 category. spec:
// §25.5 lines 2795-2802, line 2763.
var eventSubscriptionErrorMap = map[string]struct {
	status   int
	category conventions.ErrorCategory
}{
	eventsubscription.ErrCodeInvalidFilter:     {http.StatusBadRequest, conventions.CategoryPermanent},
	eventsubscription.ErrCodeWebhookValidation: {http.StatusUnprocessableEntity, conventions.CategoryPermanent},
	eventsubscription.ErrCodeNotFound:          {http.StatusNotFound, conventions.CategoryPermanent},
	eventsubscription.ErrCodeNoDurableStore:    {http.StatusServiceUnavailable, conventions.CategoryTransient},
	eventsubscription.ErrCodeTenantForbidden:   {http.StatusForbidden, conventions.CategoryAuth},
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

// subscriptionCaller resolves the §25.5 authenticated principal from the
// verified bearer (or the dev header fallbacks when no AuthConfig is
// wired). spec: §25.5 lines 2758-2766.
func subscriptionCaller(r *http.Request) eventsubscription.Caller {
	c := eventsubscription.Caller{
		Subject:  callerIdentity(r),
		TenantID: callerTenant(r),
	}
	if p, ok := callerPrincipal(r); ok {
		c.PlatformAdmin = p.HasRole(auth.RolePlatformAdmin)
	} else {
		// Dev / embedded fallback: the X-Lenny-Role header drives the
		// platform-admin determination, defaulting to platform-admin when
		// no role header is present (matching callerRole).
		c.PlatformAdmin = r.Header.Get("X-Lenny-Role") != "tenant-admin"
	}
	// A platform-admin acts in platform scope; its tenant claim does not
	// scope its subscriptions.
	if c.PlatformAdmin {
		c.TenantID = ""
	}
	return c
}

// registerEventSubscriptionRoutes wires the §25.5
// /v1/admin/event-subscriptions endpoints onto the Server's mux. The
// routes are registered only when the Server was constructed with an
// EventSubscriptions service. spec: §25.5 lines 2562-2570.
func (s *Server) registerEventSubscriptionRoutes() {
	if s.eventSubscriptions == nil {
		return
	}
	s.mux.HandleFunc("POST /v1/admin/event-subscriptions", s.handleCreateEventSubscription)
	s.mux.HandleFunc("GET /v1/admin/event-subscriptions", s.handleListEventSubscriptions)
	s.mux.HandleFunc("GET /v1/admin/event-subscriptions/{id}", s.handleGetEventSubscription)
	s.mux.HandleFunc("PUT /v1/admin/event-subscriptions/{id}", s.handleUpdateEventSubscription)
	s.mux.HandleFunc("DELETE /v1/admin/event-subscriptions/{id}", s.handleDeleteEventSubscription)
	s.mux.HandleFunc("POST /v1/admin/event-subscriptions/{id}/rotate-secret", s.handleRotateEventSubscriptionSecret)
	s.mux.HandleFunc("GET /v1/admin/event-subscriptions/{id}/deliveries", s.handleListEventSubscriptionDeliveries)
}

// registerCacheInvalidateRoute wires the §25.5 internal
// subscription_cache_invalidate RPC. It registers only when both a
// CacheInvalidator and a non-empty token are configured; otherwise the
// path is unmapped and the cache degrades to periodic-refresh-only.
// spec: §25.5 line 2751.
func (s *Server) registerCacheInvalidateRoute() {
	if s.cacheInvalidator == nil || s.cacheInvalidateToken == "" {
		return
	}
	s.mux.HandleFunc("POST "+opsservice.DefaultCacheInvalidatePath, s.handleCacheInvalidate)
}

// handleCacheInvalidate handles the §25.5 subscription_cache_invalidate
// peer RPC: it authenticates the shared-secret-derived token and forces
// an immediate cache refresh. The RPC injects no data — it only kicks a
// refresh — so a mismatched token returns 401 and a matching one returns
// 204. spec: §25.5 line 2751.
func (s *Server) handleCacheInvalidate(w http.ResponseWriter, r *http.Request) {
	if !opsservice.VerifyInvalidateToken(s.cacheInvalidateToken, r.Header.Get(opsservice.CacheInvalidateHeader)) {
		conventions.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED",
			conventions.CategoryAuth, "invalid cache-invalidate token")
		return
	}
	if err := s.cacheInvalidator.Invalidate(r.Context()); err != nil {
		conventions.WriteError(w, http.StatusServiceUnavailable, eventsubscription.ErrCodeNoDurableStore,
			conventions.CategoryTransient, "cache refresh failed: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateEventSubscription implements
// POST /v1/admin/event-subscriptions. The secret is generated
// server-side and returned once in the response. spec: §25.5 lines
// 2702-2710.
func (s *Server) handleCreateEventSubscription(w http.ResponseWriter, r *http.Request) {
	var req eventsubscription.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, eventsubscription.ErrCodeInvalidFilter,
			conventions.CategoryPermanent, "request body is not valid JSON: "+err.Error())
		return
	}
	reveal, err := s.eventSubscriptions.Create(r.Context(), req, subscriptionCaller(r))
	if err != nil {
		writeEventSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, reveal)
}

// handleListEventSubscriptions implements
// GET /v1/admin/event-subscriptions. The redacted read-view omits the
// secret. spec: §25.5 line 2713.
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

// handleUpdateEventSubscription implements
// PUT /v1/admin/event-subscriptions/{id}. spec: §25.5 line 2568.
func (s *Server) handleUpdateEventSubscription(w http.ResponseWriter, r *http.Request) {
	var req eventsubscription.UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, eventsubscription.ErrCodeInvalidFilter,
			conventions.CategoryPermanent, "request body is not valid JSON: "+err.Error())
		return
	}
	sub, err := s.eventSubscriptions.Update(r.Context(), r.PathValue("id"), req, subscriptionCaller(r))
	if err != nil {
		writeEventSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

// handleDeleteEventSubscription implements
// DELETE /v1/admin/event-subscriptions/{id}.
func (s *Server) handleDeleteEventSubscription(w http.ResponseWriter, r *http.Request) {
	if err := s.eventSubscriptions.Delete(r.Context(), r.PathValue("id"), subscriptionCaller(r)); err != nil {
		writeEventSubscriptionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRotateEventSubscriptionSecret implements
// POST /v1/admin/event-subscriptions/{id}/rotate-secret. The new secret
// is returned once. spec: §25.5 lines 2723-2733.
func (s *Server) handleRotateEventSubscriptionSecret(w http.ResponseWriter, r *http.Request) {
	reveal, err := s.eventSubscriptions.RotateSecret(r.Context(), r.PathValue("id"), subscriptionCaller(r))
	if err != nil {
		writeEventSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reveal)
}

// handleListEventSubscriptionDeliveries implements
// GET /v1/admin/event-subscriptions/{id}/deliveries. spec: §25.5 line
// 2569.
func (s *Server) handleListEventSubscriptionDeliveries(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	deliveries, err := s.eventSubscriptions.ListDeliveries(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		writeEventSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": deliveries})
}
