// SPDX-License-Identifier: MIT

package podspec_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

// spec: §5.2 lines 631-636 — the builder stamps the resolved topology
// spread constraints onto the agent pod so the scheduler distributes
// the pool's pods across zones and nodes.
func TestBuildStampsTopologyConstraints_spec_5_2_631(t *testing.T) {
	in := inputs()
	in.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{
		{
			MaxSkew:           1,
			TopologyKey:       "topology.kubernetes.io/zone",
			WhenUnsatisfiable: corev1.ScheduleAnyway,
			LabelSelector:     &metav1.LabelSelector{MatchLabels: map[string]string{"lenny.dev/pool": "claude-worker"}},
		},
		{
			MaxSkew:           1,
			TopologyKey:       "kubernetes.io/hostname",
			WhenUnsatisfiable: corev1.ScheduleAnyway,
		},
	}

	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := pod.Spec.TopologySpreadConstraints
	if len(got) != 2 {
		t.Fatalf("pod topology constraints = %d, want 2", len(got))
	}
	if got[0].TopologyKey != "topology.kubernetes.io/zone" || got[1].TopologyKey != "kubernetes.io/hostname" {
		t.Errorf("topology keys = %q, %q; want zone, hostname", got[0].TopologyKey, got[1].TopologyKey)
	}
}

// A Build with no topology constraints leaves the pod's field empty
// rather than synthesizing defaults (defaulting is the
// PoolScalingController's responsibility, §5.2).
func TestBuildWithoutTopologyConstraints_spec_5_2_631(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pod.Spec.TopologySpreadConstraints) != 0 {
		t.Errorf("pod carried unexpected topology constraints: %+v", pod.Spec.TopologySpreadConstraints)
	}
}

// spec: §5.2 (concurrent-session acknowledgment), §13.1 (pod security)
// — to mitigate cross-slot raw-socket sniffing on the shared network
// namespace when maxConcurrentSessions > 1, the agent container's
// securityContext MUST drop CAP_NET_RAW. §5.2 phrases this as a CRD
// validation-webhook rejection, but the SandboxTemplate/SandboxWarmPool
// CRD carries no pod-template securityContext for the webhook to inspect,
// so the invariant is enforced here at pod materialization instead: the
// builder is the sole author of the pod template and drops ALL
// capabilities (which subsumes NET_RAW) while adding none on every
// container. No agent pod — session or service mode, at any
// maxConcurrentSessions — can hold CAP_NET_RAW. This guards the invariant
// against a regression that would narrow the drop list or add a
// capability back.
func TestBuildDropsNetRawOnEveryContainer_spec_5_2_496(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	all := append([]corev1.Container{}, pod.Spec.InitContainers...)
	all = append(all, pod.Spec.Containers...)
	if len(all) == 0 {
		t.Fatal("pod has no containers")
	}
	for _, c := range all {
		caps := c.SecurityContext.Capabilities
		if caps == nil {
			t.Fatalf("%s: no capabilities block", c.Name)
		}
		dropsAll := false
		for _, d := range caps.Drop {
			if d == "ALL" {
				dropsAll = true
			}
		}
		if !dropsAll {
			t.Errorf("%s: drop list %v does not contain ALL, so CAP_NET_RAW is not dropped", c.Name, caps.Drop)
		}
		// A NET_RAW (or any capability) add would re-grant the cap even
		// with ALL dropped; the builder must never add one.
		if len(caps.Add) != 0 {
			t.Errorf("%s: capabilities.add = %v, want empty (no CAP_NET_RAW add)", c.Name, caps.Add)
		}
	}
}
