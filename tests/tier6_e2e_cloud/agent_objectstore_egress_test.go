// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 cloud e2e for the §13.2 agent-to-object-store egress SELECTIVITY.
//
// The paired tier-9 kindnet test
// (TestAgentPodReachesObjectStoreButNoOtherLennySystemPort_spec_13_2) asserts
// the POSITIVE control — an agent pod reaches the object store on its TLS port —
// but cannot assert the NEGATIVE controls, because kindnet does not enforce the
// port field of an egress ipBlock rule: it admits the whole allow-pod-egress-
// objectstore CIDR on every port, so a probe to another lenny-system service on
// its own port slips past the port restriction the rendered policy declares. A
// managed cluster runs a port-enforcing CNI (the EKS/GKE/AKS network plugin), so
// the negative selectivity is verifiable here and only here.
//
// This test schedules a §13.1-compliant probe pod in an agent namespace and
// asserts the object-store egress rule opened exactly the object-store port and
// nothing else: the pod is DROPPED reaching the lenny-ops admin port and the
// token service on their own ports. The probe self-reports through its terminal
// phase — it exits 0 (pod Succeeded) only when every forbidden destination is
// blocked, and exits 1 (pod Failed) the moment one connects.

package tier6_e2e_cloud_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// spec: §13.2 (NET-071 — allow-pod-egress-objectstore opens exactly the
// object-store port to the agent namespace and no other lenny-system
// destination; the port restriction is enforced by a port-aware CNI).
// diagnosis: a Failed probe means the object-store egress rule widened agent
// egress beyond the object-store port to another lenny-system service on a real,
// port-enforcing CNI — a §13.2 lateral-movement violation the kindnet tier
// cannot catch. A Skip means no reachable managed cluster; run against an
// EKS/GKE/AKS install (LENNY_CLOUD_PROVIDER set, kubeconfig pointing at it).
func TestAgentPodObjectStoreEgressIsPortScoped_spec_13_2(t *testing.T) {
	_ = requireCloud(t)
	cli := kube(t)
	requireGatewayInstalled(t, cli)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ns := requireAgentNamespace(t, cli, ctx)

	// The probe curls each forbidden lenny-system destination on its own port
	// with a short timeout. A connection that establishes (curl exit 0) is a
	// §13.2 violation and fails the pod immediately; when both are blocked the
	// pod exits 0. It reaches nothing but a DNS lookup and the two blocked
	// dials, so it is compliant with the §13.1 default-deny egress posture.
	const script = `set -e
if curl -sS -o /dev/null -m 5 http://lenny-ops.lenny-system.svc:8090/readyz; then echo "VIOLATION: reached lenny-ops:8090"; exit 1; fi
if curl -sS -o /dev/null -m 5 http://token-service.lenny-system.svc:50052/; then echo "VIOLATION: reached token-service:50052"; exit 1; fi
echo "boundary held: lenny-ops and token-service both blocked"; exit 0`

	nonRoot := true
	noPrivEsc := false
	roFS := true
	var uid int64 = 65532
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "objstore-egress-boundary-probe",
			Namespace: ns,
			Labels:    map[string]string{"lenny.dev/managed": "true"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: &nonRoot,
				RunAsUser:    &uid,
			},
			Containers: []corev1.Container{{
				Name:    "probe",
				Image:   "curlimages/curl:8.9.1",
				Command: []string{"sh", "-c", script},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &noPrivEsc,
					ReadOnlyRootFilesystem:   &roFS,
					RunAsNonRoot:             &nonRoot,
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
		},
	}

	_ = cli.CoreV1().Pods(ns).Delete(ctx, pod.Name, metav1.DeleteOptions{})
	if _, err := cli.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create egress-boundary probe pod in %s: %v", ns, err)
	}
	t.Cleanup(func() {
		_ = cli.CoreV1().Pods(ns).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
	})

	phase := waitForPodTerminal(t, cli, ctx, ns, pod.Name)
	switch phase {
	case corev1.PodSucceeded:
		t.Logf("§13.2 boundary held: the agent pod reached neither lenny-ops:8090 nor token-service:50052; "+
			"allow-pod-egress-objectstore opened the object-store port alone (namespace %s)", ns)
	case corev1.PodFailed:
		t.Fatalf("§13.2 violation: an agent pod in %s reached a forbidden lenny-system destination. The "+
			"allow-pod-egress-objectstore egress rule must open the object-store port alone; it must not "+
			"admit lenny-ops or the token service. See the probe pod logs for which destination connected.", ns)
	default:
		t.Fatalf("egress-boundary probe pod in %s did not reach a terminal phase (last: %s)", ns, phase)
	}
}

// requireAgentNamespace returns the name of an agent namespace (one carrying the
// lenny.dev/agent-namespace marker the chart stamps), or skips when none exists
// on the target cluster.
func requireAgentNamespace(t *testing.T, cli *kubernetes.Clientset, ctx context.Context) string {
	t.Helper()
	nss, err := cli.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: "lenny.dev/agent-namespace",
	})
	if err != nil {
		t.Skipf("list agent namespaces: %v", err)
	}
	if len(nss.Items) == 0 {
		t.Skip("no lenny.dev/agent-namespace namespace on the target cluster; nothing to probe")
	}
	return nss.Items[0].Name
}

// waitForPodTerminal polls the named pod until it reaches a terminal phase
// (Succeeded or Failed) or the context expires, returning the last phase seen.
func waitForPodTerminal(t *testing.T, cli *kubernetes.Clientset, ctx context.Context, ns, name string) corev1.PodPhase {
	t.Helper()
	var last corev1.PodPhase
	for {
		p, err := cli.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			last = p.Status.Phase
			if last == corev1.PodSucceeded || last == corev1.PodFailed {
				return last
			}
		}
		select {
		case <-ctx.Done():
			return last
		case <-time.After(3 * time.Second):
		}
	}
}
