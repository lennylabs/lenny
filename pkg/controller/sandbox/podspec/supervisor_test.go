// SPDX-License-Identifier: MIT

package podspec_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

// supervisedInputs is a supervised sidecar pod: a runtime command, the
// supervisor image, and a launch mode.
func supervisedInputs() podspec.Inputs {
	in := inputs()
	in.RuntimeCommand = []string{"/usr/local/bin/agent", "--serve"}
	in.RuntimeEnv = []podspec.EnvPair{{Name: "PATH", Value: "/opt/venv/bin:/usr/bin"}, {Name: "MODE", Value: "x"}}
	in.SupervisorImage = "ghcr.io/lennylabs/lenny-supervisor@sha256:" + strings.Repeat("a", 64)
	in.LaunchMode = podspec.LaunchSession
	return in
}

func buildOK(t *testing.T, in podspec.Inputs) *corev1.Pod {
	t.Helper()
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return pod
}

func hasFlagPrefix(args []string, prefix string) bool {
	return slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, prefix) })
}

// spec: 4.7.10 (Runtime supervisor; Runtime supervisor, Environment), 13.1 (Runtime supervisor volume), 28.5.3 (CH-SUPERVISE)
func TestBuildSupervisedPodRendersSupervisorStageAndCommand_spec_4_7_10(t *testing.T) {
	in := supervisedInputs()
	pod := buildOK(t, in)

	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("init containers = %d, want the one stage container", len(pod.Spec.InitContainers))
	}
	stage := pod.Spec.InitContainers[0]
	if stage.Name != "lenny-supervisor-stage" || stage.Image != in.SupervisorImage {
		t.Errorf("stage container = %s/%s", stage.Name, stage.Image)
	}
	if !slices.Equal(stage.Args, []string{"stage", "/stage"}) || len(stage.Command) != 0 {
		t.Errorf("stage argv = %v %v, want the image entrypoint with stage /stage", stage.Command, stage.Args)
	}
	if stage.RestartPolicy != nil {
		t.Errorf("stage restartPolicy = %v, want unset (run to completion)", *stage.RestartPolicy)
	}
	if uid := *stage.SecurityContext.RunAsUser; uid == podspec.AdapterUID || uid == podspec.AgentUID || uid != podspec.SupervisorStageUID {
		t.Errorf("stage UID = %d, want the dedicated stage UID %d", uid, podspec.SupervisorStageUID)
	}
	if len(stage.VolumeMounts) != 1 || stage.VolumeMounts[0].Name != podspec.SupervisorVolumeName || stage.VolumeMounts[0].ReadOnly {
		t.Errorf("stage mounts = %+v, want one read-write supervisor volume mount", stage.VolumeMounts)
	}

	var vol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == podspec.SupervisorVolumeName {
			vol = &pod.Spec.Volumes[i]
		}
	}
	if vol == nil || vol.EmptyDir == nil {
		t.Fatalf("supervisor volume missing or not an emptyDir: %+v", vol)
	}

	rt := container(t, pod, "runtime")
	want := []string{
		"/lenny-supervisor/lenny-supervisor", "run", "--socket", "@lenny-supervise",
		"--adapter-uid", "65532",
		"--env", "PATH=/opt/venv/bin:/usr/bin", "--env", "MODE=x",
		"--env", "LENNY_ADAPTER_SOCKET=@lenny-runtime",
		"--", "/usr/local/bin/agent", "--serve",
	}
	if !slices.Equal(rt.Command, want) {
		t.Errorf("runtime command =\n %q\nwant\n %q", rt.Command, want)
	}
	if len(rt.Env) != 0 || len(rt.Args) != 0 {
		t.Errorf("runtime env/args = %v %v, want none (the supervisor clears its environment)", rt.Env, rt.Args)
	}
	found := false
	for _, m := range rt.VolumeMounts {
		if m.Name == podspec.SupervisorVolumeName {
			found = true
			if !m.ReadOnly || m.MountPath != "/lenny-supervisor" {
				t.Errorf("runtime supervisor mount = %+v, want read-only at /lenny-supervisor", m)
			}
		}
	}
	if !found {
		t.Error("runtime container does not mount the supervisor volume")
	}
	if rt.ReadinessProbe != nil {
		t.Error("runtime container carries a readiness probe; only the adapter does")
	}

	ad := container(t, pod, "adapter")
	for _, f := range []string{"--supervise-socket=@lenny-supervise", "--runtime-process=keep", "--launch-mode=session"} {
		if !slices.Contains(ad.Args, f) {
			t.Errorf("adapter args %v lack %s", ad.Args, f)
		}
	}
}

// spec: 4.7.10 (Runtime supervisor, Environment), 4.7.11 (item 8)
func TestBuildSupervisorAdapterUIDFollowsNonceOnlyMode_spec_4_7_10(t *testing.T) {
	in := supervisedInputs()
	in.RequireSoPeercred = ptr.To(false)
	rt := container(t, buildOK(t, in), "runtime")
	if slices.Contains(rt.Command, "--adapter-uid") {
		t.Errorf("nonce-only pod renders --adapter-uid: %v", rt.Command)
	}
	in.RequireSoPeercred = ptr.To(true)
	in.AdapterUID = 4100
	rt = container(t, buildOK(t, in), "runtime")
	i := slices.Index(rt.Command, "--adapter-uid")
	if i < 0 || rt.Command[i+1] != "4100" {
		t.Errorf("enforcing pod command %v lacks --adapter-uid 4100", rt.Command)
	}
}

// spec: 4.7.10 (Runtime supervisor, Environment)
func TestBuildSupervisorEnvOrderRuntimeEnvThenPlatform_spec_4_7_10(t *testing.T) {
	in := supervisedInputs()
	// A Runtime env pair with the platform name is rendered first, so the
	// platform pair after it is the one the supervisor keeps.
	in.RuntimeEnv = []podspec.EnvPair{{Name: "LENNY_ADAPTER_SOCKET", Value: "@spoof"}}
	cmd := container(t, buildOK(t, in), "runtime").Command
	spoof := slices.Index(cmd, "LENNY_ADAPTER_SOCKET=@spoof")
	platform := slices.Index(cmd, "LENNY_ADAPTER_SOCKET=@lenny-runtime")
	if spoof < 0 || platform < 0 || spoof > platform {
		t.Errorf("env order wrong in %v: runtime env must precede the platform variables", cmd)
	}
	in.RuntimeEnv = nil
	cmd = container(t, buildOK(t, in), "runtime").Command
	if n := strings.Count(strings.Join(cmd, " "), "--env"); n != 1 {
		t.Errorf("no runtime env renders %d --env pairs, want the one platform pair", n)
	}
}

// spec: 4.7.10 (Runtime supervisor, Launch; Runtime supervisor, SDK-warm), 5.2 (Runtime-process lifetime)
func TestBuildSupervisedAdapterFlagsPerLaunchModeAndLifetime_spec_4_7_10(t *testing.T) {
	cases := []struct {
		mode, process, wantProcess string
	}{
		{podspec.LaunchSession, "", "keep"},
		{podspec.LaunchSession, "keep", "keep"},
		{podspec.LaunchSession, "restart", "restart"},
		{podspec.LaunchService, "keep", "keep"},
		{podspec.LaunchPreconnectSDKWarm, "", "keep"},
		{podspec.LaunchPreconnectPodWarm, "keep", "keep"},
	}
	for _, c := range cases {
		in := supervisedInputs()
		in.LaunchMode, in.RuntimeProcess = c.mode, c.process
		pod := buildOK(t, in)
		args := container(t, pod, "adapter").Args
		if !slices.Contains(args, "--launch-mode="+c.mode) || !slices.Contains(args, "--runtime-process="+c.wantProcess) {
			t.Errorf("%s/%q: adapter args %v", c.mode, c.process, args)
		}
		if got := pod.Labels["lenny.dev/runtime-process"]; got != c.wantProcess {
			t.Errorf("%s/%q: label = %q, want %q", c.mode, c.process, got, c.wantProcess)
		}
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Timing), 17.8.1
func TestBuildSupervisedTimingFlagsRenderOnlyWhenSet_spec_28_5_3(t *testing.T) {
	in := supervisedInputs()
	args := container(t, buildOK(t, in), "adapter").Args
	for _, f := range []string{"--supervise-ping-interval=", "--supervise-ping-timeout=", "--supervise-wait-timeout=", "--warm-up-timeout="} {
		if hasFlagPrefix(args, f) {
			t.Errorf("unset timing renders %s: %v", f, args)
		}
	}
	in.SuperviseTiming = podspec.SuperviseTiming{
		PingInterval: 3 * time.Second, PingTimeout: 4 * time.Second,
		WaitTimeout: 6 * time.Minute, WarmUpTimeout: 90 * time.Second,
	}
	args = container(t, buildOK(t, in), "adapter").Args
	for _, f := range []string{"--supervise-ping-interval=3s", "--supervise-ping-timeout=4s", "--supervise-wait-timeout=6m0s", "--warm-up-timeout=1m30s"} {
		if !slices.Contains(args, f) {
			t.Errorf("set timing lacks %s: %v", f, args)
		}
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Timing), 17.8.1
func TestSuperviseTimingValidateRefusesNonPositive_spec_28_5_3(t *testing.T) {
	ok := podspec.SuperviseTiming{PingInterval: 1, PingTimeout: 1, WaitTimeout: 1, WarmUpTimeout: 1}
	if err := ok.Validate(); err != nil {
		t.Fatalf("positive timing refused: %v", err)
	}
	fields := []func(*podspec.SuperviseTiming, time.Duration){
		func(t *podspec.SuperviseTiming, d time.Duration) { t.PingInterval = d },
		func(t *podspec.SuperviseTiming, d time.Duration) { t.PingTimeout = d },
		func(t *podspec.SuperviseTiming, d time.Duration) { t.WaitTimeout = d },
		func(t *podspec.SuperviseTiming, d time.Duration) { t.WarmUpTimeout = d },
	}
	for i, set := range fields {
		for _, d := range []time.Duration{0, -time.Second} {
			tm := ok
			set(&tm, d)
			if err := tm.Validate(); !errors.Is(err, podspec.ErrNonPositiveTiming) {
				t.Errorf("field %d = %s: err = %v, want ErrNonPositiveTiming", i, d, err)
			}
		}
	}
}

// spec: 4.7.10 (Runtime supervisor), 5.2 (Runtime-process lifetime), 13.1 (Runtime supervisor volume; Container identity)
func TestBuildRefusesSupervisedInputsTheAdapterWouldRefuse_spec_4_7_10(t *testing.T) {
	cases := map[string]func(*podspec.Inputs){
		"no supervisor image":    func(in *podspec.Inputs) { in.SupervisorImage = "" },
		"relative command":       func(in *podspec.Inputs) { in.RuntimeCommand = []string{"agent"} },
		"empty launch mode":      func(in *podspec.Inputs) { in.LaunchMode = "" },
		"unknown launch mode":    func(in *podspec.Inputs) { in.LaunchMode = "eager" },
		"restart beside service": func(in *podspec.Inputs) { in.RuntimeProcess, in.LaunchMode = "restart", podspec.LaunchService },
		"restart beside preconnect": func(in *podspec.Inputs) {
			in.RuntimeProcess, in.LaunchMode = "restart", podspec.LaunchPreconnectPodWarm
		},
		"unknown lifetime":         func(in *podspec.Inputs) { in.RuntimeProcess = "sometimes" },
		"stage UID is adapter UID": func(in *podspec.Inputs) { in.AdapterUID = podspec.SupervisorStageUID },
		"stage UID is agent UID":   func(in *podspec.Inputs) { in.AgentUID = podspec.SupervisorStageUID },
		"env name with =":          func(in *podspec.Inputs) { in.RuntimeEnv = []podspec.EnvPair{{Name: "A=B", Value: "c"}} },
		"empty env name":           func(in *podspec.Inputs) { in.RuntimeEnv = []podspec.EnvPair{{Name: "", Value: "c"}} },
		"embedded with command":    func(in *podspec.Inputs) { in.DeploymentModel = string(podspec.DeploymentEmbedded) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := supervisedInputs()
			mutate(&in)
			if pod, err := podspec.Build(in); err == nil {
				t.Fatalf("Build accepted the input and rendered %s", pod.Name)
			}
		})
	}
	// An unknown lifetime is refused on an unsupervised pod too.
	in := inputs()
	in.RuntimeProcess = "sometimes"
	if _, err := podspec.Build(in); err == nil {
		t.Error("Build accepted an unknown lifetime on an unsupervised pod")
	}
}

// spec: 4.7.10 (Runtime supervisor), 5.2 (Runtime-process lifetime), 17.2 (item 5)
func TestBuildUnsupervisedPodsCarryNoSupervisorButCarryTheLifetimeLabel_spec_5_2(t *testing.T) {
	sidecar := inputs()
	embedded := inputs()
	embedded.DeploymentModel = string(podspec.DeploymentEmbedded)
	for name, in := range map[string]podspec.Inputs{"sidecar without command": sidecar, "embedded": embedded} {
		in.LaunchMode = podspec.LaunchSession
		in.SupervisorImage = "ghcr.io/lennylabs/lenny-supervisor:v1"
		pod := buildOK(t, in)
		if len(pod.Spec.InitContainers) != 0 {
			t.Errorf("%s: init containers rendered: %+v", name, pod.Spec.InitContainers)
		}
		for _, c := range pod.Spec.Containers {
			if len(c.Command) != 0 || hasFlagPrefix(c.Args, "--supervise-socket") || hasFlagPrefix(c.Args, "--launch-mode") {
				t.Errorf("%s: container %s carries supervised rendering: %v %v", name, c.Name, c.Command, c.Args)
			}
		}
		if got := pod.Labels["lenny.dev/runtime-process"]; got != "keep" {
			t.Errorf("%s: lifetime label = %q, want keep", name, got)
		}
	}
	sidecar.RuntimeProcess = "restart"
	if got := buildOK(t, sidecar).Labels["lenny.dev/runtime-process"]; got != "restart" {
		t.Errorf("restart label = %q", got)
	}
	// The builder copies the caller's labels rather than writing into them.
	if _, ok := sidecar.Labels["lenny.dev/runtime-process"]; ok {
		t.Error("Build wrote the lifetime label into the caller's label map")
	}
}

// spec: 4.7.9 (step 4), 6.2
func TestBuildAdapterReadinessProbeOnEverySidecarPod_spec_4_7_9(t *testing.T) {
	for name, in := range map[string]podspec.Inputs{"unsupervised": inputs(), "supervised": supervisedInputs()} {
		ad := container(t, buildOK(t, in), "adapter")
		p := ad.ReadinessProbe
		if p == nil || p.TCPSocket == nil || p.TCPSocket.Port.String() != "grpc" || p.Exec != nil || p.HTTPGet != nil {
			t.Errorf("%s: adapter readiness probe = %+v, want tcpSocket on grpc", name, p)
		}
		if container(t, buildOK(t, in), "runtime").ReadinessProbe != nil {
			t.Errorf("%s: runtime container carries a readiness probe", name)
		}
	}
	emb := inputs()
	emb.DeploymentModel = string(podspec.DeploymentEmbedded)
	if p := container(t, buildOK(t, emb), "runtime").ReadinessProbe; p != nil {
		t.Errorf("embedded runtime carries a readiness probe: %+v", p)
	}
}

// spec: 6.4, 13.1 (Runtime supervisor volume)
func TestApplyResourcesStampsTheStageInitContainer_spec_6_4(t *testing.T) {
	in := supervisedInputs()
	in.Resources = &corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")}}
	pod := buildOK(t, in)
	got := pod.Spec.InitContainers[0].Resources.Limits[corev1.ResourceMemory]
	if got.String() != "1Gi" {
		t.Errorf("stage init container memory limit = %s, want 1Gi", got.String())
	}
}

// spec: 24.2 (Preflight), 4.7.11 (Adapter-Agent Security Boundary, Runtime supervisor, Property self-test), 13.1 (Container identity)
func TestSupervisorSelftestContainerRunsSelftestAtStageIdentity_spec_24_2(t *testing.T) {
	in := supervisedInputs()
	stage := buildOK(t, in).Spec.InitContainers[0]
	c := podspec.SupervisorSelftestContainer(in.SupervisorImage)
	if c.Name != podspec.SelftestContainerName || c.Image != in.SupervisorImage {
		t.Errorf("self-test container = %s/%s", c.Name, c.Image)
	}
	if !slices.Equal(c.Args, []string{"selftest"}) || len(c.Command) != 0 {
		t.Errorf("self-test argv = %v %v, want the image entrypoint with selftest", c.Command, c.Args)
	}
	if !reflect.DeepEqual(c.SecurityContext, stage.SecurityContext) {
		t.Errorf("self-test securityContext = %+v, want the stage container's %+v", c.SecurityContext, stage.SecurityContext)
	}
	if len(c.VolumeMounts) != 0 {
		t.Errorf("self-test mounts = %+v, want none", c.VolumeMounts)
	}
}
