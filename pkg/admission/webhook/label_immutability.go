// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	labelimm "github.com/lennylabs/lenny/pkg/admission/label_immutability"
)

// LabelImmutability returns the Decider for the §17.2 item 5
// lenny-label-immutability ValidatingAdmissionWebhook. It enforces the
// strictly-immutable agent-pod label set (`lenny.dev/managed`,
// `lenny.dev/delivery-mode`, `lenny.dev/egress-profile`). The
// companion `TenantLabelImmutability` decider handles the §5.2 line
// 392 / NET-003 `lenny.dev/tenant-id` transition rules — they were
// historically folded into one webhook but the spec names two distinct
// ValidatingWebhookConfigurations.
//
// spec: §17.2 item 5; §13.2.
func LabelImmutability() Decider {
	return immutabilityDecider(labelimm.DecideImmutableLabels)
}

// TenantLabelImmutability returns the Decider for the §5.2 line 392 /
// NET-003 lenny-tenant-label-immutability ValidatingAdmissionWebhook.
// It enforces the gateway-only initial-assignment edge and the
// WarmPoolController-only return-to-pool edge for `lenny.dev/tenant-id`.
//
// spec: §5.2 line 392; §13.2 line 498.
func TenantLabelImmutability() Decider {
	return immutabilityDecider(labelimm.DecideTenantTransition)
}

// immutabilityDecider is the shared adapter: extract the inbound and
// prior pod label maps and the authenticated username, then call the
// supplied decision function. Both webhook configs use the same pod
// schema and the same Request shape, so the extraction is common.
func immutabilityDecider(decide func(labelimm.Request) (labelimm.Decision, error)) Decider {
	return func(_ context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		newLabels, err := podLabels(req.Object.Raw)
		if err != nil {
			return Deny(http.StatusBadRequest, "decode pod object: "+err.Error())
		}
		var oldLabels map[string]string
		if len(req.OldObject.Raw) > 0 {
			oldLabels, err = podLabels(req.OldObject.Raw)
			if err != nil {
				return Deny(http.StatusBadRequest, "decode prior pod object: "+err.Error())
			}
		}
		decision, err := decide(labelimm.Request{
			OldLabels:        oldLabels,
			NewLabels:        newLabels,
			UserInfoUsername: req.UserInfo.Username,
		})
		if err != nil {
			return Deny(http.StatusBadRequest, err.Error())
		}
		if decision.Allowed {
			return Allow()
		}
		return Deny(int32(decision.Code), decision.Reason)
	}
}

// podLabels unmarshals a raw pod object and returns its label map. A
// successfully-parsed pod with no labels yields a non-nil empty map so
// label_immutability.Decide treats it as a real (labelless) write
// rather than a missing-input error.
func podLabels(raw []byte) (map[string]string, error) {
	var pod corev1.Pod
	if err := json.Unmarshal(raw, &pod); err != nil {
		return nil, err
	}
	if pod.Labels == nil {
		return map[string]string{}, nil
	}
	return pod.Labels, nil
}
