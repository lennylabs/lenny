// SPDX-License-Identifier: MIT

package podclaim

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/label_immutability"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// drainReasonKeptRuntimeOutsideRule is the reason the gateway logs when it
// drains a pod that served a session on a pool whose configuration keeps no
// runtime process across sessions.
const drainReasonKeptRuntimeOutsideRule = "kept_runtime_outside_rule"

// AdmitTenantPin reads the §5.2 tenant pin of the agent Pod named podName
// and reports whether an idle-pod acquisition for req may claim it. Every
// idle-pod acquisition calls it before it creates the claim: the idle scan
// in Claimer.Claim, the concurrent-slot idle pass in SlotClaimer.ClaimSlot,
// and the Postgres-backed fallback claim inside its row lock.
//
// The pin is the lenny.dev/tenant-id Pod label the gateway stamps at first
// assignment, so a pinned pod has served a session. The decision is:
//
//   - A Pod that does not exist (a NotFound read) is admitted: it carries no
//     pin and no runtime process, and the claim path tolerates a missing Pod.
//   - An unpinned Pod is admitted.
//   - When req.KeepsRuntime is false, every pinned Pod is refused, because the
//     resolved pool keeps no runtime process across sessions and a pod that
//     served a session must not serve another.
//   - Otherwise a Pod pinned to req.TenantID is admitted, and a Pod pinned to
//     any other value, including `unassigned`, is refused.
//
// Any other read error, including the expiry of ctx, is returned wrapped. It
// wraps no exhaustion sentinel, so the acquisition ends with the endpoint's
// retryable claim-failure error rather than as pool exhaustion. The function
// writes nothing and reads no SandboxClaim.
//
// spec: §5.2 (Tenant pinning: the gateway reads the pin on every idle-pod
// acquisition), §5.2 (Deployer acknowledgment (runtime process kept across
// sessions)).
func AdmitTenantPin(ctx context.Context, cl client.Client, namespace, podName string, req ClaimRequest) (bool, error) {
	var pod corev1.Pod
	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: podName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("podclaim: read tenant pin of pod %s: %w", podName, err)
	}
	pin := pod.Labels[LabelTenant]
	switch {
	case pin == "":
		return true, nil
	case !req.KeepsRuntime:
		return false, nil
	case pin == label_immutability.UnassignedTenantID:
		// The WarmPoolController's `{tenant_id} → unassigned` release; no
		// acquisition admits a pod carrying it.
		return false, nil
	default:
		return pin == req.TenantID, nil
	}
}

// DrainRefusedPod stamps the lenny.dev/drain-request annotation on a pinned
// idle pod that an acquisition refused because the resolved pool keeps no
// runtime process across sessions. The stamp is written only when no
// SandboxClaim holds the pod: the gateway stamps the pin after it creates
// the claim, so a pinned idle pod with a claim is an acquisition in progress
// on another replica, and its session keeps the pod.
//
// The claim read is a direct Get of claim-<podName>. A NotFound read stamps
// the drain request through StampDrainRequest and logs the stamp at Info.
// An existing claim writes nothing. A claim read error or a failed stamp is
// logged at Warn and otherwise ignored: the acquisition goes on to its next
// candidate, the pod stays refused, and the next acquisition that refuses it
// stamps it again. The function returns no value for that reason.
//
// spec: §5.2 (Deployer acknowledgment (runtime process kept across
// sessions): an acquisition that refuses a pinned idle pod stamps its drain
// request when no SandboxClaim holds it; a failed stamp or claim read does
// not fail the acquisition).
func DrainRefusedPod(ctx context.Context, cl client.Client, namespace, pool, podName string, now time.Time) {
	attrs := []any{
		slog.String("pool", pool),
		slog.String("pod_id", podName),
		slog.String("reason", drainReasonKeptRuntimeOutsideRule),
	}
	var claim lennyv1.SandboxClaim
	err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ClaimName(podName)}, &claim)
	switch {
	case err == nil:
		// A claim holds the pod: an acquisition is in progress elsewhere.
		return
	case !apierrors.IsNotFound(err):
		slog.WarnContext(ctx, "podclaim: read claim of refused pod failed; drain request not stamped",
			append(attrs, slog.Any("error", err))...)
		return
	}
	if err := StampDrainRequest(ctx, cl, namespace, podName, now); err != nil {
		slog.WarnContext(ctx, "podclaim: stamp drain request on refused pod failed",
			append(attrs, slog.Any("error", err))...)
		return
	}
	slog.InfoContext(ctx, "podclaim: drain requested for refused pod", attrs...)
}
