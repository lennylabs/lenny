// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	guard "github.com/lennylabs/lenny/pkg/admission/sandboxclaim_guard"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// SandboxClaimGuard returns the Decider for the lenny-sandboxclaim-guard
// ValidatingAdmissionWebhook (§4.6.1, ADR-007). The webhook intercepts
// CREATE only; PATCH and PUT are admitted without inspection and are not
// registered with the webhook. The CREATE rule reads live cluster state
// — the set of sibling SandboxClaims for the same Sandbox — so the
// Decider holds a client.Reader and queries the API server during
// admission. The guard reads no Sandbox.status.phase. A query failure is
// surfaced as a rejection, consistent with the webhook's fail-closed
// deployment.
func SandboxClaimGuard(reader client.Reader) Decider {
	return func(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		if req.Operation != admissionv1.Create {
			return Deny(http.StatusBadRequest, fmt.Sprintf("sandboxclaim-guard does not handle operation %q", req.Operation))
		}

		var claim lennyv1.SandboxClaim
		if err := json.Unmarshal(req.Object.Raw, &claim); err != nil {
			return Deny(http.StatusBadRequest, "decode SandboxClaim object: "+err.Error())
		}

		existing, err := siblingClaims(ctx, reader, req.Namespace, claim.Spec.SandboxRef, claim.Name)
		if err != nil {
			return Deny(http.StatusInternalServerError, "list sibling SandboxClaims: "+err.Error())
		}

		decision, err := guard.Decide(guard.Request{
			Operation:      guard.OpCreate,
			ClaimName:      claim.Name,
			SandboxRef:     claim.Spec.SandboxRef,
			ExistingClaims: existing,
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

// siblingClaims lists the SandboxClaims in ns whose spec.sandboxRef
// matches sandboxRef, excluding the inbound claim itself by name.
func siblingClaims(ctx context.Context, reader client.Reader, ns, sandboxRef, self string) ([]guard.ExistingClaim, error) {
	var list lennyv1.SandboxClaimList
	if err := reader.List(ctx, &list, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	var out []guard.ExistingClaim
	for i := range list.Items {
		c := &list.Items[i]
		if c.Name == self || c.Spec.SandboxRef != sandboxRef {
			continue
		}
		out = append(out, guard.ExistingClaim{
			Name:   c.Name,
			Status: guard.ClaimStatus(c.Status.Phase),
		})
	}
	return out, nil
}
