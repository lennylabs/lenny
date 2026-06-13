// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
)

// agentPod builds a minimal running agent pod backing the named Sandbox
// (the §4.6.1 reconciler names the pod identically to its Sandbox).
// runtimeClass, when non-empty, is the §5.3 RuntimeClass the pod runs
// under; the matching RuntimeClass object must already exist in the
// cluster (envtest validates spec.runtimeClassName).
func agentPod(name, runtimeClass string) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelManaged: "true", warmpool.LabelPool: testPool},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "agent", Image: "k8s.gcr.io/pause"}},
		},
	}
	if runtimeClass != "" {
		p.Spec.RuntimeClassName = &runtimeClass
	}
	return p
}

// mustCreate creates obj on c, failing the test on error.
func mustCreate(t *testing.T, c client.Client, obj client.Object) {
	t.Helper()
	if err := c.Create(context.Background(), obj); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create %T %s: %v", obj, obj.GetName(), err)
	}
}

// mustCreateRuntimeClass pre-creates the named RuntimeClass so envtest's
// apiserver admits a pod that references it (the §5.3 Kata path).
func mustCreateRuntimeClass(t *testing.T, c client.Client, name string) {
	t.Helper()
	mustCreate(t, c, &nodev1.RuntimeClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Handler:    name,
	})
}

func getPod(t *testing.T, c client.Client, name string) corev1.Pod {
	t.Helper()
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &pod); err != nil {
		t.Fatalf("get pod %s: %v", name, err)
	}
	return pod
}

// spec: §17.2 item 5 (line 46) / §5.2 line 392 / §13.2 NET-003
// The §5.2 tenant pin must land on the agent *pod*, not only the Sandbox
// CR, or the pod-scoped lenny-tenant-label-immutability webhook has no
// `unset → {tenant_id}` transition to guard. Before F-17.2.3 the
// gateway stamped lenny.dev/tenant-id only on the Sandbox.
func TestClaimSlotStampsTenantLabelOnPod_spec_17_2_3(t *testing.T) {
	claimer, c := slotClaimerFor(t, concurrentSandbox("sbx-1", "idle", ""))
	mustCreate(t, c, agentPod("sbx-1", ""))

	if _, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-1", "acme", 8)); err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}

	pod := getPod(t, c, "sbx-1")
	if got := pod.Labels[podclaim.LabelTenant]; got != "acme" {
		t.Errorf("pod %s tenant label = %q, want acme (the pin must bind on the pod)", podclaim.LabelTenant, got)
	}
}

// spec: §5.2 line 392 / §17.2 item 5
// A second slot on an already-pinned pod re-stamps the same tenant pin
// idempotently — the webhook treats unset→same-value as a no-op edge.
func TestClaimSlotPodTenantStampIsIdempotent_spec_17_2_3(t *testing.T) {
	claimer, c := slotClaimerFor(t, concurrentSandbox("sbx-1", "idle", ""))
	mustCreate(t, c, agentPod("sbx-1", ""))

	for i, sess := range []string{"sess-1", "sess-2"} {
		if _, err := claimer.ClaimSlot(context.Background(),
			slotReq(sess, "acme", 8)); err != nil {
			t.Fatalf("ClaimSlot #%d: %v", i, err)
		}
	}
	pod := getPod(t, c, "sbx-1")
	if got := pod.Labels[podclaim.LabelTenant]; got != "acme" {
		t.Errorf("pod tenant label = %q, want acme after two same-tenant slots", got)
	}
}

// spec: §17.2 item 5
// Session-mode warm-pool binding (Claimer.Claim) is also a "first
// assignment" per §5.2 line 392, so it too lands the pin on the pod.
func TestClaimStampsTenantLabelOnSessionPod_spec_17_2_3(t *testing.T) {
	claimer, c := claimerFor(t, sandboxIn(testPool, "sbx-1", "idle"))
	mustCreate(t, c, agentPod("sbx-1", ""))

	if _, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	}); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	pod := getPod(t, c, "sbx-1")
	if got := pod.Labels[podclaim.LabelTenant]; got != "acme" {
		t.Errorf("session pod tenant label = %q, want acme", got)
	}
}

// spec: §17.2 item 5
// A pod that is absent at claim time (terminating, or not yet
// materialized) must not fail the claim: the pin stands on Sandbox.status
// and the next assignment re-stamps it. The helper tolerates NotFound.
func TestClaimSlotToleratesMissingPod_spec_17_2_3(t *testing.T) {
	claimer, _ := slotClaimerFor(t, concurrentSandbox("sbx-1", "idle", ""))

	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-1", "acme", 8))
	if err != nil {
		t.Fatalf("ClaimSlot must succeed when the backing pod is absent: %v", err)
	}
	if res.SandboxName != "sbx-1" {
		t.Errorf("claimed %q, want sbx-1", res.SandboxName)
	}
}

// spec: §5.3 row 3 (microvm → kata) / §17.2 lines 97-101
// F-5.3.8: drive a microvm (Kata) pod through the gateway slot-claim
// path end-to-end — the production SlotClaimer binds a slot on a genuine
// Kata-RuntimeClass pod and lands the §5.2 tenant pin on it. Kata's slot
// binding was previously type-checked but never exercised by a test.
func TestClaimSlotBindsKataMicrovmPod_spec_5_3_8(t *testing.T) {
	sb := concurrentSandbox("kata-sbx-1", "idle", "")
	sb.Spec.IsolationProfile = "microvm"
	claimer, c := slotClaimerFor(t, sb)

	// A Kata pod references the `kata` RuntimeClass, which envtest's
	// apiserver validates exists before admitting the pod.
	mustCreateRuntimeClass(t, c, podspec.KataNodePoolValue)
	kataPod := agentPod("kata-sbx-1", "kata")
	mustCreate(t, c, kataPod)

	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-k", "acme", 4))
	if err != nil {
		t.Fatalf("ClaimSlot on a Kata pod: %v", err)
	}
	if res.SandboxName != "kata-sbx-1" {
		t.Errorf("claimed %q, want kata-sbx-1", res.SandboxName)
	}
	if res.ActiveSlots != 1 {
		t.Errorf("ActiveSlots = %d, want 1 after first slot on the Kata pod", res.ActiveSlots)
	}

	// The slot binding is persisted (the §6.2 slot is bound).
	var stored lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: res.Claim.Name}, &stored); err != nil {
		t.Fatalf("the SandboxClaim binding the Kata slot was not persisted: %v", err)
	}

	// The pod is a genuine Kata pod and carries the tenant pin.
	pod := getPod(t, c, "kata-sbx-1")
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "kata" {
		t.Errorf("pod RuntimeClassName = %v, want kata", pod.Spec.RuntimeClassName)
	}
	if got := pod.Labels[podclaim.LabelTenant]; got != "acme" {
		t.Errorf("Kata pod tenant label = %q, want acme", got)
	}
}
