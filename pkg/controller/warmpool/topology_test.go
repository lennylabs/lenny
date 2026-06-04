// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// spec: §5.2 lines 631-636 — the WarmPoolController (which owns
// Sandbox.spec) copies the pool's SandboxTemplate topology spread
// constraints onto every Sandbox it creates, so the Sandbox-to-Pod
// reconciler can stamp them onto the agent pod.
func TestCreateSandboxCopiesTopologyConstraints_spec_5_2_631(t *testing.T) {
	s := newScheme(t)

	tmpl := template()
	tmpl.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
		MaxSkew:           1,
		TopologyKey:       "topology.kubernetes.io/zone",
		WhenUnsatisfiable: corev1.ScheduleAnyway,
		LabelSelector:     &metav1.LabelSelector{MatchLabels: map[string]string{"lenny.dev/pool": testPool}},
	}}
	c := newClient(t, s, tmpl, pool(1, 10))

	reconcile(t, c, s)

	var sandboxes lennyv1.SandboxList
	if err := c.List(context.Background(), &sandboxes, client.InNamespace(testNS)); err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(sandboxes.Items) == 0 {
		t.Fatalf("expected the controller to create at least one Sandbox")
	}
	for _, sb := range sandboxes.Items {
		got := sb.Spec.TopologySpreadConstraints
		if len(got) != 1 {
			t.Fatalf("Sandbox %s topology constraints = %d, want 1", sb.Name, len(got))
		}
		if got[0].TopologyKey != "topology.kubernetes.io/zone" || got[0].MaxSkew != 1 {
			t.Errorf("Sandbox %s constraint = %+v, want zone/maxSkew 1", sb.Name, got[0])
		}
	}
}

// A template with no topology constraints produces Sandboxes with none;
// the controller copies what the template carries without inventing
// defaults (defaulting is the PoolScalingController's job per §5.2).
func TestCreateSandboxWithoutTopologyConstraints_spec_5_2_631(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(1, 10))

	reconcile(t, c, s)

	var sandboxes lennyv1.SandboxList
	if err := c.List(context.Background(), &sandboxes, client.InNamespace(testNS)); err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(sandboxes.Items) == 0 {
		t.Fatalf("expected the controller to create at least one Sandbox")
	}
	for _, sb := range sandboxes.Items {
		if len(sb.Spec.TopologySpreadConstraints) != 0 {
			t.Errorf("Sandbox %s carried unexpected constraints: %+v", sb.Name, sb.Spec.TopologySpreadConstraints)
		}
	}
}
