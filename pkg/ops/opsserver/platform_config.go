// SPDX-License-Identifier: MIT

package opsserver

import (
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/ops/configservice"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// registerPlatformConfigRoutes wires the §25.8 config diff/apply
// endpoints. They register only when a config service is configured; a
// nil service leaves them unmapped (404), the cold-start posture for a
// deployment without a gateway config client.
//
// spec: §25.8 Config Diff and Config Apply (lines 3566-3574).
func (s *Server) registerPlatformConfigRoutes() {
	if s.platformConfig == nil {
		return
	}
	s.mux.HandleFunc("GET /v1/admin/platform/config/diff", s.handleConfigDiff)
	s.mux.HandleFunc("PUT /v1/admin/platform/config", s.handleConfigApply)
}

// handleConfigDiff serves GET /v1/admin/platform/config/diff: the
// field-by-field diff between a desired config (request body) and the
// gateway's running config. Used for GitOps reconciliation.
func (s *Server) handleConfigDiff(w http.ResponseWriter, r *http.Request) {
	if s.platformConfig == nil {
		s.configUnavailable(w)
		return
	}
	var body struct {
		Desired map[string]any `json:"desired"`
	}
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	res, err := s.platformConfig.Diff(r.Context(), body.Desired)
	if err != nil {
		s.writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleConfigApply serves PUT /v1/admin/platform/config: validates the
// proposed config, returns a dry-run impact preview unless the body sets
// confirm:true, and proxies a confirmed change to the gateway.
func (s *Server) handleConfigApply(w http.ResponseWriter, r *http.Request) {
	if s.platformConfig == nil {
		s.configUnavailable(w)
		return
	}
	var req configservice.ApplyRequest
	if err := readJSONBody(r, &req); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	res, err := s.platformConfig.Apply(r.Context(), req)
	if err != nil {
		s.writeConfigError(w, err)
		return
	}
	// §25.8 line 3574: a confirmed apply that needs a gateway restart
	// returns 422 CONFIG_RESTART_REQUIRED — the change is applied but takes
	// effect only after restart. The dry-run never returns the code.
	if res.Applied && res.RestartRequired {
		conventions.WriteErrorWithDetails(w, http.StatusUnprocessableEntity,
			configservice.CodeRestartRequired, conventions.CategoryPermanent,
			"the config change was applied but requires a gateway restart to take effect",
			map[string]any{"applied": true, "restartRequired": true, "diff": res.Diff})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// configUnavailable reports the config service is not configured.
func (s *Server) configUnavailable(w http.ResponseWriter) {
	conventions.WriteError(w, http.StatusServiceUnavailable, "CONFIG_SERVICE_UNAVAILABLE",
		conventions.CategoryTransient, "the platform-config service is not configured")
}

// writeConfigError classifies a configservice error into the §25.8
// canonical envelope. A schema-validation failure maps to 422
// CONFIG_VALIDATION_FAILED with details.errors; a gateway-unavailable
// failure maps to 503 TRANSIENT (§25.8 line 3610 degradation).
func (s *Server) writeConfigError(w http.ResponseWriter, err error) {
	var invalid *configservice.ValidationFailed
	switch {
	case errors.As(err, &invalid):
		details := make([]map[string]any, 0, len(invalid.Errors))
		for _, e := range invalid.Errors {
			details = append(details, map[string]any{"field": e.Field, "message": e.Message})
		}
		conventions.WriteErrorWithDetails(w, http.StatusUnprocessableEntity,
			configservice.CodeValidationFailed, conventions.CategoryPermanent,
			invalid.Error(), map[string]any{"errors": details})
	case errors.Is(err, configservice.ErrGatewayUnavailable):
		conventions.WriteError(w, http.StatusServiceUnavailable, "CONFIG_GATEWAY_UNAVAILABLE",
			conventions.CategoryTransient, "the gateway config API is unavailable")
	default:
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, err.Error())
	}
}
