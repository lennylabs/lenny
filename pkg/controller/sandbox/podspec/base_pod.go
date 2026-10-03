// SPDX-License-Identifier: MIT

package podspec

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

const (
	// ReadinessGateSandboxReady is the §6.1 pod readiness gate
	// ("Marked 'idle and claimable' via readiness gate"). The pod spec
	// declares the gate so the kubelet holds Pod.Ready False — and the pod
	// un-claimable — until the WarmPoolController asserts the pod is warm.
	// Container readiness alone does not make a pod claimable; the
	// controller flips this gate to True (see the Sandbox-to-Pod
	// reconciler) once it has observed the containers ready, which is the
	// Lenny-controlled claimability handoff to the gateway.
	ReadinessGateSandboxReady corev1.PodConditionType = "lenny.dev/sandbox-ready"
)

// basePod returns the Pod skeleton common to both deployment models:
// the §13.1 pod security context and the §5.3 RuntimeClass, with the
// container list and volume list left for the caller to fill.
func basePod(in Inputs, runtimeClass string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      in.Name,
			Namespace: in.Namespace,
			Labels:    in.Labels,
		},
		Spec: corev1.PodSpec{
			RuntimeClassName:              &runtimeClass,
			RestartPolicy:                 corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: ptr.To(terminationGrace(in)),
			// spec: §5.2 — stamp the resolved topology spread
			// constraints so the scheduler distributes the pool's pods.
			TopologySpreadConstraints: in.TopologySpreadConstraints,
			// spec: §6.1 — the readiness gate lets the
			// WarmPoolController gate claimability: Pod.Ready stays False
			// (and the warming → idle transition, which keys off Pod.Ready,
			// does not fire) until the controller flips this gate to True.
			ReadinessGates: []corev1.PodReadinessGate{{ConditionType: ReadinessGateSandboxReady}},
			// spec: §10.3 — the agent pod presents an audience-bound
			// projected token, not the kubelet's default cluster-audience
			// one; disable the automount so only the §6.1 projected
			// token (injected below when an audience is configured) is
			// present.
			AutomountServiceAccountToken: ptr.To(false),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: ptr.To(true),
				FSGroup:      ptr.To(in.credReadersGID()),
				// spec: §13.1 — both the adapter UID and the agent
				// UID are declared in the pod's
				// spec.securityContext.supplementalGroups list, making
				// lenny-cred-readers a shared group of which both
				// containers are members. The fsGroup above sets the
				// credential tmpfs group ownership; this explicit
				// supplementalGroups declaration is the membership the §13.1
				// cross-UID delivery path requires, rather than relying on
				// the kubelet's implicit fsGroup-to-supplementary-group
				// propagation side-effect.
				SupplementalGroups: []int64{in.credReadersGID()},
				SeccompProfile:     &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
		},
	}
	// spec: §10.3 — the agent pod's ServiceAccount has zero RBAC bindings;
	// an empty name uses the namespace default SA (no bindings in agent
	// namespaces).
	if in.ServiceAccountName != "" {
		pod.Spec.ServiceAccountName = in.ServiceAccountName
	}
	// spec: §6.4 — when the resolved Runtime is at
	// `workspaceTier: T4`, the sandbox reconciler stamps the
	// `lenny.dev/workspace-tier: t4` pod label that the
	// lenny-t4-node-isolation admission webhook keys on, pins the pod to
	// the T4 node pool via nodeSelector, and tolerates the T4 NoSchedule
	// taint. With all three present, the webhook admits the pod onto a
	// dedicated T4 node; without them, the webhook (failurePolicy: Fail)
	// rejects the pod with the §6.4 STR-003 message.
	//
	// The Runtime CRD's `workspaceTier` enum uses the uppercase `T4` form
	// (§12.9) — the lowercase `t4` is the pod label / node label
	// value, not the tier-name comparison key.
	if in.WorkspaceTier == WorkspaceTierT4 {
		applyT4NodeIsolation(pod)
	}
	// spec: §17.2 — Kata (microvm) pods MUST run on dedicated
	// node pools enforced by hard scheduling constraints, not merely
	// taints/tolerations. The RuntimeClass scheduling.nodeSelector
	// (control 1, rendered by the chart) constrains the pod at admission,
	// but the controller injects a requiredDuringSchedulingIgnoredDuring-
	// Execution node affinity (control 2) so scheduling fails rather than
	// falling back to an unsuitable node, plus the dedicated-node taint
	// toleration (control 3) so the pod can land on Kata-tainted hardware.
	if isolation.Profile(in.IsolationProfile) == isolation.ProfileMicrovm {
		applyKataNodeIsolation(pod)
	}
	applyDedicatedDNS(in, pod)
	return pod
}
