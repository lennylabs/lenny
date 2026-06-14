// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IngressControllerCheck is the §13.2 NET-038 ingress-controller advisory.
// The allow-gateway-ingress NetworkPolicy admits external HTTPS to the
// gateway TLS listener only from pods in ingressControllerNamespace that
// carry the configured controllerPodLabel. When the namespace does not
// exist, or no running pod in it carries the label, the rendered policy
// admits nothing and the gateway is unreachable from the internet. The
// check is a non-blocking warning that catches the two most common
// misconfigurations (wrong namespace, wrong pod label) at deploy time.
//
// spec: §13.2 line 292 (NET-038 lenny-preflight validates that (a) a
// namespace with the configured name exists and (b) at least one running
// pod there carries the configured controllerPodLabel, and warns if
// either check fails). F-13.2.8.
type IngressControllerCheck struct {
	// Namespace is the ingressControllerNamespace value. Empty skips the
	// check (no ingress integration configured).
	Namespace string
	// PodLabelKey / PodLabelValue are the ingress.controllerPodLabel.key /
	// ingress.controllerPodLabel.value values. Empty skips the pod half of
	// the advisory (the namespace existence half still runs).
	PodLabelKey   string
	PodLabelValue string
	// NamespaceExists reports whether a namespace named Namespace exists in
	// the cluster.
	NamespaceExists bool
	// HasRunningControllerPod reports whether at least one running pod in
	// Namespace carries the controllerPodLabel.
	HasRunningControllerPod bool
}

// Decide evaluates the §13.2 NET-038 ingress-controller advisory. A
// missing namespace warns that the namespaceSelector matches nothing; a
// present namespace with no running controller pod warns that the
// podSelector matches nothing. Both warnings are non-blocking (Passed
// stays true) because the gateway TLS listener may be intentionally
// reached through a different path (a cloud LoadBalancer, a service mesh)
// where the rendered NetworkPolicy is not the only route.
//
// spec: §13.2 line 292. F-13.2.8.
func (c IngressControllerCheck) Decide() Decision {
	if strings.TrimSpace(c.Namespace) == "" {
		return Decision{Passed: true}
	}
	if !c.NamespaceExists {
		return Decision{Passed: true, Reason: fmt.Sprintf(
			"WARNING: ingressControllerNamespace '%s' does not exist in the cluster; the allow-gateway-ingress "+
				"NetworkPolicy namespaceSelector matches no namespace, so no external HTTPS reaches the gateway TLS "+
				"listener. Set ingressControllerNamespace to the namespace the Ingress controller runs in (§13.2 NET-038).",
			c.Namespace,
		)}
	}
	if strings.TrimSpace(c.PodLabelKey) == "" || strings.TrimSpace(c.PodLabelValue) == "" {
		return Decision{Passed: true}
	}
	if !c.HasRunningControllerPod {
		return Decision{Passed: true, Reason: fmt.Sprintf(
			"WARNING: no running pod in namespace '%s' carries the label '%s=%s'; the allow-gateway-ingress "+
				"NetworkPolicy podSelector matches no Ingress controller pod, so no external HTTPS reaches the gateway "+
				"TLS listener. Set ingress.controllerPodLabel.key/value to a label on the Ingress controller pods (§13.2 NET-038).",
			c.Namespace, c.PodLabelKey, c.PodLabelValue,
		)}
	}
	return Decision{Passed: true}
}

// gatherIngressController reads the §13.2 NET-038 posture of the Ingress
// controller namespace: whether the namespace exists and whether at least
// one running pod there carries the controllerPodLabel. A missing
// namespace returns NamespaceExists=false; an empty namespace or pod label
// short-circuits the cluster reads. Reuses the namespace get and pod list
// the namespace-governance and monitoring-namespace checks already require,
// so no extra RBAC is needed.
//
// spec: §13.2 line 292. F-13.2.8.
func gatherIngressController(ctx context.Context, reader client.Reader, namespace, key, value string) (nsExists, hasRunningPod bool, err error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return false, false, nil
	}
	var ns corev1.Namespace
	if err := reader.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err != nil {
		if apierrors.IsNotFound(err) {
			return false, false, nil
		}
		return false, false, err
	}
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	if key == "" || value == "" {
		return true, false, nil
	}
	var pods corev1.PodList
	if err := reader.List(ctx, &pods, client.InNamespace(namespace), client.MatchingLabels{key: value}); err != nil {
		if apierrors.IsNotFound(err) {
			return true, false, nil
		}
		return true, false, err
	}
	for i := range pods.Items {
		if pods.Items[i].Status.Phase == corev1.PodRunning {
			return true, true, nil
		}
	}
	return true, false, nil
}
