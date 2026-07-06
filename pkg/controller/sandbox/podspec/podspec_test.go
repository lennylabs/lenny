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

// TestBuildDeclaresCredReadersSupplementalGroups_spec_13_1 asserts the
// agent pod declares the lenny-cred-readers GID in the pod-level
// supplementalGroups list — the §13.1 line 25 explicit membership the
// cross-UID credential-delivery path requires, rather than relying on
// the kubelet's implicit fsGroup-to-supplementary-group propagation
// (F-13.1.11).
func TestBuildDeclaresCredReadersSupplementalGroups_spec_13_1(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	sc := pod.Spec.SecurityContext
	if sc == nil {
		t.Fatal("pod has no securityContext")
	}
	found := false
	for _, g := range sc.SupplementalGroups {
		if g == podspec.CredReadersGID {
			found = true
		}
	}
	if !found {
		t.Errorf("pod supplementalGroups = %v, want it to include the lenny-cred-readers GID %d (§13.1 line 25)", sc.SupplementalGroups, podspec.CredReadersGID)
	}
}

// TestBuildDisablesDefaultSATokenAutomount_spec_13_1 asserts §13.1 line
// 12 ("No standing credentials"): the agent pod disables the kubelet's
// default ServiceAccount-token automount so the long-lived
// cluster-audience token is not mounted; the only token present is the
// §10.3 audience-bound projected token (F-13.1.9).
func TestBuildDisablesDefaultSATokenAutomount_spec_13_1(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Errorf("automountServiceAccountToken = %v, want false (§13.1 no standing credentials)", pod.Spec.AutomountServiceAccountToken)
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

// TestBuildAdapterCarriesPodNameDownwardAPIEnv_spec_4_7_5_2 asserts the
// §4.7/§5.2 adapter carries a POD_NAME env sourced from the Downward API
// fieldRef metadata.name in both deployment models. This env is the
// adapter's pod identity for every ReportSessionScrub and ReportPodScrub.
// An absent, misnamed, or wrong-fieldRef env yields an empty cached podID
// that the gateway rejects InvalidArgument, silently disabling
// sessions_served advancement, the leak ledger, and the whole-pod scrub
// trigger. In the sidecar model the reporter is the "adapter" container;
// in the embedded model the single "runtime" container is the adapter and
// the gateway-facing process, so it must carry the same env. The test
// pins the exact fieldRef on both models so a regression on either is
// caught at build time rather than at runtime. spec: 4.7 (adapter pod
// identity), 5.2 (ReportSessionScrub). F-5.2.31.
func TestBuildAdapterCarriesPodNameDownwardAPIEnv_spec_4_7_5_2(t *testing.T) {
	// The reporter container differs by model: the sidecar model splits
	// the adapter out, the embedded model links it into the runtime.
	cases := []struct{ model, reporterContainer string }{
		{"sidecar", "adapter"},
		{"embedded", "runtime"},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			in := inputs()
			in.DeploymentModel = tc.model
			pod, err := podspec.Build(in)
			if err != nil {
				t.Fatalf("Build(%s): %v", tc.model, err)
			}
			assertPodNameDownwardAPIEnv(t, container(t, pod, tc.reporterContainer))
		})
	}
}

// assertPodNameDownwardAPIEnv fails the test unless the container carries
// exactly one POD_NAME env sourced from the Downward API fieldRef
// metadata.name, the §4.7/§5.2 adapter pod-identity contract.
func assertPodNameDownwardAPIEnv(t *testing.T, c corev1.Container) {
	t.Helper()
	var podNameEnv corev1.EnvVar
	var found bool
	for _, e := range c.Env {
		if e.Name == podspec.PodNameEnvVar {
			podNameEnv = e
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("%q container must carry a %q env (§4.7/§5.2 pod identity)", c.Name, podspec.PodNameEnvVar)
	}
	if podNameEnv.Value != "" {
		t.Errorf("%s must source from the Downward API, not a literal value %q", podspec.PodNameEnvVar, podNameEnv.Value)
	}
	if podNameEnv.ValueFrom == nil || podNameEnv.ValueFrom.FieldRef == nil {
		t.Fatalf("%s must set ValueFrom.FieldRef (Downward API)", podspec.PodNameEnvVar)
	}
	if got := podNameEnv.ValueFrom.FieldRef.FieldPath; got != "metadata.name" {
		t.Errorf("%s fieldRef.fieldPath = %q, want metadata.name", podspec.PodNameEnvVar, got)
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

// TestBuildAppliesTerminationGraceBaseOverride_spec_5_2 confirms the
// deployer-set TerminationGraceSeconds replaces the 120s default base
// (§5.2 line 516) and that the ceiling still clamps the override down.
func TestBuildAppliesTerminationGraceBaseOverride_spec_5_2(t *testing.T) {
	cases := []struct {
		name    string
		base    *int64
		ceiling *int64
		want    int64
	}{
		{"base above default", ptr.To(int64(840)), nil, 840},
		{"base below default", ptr.To(int64(45)), nil, 45},
		{"base clamped by ceiling", ptr.To(int64(840)), ptr.To(int64(600)), 600},
		{"base below ceiling", ptr.To(int64(300)), ptr.To(int64(600)), 300},
		{"zero base ignored", ptr.To(int64(0)), nil, 120},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := inputs()
			in.TerminationGraceSeconds = tc.base
			in.MaxTerminationGraceSeconds = tc.ceiling
			pod, err := podspec.Build(in)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got := pod.Spec.TerminationGracePeriodSeconds; got == nil || *got != tc.want {
				t.Fatalf("terminationGracePeriodSeconds = %v, want %d", got, tc.want)
			}
		})
	}
}

// TestBuildPreConnectFloorsTerminationGrace_spec_6_1_67 confirms the §6.1
// line 67 SDK-warm grace floor: a preConnect pod's
// terminationGracePeriodSeconds is at least LENNY_DEMOTE_TIMEOUT_SECONDS
// (default 5s) + 5s = 10s so the adapter can run its bounded DemoteSDK
// teardown on SIGTERM. The floor takes precedence over a lower §5.2 base or
// ceiling because abandoning the SDK mid-connection leaks credentials.
func TestBuildPreConnectFloorsTerminationGrace_spec_6_1_67(t *testing.T) {
	cases := []struct {
		name       string
		preConnect bool
		base       *int64
		ceiling    *int64
		want       int64
	}{
		{"preconnect default grace unaffected", true, nil, nil, 120},
		{"preconnect low base floored", true, ptr.To(int64(4)), nil, 10},
		{"preconnect low ceiling floored", true, nil, ptr.To(int64(6)), 10},
		{"preconnect base at floor", true, ptr.To(int64(10)), nil, 10},
		{"preconnect base above floor unchanged", true, ptr.To(int64(45)), nil, 45},
		{"pod-warm low base not floored", false, ptr.To(int64(4)), nil, 4},
		{"pod-warm low ceiling not floored", false, nil, ptr.To(int64(6)), 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := inputs()
			in.PreConnect = tc.preConnect
			in.TerminationGraceSeconds = tc.base
			in.MaxTerminationGraceSeconds = tc.ceiling
			pod, err := podspec.Build(in)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got := pod.Spec.TerminationGracePeriodSeconds; got == nil || *got != tc.want {
				t.Fatalf("terminationGracePeriodSeconds = %v, want %d", got, tc.want)
			}
		})
	}
}

// TestBuildCapsDevShm_spec_6_4 confirms the §6.4 line 420 "/dev/shm is
// limited to 64MB" invariant: a memory-backed emptyDir with an explicit
// 64Mi SizeLimit, mounted at /dev/shm in every agent container.
func TestBuildCapsDevShm_spec_6_4(t *testing.T) {
	for _, model := range []string{"", "embedded"} {
		in := inputs()
		in.DeploymentModel = model
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(%q): %v", model, err)
		}
		var vol *corev1.Volume
		for i := range pod.Spec.Volumes {
			if pod.Spec.Volumes[i].Name == "dshm" {
				vol = &pod.Spec.Volumes[i]
			}
		}
		if vol == nil {
			t.Fatalf("model %q: pod has no dshm volume", model)
		}
		ed := vol.VolumeSource.EmptyDir
		if ed == nil || ed.Medium != corev1.StorageMediumMemory {
			t.Fatalf("model %q: dshm volume must be a memory-backed emptyDir, got %+v", model, ed)
		}
		if ed.SizeLimit == nil || ed.SizeLimit.String() != "64Mi" {
			t.Fatalf("model %q: dshm SizeLimit = %v, want 64Mi", model, ed.SizeLimit)
		}
		for _, c := range pod.Spec.Containers {
			var mounted bool
			for _, m := range c.VolumeMounts {
				if m.Name == "dshm" && m.MountPath == "/dev/shm" {
					mounted = true
				}
			}
			if !mounted {
				t.Errorf("model %q: container %q does not mount dshm at /dev/shm", model, c.Name)
			}
		}
	}
}

// findVolume returns the named pod volume, or fails the test.
func findVolume(t *testing.T, pod *corev1.Pod, name string) corev1.Volume {
	t.Helper()
	for _, v := range pod.Spec.Volumes {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("pod has no %q volume", name)
	return corev1.Volume{}
}

// mountsPath reports whether a container mounts the named volume at path.
func mountsPath(c corev1.Container, name, path string) bool {
	for _, m := range c.VolumeMounts {
		if m.Name == name && m.MountPath == path {
			return true
		}
	}
	return false
}

// TestBuildMountsSessionsAndArtifacts_spec_6_4 confirms the §6.4 lines
// 380-381 / §13.1 line 10 filesystem layout: /sessions is a memory-backed
// (tmpfs) volume, /artifacts is disk-backed, and every agent container
// mounts both so a runtime write does not land on the read-only root
// filesystem (EROFS).
func TestBuildMountsSessionsAndArtifacts_spec_6_4(t *testing.T) {
	for _, model := range []string{"", "embedded"} {
		in := inputs()
		in.DeploymentModel = model
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(%q): %v", model, err)
		}

		// spec: §6.4 line 380 — /sessions is tmpfs (Memory medium).
		sessions := findVolume(t, pod, "sessions").VolumeSource.EmptyDir
		if sessions == nil || sessions.Medium != corev1.StorageMediumMemory {
			t.Errorf("model %q: sessions volume must be a memory-backed emptyDir, got %+v", model, sessions)
		}
		// spec: §6.4 line 414 — /artifacts is disk-backed (no Memory medium).
		artifacts := findVolume(t, pod, "artifacts").VolumeSource.EmptyDir
		if artifacts == nil || artifacts.Medium != "" {
			t.Errorf("model %q: artifacts volume must be a disk-backed emptyDir, got %+v", model, artifacts)
		}

		for _, c := range pod.Spec.Containers {
			if !mountsPath(c, "sessions", "/sessions") {
				t.Errorf("model %q: container %q does not mount sessions at /sessions", model, c.Name)
			}
			if !mountsPath(c, "artifacts", "/artifacts") {
				t.Errorf("model %q: container %q does not mount artifacts at /artifacts", model, c.Name)
			}
		}
	}
}

// TestBuildCapsTmpfs_spec_6_4 confirms the §6.4 line 413 recommended
// tmpfs size caps: /sessions and /tmp carry a 256Mi SizeLimit so tmpfs
// growth has a predictable OOM boundary. The disk-backed /workspace and
// /artifacts volumes carry no Memory medium.
func TestBuildCapsTmpfs_spec_6_4(t *testing.T) {
	for _, model := range []string{"", "embedded"} {
		in := inputs()
		in.DeploymentModel = model
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(%q): %v", model, err)
		}
		for _, name := range []string{"sessions", "tmp"} {
			ed := findVolume(t, pod, name).VolumeSource.EmptyDir
			if ed == nil || ed.Medium != corev1.StorageMediumMemory {
				t.Fatalf("model %q: %q must be a memory-backed emptyDir, got %+v", model, name, ed)
			}
			if ed.SizeLimit == nil || ed.SizeLimit.String() != "256Mi" {
				t.Errorf("model %q: %q SizeLimit = %v, want 256Mi", model, name, ed.SizeLimit)
			}
		}
		for _, name := range []string{"workspace", "artifacts"} {
			ed := findVolume(t, pod, name).VolumeSource.EmptyDir
			if ed == nil || ed.Medium != "" {
				t.Errorf("model %q: %q must be a disk-backed emptyDir, got %+v", model, name, ed)
			}
		}
	}
}

// TestBuildMasksProc_spec_6_4 confirms the §6.4 line 420 "procfs and
// sysfs are masked/read-only" invariant: each agent container sets the
// default masked /proc mount explicitly.
func TestBuildMasksProc_spec_6_4(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, c := range pod.Spec.Containers {
		sc := c.SecurityContext
		if sc == nil || sc.ProcMount == nil || *sc.ProcMount != corev1.DefaultProcMount {
			t.Errorf("container %q: procMount = %v, want Default (masked /proc)", c.Name, sc.ProcMount)
		}
	}
}

// TestBuildCredentialsVolumeIsTmpfs_spec_6_4 confirms the §13.1 / §6.4
// invariant that the credential volume is memory-backed (tmpfs) so the
// short-lived credential file never persists to a node disk. The mount
// is read-only into the runtime container in the sidecar model (already
// asserted by TestBuildMountsTheWorkspaceAndCredentialVolumes); this
// test pins the volume backing.
func TestBuildCredentialsVolumeIsTmpfs_spec_6_4(t *testing.T) {
	for _, model := range []string{"", "embedded"} {
		in := inputs()
		in.DeploymentModel = model
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(%q): %v", model, err)
		}
		ed := findVolume(t, pod, podspec.CredVolumeName).VolumeSource.EmptyDir
		if ed == nil || ed.Medium != corev1.StorageMediumMemory {
			t.Errorf("model %q: credentials volume must be memory-backed (tmpfs), got %+v", model, ed)
		}
	}
}

// TestBuildMountsTheWorkspaceVolume_spec_6_4 confirms the §6.4 line 377
// "/workspace" mount: a disk-backed emptyDir mounted at /workspace on
// every agent container, so the §7.4 staging→current promotion and the
// FinalizeWorkspace handoff are within one volume.
func TestBuildMountsTheWorkspaceVolume_spec_6_4(t *testing.T) {
	for _, model := range []string{"", "embedded"} {
		in := inputs()
		in.DeploymentModel = model
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(%q): %v", model, err)
		}
		for _, c := range pod.Spec.Containers {
			if !mountsPath(c, "workspace", "/workspace") {
				t.Errorf("model %q: container %q does not mount workspace at /workspace", model, c.Name)
			}
		}
	}
}

// TestBuildMountsSharedReadOnly_spec_6_4 confirms the §6.4 line 409
// /workspace/shared layout: a separate disk-backed emptyDir is always
// present, mounted read-only on the runtime container (the EROFS write
// boundary) and read-write on the adapter container (the populator). The
// volume is mounted even with no sharedAssets configured, so the runtime
// cannot use the path as writable scratch space.
func TestBuildMountsSharedReadOnly_spec_6_4(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// spec: §6.4 line 409 — a separate disk-backed emptyDir backs the mount.
	shared := findVolume(t, pod, "shared").VolumeSource.EmptyDir
	if shared == nil || shared.Medium != "" {
		t.Errorf("shared volume must be a disk-backed emptyDir, got %+v", shared)
	}
	// spec: §6.4 line 409 — read-only on the runtime container (EROFS).
	if !mountsPathReadOnly(container(t, pod, "runtime"), "shared", "/workspace/shared", true) {
		t.Error("runtime container must mount shared at /workspace/shared read-only")
	}
	// The adapter populates the tree at warm time, so its mount is writable.
	if !mountsPathReadOnly(container(t, pod, "adapter"), "shared", "/workspace/shared", false) {
		t.Error("adapter container must mount shared at /workspace/shared read-write")
	}
}

// TestBuildEmbeddedMountsShared_spec_6_4 confirms the embedded model
// mounts /workspace/shared read-write on its single container, which is
// both adapter and runtime and therefore the populator. The kernel-level
// EROFS boundary is a sidecar-model property.
func TestBuildEmbeddedMountsShared_spec_6_4(t *testing.T) {
	in := inputs()
	in.DeploymentModel = "embedded"
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build(embedded): %v", err)
	}
	if _, err := findVolumeErr(pod, "shared"); err != nil {
		t.Fatal("embedded pod is missing the shared volume")
	}
	if !mountsPathReadOnly(container(t, pod, "runtime"), "shared", "/workspace/shared", false) {
		t.Error("embedded runtime must mount shared at /workspace/shared read-write (it is the populator)")
	}
}

// TestBuildWiresSharedAssetsArgs_spec_6_4 confirms the §6.4 line 409
// adapter wiring: --shared-assets-dir is always passed, and the inline
// asset set rides --shared-assets only when the Runtime declares any.
func TestBuildWiresSharedAssetsArgs_spec_6_4(t *testing.T) {
	t.Run("no assets passes only the dir flag", func(t *testing.T) {
		pod, err := podspec.Build(inputs())
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		args := container(t, pod, "adapter").Args
		if !hasArg(args, "--shared-assets-dir=/workspace/shared") {
			t.Errorf("adapter args %v must set --shared-assets-dir", args)
		}
		if hasArgPrefix(args, "--shared-assets=") {
			t.Errorf("adapter args %v must omit --shared-assets when none configured", args)
		}
	})
	t.Run("assets ride the shared-assets flag", func(t *testing.T) {
		in := inputs()
		in.SharedAssetsArg = "ZW5jb2RlZA==" // opaque encoded payload
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		args := container(t, pod, "adapter").Args
		if !hasArg(args, "--shared-assets=ZW5jb2RlZA==") {
			t.Errorf("adapter args %v must carry the encoded --shared-assets payload", args)
		}
	})
}

// mountsPathReadOnly reports whether c mounts name at path with the
// expected ReadOnly setting.
func mountsPathReadOnly(c corev1.Container, name, path string, readOnly bool) bool {
	for _, m := range c.VolumeMounts {
		if m.Name == name && m.MountPath == path {
			return m.ReadOnly == readOnly
		}
	}
	return false
}

func findVolumeErr(pod *corev1.Pod, name string) (corev1.Volume, error) {
	for _, v := range pod.Spec.Volumes {
		if v.Name == name {
			return v, nil
		}
	}
	return corev1.Volume{}, fmt.Errorf("no %q volume", name)
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func hasArgPrefix(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

// TestBuildInjectsT4NodeIsolation_spec_6_4 confirms the §6.4 lines 416-419
// dedicated-node injection: a Runtime declared at `workspaceTier: T4`
// produces a pod carrying the `lenny.dev/workspace-tier: t4` label, the T4
// nodeSelector, and the T4 NoSchedule toleration so the
// lenny-t4-node-isolation admission webhook admits it onto a T4 node pool.
func TestBuildInjectsT4NodeIsolation_spec_6_4(t *testing.T) {
	for _, model := range []string{"", "embedded"} {
		in := inputs()
		in.DeploymentModel = model
		in.WorkspaceTier = "T4"
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(%q): %v", model, err)
		}
		if got := pod.Labels["lenny.dev/workspace-tier"]; got != "t4" {
			t.Errorf("model %q: pod label lenny.dev/workspace-tier = %q, want %q", model, got, "t4")
		}
		if got := pod.Spec.NodeSelector["lenny.dev/workspace-tier"]; got != "t4" {
			t.Errorf("model %q: nodeSelector lenny.dev/workspace-tier = %q, want %q", model, got, "t4")
		}
		var found bool
		for _, tol := range pod.Spec.Tolerations {
			if tol.Key == "lenny.dev/workspace-tier" &&
				tol.Operator == corev1.TolerationOpEqual &&
				tol.Value == "t4" &&
				tol.Effect == corev1.TaintEffectNoSchedule {
				found = true
			}
		}
		if !found {
			t.Errorf("model %q: tolerations = %+v, want one lenny.dev/workspace-tier=t4:NoSchedule entry",
				model, pod.Spec.Tolerations)
		}
	}
}

// TestBuildLeavesNonT4PodsAlone_spec_6_4 confirms a non-T4 Runtime (T3
// default) produces a pod with no T4 label, no T4 nodeSelector, and no T4
// toleration. The webhook rejects a non-T4 pod that carries any of those —
// see pkg/admission/t4_node_isolation — so the injection must be strictly
// gated on the WorkspaceTier value.
func TestBuildLeavesNonT4PodsAlone_spec_6_4(t *testing.T) {
	for _, tier := range []string{"", "T3"} {
		in := inputs()
		in.WorkspaceTier = tier
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(tier=%q): %v", tier, err)
		}
		if _, ok := pod.Labels["lenny.dev/workspace-tier"]; ok {
			t.Errorf("tier %q: pod carries lenny.dev/workspace-tier label, want absent", tier)
		}
		if _, ok := pod.Spec.NodeSelector["lenny.dev/workspace-tier"]; ok {
			t.Errorf("tier %q: nodeSelector carries lenny.dev/workspace-tier, want absent", tier)
		}
		for _, tol := range pod.Spec.Tolerations {
			if tol.Key == "lenny.dev/workspace-tier" {
				t.Errorf("tier %q: pod tolerates lenny.dev/workspace-tier, want absent (got %+v)", tier, tol)
			}
		}
	}
}

// TestBuildT4InjectionPreservesDeployerNodeSelector_spec_6_4 confirms the
// T4 injection is additive: a Runtime declared at T4 with a
// deployer-supplied label on the Pod retains its other labels and the T4
// label is merged in. The same additive property must hold for the
// nodeSelector and toleration — the latter must not duplicate when applied
// twice.
func TestBuildT4InjectionPreservesDeployerNodeSelector_spec_6_4(t *testing.T) {
	in := inputs()
	in.Labels = map[string]string{"lenny.dev/pool": "claude-worker"}
	in.WorkspaceTier = "T4"
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if pod.Labels["lenny.dev/pool"] != "claude-worker" {
		t.Errorf("pre-existing label dropped after T4 injection: labels = %v", pod.Labels)
	}
	if pod.Labels["lenny.dev/workspace-tier"] != "t4" {
		t.Errorf("T4 label missing after merge: labels = %v", pod.Labels)
	}

	// Idempotence: a second injection (e.g., a re-reconcile that calls Build
	// again on the same Inputs) must not produce a duplicate toleration.
	pod2, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build (second call): %v", err)
	}
	var count int
	for _, tol := range pod2.Spec.Tolerations {
		if tol.Key == "lenny.dev/workspace-tier" && tol.Value == "t4" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("T4 toleration count after second Build = %d, want exactly 1", count)
	}
}

// TestBuildInjectsKataNodeIsolation_spec_17_2 confirms a microvm (Kata)
// pod carries the §17.2 lines 99-101 control 2 (required node affinity on
// lenny.dev/node-pool=kata) and control 3 (NoSchedule toleration for the
// lenny.dev/isolation=kata taint) in both deployment models.
func TestBuildInjectsKataNodeIsolation_spec_17_2(t *testing.T) {
	for _, model := range []string{"", "embedded"} {
		in := inputs()
		in.DeploymentModel = model
		in.IsolationProfile = "microvm"
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(%q): %v", model, err)
		}
		req := requiredNodeAffinity(t, pod)
		var affinityFound bool
		for _, term := range req.NodeSelectorTerms {
			for _, e := range term.MatchExpressions {
				if e.Key == podspec.KataNodePoolLabelKey &&
					e.Operator == corev1.NodeSelectorOpIn &&
					len(e.Values) == 1 && e.Values[0] == podspec.KataNodePoolValue {
					affinityFound = true
				}
			}
		}
		if !affinityFound {
			t.Errorf("model %q: required node affinity = %+v, want %s In [%s]",
				model, req.NodeSelectorTerms, podspec.KataNodePoolLabelKey, podspec.KataNodePoolValue)
		}
		var tolFound bool
		for _, tol := range pod.Spec.Tolerations {
			if tol.Key == podspec.KataIsolationTaintKey &&
				tol.Operator == corev1.TolerationOpEqual &&
				tol.Value == podspec.KataIsolationTaintValue &&
				tol.Effect == corev1.TaintEffectNoSchedule {
				tolFound = true
			}
		}
		if !tolFound {
			t.Errorf("model %q: tolerations = %+v, want one %s=%s:NoSchedule entry",
				model, pod.Spec.Tolerations, podspec.KataIsolationTaintKey, podspec.KataIsolationTaintValue)
		}
	}
}

// TestBuildLeavesNonKataPodsAlone_spec_17_2 confirms the standard and
// sandboxed profiles produce a pod with no Kata node affinity and no Kata
// toleration: the §17.2 hard scheduling constraints must be strictly gated
// on the microvm profile so runc/gVisor pods are not pinned to Kata nodes.
func TestBuildLeavesNonKataPodsAlone_spec_17_2(t *testing.T) {
	for _, profile := range []string{"standard", "sandboxed"} {
		in := inputs()
		in.IsolationProfile = profile
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(profile=%q): %v", profile, err)
		}
		if pod.Spec.Affinity != nil && pod.Spec.Affinity.NodeAffinity != nil &&
			pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
			t.Errorf("profile %q: pod carries required node affinity, want none", profile)
		}
		for _, tol := range pod.Spec.Tolerations {
			if tol.Key == podspec.KataIsolationTaintKey {
				t.Errorf("profile %q: pod tolerates %s, want absent (got %+v)",
					profile, podspec.KataIsolationTaintKey, tol)
			}
		}
	}
}

// TestBuildKataInjectionIsIdempotent_spec_17_2 confirms a re-reconcile
// (a second Build on the same Inputs) does not accumulate duplicate Kata
// node-affinity requirements or tolerations.
func TestBuildKataInjectionIsIdempotent_spec_17_2(t *testing.T) {
	in := inputs()
	in.IsolationProfile = "microvm"
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Build does not mutate Inputs, so a second call models a re-reconcile.
	pod2, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build (second call): %v", err)
	}
	for _, p := range []*corev1.Pod{pod, pod2} {
		req := requiredNodeAffinity(t, p)
		var reqCount int
		for _, term := range req.NodeSelectorTerms {
			for _, e := range term.MatchExpressions {
				if e.Key == podspec.KataNodePoolLabelKey {
					reqCount++
				}
			}
		}
		if reqCount != 1 {
			t.Errorf("Kata node-affinity requirement count = %d, want exactly 1", reqCount)
		}
		var tolCount int
		for _, tol := range p.Spec.Tolerations {
			if tol.Key == podspec.KataIsolationTaintKey {
				tolCount++
			}
		}
		if tolCount != 1 {
			t.Errorf("Kata toleration count = %d, want exactly 1", tolCount)
		}
	}
}

// requiredNodeAffinity returns the pod's
// requiredDuringSchedulingIgnoredDuringExecution node selector, failing the
// test if any link in the chain is nil.
func requiredNodeAffinity(t *testing.T, pod *corev1.Pod) *corev1.NodeSelector {
	t.Helper()
	if pod.Spec.Affinity == nil || pod.Spec.Affinity.NodeAffinity == nil ||
		pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		t.Fatalf("pod has no required node affinity: %+v", pod.Spec.Affinity)
	}
	return pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
}
