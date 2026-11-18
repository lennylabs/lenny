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

// spec: §5.2 — the WarmPoolController (which owns
// AgentPod.spec) copies the pool's PodSet topology spread
// constraints onto every AgentPod it creates, so the AgentPod-to-Pod
// reconciler can stamp them onto the agent pod.
func TestCreateAgentPodCopiesTopologyConstraints(t *testing.T) {
	ns := testNamespace(t)
	s := newScheme(t)

	tmpl := pool(ns, 1, 10)
	tmpl.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
		MaxSkew:           1,
		TopologyKey:       "topology.kubernetes.io/zone",
		WhenUnsatisfiable: corev1.ScheduleAnyway,
		LabelSelector:     &metav1.LabelSelector{MatchLabels: map[string]string{"lenny.dev/pool": testPool}},
	}}
	c := newClient(t, s, tmpl)

	reconcile(t, c, s)

	var agentPods lennyv1.AgentPodList
	if err := c.List(context.Background(), &agentPods, client.InNamespace(ns)); err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(agentPods.Items) == 0 {
		t.Fatalf("expected the controller to create at least one AgentPod")
	}
	for _, sb := range agentPods.Items {
		got := sb.Spec.TopologySpreadConstraints
		if len(got) != 1 {
			t.Fatalf("AgentPod %s topology constraints = %d, want 1", sb.Name, len(got))
		}
		if got[0].TopologyKey != "topology.kubernetes.io/zone" || got[0].MaxSkew != 1 {
			t.Errorf("AgentPod %s constraint = %+v, want zone/maxSkew 1", sb.Name, got[0])
		}
	}
}

// A PodSet with no topology constraints produces AgentPods with none;
// the controller copies what the PodSet carries without inventing
// defaults (defaulting is the PoolScalingController's job per §5.2).
func TestCreateAgentPodWithoutTopologyConstraints(t *testing.T) {
	ns := testNamespace(t)
	s := newScheme(t)
	c := newClient(t, s, pool(ns, 1, 10))

	reconcile(t, c, s)

	var agentPods lennyv1.AgentPodList
	if err := c.List(context.Background(), &agentPods, client.InNamespace(ns)); err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(agentPods.Items) == 0 {
		t.Fatalf("expected the controller to create at least one AgentPod")
	}
	for _, sb := range agentPods.Items {
		if len(sb.Spec.TopologySpreadConstraints) != 0 {
			t.Errorf("AgentPod %s carried unexpected constraints: %+v", sb.Name, sb.Spec.TopologySpreadConstraints)
		}
	}
}
