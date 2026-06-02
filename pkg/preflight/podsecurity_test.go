// SPDX-License-Identifier: MIT

package preflight

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func boolPtr(b bool) *bool { return &b }

// compliantContainer mirrors the §13.1 baseline the chart authors:
// non-root, all capabilities dropped, read-only root filesystem.
func compliantContainer(name string) corev1.Container {
	return corev1.Container{
		Name: name,
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:           boolPtr(true),
			ReadOnlyRootFilesystem: boolPtr(true),
			Capabilities:           &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
}

// spec: §13.1 lines 6-8 — a workload whose containers run as non-root,
// drop all capabilities, and mount a read-only root filesystem passes
// the baseline. F-13.1.12.
func TestCheckPodSecurityBaseline_compliant_spec_13_1(t *testing.T) {
	w := []WorkloadPodSecurity{{
		Workload: "lenny-system/Deployment/lenny-gateway",
		Containers: []ContainerSecurity{
			{Name: "gateway", RunAsNonRoot: true, ReadOnlyRootFilesystem: true, DropsAllCapabilities: true},
		},
	}}
	if d := CheckPodSecurityBaseline(w); !d.Passed {
		t.Fatalf("compliant baseline failed: %s", d.Reason)
	}
}

// spec: §13.1 lines 6-8 — each missing control fails fail-closed with
// POD_SPEC_SECURITY_BASELINE_VIOLATION. F-13.1.12.
func TestCheckPodSecurityBaseline_violations_spec_13_1(t *testing.T) {
	base := ContainerSecurity{Name: "gateway", RunAsNonRoot: true, ReadOnlyRootFilesystem: true, DropsAllCapabilities: true}
	cases := []struct {
		name    string
		mutate  func(*ContainerSecurity)
		wantSub string
	}{
		{"not-non-root", func(c *ContainerSecurity) { c.RunAsNonRoot = false }, "run as non-root"},
		{"keeps-capabilities", func(c *ContainerSecurity) { c.DropsAllCapabilities = false }, "drop all capabilities"},
		{"writable-root", func(c *ContainerSecurity) { c.ReadOnlyRootFilesystem = false }, "read-only root filesystem"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.mutate(&c)
			d := CheckPodSecurityBaseline([]WorkloadPodSecurity{{Workload: "ns/Deployment/x", Containers: []ContainerSecurity{c}}})
			if d.Passed {
				t.Fatalf("expected baseline failure for %s", tc.name)
			}
			if !strings.HasPrefix(d.Reason, "POD_SPEC_SECURITY_BASELINE_VIOLATION:") {
				t.Errorf("missing error code prefix: %s", d.Reason)
			}
			if !strings.Contains(d.Reason, tc.wantSub) {
				t.Errorf("reason %q does not mention %q", d.Reason, tc.wantSub)
			}
		})
	}
}

// spec: §13.1 — an empty workload set (fresh install, no Lenny workloads
// in the release namespace) passes. F-13.1.12.
func TestCheckPodSecurityBaseline_empty_passes(t *testing.T) {
	if d := CheckPodSecurityBaseline(nil); !d.Passed {
		t.Fatalf("empty workload set should pass: %s", d.Reason)
	}
}

// spec: §13.1 lines 6-8 — projectPodSecurity resolves each container's
// effective runAsNonRoot from the container securityContext when set,
// else the pod-level securityContext, and reads the capability and
// root-filesystem fields from the container. F-13.1.12.
func TestProjectPodSecurity_effective_runAsNonRoot_spec_13_1(t *testing.T) {
	spec := &corev1.PodSpec{
		SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: boolPtr(true)},
		Containers: []corev1.Container{
			// inherits pod-level runAsNonRoot, sets the container controls
			{
				Name: "inherits",
				SecurityContext: &corev1.SecurityContext{
					ReadOnlyRootFilesystem: boolPtr(true),
					Capabilities:           &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			},
			// container override flips runAsNonRoot back to root
			{
				Name: "override-root",
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot:           boolPtr(false),
					ReadOnlyRootFilesystem: boolPtr(true),
					Capabilities:           &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			},
		},
	}
	got := projectPodSecurity("lenny-system/Deployment/lenny-gateway", spec)
	if len(got.Containers) != 2 {
		t.Fatalf("want 2 containers, got %d", len(got.Containers))
	}
	if !got.Containers[0].RunAsNonRoot {
		t.Error("container 0 should inherit pod-level runAsNonRoot=true")
	}
	if got.Containers[1].RunAsNonRoot {
		t.Error("container 1 should honor its securityContext override runAsNonRoot=false")
	}
	// The whole-workload check must reject the overriding root container.
	if d := CheckPodSecurityBaseline([]WorkloadPodSecurity{got}); d.Passed {
		t.Error("a container overriding runAsNonRoot=false must fail the baseline")
	}
}

// spec: §13.1 — a pod that sets no securityContext at all (the bare
// Deployment shape the host-sharing fixtures use) is non-compliant: the
// baseline is opt-in by authoring, and an absent context means root +
// retained capabilities + writable root. F-13.1.12.
func TestProjectPodSecurity_absent_context_fails(t *testing.T) {
	spec := &corev1.PodSpec{Containers: []corev1.Container{{Name: "x"}}}
	got := projectPodSecurity("ns/Deployment/x", spec)
	if got.Containers[0].RunAsNonRoot || got.Containers[0].DropsAllCapabilities || got.Containers[0].ReadOnlyRootFilesystem {
		t.Errorf("absent securityContext should project to all-false, got %+v", got.Containers[0])
	}
	if d := CheckPodSecurityBaseline([]WorkloadPodSecurity{got}); d.Passed {
		t.Error("a pod with no securityContext must fail the baseline")
	}
}

func TestProjectContainerSecurity_dropsAll_caseSensitive(t *testing.T) {
	// Kubernetes treats the capability name "ALL" case-sensitively; a
	// lowercase "all" does not drop the set.
	c := &corev1.Container{
		Name:            "x",
		SecurityContext: &corev1.SecurityContext{Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"all"}}},
	}
	if projectContainerSecurity(c, true).DropsAllCapabilities {
		t.Error(`lowercase "all" must not count as dropping ALL`)
	}
}
