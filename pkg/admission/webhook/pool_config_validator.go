// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"

	pcv "github.com/lennylabs/lenny/pkg/admission/pool_config_validator"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// PoolConfigMetricsSink receives the §16.1 line 129
// lenny_pool_termination_budget_exceeded_total counter, labeled by pool,
// each time the webhook rejects a SandboxTemplate write on the §5.2 /
// §10.1 line 119 agent-pod grace floor (maxConcurrentSessions ×
// max_tiered_checkpoint_cap + 30 against the effective
// terminationGracePeriodSeconds). The lenny-webhook binary supplies a
// CounterVec-backed implementation; a nil sink disables emission without
// changing the admission decision.
type PoolConfigMetricsSink interface {
	IncPoolTerminationBudgetExceeded(pool string)
}

// PoolConfigValidator returns the Decider for the
// lenny-pool-config-validator ValidatingAdmissionWebhook (§4.6.3). It
// is installed fail-closed on SandboxWarmPool and SandboxTemplate
// CREATE and UPDATE and is the sole admission gate for the §4.6.2 /
// §4.6.3 semantic budget invariants of pool configuration (rule set 1
// — spec/04_system-components.md §4.6.3 line 598).
//
// The Decider dispatches on the admitted resource's Kind: a
// SandboxWarmPool is validated against the warm-count budget rules and
// a SandboxTemplate against the §5.2 execution-mode rules and the §5.2 /
// §10.1 agent-pod grace floor. A rule-set-1 violation is rejected with
// HTTP 422 and the INVALID_POOL_CONFIGURATION reason code the
// PoolScalingController keys its admission-denial backoff on.
//
// metrics, when non-nil, receives the §16.1 line 129 budget-rejection
// counter for every SandboxTemplate write rejected on the §5.2 / §10.1
// line 119 agent-pod grace floor.
//
// A decode failure or an unexpected Kind rejects, consistent with the
// webhook's fail-closed deployment: a pool configuration the webhook
// cannot inspect must not be admitted into Postgres-authoritative state
// (spec/04_system-components.md §4.6.3 line 603).
func PoolConfigValidator(metrics PoolConfigMetricsSink) Decider {
	return func(_ context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		// Rule set 1: semantic budget invariants, applied to every write
		// including the PoolScalingController's own SSA applies
		// (spec/04_system-components.md §4.6.3 line 600).
		var ruleSet1 pcv.Decision
		switch pcv.Kind(req.Kind.Kind) {
		case pcv.KindSandboxWarmPool:
			var pool lennyv1.SandboxWarmPool
			if err := json.Unmarshal(req.Object.Raw, &pool); err != nil {
				return Deny(http.StatusBadRequest, "decode SandboxWarmPool object: "+err.Error())
			}
			ruleSet1 = pcv.DecideWarmPool(&pool)
		case pcv.KindSandboxTemplate:
			var tpl lennyv1.SandboxTemplate
			if err := json.Unmarshal(req.Object.Raw, &tpl); err != nil {
				return Deny(http.StatusBadRequest, "decode SandboxTemplate object: "+err.Error())
			}
			ruleSet1 = pcv.DecideTemplate(&tpl)
			// spec: §16.1 line 129 — count the §5.2 / §10.1 line 119
			// agent-pod grace floor rejections, labeled by pool. Only the
			// SandboxTemplate carries the budget fields, so only this branch
			// can produce a BudgetExceeded decision.
			if metrics != nil && !ruleSet1.Allowed && ruleSet1.BudgetExceeded {
				metrics.IncPoolTerminationBudgetExceeded(poolLabel(tpl.GetName(), req.Name))
			}
		default:
			return Deny(http.StatusBadRequest, fmt.Sprintf(
				"pool-config-validator does not handle resource kind %q", req.Kind.Kind,
			))
		}
		if !ruleSet1.Allowed {
			return poolConfigResponse(ruleSet1)
		}

		// Rule set 2: userInfo authorization backstop, applied
		// additionally to non-PoolScalingController writers
		// (spec/04_system-components.md §4.6.3 line 601). userInfo is
		// populated by the API server from the authenticated principal and
		// cannot be spoofed by the caller. Rule-set-1 warnings (e.g. the
		// §5.2 line 516 terminationGracePeriodSeconds floor advisory) are
		// preserved on the rule-set-2 decision so they surface even when
		// rule set 2 admits.
		decision := pcv.DecideAuthorization(req.UserInfo.Username)
		if decision.Allowed && len(ruleSet1.Warnings) > 0 {
			decision.Warnings = append(decision.Warnings, ruleSet1.Warnings...)
		}
		return poolConfigResponse(decision)
	}
}

// poolConfigResponse maps a pool_config_validator Decision onto an
// AdmissionResponse. Advisory warnings are propagated onto
// AdmissionResponse.Warnings so the API server relays them to the client
// without rejecting the request (spec/05_runtime-registry-and-pool-model.md
// §5.2 line 516 — `terminationGracePeriodSeconds` floor above 600s emits
// a warning, not a rejection).
// poolLabel resolves the `pool` metric label for a rejected
// SandboxTemplate. The object's metadata.name is authoritative; on a
// CREATE with generateName the decoded object name may be empty, so the
// AdmissionRequest.Name fallback is used, and "unknown" is the last
// resort so the counter never carries an empty label.
func poolLabel(objName, reqName string) string {
	if objName != "" {
		return objName
	}
	if reqName != "" {
		return reqName
	}
	return "unknown"
}

func poolConfigResponse(decision pcv.Decision) *admissionv1.AdmissionResponse {
	if decision.Allowed {
		resp := Allow()
		if len(decision.Warnings) > 0 {
			resp.Warnings = append(resp.Warnings, decision.Warnings...)
		}
		return resp
	}
	return Deny(int32(decision.Code), decision.Reason)
}
