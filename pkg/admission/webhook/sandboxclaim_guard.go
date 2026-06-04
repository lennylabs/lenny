// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	guard "github.com/lennylabs/lenny/pkg/admission/sandboxclaim_guard"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// SandboxClaimGuard returns the Decider for the lenny-sandboxclaim-guard
// ValidatingAdmissionWebhook (§4.6.1, ADR-007). The §4.6.1 rules read
// live cluster state — the referenced Sandbox's phase and any sibling
// claims — so the Decider holds a client.Reader and queries the API
// server during admission. A query failure is surfaced as a rejection,
// consistent with the webhook's fail-closed deployment.
func SandboxClaimGuard(reader client.Reader) Decider {
	return func(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		var claim lennyv1.SandboxClaim
		if err := json.Unmarshal(req.Object.Raw, &claim); err != nil {
			return Deny(http.StatusBadRequest, "decode SandboxClaim object: "+err.Error())
		}

		guardReq := guard.Request{
			ClaimName:  claim.Name,
			SandboxRef: claim.Spec.SandboxRef,
			HasSlotID:  claim.Spec.SlotID != "",
		}

		switch req.Operation {
		case admissionv1.Create:
			guardReq.Operation = guard.OpCreate
			existing, err := siblingClaims(ctx, reader, req.Namespace, claim.Spec.SandboxRef, claim.Name)
			if err != nil {
				return Deny(http.StatusInternalServerError, "list sibling SandboxClaims: "+err.Error())
			}
			guardReq.ExistingClaims = existing
			// §5.2 concurrent-mode dispatch opens multiple non-terminal
			// claims against the same Sandbox; the guard distinguishes
			// that case from session-mode duplicates by two signals:
			// the inbound claim's own .spec.slotId (authoritative for
			// the first slot, before the Sandbox.status.phase mirror
			// patch has landed), and the Sandbox's phase
			// (slot_active vs idle/claimed) for subsequent slots.
			// Read the phase on CREATE too so Decide can apply the
			// §5.2 exemption on either signal.
			phase, err := referencedSandboxPhase(ctx, reader, req.Namespace, claim.Spec.SandboxRef)
			if err != nil {
				return Deny(http.StatusInternalServerError, "read referenced Sandbox: "+err.Error())
			}
			guardReq.SandboxPhase = phase
		case admissionv1.Update:
			guardReq.Operation = guard.OpPut
			guardReq.UnderDeletion = !claim.DeletionTimestamp.IsZero()
			// A claim under deletion is exempt from the staleness rule,
			// so the referenced Sandbox's phase is not read: the lookup
			// is irrelevant and would otherwise fail the teardown write
			// when the Sandbox is already gone.
			if !guardReq.UnderDeletion {
				phase, err := referencedSandboxPhase(ctx, reader, req.Namespace, claim.Spec.SandboxRef)
				if err != nil {
					return Deny(http.StatusInternalServerError, "read referenced Sandbox: "+err.Error())
				}
				guardReq.SandboxPhase = phase
			}
		default:
			return Deny(http.StatusBadRequest, fmt.Sprintf("sandboxclaim-guard does not handle operation %q", req.Operation))
		}

		decision, err := guard.Decide(guardReq)
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

// referencedSandboxPhase reads the status.phase of the Sandbox the
// claim targets. A missing Sandbox yields an empty phase, which the
// guard treats as a stale write and rejects.
func referencedSandboxPhase(ctx context.Context, reader client.Reader, ns, sandboxRef string) (guard.SandboxPhase, error) {
	var sb lennyv1.Sandbox
	err := reader.Get(ctx, client.ObjectKey{Namespace: ns, Name: sandboxRef}, &sb)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return guard.SandboxPhase(sb.Status.Phase), nil
}
