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
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// drainReasonKeptRuntimeOutsideRule is the reason the gateway logs when it
// drains a pod that served a session and that an acquisition refused under
// the runtime-process acknowledgment: the resolved pool does not reuse pods,
// or the pod is a `keep` pod and the resolved pool does not require the
// acknowledgment. A pod refused only for its tenant is never drained.
const drainReasonKeptRuntimeOutsideRule = "kept_runtime_outside_rule"

// LabelRuntimeProcess is the immutable pod label in which the
// WarmPoolController records the pod's runtime-process lifetime (`keep` or
// `restart`) when it creates the pod. spec: §5.2 (Runtime-process
// lifetime), §17.2 item 5.
const LabelRuntimeProcess = state.LabelRuntimeProcess

// refusesUsedPodLifetime reports whether an acquisition for req refuses a
// pod that has served an earlier session because of the pod's
// runtime-process lifetime: a pod whose lenny.dev/runtime-process label is
// not `restart` is a `keep` pod (an absent or unknown value reads as
// `keep`), and a `keep` pod is admitted only while the resolved
// configuration requires the runtime-process acknowledgment. A `restart`
// pod is never refused on its lifetime. spec: §5.2 (Runtime-process
// lifetime; Deployer acknowledgment (runtime process kept across
// sessions)).
func refusesUsedPodLifetime(pod *corev1.Pod, req ClaimRequest) bool {
	if req.RequiresProcessAcknowledgment {
		return false
	}
	return pod.Labels[LabelRuntimeProcess] != string(runtimestore.RuntimeProcessRestart)
}

// AdmitTenantPin reads the §5.2 tenant pin of the agent Pod named podName
// and reports whether an idle-pod acquisition for req may claim it, and
// whether a refused pod must drain. Every idle-pod acquisition calls it
// before it creates the claim: the idle scan in Claimer.Claim, the
// concurrent-slot idle pass in SlotClaimer.ClaimSlot, and the
// Postgres-backed fallback claim inside its row lock. Only the idle scan
// acts on the drain report; the slot pass and the fallback claim ignore it.
//
// The pin is the lenny.dev/tenant-id Pod label the gateway stamps at first
// assignment, so a pinned pod has served a session. The decision is:
//
//   - A Pod that does not exist (a NotFound read) is admitted: it carries no
//     pin and no runtime process, and the claim path tolerates a missing Pod.
//   - An unpinned Pod is admitted.
//   - When req.ReusesPods is false, every pinned Pod is refused and drains,
//     because the resolved pool reuses no pod and a pod that served a session
//     must not serve another.
//   - A pinned `keep` pod (refusesUsedPodLifetime) is refused and drains
//     when req.RequiresProcessAcknowledgment is false, whatever its tenant:
//     an acquisition refuses the pod under the runtime-process
//     acknowledgment.
//   - Otherwise a Pod pinned to req.TenantID is admitted, and a Pod pinned to
//     any other value, including `unassigned`, is refused and does not drain:
//     a pod refused only for its tenant stays pinned for that tenant.
//
// Any other read error, including the expiry of ctx, is returned wrapped. It
// wraps no exhaustion sentinel, so the acquisition ends with the endpoint's
// retryable claim-failure error rather than as pool exhaustion. The function
// writes nothing and reads no SandboxClaim.
//
// spec: §5.2 (Tenant pinning: the gateway reads the pin on every idle-pod
// acquisition), §5.2 (Runtime-process lifetime), §5.2 (Deployer
// acknowledgment (runtime process kept across sessions)).
func AdmitTenantPin(ctx context.Context, cl client.Client, namespace, podName string, req ClaimRequest) (admitted, drain bool, err error) {
	var pod corev1.Pod
	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: podName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return true, false, nil
		}
		return false, false, fmt.Errorf("podclaim: read tenant pin of pod %s: %w", podName, err)
	}
	pin := pod.Labels[LabelTenant]
	switch {
	case pin == "":
		return true, false, nil
	case !req.ReusesPods:
		return false, true, nil
	case refusesUsedPodLifetime(&pod, req):
		return false, true, nil
	case pin == label_immutability.UnassignedTenantID:
		// The WarmPoolController's `{tenant_id} → unassigned` release; no
		// acquisition admits a pod carrying it.
		return false, false, nil
	default:
		return pin == req.TenantID, false, nil
	}
}

// DrainRefusedPod stamps the lenny.dev/drain-request annotation on a pinned
// idle pod that an acquisition refused under the runtime-process
// acknowledgment (AdmitTenantPin's drain report): the resolved pool reuses no
// pod, or the pod is a `keep` pod and the resolved pool does not require the
// acknowledgment. A pod refused only for its tenant does not drain. The stamp is written only when no
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
