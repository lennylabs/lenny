// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/blobstore/replication"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// ArtifactReplicationController is the §25.11 ArtifactStore-replication
// seam the admin Router calls for the two operator-facing endpoints:
// resume a region suspended by the runtime residency preflight, and read
// a region's current replication state. *replication.Controller
// satisfies it directly; the gateway wires the live controller via
// WithArtifactReplication once it is built. A nil controller leaves the
// endpoints answering 503 (replication is not configured on this
// deployment, the Tier-1 dev default).
//
// spec: §25.11 lines 3898-3899.
type ArtifactReplicationController interface {
	// Resume re-runs the residency preflight for a suspended region and
	// clears the suspension only when the preflight passes, recording the
	// operator identity and justification on the audit trail. A persisting
	// jurisdiction mismatch returns a replication.ErrRegionUnresolvable-
	// wrapped error; an unknown region returns a plain error.
	Resume(ctx context.Context, region, operatorSub, justification string) error
	// GetState returns a region's current ops_artifact_replication_state
	// row. ok is false when the region has no configuration.
	GetState(ctx context.Context, region string) (replication.RegionState, bool, error)
}

// WithArtifactReplication wires the §25.11 ArtifactStore-replication
// controller onto the Router so POST/GET
// /v1/admin/artifact-replication/{region}/{resume,status} reach the live
// controller. The gateway calls this once the controller is built
// (after the admin Handler is mounted); the handlers read the field at
// request time, so the late wiring takes effect. spec: §25.11 line 3898.
func (r *Router) WithArtifactReplication(c ArtifactReplicationController) *Router {
	r.artifactReplication = c
	return r
}

// resumeArtifactReplicationRequest is the §25.11 POST
// /v1/admin/artifact-replication/{region}/resume body.
type resumeArtifactReplicationRequest struct {
	Justification string `json:"justification"`
}

// writeArtifactReplicationError maps a §25.11 replication error to the
// canonical envelope. A persisting jurisdiction mismatch is the
// PERMANENT 422 ARTIFACT_REPLICATION_REGION_UNRESOLVABLE (§25.11 Error
// Codes table line 4337); an unknown region is a 404; anything else is a
// 500.
func writeArtifactReplicationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, replication.ErrRegionUnresolvable):
		writeError(w, http.StatusUnprocessableEntity, "ARTIFACT_REPLICATION_REGION_UNRESOLVABLE",
			err.Error(), nil)
	case strings.Contains(err.Error(), "no configuration for region"):
		writeError(w, http.StatusNotFound, "ARTIFACT_REPLICATION_REGION_NOT_FOUND",
			err.Error(), nil)
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
	}
}

// artifactReplicationUnconfigured reports the §25.11 replication surface
// as not configured on this deployment. Replication is disabled by
// default at Tier 1 (dev), so the controller is nil and the two
// operator endpoints answer 503 rather than 404 — the subsystem exists
// but is not wired, the same posture the backup endpoints use when
// lenny-ops has no BackupService.
func (r *Router) artifactReplicationUnconfigured(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "ARTIFACT_REPLICATION_UNAVAILABLE",
		"artifact replication is not configured on this deployment", nil)
}

// handleResumeArtifactReplication serves POST
// /v1/admin/artifact-replication/{region}/resume. It re-runs the
// residency preflight synchronously: a persisting jurisdiction mismatch
// is rejected with 422 ARTIFACT_REPLICATION_REGION_UNRESOLVABLE and
// replication stays suspended; on success the controller clears the
// suspension and emits the artifact_replication.resumed audit event
// carrying region, operator_sub, justification, resumed_at, and the
// post-fix destination_jurisdiction_tag. Requires platform-admin (the
// requireAdmin gate the route is mounted behind).
//
// spec: §25.11 line 3898.
func (r *Router) handleResumeArtifactReplication(w http.ResponseWriter, req *http.Request) {
	if r.artifactReplication == nil {
		r.artifactReplicationUnconfigured(w)
		return
	}
	region := strings.TrimSpace(req.PathValue("region"))
	if region == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "region is required", nil)
		return
	}
	var body resumeArtifactReplicationRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "malformed request body", nil)
		return
	}
	if strings.TrimSpace(body.Justification) == "" {
		// §25.11 line 3898: the operator justification is recorded in the
		// audit trail. An empty justification would leave the compliance
		// resume unattributed, so it is rejected before the preflight runs.
		writeError(w, http.StatusBadRequest, "JUSTIFICATION_REQUIRED",
			"a justification is required to resume artifact replication", nil)
		return
	}
	operator := ""
	if p, ok := authmw.FromContext(req.Context()); ok {
		operator = p.Subject
	}
	if err := r.artifactReplication.Resume(req.Context(), region, operator, body.Justification); err != nil {
		writeArtifactReplicationError(w, err)
		return
	}
	// The resume cleared the suspension; return the fresh state so the
	// agent confirms the post-fix jurisdiction tag (§25.11 line 3898).
	st, ok, err := r.artifactReplication.GetState(req.Context(), region)
	if err != nil || !ok {
		writeJSON(w, http.StatusOK, replication.RegionState{Region: region, State: replication.StateActive})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleArtifactReplicationStatus serves GET
// /v1/admin/artifact-replication/{region}/status. It returns the
// region's current replication state — status, last-preflight time and
// result, destination endpoint/bucket/jurisdiction-tag, replication-lag
// seconds, and suspended-since — so agents triage residency-driven
// suspensions before the ArtifactReplicationResidencyViolation alert
// escalates. Requires platform-admin.
//
// spec: §25.11 line 3899.
func (r *Router) handleArtifactReplicationStatus(w http.ResponseWriter, req *http.Request) {
	if r.artifactReplication == nil {
		r.artifactReplicationUnconfigured(w)
		return
	}
	region := strings.TrimSpace(req.PathValue("region"))
	if region == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "region is required", nil)
		return
	}
	st, ok, err := r.artifactReplication.GetState(req.Context(), region)
	if err != nil {
		writeArtifactReplicationError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "ARTIFACT_REPLICATION_REGION_NOT_FOUND",
			"no replication state for region "+region, nil)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
