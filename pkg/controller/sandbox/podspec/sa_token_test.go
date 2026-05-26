// SPDX-License-Identifier: MIT

package podspec_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

// volume returns the named pod volume, failing the test when absent.
func volume(t *testing.T, pod *corev1.Pod, name string) corev1.Volume {
	t.Helper()
	for _, v := range pod.Spec.Volumes {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("pod has no %q volume", name)
	return corev1.Volume{}
}

func hasMount(c corev1.Container, name string) (corev1.VolumeMount, bool) {
	for _, m := range c.VolumeMounts {
		if m.Name == name {
			return m, true
		}
	}
	return corev1.VolumeMount{}, false
}

// TestBuildMountsProjectedSAToken_spec_6_1 verifies the §6.1 line 14 /
// §10.3 projected service-account token: an audience-bound, 900s-TTL token
// mounted read-only on the adapter container (the sidecar model's
// gateway-facing process), with the default token automount disabled.
func TestBuildMountsProjectedSAToken_spec_6_1(t *testing.T) {
	in := inputs()
	in.SATokenAudience = "lenny-gateway-acme"
	in.ServiceAccountName = "lenny-agent"
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// §10.3: the kubelet default (cluster-audience) token automount is off.
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Errorf("AutomountServiceAccountToken = %v, want false (§10.3)", pod.Spec.AutomountServiceAccountToken)
	}
	if got := pod.Spec.ServiceAccountName; got != "lenny-agent" {
		t.Errorf("ServiceAccountName = %q, want lenny-agent", got)
	}

	v := volume(t, pod, "lenny-sa-token")
	if v.Projected == nil || len(v.Projected.Sources) != 1 || v.Projected.Sources[0].ServiceAccountToken == nil {
		t.Fatalf("lenny-sa-token volume is not a projected service-account token: %+v", v)
	}
	tok := v.Projected.Sources[0].ServiceAccountToken
	if tok.Audience != "lenny-gateway-acme" {
		t.Errorf("token audience = %q, want lenny-gateway-acme (§10.3 deployment-specific)", tok.Audience)
	}
	if tok.ExpirationSeconds == nil || *tok.ExpirationSeconds != 900 {
		t.Errorf("token expirationSeconds = %v, want 900 (§10.3)", tok.ExpirationSeconds)
	}
	if tok.Path != "token" {
		t.Errorf("token path = %q, want token", tok.Path)
	}

	// The adapter is the sidecar model's gateway-facing process.
	m, ok := hasMount(container(t, pod, "adapter"), "lenny-sa-token")
	if !ok {
		t.Fatal("adapter container does not mount the projected SA token")
	}
	if !m.ReadOnly || m.MountPath != "/var/run/secrets/lenny.dev/serviceaccount" {
		t.Errorf("SA token mount = %+v, want read-only at /var/run/secrets/lenny.dev/serviceaccount", m)
	}
}

// TestBuildEmbeddedMountsProjectedSAToken_spec_6_1 verifies the embedded
// model mounts the projected token on the single runtime container (the
// embedded runtime is the pod's gateway-facing process).
func TestBuildEmbeddedMountsProjectedSAToken_spec_6_1(t *testing.T) {
	in := inputs()
	in.DeploymentModel = "embedded"
	in.SATokenAudience = "lenny-gateway-acme"
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !hasVolume(pod, "lenny-sa-token") {
		t.Fatal("embedded pod is missing the projected SA token volume")
	}
	if _, ok := hasMount(container(t, pod, "runtime"), "lenny-sa-token"); !ok {
		t.Error("embedded runtime container does not mount the projected SA token")
	}
}

// TestBuildOmitsSATokenWithoutAudience_spec_10_3 verifies the builder does
// not mount a wrong-audience token when no audience is configured: the
// projected volume is absent, but the default automount stays disabled.
func TestBuildOmitsSATokenWithoutAudience_spec_10_3(t *testing.T) {
	pod, err := podspec.Build(inputs()) // no SATokenAudience
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if hasVolume(pod, "lenny-sa-token") {
		t.Error("pod has a projected SA token volume even though no audience was configured")
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Errorf("AutomountServiceAccountToken = %v, want false even without an audience (§10.3)", pod.Spec.AutomountServiceAccountToken)
	}
}

// TestBuildDeclaresReadinessGate_spec_6_1 verifies the §6.1 line 18 pod
// readiness gate is declared so the WarmPoolController gates claimability.
func TestBuildDeclaresReadinessGate_spec_6_1(t *testing.T) {
	for _, model := range []string{"", "embedded"} {
		in := inputs()
		in.DeploymentModel = model
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(model=%q): %v", model, err)
		}
		var found bool
		for _, g := range pod.Spec.ReadinessGates {
			if g.ConditionType == podspec.ReadinessGateSandboxReady {
				found = true
			}
		}
		if !found {
			t.Errorf("model=%q: pod is missing the %q readiness gate (§6.1 line 18)", model, podspec.ReadinessGateSandboxReady)
		}
	}
}
