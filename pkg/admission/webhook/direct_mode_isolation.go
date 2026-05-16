// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"

	dmi "github.com/lennylabs/lenny/pkg/admission/direct_mode_isolation"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

// DirectModeIsolation returns the Decider for the
// lenny-direct-mode-isolation ValidatingAdmissionWebhook (§4.9, §13.2).
// It is installed on SandboxTemplate resources in agent namespaces and
// rejects, fail-closed, the credential-delivery configurations that
// expose cross-tenant credential risk in a multi-tenant deployment:
// deliveryMode direct with isolationProfile standard, and deliveryMode
// proxy with spiffeBinding disabled.
//
// tenancyMode is the platform tenancy.mode setting and devMode is
// global.devMode. Together they determine whether the webhook enforces
// (multi-tenant, non-development) or admits every resource (single-tenant
// or development), per §4.9.
func DirectModeIsolation(tenancyMode string, devMode bool) Decider {
	return func(_ context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		var tmpl lennyv1.SandboxTemplate
		if err := json.Unmarshal(req.Object.Raw, &tmpl); err != nil {
			return Deny(http.StatusBadRequest, "decode SandboxTemplate object: "+err.Error())
		}
		decision := dmi.Decide(dmi.Request{
			TenancyMode:      tenancyMode,
			DevMode:          devMode,
			Kind:             req.Kind.Kind,
			DeliveryMode:     tmpl.Spec.DeliveryMode,
			IsolationProfile: tmpl.Spec.IsolationProfile,
			SpiffeBinding:    tmpl.Spec.SpiffeBinding,
		})
		if decision.Allowed {
			return Allow()
		}
		return Deny(int32(decision.Code), decision.Reason)
	}
}
