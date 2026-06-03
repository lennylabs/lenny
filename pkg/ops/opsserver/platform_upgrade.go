// SPDX-License-Identifier: MIT

package opsserver

import (
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/releasechannel"
)

// registerPlatformUpgradeRoutes wires the §25.8 platform-upgrade
// orchestration endpoints. The upgrade-check route is registered when a
// Checker is configured; the lifecycle routes when an upgrade Service
// is configured. A nil dependency leaves its routes unmapped (404), the
// cold-start posture for a deployment without the orchestrator wired.
//
// spec: §25.8 (Upgrade orchestration endpoints).
func (s *Server) registerPlatformUpgradeRoutes() {
	if s.upgradeChecker != nil {
		s.mux.HandleFunc("GET /v1/admin/platform/upgrade-check", s.handleUpgradeCheck)
	}
	if s.versionAggregator != nil {
		s.mux.HandleFunc("GET /v1/admin/platform/version/full", s.handleVersionFull)
	}
	if s.upgrade == nil {
		return
	}
	s.mux.HandleFunc("POST /v1/admin/platform/upgrade/start", s.handleUpgradeStart)
	s.mux.HandleFunc("POST /v1/admin/platform/upgrade/proceed", s.handleUpgradeProceed)
	s.mux.HandleFunc("POST /v1/admin/platform/upgrade/pause", s.handleUpgradePause)
	s.mux.HandleFunc("POST /v1/admin/platform/upgrade/rollback", s.handleUpgradeRollback)
	s.mux.HandleFunc("POST /v1/admin/platform/upgrade/verify", s.handleUpgradeVerify)
	s.mux.HandleFunc("GET /v1/admin/platform/upgrade/status", s.handleUpgradeStatus)
}

// upgradeUnavailable reports the orchestrator is not configured.
func (s *Server) upgradeUnavailable(w http.ResponseWriter) {
	conventions.WriteError(w, http.StatusServiceUnavailable, upgradeservice.CodeUnavailable,
		conventions.CategoryTransient, "the platform-upgrade orchestrator is not configured")
}

// handleUpgradeStart serves POST /v1/admin/platform/upgrade/start.
func (s *Server) handleUpgradeStart(w http.ResponseWriter, r *http.Request) {
	if s.upgrade == nil {
		s.upgradeUnavailable(w)
		return
	}
	var req upgradeservice.StartRequest
	if err := readJSONBody(r, &req); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	req.StartedBy = callerIdentity(r)
	st, err := s.upgrade.Start(r.Context(), req)
	if err != nil {
		s.writeUpgradeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, upgradeStatusBody(st))
}

// handleUpgradeProceed serves POST /v1/admin/platform/upgrade/proceed.
func (s *Server) handleUpgradeProceed(w http.ResponseWriter, r *http.Request) {
	s.upgradeTransition(w, r, func() (upgradeservice.State, error) {
		return s.upgrade.Proceed(r.Context())
	})
}

// handleUpgradePause serves POST /v1/admin/platform/upgrade/pause.
func (s *Server) handleUpgradePause(w http.ResponseWriter, r *http.Request) {
	reason := upgradeReason(r)
	s.upgradeTransition(w, r, func() (upgradeservice.State, error) {
		return s.upgrade.Pause(r.Context(), reason)
	})
}

// handleUpgradeRollback serves POST /v1/admin/platform/upgrade/rollback.
func (s *Server) handleUpgradeRollback(w http.ResponseWriter, r *http.Request) {
	reason := upgradeReason(r)
	s.upgradeTransition(w, r, func() (upgradeservice.State, error) {
		return s.upgrade.Rollback(r.Context(), reason)
	})
}

// handleUpgradeVerify serves POST /v1/admin/platform/upgrade/verify.
func (s *Server) handleUpgradeVerify(w http.ResponseWriter, r *http.Request) {
	s.upgradeTransition(w, r, func() (upgradeservice.State, error) {
		return s.upgrade.Verify(r.Context())
	})
}

// upgradeTransition runs a mutating upgrade transition and writes the
// resulting state or the classified error.
func (s *Server) upgradeTransition(w http.ResponseWriter, _ *http.Request, fn func() (upgradeservice.State, error)) {
	if s.upgrade == nil {
		s.upgradeUnavailable(w)
		return
	}
	st, err := fn()
	if err != nil {
		s.writeUpgradeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, upgradeStatusBody(st))
}

// handleUpgradeStatus serves GET /v1/admin/platform/upgrade/status.
func (s *Server) handleUpgradeStatus(w http.ResponseWriter, r *http.Request) {
	if s.upgrade == nil {
		s.upgradeUnavailable(w)
		return
	}
	st, ok, err := s.upgrade.Status(r.Context())
	if err != nil {
		s.writeUpgradeError(w, err)
		return
	}
	if !ok {
		conventions.WriteError(w, http.StatusNotFound, upgradeservice.CodeNoUpgrade,
			conventions.CategoryPermanent, "no platform upgrade has been started")
		return
	}
	writeJSON(w, http.StatusOK, upgradeStatusBody(st))
}

// handleUpgradeCheck serves GET /v1/admin/platform/upgrade-check.
func (s *Server) handleUpgradeCheck(w http.ResponseWriter, r *http.Request) {
	if s.upgradeChecker == nil {
		s.upgradeUnavailable(w)
		return
	}
	res, err := s.upgradeChecker.Check(r.Context())
	if err != nil {
		switch {
		case errors.Is(err, upgradeservice.ErrChannelDisabled):
			conventions.WriteError(w, http.StatusNotFound, "UPGRADE_CHANNEL_DISABLED",
				conventions.CategoryPermanent, "the release channel is disabled (platform.upgradeChannel is empty)")
		case errors.Is(err, releasechannel.ErrManifestNotFound):
			conventions.WriteError(w, http.StatusServiceUnavailable, upgradeservice.CodeChannelUnreachable,
				conventions.CategoryTransient, "the release channel has no advertised release")
		default:
			conventions.WriteError(w, http.StatusServiceUnavailable, upgradeservice.CodeChannelUnreachable,
				conventions.CategoryTransient, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleVersionFull serves GET /v1/admin/platform/version/full: the
// §25.8 aggregated version report across the components lenny-ops can
// query, with per-component drift against the running build. Sources
// that cannot be reached degrade their component to unavailable and add
// a degradation warning rather than failing the report.
func (s *Server) handleVersionFull(w http.ResponseWriter, r *http.Request) {
	if s.versionAggregator == nil {
		conventions.WriteError(w, http.StatusServiceUnavailable, upgradeservice.CodeUnavailable,
			conventions.CategoryTransient, "the platform version aggregator is not configured")
		return
	}
	writeJSON(w, http.StatusOK, s.versionAggregator.Aggregate(r.Context()))
}

// upgradeReason extracts the optional operator justification from a
// pause/rollback body. A malformed body yields no reason rather than a
// 400, since the field is optional.
func upgradeReason(r *http.Request) string {
	var body struct {
		Reason string `json:"reason"`
	}
	_ = readJSONBody(r, &body)
	return body.Reason
}

// upgradeStatusBody renders the §25.8 upgrade-status response: the
// singleton state plus the §25.8 line 357 progress object and the
// canonical rollbackable flag the agent reads before calling rollback.
func upgradeStatusBody(st upgradeservice.State) map[string]any {
	return map[string]any{
		"operationId":   st.OperationID,
		"phase":         string(st.Phase),
		"targetVersion": st.TargetVersion,
		"imageDigest":   st.ImageDigest,
		"startedBy":     st.StartedBy,
		"startedAt":     st.StartedAt,
		"updatedAt":     st.UpdatedAt,
		"paused":        st.Paused,
		"verified":      st.Verified,
		"reason":        st.Reason,
		"active":        st.Active(),
		"progress":      st.Progress(),
	}
}

// writeUpgradeError classifies an upgradeservice error into the §25.2
// canonical envelope with the appropriate HTTP status.
func (s *Server) writeUpgradeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, upgradeservice.ErrUpgradeInProgress):
		// §25.8: UPGRADE_ALREADY_IN_PROGRESS is category POLICY, 409.
		conventions.WriteError(w, http.StatusConflict, upgradeservice.CodeUpgradeInProgress,
			conventions.CategoryPolicy, err.Error())
	case errors.Is(err, upgradeservice.ErrNoUpgrade):
		// §25.8: UPGRADE_NOT_IN_PROGRESS is 409 PERMANENT for a mutating
		// proceed/pause/rollback/verify with no active upgrade. (A GET
		// /status with no upgrade is handled separately as a 404.)
		conventions.WriteError(w, http.StatusConflict, upgradeservice.CodeNoUpgrade,
			conventions.CategoryPermanent, err.Error())
	case errors.Is(err, upgradeservice.ErrUpgradeTerminal):
		conventions.WriteError(w, http.StatusConflict, upgradeservice.CodeUpgradeTerminal,
			conventions.CategoryPermanent, err.Error())
	case errors.Is(err, upgradeservice.ErrNotRollbackable):
		conventions.WriteError(w, http.StatusConflict, upgradeservice.CodeNotRollbackable,
			conventions.CategoryPermanent, err.Error())
	case errors.Is(err, upgradeservice.ErrNotVerifiable):
		conventions.WriteError(w, http.StatusConflict, upgradeservice.CodeNotVerifiable,
			conventions.CategoryPermanent, err.Error())
	default:
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, err.Error())
	}
}
