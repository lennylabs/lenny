// SPDX-License-Identifier: MIT

package podspec_test

import (
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

func inputs() podspec.Inputs {
	return podspec.Inputs{
		Name:             "claude-worker-abc",
		Namespace:        "lenny-agents",
		Labels:           map[string]string{"lenny.dev/pool": "claude-worker"},
		RuntimeImage:     "ghcr.io/acme/claude-code:v1",
		AdapterImage:     "ghcr.io/lennylabs/lenny-adapter:v1",
		IsolationProfile: "sandboxed",
	}
}

func container(t *testing.T, pod *corev1.Pod, name string) corev1.Container {
	t.Helper()
	for _, c := range pod.Spec.Containers {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("pod has no %q container", name)
	return corev1.Container{}
}

func TestBuildProducesTheAdapterAndRuntimeContainers(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if pod.Name != "claude-worker-abc" || pod.Namespace != "lenny-agents" {
		t.Errorf("pod identity = %s/%s, want lenny-agents/claude-worker-abc", pod.Namespace, pod.Name)
	}
	if pod.Labels["lenny.dev/pool"] != "claude-worker" {
		t.Errorf("pod labels = %v, want the pool label propagated", pod.Labels)
	}
	if len(pod.Spec.Containers) != 2 {
		t.Fatalf("pod has %d containers, want 2 (adapter + runtime)", len(pod.Spec.Containers))
	}
	if got := container(t, pod, "adapter").Image; got != "ghcr.io/lennylabs/lenny-adapter:v1" {
		t.Errorf("adapter image = %q", got)
	}
	if got := container(t, pod, "runtime").Image; got != "ghcr.io/acme/claude-code:v1" {
		t.Errorf("runtime image = %q", got)
	}
}

func TestBuildSelectsTheRuntimeClassFromIsolationProfile(t *testing.T) {
	cases := map[string]string{
		"standard":  "runc",
		"sandboxed": "gvisor",
		"microvm":   "kata",
	}
	for profile, want := range cases {
		in := inputs()
		in.IsolationProfile = profile
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(%s): %v", profile, err)
		}
		if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != want {
			t.Errorf("isolation %q: runtimeClassName = %v, want %q", profile, pod.Spec.RuntimeClassName, want)
		}
	}
}

func TestBuildAppliesPodSecurityPosture(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	sc := pod.Spec.SecurityContext
	if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("pod securityContext must set runAsNonRoot")
	}
	if sc == nil || sc.FSGroup == nil {
		t.Error("pod securityContext must set the lenny-cred-readers fsGroup (POD_SPEC_CRED_FSGROUP_MISSING)")
	}
	if sc == nil || sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Error("pod securityContext must set the RuntimeDefault seccomp profile")
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restartPolicy = %q, want Never", pod.Spec.RestartPolicy)
	}
}

func TestBuildAppliesContainerSecurityPosture(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	uids := map[string]int64{}
	for _, name := range []string{"adapter", "runtime"} {
		c := container(t, pod, name)
		sc := c.SecurityContext
		if sc == nil {
			t.Fatalf("%s container has no securityContext", name)
		}
		if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
			t.Errorf("%s: allowPrivilegeEscalation must be false", name)
		}
		if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
			t.Errorf("%s: readOnlyRootFilesystem must be true", name)
		}
		if sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
			t.Errorf("%s: capabilities must drop ALL, got %+v", name, sc.Capabilities)
		}
		if sc.RunAsUser == nil || *sc.RunAsUser == 0 {
			t.Errorf("%s: runAsUser must be a non-root UID", name)
		} else {
			uids[name] = *sc.RunAsUser
		}
	}
	if uids["adapter"] == uids["runtime"] {
		t.Error("the adapter and runtime containers must run as distinct UIDs (§13.1)")
	}
}

func TestBuildLeavesHostSharingFlagsUnset(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	s := pod.Spec
	if s.HostPID || s.HostNetwork || s.HostIPC {
		t.Error("host-sharing flags must be unset on an agent pod (§13.1)")
	}
	if s.ShareProcessNamespace != nil && *s.ShareProcessNamespace {
		t.Error("shareProcessNamespace must not be enabled on an agent pod (§13.1)")
	}
}

func TestBuildMountsTheWorkspaceAndCredentialVolumes(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	names := map[string]bool{}
	for _, v := range pod.Spec.Volumes {
		names[v.Name] = true
	}
	for _, want := range []string{"workspace", "credentials", "tmp"} {
		if !names[want] {
			t.Errorf("pod is missing the %q volume", want)
		}
	}
	// The runtime reads the credential file; it must not be writable.
	for _, m := range container(t, pod, "runtime").VolumeMounts {
		if m.Name == "credentials" && !m.ReadOnly {
			t.Error("the runtime container must mount the credential volume read-only")
		}
	}
}

func TestBuildRejectsInvalidInputs(t *testing.T) {
	t.Run("missing runtime image", func(t *testing.T) {
		in := inputs()
		in.RuntimeImage = ""
		if _, err := podspec.Build(in); err == nil {
			t.Error("Build should reject a missing runtime image")
		}
	})
	t.Run("missing adapter image", func(t *testing.T) {
		in := inputs()
		in.AdapterImage = ""
		if _, err := podspec.Build(in); err == nil {
			t.Error("Build should reject a missing adapter image for the sidecar model")
		}
	})
	t.Run("unknown isolation profile", func(t *testing.T) {
		in := inputs()
		in.IsolationProfile = "teleport"
		if _, err := podspec.Build(in); err == nil {
			t.Error("Build should reject an unknown isolation profile")
		}
	})
	t.Run("unknown deployment model", func(t *testing.T) {
		in := inputs()
		in.DeploymentModel = "serverless"
		if _, err := podspec.Build(in); err == nil {
			t.Error("Build should reject an unknown deployment model")
		}
	})
}

func TestBuildDefaultsToTheSidecarModel(t *testing.T) {
	in := inputs()
	in.DeploymentModel = ""
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pod.Spec.Containers) != 2 {
		t.Fatalf("the sidecar model produces %d containers, want 2", len(pod.Spec.Containers))
	}
	if pod.Spec.Containers[0].Name != "adapter" {
		t.Errorf("first container = %q, want adapter", pod.Spec.Containers[0].Name)
	}
}

// TestBuildSidecarPassesTheRuntimeSocket asserts the §4.7 sidecar
// transport wiring: the adapter binds the abstract runtime socket and
// the runtime container learns its name from LENNY_ADAPTER_SOCKET.
func TestBuildSidecarPassesTheRuntimeSocket(t *testing.T) {
	in := inputs()
	in.DeploymentModel = "sidecar"
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	adapterArgs := container(t, pod, "adapter").Args
	var sawSocketArg bool
	for _, a := range adapterArgs {
		if a == "--runtime-socket="+podspec.RuntimeSocketName {
			sawSocketArg = true
		}
	}
	if !sawSocketArg {
		t.Errorf("adapter args %v must bind the runtime socket %q", adapterArgs, podspec.RuntimeSocketName)
	}

	var socketEnv string
	for _, e := range container(t, pod, "runtime").Env {
		if e.Name == podspec.RuntimeSocketEnvVar {
			socketEnv = e.Value
		}
	}
	if socketEnv != podspec.RuntimeSocketName {
		t.Errorf("runtime %s = %q, want %q", podspec.RuntimeSocketEnvVar, socketEnv, podspec.RuntimeSocketName)
	}

	// §13.1: the sidecar model keeps the process-isolation boundary.
	if pod.Spec.ShareProcessNamespace != nil && *pod.Spec.ShareProcessNamespace {
		t.Error("the sidecar model must leave shareProcessNamespace unset (§4.7, §13.1)")
	}
}

// TestBuildSidecarWiresStagingAndRuntimeUID asserts the §4.7 adapter
// receives --staging-dir (so PrepareWorkspace succeeds, F-4.7.10) and
// --runtime-uid set to the runtime container's runAsUser (so the
// SO_PEERCRED MCP peer check is active in production, F-4.7.11).
func TestBuildSidecarWiresStagingAndRuntimeUID(t *testing.T) {
	in := inputs()
	in.DeploymentModel = "sidecar"
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	adapterArgs := container(t, pod, "adapter").Args
	wantUID := fmt.Sprintf("--runtime-uid=%d", podspec.AgentUID)
	var sawStaging, sawUID bool
	for _, a := range adapterArgs {
		switch a {
		case "--staging-dir=/workspace/.staging":
			sawStaging = true
		case wantUID:
			sawUID = true
		}
	}
	if !sawStaging {
		t.Errorf("adapter args %v must set --staging-dir (§4.7)", adapterArgs)
	}
	if !sawUID {
		t.Errorf("adapter args %v must set %q (§4.7/§13)", adapterArgs, wantUID)
	}
}

// TestBuildEmbeddedProducesOneContainer asserts the §4.7 embedded
// model: a single runtime container serving the gRPC contract, with no
// separate adapter container.
func TestBuildEmbeddedProducesOneContainer(t *testing.T) {
	in := inputs()
	in.DeploymentModel = "embedded"
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("the embedded model produces %d containers, want 1", len(pod.Spec.Containers))
	}
	c := container(t, pod, "runtime")
	if c.Image != in.RuntimeImage {
		t.Errorf("embedded runtime image = %q, want %q", c.Image, in.RuntimeImage)
	}
	// The embedded runtime serves the adapter gRPC port itself.
	var sawGRPCPort bool
	for _, p := range c.Ports {
		if p.Name == "grpc" {
			sawGRPCPort = true
		}
	}
	if !sawGRPCPort {
		t.Error("the embedded runtime container must expose the adapter gRPC port")
	}
	// One process is the adapter, so it writes the credential volume:
	// the embedded model has no read-only-to-runtime split.
	for _, m := range c.VolumeMounts {
		if m.Name == "credentials" && m.ReadOnly {
			t.Error("the embedded runtime writes the credential volume; it must not be read-only")
		}
	}
	// §13.1 posture still applies.
	sc := c.SecurityContext
	if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser == 0 {
		t.Error("the embedded runtime container must run as a non-root UID")
	}
}

// TestBuildEmbeddedDoesNotRequireAnAdapterImage confirms the embedded
// model builds without an adapter image, since it has no adapter
// container.
func TestBuildEmbeddedDoesNotRequireAnAdapterImage(t *testing.T) {
	in := inputs()
	in.DeploymentModel = "embedded"
	in.AdapterImage = ""
	if _, err := podspec.Build(in); err != nil {
		t.Errorf("the embedded model must not require an adapter image: %v", err)
	}
}

// TestBuildDefaultsTerminationGraceTo120_spec_4_6_1 confirms the §4.6.1
// disruption-protection default: 120s, not the prior hardcoded 30s that
// would SIGKILL the adapter mid-checkpoint on a node drain.
func TestBuildDefaultsTerminationGraceTo120_spec_4_6_1(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := pod.Spec.TerminationGracePeriodSeconds
	if got == nil || *got != 120 {
		t.Fatalf("terminationGracePeriodSeconds = %v, want 120 (§4.6.1)", got)
	}
}

// TestBuildClampsTerminationGraceToCeiling_spec_4_6_1 confirms the
// SandboxTemplate maxTerminationGracePeriodSeconds ceiling clamps the
// pod grace below the 120s default.
func TestBuildClampsTerminationGraceToCeiling_spec_4_6_1(t *testing.T) {
	in := inputs()
	in.MaxTerminationGraceSeconds = ptr.To(int64(60))
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := pod.Spec.TerminationGracePeriodSeconds; got == nil || *got != 60 {
		t.Fatalf("terminationGracePeriodSeconds = %v, want 60 (clamped to ceiling)", got)
	}
}

// TestBuildIgnoresCeilingAboveDefault_spec_4_6_1 confirms a ceiling at
// or above the default leaves the 120s default in force (the ceiling is
// a cap, not a setter).
func TestBuildIgnoresCeilingAboveDefault_spec_4_6_1(t *testing.T) {
	for _, ceiling := range []int64{120, 240} {
		in := inputs()
		in.MaxTerminationGraceSeconds = ptr.To(ceiling)
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(ceiling=%d): %v", ceiling, err)
		}
		if got := pod.Spec.TerminationGracePeriodSeconds; got == nil || *got != 120 {
			t.Errorf("ceiling=%d: grace = %v, want 120", ceiling, got)
		}
	}
}

// TestBuildAddsPreStopDrainHook_spec_4_6_1 confirms every agent
// container carries the §4.6.1 preStop checkpoint-drain hook with a
// timeout below the grace period (so the kubelet does not cut the drain
// short).
func TestBuildAddsPreStopDrainHook_spec_4_6_1(t *testing.T) {
	cases := []struct {
		model     string
		container string
	}{
		{"", "adapter"},
		{"embedded", "runtime"},
	}
	for _, tc := range cases {
		in := inputs()
		in.DeploymentModel = tc.model
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(%q): %v", tc.model, err)
		}
		c := container(t, pod, tc.container)
		if c.Lifecycle == nil || c.Lifecycle.PreStop == nil || c.Lifecycle.PreStop.Exec == nil {
			t.Fatalf("model %q: %s container has no preStop exec hook", tc.model, tc.container)
		}
		cmd := c.Lifecycle.PreStop.Exec.Command
		if len(cmd) < 2 || cmd[0] != "lenny-adapter" || cmd[1] != "prestop" {
			t.Fatalf("model %q: preStop command = %v, want lenny-adapter prestop", tc.model, cmd)
		}
		var sawTimeout bool
		for _, arg := range cmd {
			if strings.HasPrefix(arg, "--timeout=") {
				sawTimeout = true
				// 120s grace - 10s margin = 110s.
				if arg != "--timeout=110s" {
					t.Errorf("model %q: preStop timeout arg = %q, want --timeout=110s", tc.model, arg)
				}
			}
		}
		if !sawTimeout {
			t.Errorf("model %q: preStop command %v carries no --timeout", tc.model, cmd)
		}
	}
}
