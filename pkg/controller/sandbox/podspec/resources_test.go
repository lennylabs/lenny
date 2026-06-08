// SPDX-License-Identifier: MIT

package podspec_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/resourceclass"
)

// TestBuildStampsResourcesOnEveryContainer asserts that, given a resolved
// §5.2 resource class, the builder applies the CPU/memory requests and
// limits to both the adapter and runtime containers. spec: §6.4 line 413.
func TestBuildStampsResourcesOnEveryContainer_spec_6_4_413(t *testing.T) {
	req, ok := resourceclass.DefaultRegistry().Resolve("medium")
	if !ok {
		t.Fatal("medium class missing")
	}
	in := inputs()
	in.Resources = &req
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := resource.MustParse("2Gi")
	for _, name := range []string{"adapter", "runtime"} {
		c := container(t, pod, name)
		if c.Resources.Limits.Memory().Cmp(want) != 0 {
			t.Errorf("%s memory limit = %s, want %s", name, c.Resources.Limits.Memory().String(), want.String())
		}
		if c.Resources.Requests.Cpu().IsZero() {
			t.Errorf("%s has no cpu request", name)
		}
	}
}

// TestBuildEmbeddedStampsResources covers the single-container embedded
// model path. spec: §6.4 line 413.
func TestBuildEmbeddedStampsResources_spec_6_4_413(t *testing.T) {
	req, _ := resourceclass.DefaultRegistry().Resolve("large")
	in := inputs()
	in.DeploymentModel = string(podspec.DeploymentEmbedded)
	in.Resources = &req
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	c := container(t, pod, "runtime")
	if c.Resources.Limits.Memory().Cmp(resource.MustParse("4Gi")) != 0 {
		t.Errorf("embedded runtime memory limit = %s, want 4Gi", c.Resources.Limits.Memory().String())
	}
}

// TestBuildNilResourcesLeavesContainersUnconstrained preserves the prior
// (dev / unconfigured) behavior when no class resolves.
func TestBuildNilResourcesLeavesContainersUnconstrained(t *testing.T) {
	in := inputs()
	in.Resources = nil
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	c := container(t, pod, "runtime")
	if len(c.Resources.Limits) != 0 || len(c.Resources.Requests) != 0 {
		t.Errorf("runtime resources = %+v, want empty when Resources is nil", c.Resources)
	}
}

// TestBuildDoesNotStampResourcesOnEgressSidecar asserts the test-only
// egress-capture sidecar does not receive the agent resource budget (it is
// injected after applyResources runs).
func TestBuildDoesNotStampResourcesOnEgressSidecar(t *testing.T) {
	req, _ := resourceclass.DefaultRegistry().Resolve("small")
	in := inputs()
	in.Resources = &req
	in.EgressCapture = &podspec.EgressCapture{Image: "ghcr.io/lennylabs/egress:test", Upstream: "api.openai.com:443"}
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	c := container(t, pod, podspec.EgressCaptureContainerName)
	if len(c.Resources.Limits) != 0 {
		t.Errorf("egress sidecar resources = %+v, want empty", c.Resources)
	}
}

// TestBuildResourcesAreIndependentPerPod confirms the per-pod DeepCopy so
// mutating one pod's resources does not affect another's.
func TestBuildResourcesAreIndependentPerPod(t *testing.T) {
	req, _ := resourceclass.DefaultRegistry().Resolve("medium")
	in := inputs()
	in.Resources = &req
	a, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build a: %v", err)
	}
	b, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build b: %v", err)
	}
	container(t, a, "runtime").Resources.Limits[corev1.ResourceCPU] = resource.MustParse("99")
	bLimits := container(t, b, "runtime").Resources.Limits
	if bLimits.Cpu().Value() == 99 {
		t.Fatal("pods share a ResourceList; mutation bled across pods")
	}
}
