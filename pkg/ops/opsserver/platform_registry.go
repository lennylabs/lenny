// SPDX-License-Identifier: MIT

package opsserver

import (
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/registryservice"
)

// registerPlatformRegistryRoutes wires the §25.8 runtime registry API. The
// routes register only when a registry service is configured; a nil
// service leaves them unmapped (404), the cold-start posture for a
// deployment without the registry service wired.
//
// spec: §25.8 Image Registry Configuration / Runtime API (lines
// 3300-3301, 3360-3362).
func (s *Server) registerPlatformRegistryRoutes() {
	if s.registry == nil {
		return
	}
	s.mux.HandleFunc("GET /v1/admin/platform/registry", s.handleRegistryGet)
	s.mux.HandleFunc("PUT /v1/admin/platform/registry", s.handleRegistryPut)
}

// registryUnavailable reports the registry service is not configured.
func (s *Server) registryUnavailable(w http.ResponseWriter) {
	conventions.WriteError(w, http.StatusServiceUnavailable, "REGISTRY_SERVICE_UNAVAILABLE",
		conventions.CategoryTransient, "the platform-registry service is not configured")
}

// handleRegistryGet serves GET /v1/admin/platform/registry: the effective
// registry configuration with the pull-secret name but not its value
// (§25.8 line 3362).
func (s *Server) handleRegistryGet(w http.ResponseWriter, r *http.Request) {
	if s.registry == nil {
		s.registryUnavailable(w)
		return
	}
	cfg, err := s.registry.Effective(r.Context())
	if err != nil {
		// §25.8 line 3610: Postgres down — the runtime override is
		// unreadable. Surface a transient error rather than the chart base,
		// so the operator does not act on a stale view.
		conventions.WriteError(w, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE",
			conventions.CategoryTransient, "the registry configuration store is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// handleRegistryPut serves PUT /v1/admin/platform/registry: a restart-free
// update of the registry URL and per-component overrides, persisted to
// Postgres and effective on the next image resolution.
func (s *Server) handleRegistryPut(w http.ResponseWriter, r *http.Request) {
	if s.registry == nil {
		s.registryUnavailable(w)
		return
	}
	var req registryservice.UpdateRequest
	if err := readJSONBody(r, &req); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	req.Actor = callerIdentity(r)
	cfg, err := s.registry.Update(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, registryservice.ErrReadOnly):
			conventions.WriteError(w, http.StatusServiceUnavailable, "REGISTRY_READ_ONLY",
				conventions.CategoryTransient, "the registry has no runtime store; configure platform.registry.* via the chart")
		case errors.Is(err, registryservice.ErrNoBase):
			conventions.WriteError(w, http.StatusUnprocessableEntity, "REGISTRY_VALIDATION_FAILED",
				conventions.CategoryPermanent, "a registry url or per-component overrides are required")
		default:
			conventions.WriteError(w, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE",
				conventions.CategoryTransient, "the registry configuration store is unavailable")
		}
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}
