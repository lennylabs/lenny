// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

func sandboxedSpec() SandboxedPodSpec {
	return SandboxedPodSpec{
		Name: "p", Namespace: "ns", RuntimeClassName: "gvisor-kvm",
		SupervisorImage: "sup:1", ProbeImage: "probe:1", PingInterval: "2s", PingTimeout: "3s",
	}
}

// spec: §4.7.10 (Runtime supervisor), §13.1 (Runtime supervisor volume;
// Container identity), §28.5.3 (CH-SUPERVISE)
//
// TestSandboxedProbePodMirrorsTheSupervisedLayout pins the tier-6 pod: the
// RuntimeClass under test, the stage init container writing the volume at
// the stage UID, the runtime container running the supervisor from the
// read-only mount at the agent UID with the adapter UID as its peer, and
// the peer at the adapter UID checking the agent UID.
func TestSandboxedProbePodMirrorsTheSupervisedLayout(t *testing.T) {
	pod := SandboxedProbePod(sandboxedSpec())
	if *pod.Spec.RuntimeClassName != "gvisor-kvm" || pod.Labels[TestLabel] != "true" || pod.Namespace != "ns" {
		t.Fatalf("pod meta = %+v runtimeClass %s", pod.ObjectMeta, *pod.Spec.RuntimeClassName)
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever ||
		pod.Spec.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("pod spec = %+v; want restartPolicy Never and the RuntimeDefault seccomp profile", pod.Spec)
	}
	stage := pod.Spec.InitContainers[0]
	if stage.Name != podspec.SupervisorStageContainerName || stage.Image != "sup:1" ||
		*stage.SecurityContext.RunAsUser != podspec.SupervisorStageUID || stage.VolumeMounts[0].ReadOnly {
		t.Errorf("stage init container = %+v", stage)
	}
	byName := map[string]corev1.Container{}
	for _, c := range pod.Spec.Containers {
		byName[c.Name] = c
	}
	rt, peer := byName["runtime"], byName["adapter"]
	if rt.Command[0] != SupervisorRunPath || !slices.Contains(rt.Command, podspec.SuperviseSocketName) ||
		rt.Command[len(rt.Command)-1] != ProbeExe || *rt.SecurityContext.RunAsUser != podspec.AgentUID {
		t.Errorf("runtime container = %+v", rt)
	}
	if !rt.VolumeMounts[0].ReadOnly || rt.VolumeMounts[0].MountPath != "/lenny-supervisor" {
		t.Errorf("runtime mount = %+v, want the read-only supervisor mount", rt.VolumeMounts)
	}
	if i := slices.Index(rt.Command, "--adapter-uid"); i < 0 || rt.Command[i+1] != "65532" {
		t.Errorf("runtime command %v does not name the adapter UID as the peer", rt.Command)
	}
	if peer.Command[1] != superviseprobe.SubSupervisePeer || *peer.SecurityContext.RunAsUser != podspec.AdapterUID ||
		!strings.Contains(strings.Join(peer.Command, " "), "--ping-timeout 3s") {
		t.Errorf("peer container = %+v", peer)
	}
}

// spec: §4.7.10 (Runtime supervisor, Signals)
//
// TestSyncFaultPodRunsTheHarnessCase pins the synchronous-fault pod: one
// container running the harness's synchronous-fault case under the
// RuntimeClass.
func TestSyncFaultPodRunsTheHarnessCase(t *testing.T) {
	pod := SyncFaultPod(sandboxedSpec())
	if len(pod.Spec.Containers) != 1 || *pod.Spec.RuntimeClassName != "gvisor-kvm" {
		t.Fatalf("pod = %+v", pod.Spec)
	}
	if got := pod.Spec.Containers[0].Command; !slices.Equal(got, []string{SupervisorHarnessExe, "--run", SyncFaultCase}) {
		t.Errorf("command = %v", got)
	}
}

// spec: §5.2 (Supervised scrub sequence), §7.5
//
// TestSetupResiduePlanRunsTheAdapterProbe pins the setup-residue workspace
// plan: one argv-mode setup command running the adapter image's probe with
// the key.
func TestSetupResiduePlanRunsTheAdapterProbe(t *testing.T) {
	var plan struct {
		SchemaVersion int `json:"schemaVersion"`
		SetupCommands []struct {
			Cmd string `json:"cmd"`
		} `json:"setupCommands"`
	}
	if err := json.Unmarshal(SetupResiduePlan(77), &plan); err != nil {
		t.Fatal(err)
	}
	want := AdapterProbeExe + " " + superviseprobe.SubSetupResidue + " 77"
	if plan.SchemaVersion != 1 || len(plan.SetupCommands) != 1 || plan.SetupCommands[0].Cmd != want {
		t.Errorf("plan = %+v, want one setup command %q", plan, want)
	}
}

// spec: §6.1 (Adapter SIGTERM behavior during sdk_connecting), §4.7.10
// (Runtime supervisor, Liveness)
//
// TestWatchEventsKeepTheTerminationAPollCanMiss pins the watch decoding the
// exit-code assertions rest on: every event's state is kept in order, an
// adapter termination reported in one update is found even when a later
// update drops it, a truncated trailing event is ignored, and a stream with
// no termination reports none.
func TestWatchEventsKeepTheTerminationAPollCanMiss(t *testing.T) {
	ev := func(typ, adapterState string) string {
		return `{"type":"` + typ + `","object":{"metadata":{"name":"p"},"status":{"containerStatuses":[` +
			`{"name":"adapter","state":` + adapterState + `}]}}}`
	}
	raw := ev("ADDED", `{"running":{"startedAt":"2026-10-24T00:00:00Z"}}`) + "\n" +
		ev("MODIFIED", `{"terminated":{"exitCode":0,"reason":"Completed"}}`) + "\n" +
		ev("DELETED", `{}`) + "\n" + `{"type":"MODIFIED","object":{"metad`
	states := decodeWatchEvents(raw)
	if len(states) != 3 {
		t.Fatalf("decoded %d states, want 3", len(states))
	}
	a, ok := FirstTerminated(states, "adapter")
	if !ok || a.ExitCode != 0 || a.Reason != "Completed" {
		t.Errorf("FirstTerminated = %+v, %v; want the exit-0 termination", a, ok)
	}
	if _, ok := FirstTerminated(states[:1], "adapter"); ok {
		t.Error("a stream with no termination reported one")
	}
}
