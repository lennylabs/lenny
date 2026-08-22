// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// resumeSlotFixture wires a Binder over a miniredis-backed slot counter and
// returns the miniredis handle so a case can read the pod's occupancy
// counter without perturbing it.
func resumeSlotFixture(t *testing.T, c client.Client, dial func(string) (*adapterclient.Client, error)) (*podsession.Binder, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	b := newBinder(c, dial)
	b.SlotCounter = slotcounter.New(rc)
	return b, mr
}

// resumeActiveSlots reads the pod's §5.2 occupancy counter.
func resumeActiveSlots(t *testing.T, mr *miniredis.Miniredis, sandboxName string) string {
	t.Helper()
	key := "lenny:pod:" + sandboxName + ":active_slots"
	if !mr.Exists(key) {
		return "0"
	}
	v, err := mr.Get(key)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return v
}

// resumeRequest is the concurrent-pool resume the reservation cases drive.
func resumeRequest(maxConcurrent int32) podsession.ResumeRequest {
	return podsession.ResumeRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
		Runtime: "claude-code", CheckpointID: "ckpt-1",
		MaxConcurrentSessions: maxConcurrent,
	}
}

// spec: 5.2 (every session holds a counted slot), 7.1 (resume onto a replacement pod)
// diagnosis: Binder.Resume did not reserve a slot on the pod its connect
// claimed. A resumed session that holds a whole-pod claim and no slot has no
// slot tree to restore into and the pod's occupancy ledger under-counts it,
// and a reservation taken through the pool scan would increment a different
// pod than the one the session resumes onto.
func TestResumeReservesItsSlotOnTheClaimedPod_spec_5_2(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceBase = t.TempDir()
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder, mr := resumeSlotFixture(t, c, adapterDialer(t, srv))

	res, err := binder.Resume(context.Background(), resumeRequest(4))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer res.Result.Adapter.Close()

	if res.Result.SlotID != "sess-1" {
		t.Errorf("BindResult.SlotID = %q, want the session identifier; it is the key both release paths dispatch on",
			res.Result.SlotID)
	}
	if got := resumeActiveSlots(t, mr, res.Result.SandboxName); got != "1" {
		t.Errorf("active_slots on %s = %s, want 1", res.Result.SandboxName, got)
	}
	if got := resumeActiveSlots(t, mr, "sbx-other"); got != "0" {
		t.Errorf("active_slots on an unrelated pod = %s, want 0", got)
	}
}

// spec: 5.2 (exclusive pools keep no per-pod ledger)
// diagnosis: a resume onto a pool with maxConcurrentSessions 1 reserved a
// slot. Setting SlotID there sends the session's release down the slot
// release path, which returns early on a failed adapter teardown without
// decrementing or disposing the claim, and hard-errors on a deployment with
// no Redis.
func TestResumeOntoAnExclusivePoolReservesNoSlot_spec_5_2(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceBase = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder, mr := resumeSlotFixture(t, c, adapterDialer(t, srv))

	res, err := binder.Resume(context.Background(), resumeRequest(1))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer res.Result.Adapter.Close()

	if res.Result.SlotID != "" {
		t.Errorf("BindResult.SlotID = %q on an exclusive pool, want empty", res.Result.SlotID)
	}
	if got := resumeActiveSlots(t, mr, res.Result.SandboxName); got != "0" {
		t.Errorf("active_slots = %s, want 0; an exclusive pool keeps no ledger", got)
	}
}

// getResumePod reads the agent Pod backing a Sandbox.
func getResumePod(t *testing.T, c client.Client, name string) corev1.Pod {
	t.Helper()
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &pod); err != nil {
		t.Fatalf("get pod %s: %v", name, err)
	}
	return pod
}

// spec: 4.6.1 (the gateway delivers the uptime cap the controller reads), 5.2
// diagnosis: the replacement pod carries no lenny.dev/max-pod-uptime-seconds
// annotation after a resume. The reservation is normally the first slot on
// that pod, and StampMaxPodUptime returns early on a non-positive value, so
// an unpopulated ResumeRequest.MaxPodUptimeSeconds is a silent no-op that
// leaves the §5.2 uptime drain disabled on the pod for the rest of its life.
func TestResumeStampsMaxPodUptimeOnTheReplacementPod_spec_4_6_1(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceBase = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"), drainAgentPod("sbx-1"))
	binder, _ := resumeSlotFixture(t, c, adapterDialer(t, srv))

	req := resumeRequest(4)
	req.MaxPodUptimeSeconds = 3600
	res, err := binder.Resume(context.Background(), req)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer res.Result.Adapter.Close()

	pod := getResumePod(t, c, "sbx-1")
	if got := pod.Annotations[lennyv1.AnnotationMaxPodUptimeSeconds]; got != "3600" {
		t.Errorf("pod annotation %s = %q, want 3600", lennyv1.AnnotationMaxPodUptimeSeconds, got)
	}
}

// spec: 5.2 (the reservation carries its compensating release), 7.3
// diagnosis: a failed adapter Resume left the pod's occupancy increment
// behind. A checkpoint-restore failure is the retryable case the resume slot
// reservation exists to enable, so the leak compounds per attempt and caps
// the pod's real capacity until a Redis restart rehydrates the counter.
func TestResumeReleasesItsReservationWhenTheAdapterResumeFails_spec_5_2(t *testing.T) {
	t.Run("no co-tenant: the claim is disposed at occupancy zero", func(t *testing.T) {
		srv := adapter.New("adapter-test")
		// No workspace base: the adapter refuses the Resume after the
		// handshake, which is the one failure that follows a completed
		// reservation.
		srv.Runtime = &fakeRuntime{}

		c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
		binder, mr := resumeSlotFixture(t, c, adapterDialer(t, srv))

		if _, err := binder.Resume(context.Background(), resumeRequest(4)); err == nil {
			t.Fatal("Resume err = nil, want the adapter refusal surfaced")
		}
		if got := resumeActiveSlots(t, mr, "sbx-1"); got != "0" {
			t.Errorf("active_slots = %s, want 0 (the reservation must be compensated exactly once)", got)
		}
		if podClaimExists(t, c, "sbx-1") {
			t.Error("the per-pod claim survived the occupancy-zero release")
		}
	})

	t.Run("co-tenant sibling: the claim stays bound", func(t *testing.T) {
		srv := adapter.New("adapter-test")
		srv.Runtime = &fakeRuntime{}

		c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
		binder, mr := resumeSlotFixture(t, c, adapterDialer(t, srv))
		// A sibling slot's occupancy, held by a co-tenant session that
		// landed on the pod after connect's claim CREATE.
		if _, _, err := binder.SlotCounter.Reserve(context.Background(), "sbx-1", 4); err != nil {
			t.Fatalf("seed sibling occupancy: %v", err)
		}

		if _, err := binder.Resume(context.Background(), resumeRequest(4)); err == nil {
			t.Fatal("Resume err = nil, want the adapter refusal surfaced")
		}
		if got := resumeActiveSlots(t, mr, "sbx-1"); got != "1" {
			t.Errorf("active_slots = %s, want 1 (the sibling's slot survives the release)", got)
		}
		var claim lennyv1.SandboxClaim
		if err := c.Get(context.Background(),
			client.ObjectKey{Namespace: testNS, Name: "claim-sbx-1"}, &claim); err != nil {
			t.Fatalf("the per-pod claim was disposed while a sibling slot remained: %v", err)
		}
		if claim.Status.Phase != "bound" {
			t.Errorf("claim binding state = %q, want bound", claim.Status.Phase)
		}
	})
}

// spec: 5.2 (the per-pod maxConcurrentSessions bound)
// diagnosis: a resume onto a pod filled to its bound between connect and the
// reservation either admitted the session or issued a compensating release
// for an increment that never landed. The decrement would take a co-tenant's
// slot off the count and let the gateway over-assign past the bound.
func TestResumeOntoAFullPodReservesAndReleasesNothing_spec_5_2(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceBase = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder, mr := resumeSlotFixture(t, c, adapterDialer(t, srv))
	for i := 0; i < 2; i++ {
		if _, _, err := binder.SlotCounter.Reserve(context.Background(), "sbx-1", 2); err != nil {
			t.Fatalf("seed co-tenant occupancy %d: %v", i, err)
		}
	}

	res, err := binder.Resume(context.Background(), resumeRequest(2))
	if !errors.Is(err, podclaim.ErrNoConcurrentSlot) {
		t.Fatalf("err = %v, want ErrNoConcurrentSlot", err)
	}
	if res.Result != nil {
		t.Error("Resume published a BindResult on the conflict")
	}
	if got := resumeActiveSlots(t, mr, "sbx-1"); got != "2" {
		t.Errorf("active_slots = %s, want the pod left at exactly its bound of 2", got)
	}
	if !podClaimExists(t, c, "sbx-1") {
		t.Error("the conflict disposed the per-pod claim")
	}
}
