// SPDX-License-Identifier: MIT

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/lifecycle"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// containerCase is one agent-container status shape the observer reads.
type containerCase struct {
	name       string
	statuses   []corev1.ContainerStatus
	terminated bool // whether an agent container is terminated
}

func running(name string, ready bool) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name:  name,
		Ready: ready,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}
}

func exited(name string, code int32) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name:  name,
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: code}},
	}
}

func waiting(name string) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name:  name,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
	}
}

var containerCases = []containerCase{
	{name: "no statuses yet", statuses: nil},
	{name: "sidecar both running", statuses: []corev1.ContainerStatus{
		running(podspec.AdapterContainerName, true), running(podspec.RuntimeContainerName, true),
	}},
	{name: "sidecar adapter waiting", statuses: []corev1.ContainerStatus{
		waiting(podspec.AdapterContainerName), running(podspec.RuntimeContainerName, true),
	}},
	{name: "sidecar adapter exited non-zero", terminated: true, statuses: []corev1.ContainerStatus{
		exited(podspec.AdapterContainerName, 1), running(podspec.RuntimeContainerName, true),
	}},
	{name: "sidecar adapter exited zero", terminated: true, statuses: []corev1.ContainerStatus{
		exited(podspec.AdapterContainerName, 0), running(podspec.RuntimeContainerName, true),
	}},
	{name: "sidecar runtime exited", terminated: true, statuses: []corev1.ContainerStatus{
		running(podspec.AdapterContainerName, true), exited(podspec.RuntimeContainerName, 2),
	}},
	{name: "embedded runtime exited", terminated: true, statuses: []corev1.ContainerStatus{
		exited(podspec.RuntimeContainerName, 1),
	}},
	{name: "only a non-agent sidecar exited", statuses: []corev1.ContainerStatus{
		running(podspec.AdapterContainerName, true), running(podspec.RuntimeContainerName, true),
		exited(podspec.EgressCaptureContainerName, 1),
	}},
}

func podWith(phase corev1.PodPhase, ready bool, statuses []corev1.ContainerStatus) *corev1.Pod {
	cond := corev1.ConditionFalse
	if ready {
		cond = corev1.ConditionTrue
	}
	return &corev1.Pod{Status: corev1.PodStatus{
		Phase:             phase,
		Conditions:        []corev1.PodCondition{{Type: corev1.PodReady, Status: cond}},
		ContainerStatuses: statuses,
	}}
}

// deleting returns a copy of pod carrying a deletion timestamp, the state
// Kubernetes puts a pod in when a node drain, an eviction, or a delete
// starts its graceful termination.
func deleting(pod *corev1.Pod) *corev1.Pod {
	p := pod.DeepCopy()
	now := metav1.Now()
	p.DeletionTimestamp = &now
	return p
}

// expectedObservation is the reduction the observer must produce for a
// pod phase, Pod.Ready value, agent-container termination flag, and
// deletion timestamp. A deletion timestamp wins over every other input.
func expectedObservation(phase corev1.PodPhase, ready, terminated, deleted bool) lifecycle.PodObservation {
	switch {
	case deleted:
		return lifecycle.PodTerminating
	case phase == corev1.PodFailed:
		return lifecycle.PodFailed
	case phase == corev1.PodSucceeded:
		return lifecycle.PodSucceeded
	case terminated:
		return lifecycle.PodFailed
	case phase == corev1.PodRunning && ready:
		return lifecycle.PodReady
	case phase == corev1.PodRunning:
		return lifecycle.PodNotReady
	default:
		return lifecycle.PodPending
	}
}

var podPhases = []corev1.PodPhase{
	"", corev1.PodPending, corev1.PodRunning, corev1.PodSucceeded, corev1.PodFailed, corev1.PodUnknown,
}

// spec: 6.2 (Pod State Machine), 4.7.10 (Deployment Model)
//
// TestObservePodContainerStates sweeps every pod phase, both Pod.Ready
// values, and the agent-container status shapes. A terminated adapter
// or runtime container reduces to PodFailed whatever the pod phase,
// because restartPolicy Never keeps the pod Running while another
// container runs; before the fix observePod keyed on the phase alone
// and a pod whose adapter exited stayed warming or idle forever.
//
// The deletion dimension pins that a pod with a deletion timestamp reduces
// to PodTerminating whatever its phase, readiness, or container states, so
// a container that exits on the kubelet's SIGTERM is never read as a
// warm-fill failure.
func TestObservePodContainerStates(t *testing.T) {
	for _, phase := range podPhases {
		for _, ready := range []bool{false, true} {
			for _, cc := range containerCases {
				for _, deleted := range []bool{false, true} {
					pod := podWith(phase, ready, cc.statuses)
					if deleted {
						pod = deleting(pod)
					}
					got := observePod(pod, nil)
					want := expectedObservation(phase, ready, cc.terminated, deleted)
					if got != want {
						t.Errorf("phase=%q ready=%v deleted=%v %s: observePod = %v, want %v",
							phase, ready, deleted, cc.name, got, want)
					}
				}
			}
		}
	}
	if got := observePod(&corev1.Pod{}, errors.New("not found")); got != lifecycle.PodAbsent {
		t.Errorf("observePod with a Get error = %v, want PodAbsent", got)
	}
}

// spec: 6.2 (Pod State Machine), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
//
// TestTerminatedAdapterDecisionMatrix runs the full reconcile decision
// (observePod, then Decide, then the claim hold) over the Sandbox phases,
// the agent-container shapes on a Running pod, and claim presence. It
// pins the outcomes the fix requires: a warming pod with a terminated
// agent container fails, an idle one drains, a failed Sandbox drains
// only with no claim, and a claimed or reserved pod is never acted on.
// The deleted dimension is pinned by TestDeletedPodDecisionMatrix.
func TestTerminatedAdapterDecisionMatrix(t *testing.T) {
	for _, phase := range state.All() {
		for _, cc := range containerCases {
			for _, claim := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/claim=%v", phase, cc.name, claim)
				obs := observePod(podWith(corev1.PodRunning, !cc.terminated, cc.statuses), nil)
				d := lifecycle.HoldFailedReclaim(phase, claim, lifecycle.Decide(phase, obs))

				if d.Action == lifecycle.ActionSetPhase {
					if err := state.IsValid(phase, d.NextPhase); err != nil {
						t.Errorf("%s: invalid edge: %v", name, err)
					}
				}
				switch phase {
				case state.Warming:
					if cc.terminated && d.NextPhase != state.Failed {
						t.Errorf("%s: decision %+v, want warming → failed", name, d)
					}
					if !cc.terminated && d.NextPhase == state.Failed {
						t.Errorf("%s: decision %+v fails a healthy pod", name, d)
					}
				case state.Idle:
					if cc.terminated && d.NextPhase != state.Draining {
						t.Errorf("%s: decision %+v, want idle → draining", name, d)
					}
					if !cc.terminated && d.Action != lifecycle.ActionNone {
						t.Errorf("%s: decision %+v acts on a healthy idle pod", name, d)
					}
				case state.Failed:
					wantPhase := state.Draining
					if claim {
						wantPhase = ""
					}
					if d.NextPhase != wantPhase {
						t.Errorf("%s: decision %+v, want next phase %q", name, d, wantPhase)
					}
				case state.Claimed, state.Reserved, state.Terminated:
					if d.Action != lifecycle.ActionNone {
						t.Errorf("%s: decision %+v acts on a %s pod", name, d, phase)
					}
				}
			}
		}
	}
}

// spec: 6.2 (Pod State Machine), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
//
// TestDeletedPodDecisionMatrix runs the full reconcile decision (observePod,
// then the pod-warm or SDK-warm planner, then the claim hold) for a pod
// carrying a deletion timestamp, over every Sandbox phase, every pod phase,
// both readiness values, every agent-container shape, claim presence, and
// both SDK-warm edges. A deleted pod is never routed through failed:
// sdk_connecting takes the §6.2 SIGTERM edge to terminated, warming holds
// without creating, promoting, or failing, idle drains, draining waits, a
// failed Sandbox keeps its claim-gated reclaim, and claimed, reserved, and
// terminated are left alone.
func TestDeletedPodDecisionMatrix(t *testing.T) {
	for _, phase := range state.All() {
		for _, podPhase := range podPhases {
			for _, ready := range []bool{false, true} {
				for _, cc := range containerCases {
					for _, claim := range []bool{false, true} {
						obs := observePod(deleting(podWith(podPhase, ready, cc.statuses)), nil)
						if obs != lifecycle.PodTerminating {
							t.Fatalf("observePod on a deleted pod = %v, want PodTerminating", obs)
						}
						decisions := map[string]lifecycle.Decision{
							"pod-warm": lifecycle.Decide(phase, obs),
							"sdk-warm warm-fill": lifecycle.DecideSDKWarm(lifecycle.SDKWarmInputs{
								Phase: phase, Pod: obs,
							}),
							"sdk-warm recycle": lifecycle.DecideSDKWarm(lifecycle.SDKWarmInputs{
								Phase: phase, Pod: obs, Recycle: true,
							}),
						}
						for planner, raw := range decisions {
							name := fmt.Sprintf("%s/%s/pod=%q/ready=%v/%s/claim=%v",
								planner, phase, podPhase, ready, cc.name, claim)
							d := lifecycle.HoldFailedReclaim(phase, claim, raw)
							assertDeletedPodDecision(t, name, phase, planner != "pod-warm", claim, d)
						}
					}
				}
			}
		}
	}
}

// assertDeletedPodDecision checks one deleted-pod decision against the
// per-phase outcome TestDeletedPodDecisionMatrix states.
func assertDeletedPodDecision(t *testing.T, name string, phase state.State, sdkWarm, claim bool, d lifecycle.Decision) {
	t.Helper()
	if d.Action == lifecycle.ActionSetPhase {
		if d.NextPhase == state.Failed {
			t.Errorf("%s: a deleted pod is routed through failed", name)
		}
		if err := state.IsValid(phase, d.NextPhase); err != nil {
			t.Errorf("%s: invalid edge: %v", name, err)
		}
	}
	if d.Action == lifecycle.ActionCreatePod || d.Action == lifecycle.ActionDeletePod {
		t.Errorf("%s: decision %+v acts on a pod Kubernetes is already deleting", name, d)
	}
	want := lifecycle.Decision{}
	switch phase {
	case state.SDKConnecting:
		if sdkWarm {
			want = lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Terminated}
		}
	case state.Idle:
		want = lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Draining}
	case state.Failed:
		if !claim {
			want = lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Draining}
		}
	}
	if d != want {
		t.Errorf("%s: decision %+v, want %+v", name, d, want)
	}
}

// spec: 6.2 (Pod State Machine), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
//
// TestClaimPresent pins the claim read that gates the failed → draining
// reclaim: an existing claim holds, a missing claim does not, and any
// other read error is returned so the reconcile fails closed.
func TestClaimPresent(t *testing.T) {
	scheme := sdkWarmScheme(t)
	sb := &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-1", Namespace: "lenny-agents"}}
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-sb-1", Namespace: "lenny-agents"},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: "sb-1"},
	}
	ctx := context.Background()

	r := &Reconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(claim).Build()}
	if got, err := r.claimPresent(ctx, sb); err != nil || !got {
		t.Fatalf("claimPresent with a claim = %v, %v; want true, nil", got, err)
	}
	r = &Reconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	if got, err := r.claimPresent(ctx, sb); err != nil || got {
		t.Fatalf("claimPresent with no claim = %v, %v; want false, nil", got, err)
	}
	boom := errors.New("apiserver unavailable")
	r = &Reconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return boom
		},
	}).Build()}
	if _, err := r.claimPresent(ctx, sb); !errors.Is(err, boom) {
		t.Fatalf("claimPresent on a read error = %v, want wrapped %v", err, boom)
	}
}

// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle))
//
// TestClaimToSandbox pins the claim-event mapping that wakes the
// Sandbox-to-Pod reconciler when a claim holding a failed reclaim is
// deleted.
func TestClaimToSandbox(t *testing.T) {
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-sb-1", Namespace: "lenny-agents"},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: "sb-1"},
	}
	reqs := claimToSandbox(context.Background(), claim)
	if len(reqs) != 1 || reqs[0].Namespace != "lenny-agents" || reqs[0].Name != "sb-1" {
		t.Fatalf("claimToSandbox = %+v, want lenny-agents/sb-1", reqs)
	}
	if reqs := claimToSandbox(context.Background(), &lennyv1.SandboxClaim{}); reqs != nil {
		t.Fatalf("claimToSandbox with no sandboxRef = %+v, want nil", reqs)
	}
	if reqs := claimToSandbox(context.Background(), &corev1.Pod{}); reqs != nil {
		t.Fatalf("claimToSandbox on a non-claim = %+v, want nil", reqs)
	}
}

// spec: 6.1 (What a Pre-Warmed Pod Looks Like), 6.2 (Pod State Machine)
//
// TestReadinessGateNotFlippedOnTerminatingPod pins that the claimability
// gate is never set on a pod Kubernetes is deleting, even when its
// containers still report ready, so a terminating pod cannot become
// claimable. The same pod without a deletion timestamp gets the gate.
func TestReadinessGateNotFlippedOnTerminatingPod(t *testing.T) {
	ctx := context.Background()
	newPod := func() *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "sb-1", Namespace: "lenny-agents"},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.ContainersReady, Status: corev1.ConditionTrue}},
			},
		}
	}
	for _, tc := range []struct {
		obs      lifecycle.PodObservation
		wantGate bool
	}{
		{lifecycle.PodTerminating, false},
		{lifecycle.PodNotReady, true},
	} {
		pod := newPod()
		c := fake.NewClientBuilder().WithScheme(sdkWarmScheme(t)).
			WithObjects(pod).WithStatusSubresource(&corev1.Pod{}).Build()
		r := &Reconciler{Client: c}
		if err := r.syncReadinessGate(ctx, pod, tc.obs); err != nil {
			t.Fatalf("obs=%v: syncReadinessGate: %v", tc.obs, err)
		}
		var live corev1.Pod
		if err := c.Get(ctx, client.ObjectKeyFromObject(pod), &live); err != nil {
			t.Fatalf("get pod: %v", err)
		}
		got := podConditionTrue(&live, corev1.PodConditionType(podspec.ReadinessGateSandboxReady))
		if got != tc.wantGate {
			t.Errorf("obs=%v: readiness gate set = %v, want %v", tc.obs, got, tc.wantGate)
		}
	}
}
