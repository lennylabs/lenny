// SPDX-License-Identifier: MIT

package podspec_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

// spec: 12.9.8
// diagnosis: pods that opt into the §12.9.8 egress-capture sidecar
// carry the lenny-egress-capture container alongside the runtime,
// a shared emptyDir for the JSONL capture file, and the runtime
// container mounts the capture volume read-only so the §12.9.8
// probe can read it via `kubectl exec`.
func TestBuildSidecarInjectsEgressCaptureWhenConfigured(t *testing.T) {
	in := inputs()
	in.EgressCapture = &podspec.EgressCapture{
		Image:    "ghcr.io/lennylabs/lenny-egress-capture:e2e",
		Upstream: "api.openai.com:443",
	}

	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(pod.Spec.Containers) != 3 {
		t.Fatalf("pod has %d containers, want 3 (adapter + runtime + egress-capture)", len(pod.Spec.Containers))
	}
	capture := container(t, pod, podspec.EgressCaptureContainerName)
	if capture.Image != "ghcr.io/lennylabs/lenny-egress-capture:e2e" {
		t.Errorf("egress-capture image = %q, want the configured image", capture.Image)
	}

	wantUpstream := "--upstream=api.openai.com:443"
	found := false
	for _, a := range capture.Args {
		if a == wantUpstream {
			found = true
		}
	}
	if !found {
		t.Errorf("egress-capture args = %v, missing %q", capture.Args, wantUpstream)
	}

	if !hasVolume(pod, podspec.EgressCaptureVolumeName) {
		t.Errorf("pod volumes = %v, want %q", volumeNames(pod), podspec.EgressCaptureVolumeName)
	}
	runtime := container(t, pod, "runtime")
	if !mountsCaptureVolume(runtime, podspec.EgressCaptureVolumeName) {
		t.Errorf("runtime container mounts = %+v, want capture volume mounted read-only", runtime.VolumeMounts)
	}
}

// spec: 12.9.8
// diagnosis: the embedded-model pod also carries the capture sidecar
// alongside the single runtime container when egress capture is
// configured. The probe reads the capture file from the runtime
// container's read-only mount.
func TestBuildEmbeddedInjectsEgressCaptureWhenConfigured(t *testing.T) {
	in := inputs()
	in.DeploymentModel = string(podspec.DeploymentEmbedded)
	in.EgressCapture = &podspec.EgressCapture{
		Image:    "ghcr.io/lennylabs/lenny-egress-capture:e2e",
		Upstream: "api.openai.com:443",
	}

	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pod.Spec.Containers) != 2 {
		t.Fatalf("embedded pod has %d containers, want 2 (runtime + egress-capture)", len(pod.Spec.Containers))
	}
	capture := container(t, pod, podspec.EgressCaptureContainerName)
	if !strings.HasPrefix(capture.Image, "ghcr.io/lennylabs/lenny-egress-capture") {
		t.Errorf("egress-capture image = %q", capture.Image)
	}
	runtime := container(t, pod, "runtime")
	if !mountsCaptureVolume(runtime, podspec.EgressCaptureVolumeName) {
		t.Errorf("embedded runtime container mounts = %+v, want capture volume mounted", runtime.VolumeMounts)
	}
}

// spec: 12.9.8
// diagnosis: omitting the EgressCapture field produces a stock pod
// with no capture container and no capture volume. The §12.9.8
// sidecar is opt-in per-template.
func TestBuildOmitsEgressCaptureByDefault(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, c := range pod.Spec.Containers {
		if c.Name == podspec.EgressCaptureContainerName {
			t.Errorf("default pod has the egress-capture container; want it opt-in")
		}
	}
	if hasVolume(pod, podspec.EgressCaptureVolumeName) {
		t.Errorf("default pod has the capture volume; want it absent without EgressCapture")
	}
}

func hasVolume(pod *corev1.Pod, name string) bool {
	for _, v := range pod.Spec.Volumes {
		if v.Name == name {
			return true
		}
	}
	return false
}

func volumeNames(pod *corev1.Pod) []string {
	names := make([]string, 0, len(pod.Spec.Volumes))
	for _, v := range pod.Spec.Volumes {
		names = append(names, v.Name)
	}
	return names
}

func mountsCaptureVolume(c corev1.Container, name string) bool {
	for _, m := range c.VolumeMounts {
		if m.Name == name {
			return true
		}
	}
	return false
}
