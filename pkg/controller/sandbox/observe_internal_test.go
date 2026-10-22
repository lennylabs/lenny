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

// expectedObservation is the reduction the observer must produce for a
// pod phase, Pod.Ready value, and agent-container termination flag.
func expectedObservation(phase corev1.PodPhase, ready, terminated bool) lifecycle.PodObservation {
	switch {
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
func TestObservePodContainerStates(t *testing.T) {
	for _, phase := range podPhases {
		for _, ready := range []bool{false, true} {
			for _, cc := range containerCases {
				pod := podWith(phase, ready, cc.statuses)
				got := observePod(pod, nil)
				want := expectedObservation(phase, ready, cc.terminated)
				if got != want {
					t.Errorf("phase=%q ready=%v %s: observePod = %v, want %v", phase, ready, cc.name, got, want)
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
