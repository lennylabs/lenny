// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"

	pcv "github.com/lennylabs/lenny/pkg/admission/pool_config_validator"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

// PoolConfigValidator returns the Decider for the
// lenny-pool-config-validator ValidatingAdmissionWebhook (§4.6.3). It
// is installed fail-closed on SandboxWarmPool and SandboxTemplate
// CREATE and UPDATE and is the sole admission gate for the §4.6.2 /
// §4.6.3 semantic budget invariants of pool configuration (rule set 1
// — spec/04_system-components.md §4.6.3 line 598).
//
// The Decider dispatches on the admitted resource's Kind: a
// SandboxWarmPool is validated against the warm-count budget rules and
// a SandboxTemplate against the §5.2 execution-mode rules. A rule-set-1
// violation is rejected with HTTP 422 and the INVALID_POOL_CONFIGURATION
// reason code the PoolScalingController keys its admission-denial
// backoff on.
//
// A decode failure or an unexpected Kind rejects, consistent with the
// webhook's fail-closed deployment: a pool configuration the webhook
// cannot inspect must not be admitted into Postgres-authoritative state
// (spec/04_system-components.md §4.6.3 line 603).
func PoolConfigValidator() Decider {
	return func(_ context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		switch pcv.Kind(req.Kind.Kind) {
		case pcv.KindSandboxWarmPool:
			var pool lennyv1.SandboxWarmPool
			if err := json.Unmarshal(req.Object.Raw, &pool); err != nil {
				return Deny(http.StatusBadRequest, "decode SandboxWarmPool object: "+err.Error())
			}
			return poolConfigResponse(pcv.DecideWarmPool(&pool))
		case pcv.KindSandboxTemplate:
			var tpl lennyv1.SandboxTemplate
			if err := json.Unmarshal(req.Object.Raw, &tpl); err != nil {
				return Deny(http.StatusBadRequest, "decode SandboxTemplate object: "+err.Error())
			}
			return poolConfigResponse(pcv.DecideTemplate(&tpl))
		default:
			return Deny(http.StatusBadRequest, fmt.Sprintf(
				"pool-config-validator does not handle resource kind %q", req.Kind.Kind,
			))
		}
	}
}

// poolConfigResponse maps a pool_config_validator Decision onto an
// AdmissionResponse.
func poolConfigResponse(decision pcv.Decision) *admissionv1.AdmissionResponse {
	if decision.Allowed {
		return Allow()
	}
	return Deny(int32(decision.Code), decision.Reason)
}
