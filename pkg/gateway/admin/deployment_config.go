// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/deploymentconfigstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	corr "github.com/lennylabs/lenny/pkg/observability/correlation"
)

// platformAuditTenant is the §11.7 platform tenant the deployment-scope
// audit events are written under. The floor-clamp fanout writes per-tenant
// rows under each target tenant instead.
const platformAuditTenant = "platform"

// DeploymentConfigChangeRequest is the body of
// POST /v1/admin/deployment/config-change. The chart's post-upgrade hook
// submits the rendered deployment-scope Helm configuration plus the
// operator-supplied justifications and feature-flag downgrade
// acknowledgements for this release. The gateway diffs it against the
// persisted baseline and emits the §16.7 deployment-transition audit
// events under the authenticated operator's identity. spec: §16.7 lines
// 672, 676, 677, 682; §17.2 line 86. F-8.2.5, F-9.2.10, F-17.2.8.
type DeploymentConfigChangeRequest struct {
	// ReleaseRevision is the Helm .Release.Revision of this upgrade. A
	// revision at or below the persisted baseline is treated as an
	// idempotent replay (a retried hook) and emits nothing.
	ReleaseRevision int64 `json:"releaseRevision"`
	// CycleDetection carries the §8.2 gateway.cycleDetection.mode value and
	// the justification required when transitioning into warn/permissive.
	CycleDetection struct {
		Mode          string `json:"mode"`
		Justification string `json:"justification"`
	} `json:"cycleDetection"`
	// AllowSelfRecursion carries the §8.2 gateway.allowSelfRecursion master
	// gate (yes | no) and the justification required for a no→yes raise.
	AllowSelfRecursion struct {
		Value         string `json:"value"`
		Justification string `json:"justification"`
	} `json:"allowSelfRecursion"`
	// DefaultMaxDepth is the §8.2 step-5 gateway.delegation.defaultMaxDepth
	// Helm fallback. 0 is treated as unset (no event).
	DefaultMaxDepth int `json:"defaultMaxDepth"`
	// ElicitationFloor is the §9.2 / §17.2 rendered
	// security.elicitationContentIntegrity.floor (off | detect-only | enforce).
	ElicitationFloor string `json:"elicitationFloor"`
	// AcknowledgedDowngrades lists the per-(flag, webhook) feature-flag
	// downgrade acknowledgements set for this release via
	// acceptFeatureFlagDowngrade.<flag>=true.
	AcknowledgedDowngrades []DowngradeAck `json:"acknowledgedDowngrades"`
}

// DowngradeAck is one §17.2 acknowledged admission-plane feature-flag
// downgrade. The chart emits one per (flag, expected_webhook) pair.
type DowngradeAck struct {
	Flag            string `json:"flag"`
	ExpectedWebhook string `json:"expectedWebhook"`
	Justification   string `json:"justification"`
}

// DeploymentConfigChangeResponse reports what the reconciliation did. A
// replay returns applied=false with an empty event list.
type DeploymentConfigChangeResponse struct {
	Applied       bool           `json:"applied"`
	Revision      int64          `json:"revision"`
	EmittedEvents []EmittedAudit `json:"emittedEvents"`
}

// EmittedAudit identifies one audit row the reconciliation wrote.
type EmittedAudit struct {
	Type    string `json:"type"`
	EventID string `json:"eventId"`
	Tenant  string `json:"tenant,omitempty"`
}

var validCycleModes = map[string]bool{"enforce": true, "warn": true, "permissive": true}

// handleDeploymentConfigChange implements
// POST /v1/admin/deployment/config-change. It is the §16.7 platform-tenant
// deployment-transition audit emitter: it sources `changed_by_sub` /
// `acknowledged_by_sub` from the authenticated operator (the post-upgrade
// hook runs with the operator's platform-admin token), reads the persisted
// baseline, and emits one audit event per Helm-driven transition. The
// persisted baseline survives a gateway restart, so a transition is audited
// once per upgrade rather than re-emitted on every replica reconcile —
// which is exactly why a gateway-runtime ConfigMap watch (F-17.2.9) could
// not satisfy these events. spec: §16.7 lines 672, 676, 677, 682.
func (r *Router) handleDeploymentConfigChange(w http.ResponseWriter, req *http.Request) {
	if r.auditLog == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"deployment-config audit emitter reached without an audit log", nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	var body DeploymentConfigChangeRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if body.ReleaseRevision < 1 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"releaseRevision must be a positive Helm release revision",
			map[string]any{"field": "releaseRevision"})
		return
	}
	// Validate the closed enums up front so a malformed render is rejected
	// rather than recorded as a baseline.
	if body.CycleDetection.Mode != "" && !validCycleModes[body.CycleDetection.Mode] {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"cycleDetection.mode must be one of enforce, warn, or permissive",
			map[string]any{"field": "cycleDetection.mode"})
		return
	}
	if body.AllowSelfRecursion.Value != "" &&
		body.AllowSelfRecursion.Value != "yes" && body.AllowSelfRecursion.Value != "no" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"allowSelfRecursion.value must be yes or no",
			map[string]any{"field": "allowSelfRecursion.value"})
		return
	}
	if body.ElicitationFloor != "" && !elicitation.EnforcementMode(body.ElicitationFloor).IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"elicitationFloor must be one of off, detect-only, or enforce",
			map[string]any{"field": "elicitationFloor"})
		return
	}

	baseline, found, err := r.deploymentConfig.Get(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// Replay guard: a retried post-upgrade hook (same or lower release
	// revision) is idempotent. The first ever call has found=false.
	if found && body.ReleaseRevision <= baseline.LastRevision {
		writeJSON(w, http.StatusOK, DeploymentConfigChangeResponse{
			Applied: false, Revision: baseline.LastRevision, EmittedEvents: []EmittedAudit{},
		})
		return
	}

	// Justification gating mirrors the chart's render-time required-field
	// discipline, validated here because this endpoint is the emitter of
	// record. A justification is required only when the corresponding
	// security-relaxing transition will actually be emitted.
	cycleChanged := body.CycleDetection.Mode != "" && body.CycleDetection.Mode != baseline.CycleDetectionMode
	if cycleChanged && (body.CycleDetection.Mode == "warn" || body.CycleDetection.Mode == "permissive") &&
		body.CycleDetection.Justification == "" {
		writeError(w, http.StatusBadRequest, "CYCLE_DETECTION_MODE_JUSTIFICATION_REQUIRED",
			"justification is required when cycleDetection.mode is warn or permissive",
			map[string]any{"field": "cycleDetection.justification"})
		return
	}
	selfRecursionChanged := body.AllowSelfRecursion.Value != "" && body.AllowSelfRecursion.Value != baseline.AllowSelfRecursion
	if selfRecursionChanged && baseline.AllowSelfRecursion == "no" && body.AllowSelfRecursion.Value == "yes" &&
		body.AllowSelfRecursion.Justification == "" {
		writeError(w, http.StatusBadRequest, "ALLOW_SELF_RECURSION_JUSTIFICATION_REQUIRED",
			"justification is required when allowSelfRecursion transitions from no to yes",
			map[string]any{"field": "allowSelfRecursion.justification"})
		return
	}
	for i, ack := range body.AcknowledgedDowngrades {
		if ack.Flag == "" || ack.ExpectedWebhook == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"each acknowledgedDowngrades entry requires flag and expectedWebhook",
				map[string]any{"field": "acknowledgedDowngrades", "index": i})
			return
		}
		if ack.Justification == "" {
			writeError(w, http.StatusBadRequest, "PHASE_STAMP_FEATURE_FLAG_DOWNGRADE_JUSTIFICATION_REQUIRED",
				"justification is required for an acknowledged feature-flag downgrade",
				map[string]any{"field": "acknowledgedDowngrades", "index": i, "flag": ack.Flag})
			return
		}
	}

	emitted := []EmittedAudit{}
	at := r.clock()
	sub := principal.Subject

	// §8.2 cycle-detection mode transition.
	if cycleChanged {
		row, e := r.appendDeploymentAudit(req.Context(), platformAuditTenant, sub,
			"gateway.cycle_detection_mode_changed", "platform",
			map[string]any{
				"previous_mode":        nullableString(baseline.CycleDetectionMode, found),
				"new_mode":             body.CycleDetection.Mode,
				"changed_by_sub":       sub,
				"changed_by_tenant_id": platformAuditTenant,
				"justification":        body.CycleDetection.Justification,
				"changed_at":           at.UTC().Format(time.RFC3339Nano),
			}, at)
		if e != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", e.Error(), nil)
			return
		}
		emitted = append(emitted, EmittedAudit{Type: "gateway.cycle_detection_mode_changed", EventID: row.ID, Tenant: platformAuditTenant})
	}

	// §8.2 self-recursion master-gate transition.
	if selfRecursionChanged {
		row, e := r.appendDeploymentAudit(req.Context(), platformAuditTenant, sub,
			"gateway.allow_self_recursion_changed", "platform",
			map[string]any{
				"previous_value":       nullableString(baseline.AllowSelfRecursion, found),
				"new_value":            body.AllowSelfRecursion.Value,
				"changed_by_sub":       sub,
				"changed_by_tenant_id": platformAuditTenant,
				"justification":        body.AllowSelfRecursion.Justification,
				"changed_at":           at.UTC().Format(time.RFC3339Nano),
			}, at)
		if e != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", e.Error(), nil)
			return
		}
		emitted = append(emitted, EmittedAudit{Type: "gateway.allow_self_recursion_changed", EventID: row.ID, Tenant: platformAuditTenant})
	}

	// §8.2 step-5 default-maxDepth transition.
	if body.DefaultMaxDepth != 0 && body.DefaultMaxDepth != baseline.DefaultMaxDepth {
		row, e := r.appendDeploymentAudit(req.Context(), platformAuditTenant, sub,
			"gateway.default_max_depth_changed", "platform",
			map[string]any{
				"previous_value":       nullableInt(baseline.DefaultMaxDepth, found),
				"new_value":            body.DefaultMaxDepth,
				"changed_by_sub":       sub,
				"changed_by_tenant_id": platformAuditTenant,
				"changed_at":           at.UTC().Format(time.RFC3339Nano),
			}, at)
		if e != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", e.Error(), nil)
			return
		}
		emitted = append(emitted, EmittedAudit{Type: "gateway.default_max_depth_changed", EventID: row.ID, Tenant: platformAuditTenant})
	}

	// §9.2 / §17.2 elicitation-content-integrity floor transition + per-tenant clamp fanout.
	if body.ElicitationFloor != "" && body.ElicitationFloor != baseline.ElicitationFloor {
		clampTargets, e := r.computeFloorClamps(req.Context(), baseline.ElicitationFloor, body.ElicitationFloor)
		if e != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", e.Error(), nil)
			return
		}
		row, e := r.appendDeploymentAudit(req.Context(), platformAuditTenant, sub,
			"platform.elicitation_content_integrity_floor_changed", "platform",
			map[string]any{
				"previous_floor":         nullableString(baseline.ElicitationFloor, found),
				"new_floor":              body.ElicitationFloor,
				"changed_by_sub":         sub,
				"changed_at":             at.UTC().Format(time.RFC3339Nano),
				"tenants_affected_count": len(clampTargets),
			}, at)
		if e != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", e.Error(), nil)
			return
		}
		emitted = append(emitted, EmittedAudit{Type: "platform.elicitation_content_integrity_floor_changed", EventID: row.ID, Tenant: platformAuditTenant})
		pairedID := row.ID
		for _, ct := range clampTargets {
			// Written under the target tenant so the record is co-located
			// with the tenant it governs (§16.7 line 677).
			crow, ce := r.appendDeploymentAudit(req.Context(), ct.tenantID, sub,
				"tenant.elicitation_content_integrity_floor_clamp", ct.tenantID,
				map[string]any{
					"tenant_id":                ct.tenantID,
					"stored_mode":              ct.stored,
					"previous_effective_mode":  ct.prevEffective,
					"new_effective_mode":       ct.newEffective,
					"new_platform_floor":       body.ElicitationFloor,
					"paired_platform_event_id": pairedID,
					"clamped_at":               at.UTC().Format(time.RFC3339Nano),
				}, at)
			if ce != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", ce.Error(), nil)
				return
			}
			emitted = append(emitted, EmittedAudit{Type: "tenant.elicitation_content_integrity_floor_clamp", EventID: crow.ID, Tenant: ct.tenantID})
		}
	}

	// §17.2 acknowledged feature-flag downgrades — one event per (flag, webhook).
	for _, ack := range body.AcknowledgedDowngrades {
		row, e := r.appendDeploymentAudit(req.Context(), platformAuditTenant, sub,
			"deployment.feature_flag_downgrade_acknowledged", "platform",
			map[string]any{
				"flag_name":                 ack.Flag,
				"expected_webhook_name":     ack.ExpectedWebhook,
				"acknowledged_by_sub":       sub,
				"acknowledged_by_tenant_id": platformAuditTenant,
				"justification":             ack.Justification,
				"acknowledged_at":           at.UTC().Format(time.RFC3339Nano),
			}, at)
		if e != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", e.Error(), nil)
			return
		}
		emitted = append(emitted, EmittedAudit{Type: "deployment.feature_flag_downgrade_acknowledged", EventID: row.ID, Tenant: platformAuditTenant})
	}

	// Persist the new baseline. Empty rendered fields fall back to the prior
	// value so a partial submission never erases a recorded baseline.
	next := baseline
	if body.CycleDetection.Mode != "" {
		next.CycleDetectionMode = body.CycleDetection.Mode
	}
	if body.AllowSelfRecursion.Value != "" {
		next.AllowSelfRecursion = body.AllowSelfRecursion.Value
	}
	if body.DefaultMaxDepth != 0 {
		next.DefaultMaxDepth = body.DefaultMaxDepth
	}
	if body.ElicitationFloor != "" {
		next.ElicitationFloor = body.ElicitationFloor
	}
	next.LastRevision = body.ReleaseRevision
	if e := r.deploymentConfig.Put(req.Context(), next); e != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", e.Error(), nil)
		return
	}

	writeJSON(w, http.StatusOK, DeploymentConfigChangeResponse{
		Applied: true, Revision: body.ReleaseRevision, EmittedEvents: emitted,
	})
}

// floorClampTarget is one tenant whose effective elicitation
// content-integrity mode is raised by a platform floor change.
type floorClampTarget struct {
	tenantID      string
	stored        string
	prevEffective string
	newEffective  string
}

// computeFloorClamps lists active tenants and returns those whose effective
// mode `max(floor, stored)` is strictly stricter under newFloor than under
// oldFloor. A floor lowering returns no targets (stored modes are preserved
// and the stricter tenant value continues to dominate). spec: §16.7 line 676.
func (r *Router) computeFloorClamps(ctx context.Context, oldFloor, newFloor string) ([]floorClampTarget, error) {
	tenants, err := r.tenants.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	var targets []floorClampTarget
	for _, t := range tenants {
		if !t.AcceptsNewWork() {
			continue
		}
		stored := effectiveStoredMode(t)
		prevEff := elicitation.ResolveEffectiveWithDefaults(oldFloor, stored)
		newEff := elicitation.ResolveEffectiveWithDefaults(newFloor, stored)
		if newEff.Rank() > prevEff.Rank() {
			targets = append(targets, floorClampTarget{
				tenantID:      t.ID,
				stored:        stored,
				prevEffective: string(prevEff),
				newEffective:  string(newEff),
			})
		}
	}
	return targets, nil
}

// appendDeploymentAudit writes one deployment-transition audit row to the
// chainTenant's §11.7 hash chain and returns the committed row (its ID is
// the floor-change `paired_platform_event_id`). It mirrors the field
// wrapping ChainAuditSink.EmitAdminEvent applies so audit-query consumers
// read the spec payload under `detail`; the platform-admin gate on the
// route authorizes writing both the platform chain and the per-tenant
// clamp chains. spec: §11.7; §16.7.
func (r *Router) appendDeploymentAudit(ctx context.Context, chainTenant, actorSub, eventType, resource string, detail map[string]any, at time.Time) (audit.Row, error) {
	fields := map[string]any{
		"actor_subject":   actorSub,
		"actor_tenant_id": platformAuditTenant,
		"target_resource": resource,
		"detail":          detail,
	}
	if op := corr.From(ctx).OperationID; op != "" {
		fields["operation_id"] = op
	}
	if an := corr.From(ctx).AgentName; an != "" {
		fields["agent_name"] = an
	}
	if p, ok := authmw.FromContext(ctx); ok && p.CallerType != "" {
		fields["caller_kind"] = p.CallerType
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return audit.Row{}, err
	}
	return r.auditLog.Append(ctx, chainTenant, eventType, payload, at)
}

// nullableString returns s when a prior baseline existed, else nil — the
// §16.7 "null on first install" previous-value contract.
func nullableString(s string, found bool) any {
	if !found || s == "" {
		return nil
	}
	return s
}

// nullableInt returns n when a prior baseline existed and n != 0, else nil.
func nullableInt(n int, found bool) any {
	if !found || n == 0 {
		return nil
	}
	return n
}

// WithDeploymentConfig wires the §16.7 deployment-transition audit emitter
// onto the Router. A nil store leaves POST /v1/admin/deployment/config-change
// unregistered. spec: §16.7 lines 672, 676, 677, 682. F-8.2.5, F-9.2.10, F-17.2.8.
func (r *Router) WithDeploymentConfig(store deploymentconfigstore.Store) *Router {
	r.deploymentConfig = store
	return r
}
