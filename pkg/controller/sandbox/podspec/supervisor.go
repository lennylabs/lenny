// SPDX-License-Identifier: MIT

package podspec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/podsecurity"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

const (
	// SupervisorVolumeName is the emptyDir the lenny-supervisor-stage init
	// container copies the runtime supervisor into. Only that init
	// container mounts it read-write; the runtime container mounts it
	// read-only, and the lenny-pod-security webhook refuses every other
	// read-write mount of it. spec: §13.1 (Runtime supervisor volume).
	SupervisorVolumeName = "lenny-supervisor"

	// SupervisorStageContainerName is the init container that stages the
	// supervisor binary. spec: §13.1 (Runtime supervisor volume).
	SupervisorStageContainerName = podsecurity.SupervisorStageContainerName

	// SupervisorStageUID is the default UID the stage init container runs
	// at. §13.1 (Container identity) reserves the adapter and agent UIDs for
	// the regular adapter and runtime containers, so the stage container
	// needs a third non-root identity. It is distinct from the default
	// adapter, agent, and lenny-cred-readers identities; Build refuses a pod
	// whose resolved adapter or agent UID equals it. spec: §13.1 (Runtime
	// supervisor volume; Container identity).
	SupervisorStageUID int64 = 65531

	// SuperviseSocketName is the abstract socket the adapter listens on for
	// the supervisor's CH-SUPERVISE connection. The builder passes it to
	// the supervisor as --socket and to the adapter as --supervise-socket.
	// spec: §28.5.3 (CH-SUPERVISE, Endpoint).
	SuperviseSocketName = "@lenny-supervise"

	// supervisorMount is where the runtime container mounts the supervisor
	// volume read-only; the supervisor runs from supervisorBinary.
	supervisorMount  = "/lenny-supervisor"
	supervisorBinary = supervisorMount + "/lenny-supervisor"
	// supervisorStageMount is where the stage init container mounts the
	// volume. It differs from supervisorMount because the supervisor image
	// holds its binary at the file path /lenny-supervisor, which a
	// directory mount at the same path would shadow.
	supervisorStageMount = "/stage"
)

// The supervised pod's launch modes, rendered as the adapter's --launch-mode.
// spec: §4.7.10 (Runtime supervisor, Launch; Runtime supervisor, SDK-warm).
const (
	// LaunchSession launches the first runtime process at startup step 7.
	LaunchSession = "session"
	// LaunchService launches the runtime process at warm-up and keeps it
	// for the pod's life.
	LaunchService = "service"
	// LaunchPreconnectSDKWarm launches at warm-up on a preConnect runtime
	// whose pool's SDK-warm circuit breaker was closed at pod creation.
	LaunchPreconnectSDKWarm = "preconnect-sdk-warm"
	// LaunchPreconnectPodWarm defers the launch to startup step 7 on a
	// preConnect runtime whose pool's breaker was open at pod creation.
	LaunchPreconnectPodWarm = "preconnect-pod-warm"
)

// EnvPair is one name and value pair of the Runtime's `env` (§5.1).
type EnvPair struct {
	Name  string
	Value string
}

// SuperviseTiming is the set of operator-tunable CH-SUPERVISE bounds. A zero
// field renders no flag. spec: §28.5.3 (CH-SUPERVISE, Timing); §4.7.10
// (Runtime supervisor, Launch); §17.8.1.
type SuperviseTiming struct {
	// PingInterval renders --supervise-ping-interval.
	PingInterval time.Duration
	// PingTimeout renders --supervise-ping-timeout.
	PingTimeout time.Duration
	// WaitTimeout renders --supervise-wait-timeout. It bounds the wait for
	// the supervisor's ready, which runs while the kubelet pulls the
	// runtime image.
	WaitTimeout time.Duration
	// WarmUpTimeout renders --warm-up-timeout.
	WarmUpTimeout time.Duration
}

// validateRuntimeProcess refuses a recycle.runtimeProcess value outside the
// §5.2 domain. An empty value is a pool without the field, which creates a
// `keep` pod. spec: §5.2 (Runtime-process lifetime).
func validateRuntimeProcess(v string) error {
	switch v {
	case "", state.RuntimeProcessKeep, state.RuntimeProcessRestart:
		return nil
	default:
		return fmt.Errorf("podspec: unknown runtime-process lifetime %q", v)
	}
}

// validateSupervised refuses a supervised pod the adapter or the supervisor
// would refuse at startup, so a refused input creates no pod rather than a
// pod that exits before READY on every attempt.
func validateSupervised(in Inputs) error {
	if !strings.HasPrefix(in.RuntimeCommand[0], "/") {
		return fmt.Errorf("podspec: runtime command %q is not an absolute path", in.RuntimeCommand[0])
	}
	switch in.LaunchMode {
	case LaunchSession, LaunchService, LaunchPreconnectSDKWarm, LaunchPreconnectPodWarm:
	default:
		return fmt.Errorf("podspec: unknown launch mode %q on a supervised pod", in.LaunchMode)
	}
	// spec: §5.2 (Runtime-process lifetime) — only a session-mode runtime
	// without preConnect is restartable, so a `restart` pod launches at
	// startup step 7 only.
	if state.RuntimeProcessLifetime(in.RuntimeProcess) == state.RuntimeProcessRestart && in.LaunchMode != LaunchSession {
		return fmt.Errorf("podspec: a restart pod requires launch mode %q, got %q", LaunchSession, in.LaunchMode)
	}
	if uid := SupervisorStageUID; uid == in.adapterUID() || uid == in.agentUID() {
		return fmt.Errorf("podspec: the supervisor stage UID %d collides with a reserved adapter or agent UID", uid)
	}
	for _, e := range in.RuntimeEnv {
		if e.Name == "" || strings.Contains(e.Name, "=") {
			return fmt.Errorf("podspec: runtime env name %q is not a variable name", e.Name)
		}
	}
	return nil
}

// applySupervisor makes the runtime supervisor the runtime container's first
// process. It adds the supervisor volume, the lenny-supervisor-stage init
// container that copies the binary into it, the runtime container's Command
// running the supervisor with the launch environment and the runtime's
// `command`, the read-only mount, and the supervised adapter flags. The
// runtime container's Env is dropped, because the supervisor clears its
// environment before anything else and launches each process with the pairs
// rendered here. pod.Spec.Containers holds the adapter at index 0 and the
// runtime at index 1. spec: §4.7.10 (Runtime supervisor; Environment); §13.1
// (Runtime supervisor volume); §28.5.3 (CH-SUPERVISE).
func applySupervisor(in Inputs, pod *corev1.Pod) error {
	if err := validateSupervised(in); err != nil {
		return err
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name:         SupervisorVolumeName,
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	})
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, supervisorStageContainer(in))

	adapter := &pod.Spec.Containers[0]
	adapter.Args = append(adapter.Args, supervisedAdapterArgs(in)...)

	runtime := &pod.Spec.Containers[1]
	runtime.Command = supervisorCommand(in)
	runtime.Args = nil
	runtime.Env = nil
	runtime.VolumeMounts = append(runtime.VolumeMounts, corev1.VolumeMount{
		Name: SupervisorVolumeName, MountPath: supervisorMount, ReadOnly: true,
	})
	return nil
}

// supervisorStageContainer is the init container that runs the supervisor
// image's `stage` mode, which copies the binary into the volume with mode
// 0555. It runs at the dedicated stage UID with the §13.1 container baseline
// and no restartPolicy, so it is an ordinary run-to-completion init
// container and holds no writer on the volume once the runtime starts.
// spec: §13.1 (Runtime supervisor volume).
func supervisorStageContainer(in Inputs) corev1.Container {
	return corev1.Container{
		Name:            SupervisorStageContainerName,
		Image:           in.SupervisorImage,
		Args:            []string{"stage", supervisorStageMount},
		VolumeMounts:    []corev1.VolumeMount{{Name: SupervisorVolumeName, MountPath: supervisorStageMount}},
		SecurityContext: containerSecurityContext(SupervisorStageUID),
	}
}

// supervisorCommand renders the runtime container's argv:
//
//	/lenny-supervisor/lenny-supervisor run --socket @lenny-supervise
//	    [--adapter-uid UID] --env K=V ... -- COMMAND...
//
// --adapter-uid is rendered exactly when the adapter enforces SO_PEERCRED
// (nonceOnlyArgs renders no flag), so the supervisor checks its peer only
// when the adapter checks its own. The --env pairs are the Runtime's `env`
// followed by the platform's runtime variables; the supervisor applies them
// in order over its fixed PATH, and a later pair replaces an earlier one of
// the same name. Nothing secret is rendered, because the agent UID can read
// the supervisor's cmdline. spec: §4.7.10 (Runtime supervisor, Environment);
// §28.5.3 (CH-SUPERVISE, Endpoint); §4.7.11 item 8.
func supervisorCommand(in Inputs) []string {
	cmd := []string{supervisorBinary, "run", "--socket", SuperviseSocketName}
	if len(nonceOnlyArgs(in)) == 0 {
		cmd = append(cmd, "--adapter-uid", strconv.FormatInt(in.adapterUID(), 10))
	}
	for _, e := range in.RuntimeEnv {
		cmd = append(cmd, "--env", e.Name+"="+e.Value)
	}
	for _, e := range platformRuntimeEnv() {
		cmd = append(cmd, "--env", e.Name+"="+e.Value)
	}
	cmd = append(cmd, "--")
	return append(cmd, in.RuntimeCommand...)
}

// platformRuntimeEnv is the platform's runtime variables on a supervised pod:
// the SDK's adapter socket variable, which the runtime container's Env
// carries on an unsupervised pod. The manifest is at the SDK's default path,
// so no manifest variable is rendered. spec: §4.7.10 (Runtime supervisor,
// Environment).
func platformRuntimeEnv() []EnvPair {
	return []EnvPair{{Name: RuntimeSocketEnvVar, Value: RuntimeSocketName}}
}

// supervisedAdapterArgs renders the adapter flags every supervised pod
// carries: the CH-SUPERVISE socket, the pod's runtime-process lifetime, its
// launch mode, and each positive timing bound. spec: §28.5.3 (CH-SUPERVISE);
// §5.2 (Runtime-process lifetime); §4.7.10 (Runtime supervisor, Launch).
func supervisedAdapterArgs(in Inputs) []string {
	args := []string{
		"--supervise-socket=" + SuperviseSocketName,
		"--runtime-process=" + state.RuntimeProcessLifetime(in.RuntimeProcess),
		"--launch-mode=" + in.LaunchMode,
	}
	for _, d := range []struct {
		flag string
		v    time.Duration
	}{
		{"--supervise-ping-interval", in.SuperviseTiming.PingInterval},
		{"--supervise-ping-timeout", in.SuperviseTiming.PingTimeout},
		{"--supervise-wait-timeout", in.SuperviseTiming.WaitTimeout},
		{"--warm-up-timeout", in.SuperviseTiming.WarmUpTimeout},
	} {
		if d.v > 0 {
			args = append(args, d.flag+"="+d.v.String())
		}
	}
	return args
}

// ErrNonPositiveTiming reports a CH-SUPERVISE timing bound that is zero or
// negative.
var ErrNonPositiveTiming = errors.New("podspec: a CH-SUPERVISE timing bound must be positive")

// Validate refuses a zero or negative bound. The controller calls it at
// startup on the values its flags resolve, so a bound the adapter would
// refuse is refused before any pod renders it. spec: §28.5.3 (CH-SUPERVISE,
// Timing); §17.8.1.
func (t SuperviseTiming) Validate() error {
	for _, d := range []struct {
		name string
		v    time.Duration
	}{
		{"ping interval", t.PingInterval},
		{"ping timeout", t.PingTimeout},
		{"supervisor wait timeout", t.WaitTimeout},
		{"warm-up timeout", t.WarmUpTimeout},
	} {
		if d.v <= 0 {
			return fmt.Errorf("%w: %s is %s", ErrNonPositiveTiming, d.name, d.v)
		}
	}
	return nil
}
