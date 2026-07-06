// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
)

// maxUptimePod builds an agent Pod for the StampMaxPodUptime tests.
func maxUptimePod(name string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS}}
}

// TestStampMaxPodUptimeStampsAnnotation verifies StampMaxPodUptime patches the
// lenny.dev/max-pod-uptime-seconds annotation onto the agent Pod as a decimal
// integer of seconds, the §4.6.1 delivery surface the WarmPoolController reads
// to level-trigger the CreationTimestamp-derived uptime drain.
// spec: 4.6.1 (gateway delivers the uptime cap the controller reads), 4.6.3 (gateway stamps agent-pod annotations, never Sandbox.status)
func TestStampMaxPodUptimeStampsAnnotation_spec_4_6(t *testing.T) {
	cl := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(maxUptimePod("pod-1")).
		Build()
	if err := podclaim.StampMaxPodUptime(context.Background(), cl, testNS, "pod-1", 3600); err != nil {
		t.Fatalf("StampMaxPodUptime: %v", err)
	}
	var got corev1.Pod
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "pod-1"}, &got); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if v := got.Annotations[lennyv1.AnnotationMaxPodUptimeSeconds]; v != "3600" {
		t.Errorf("max-pod-uptime annotation = %q, want %q", v, "3600")
	}
}

// TestStampMaxPodUptimeSkipsNonPositiveCap verifies a pool that sets no
// maxPodUptimeSeconds (a non-positive cap) is not stamped at all: the
// annotation's absence disables the controller's uptime check, matching the
// field's optional status and the gateway-side `maxPodUptimeSeconds > 0`
// guards. A zero-value stamp would instead be read by the controller as
// created+0 (unconditionally over-uptime) and drain every no-cap pod.
// spec: 4.6.1 (optional cap; only a pool that sets it is stamped)
func TestStampMaxPodUptimeSkipsNonPositiveCap_spec_4_6(t *testing.T) {
	for _, cap := range []int64{0, -1} {
		cl := fake.NewClientBuilder().
			WithScheme(newScheme(t)).
			WithObjects(maxUptimePod("pod-1")).
			Build()
		if err := podclaim.StampMaxPodUptime(context.Background(), cl, testNS, "pod-1", cap); err != nil {
			t.Fatalf("StampMaxPodUptime(cap=%d): %v", cap, err)
		}
		var got corev1.Pod
		if err := cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "pod-1"}, &got); err != nil {
			t.Fatalf("get pod: %v", err)
		}
		if v, present := got.Annotations[lennyv1.AnnotationMaxPodUptimeSeconds]; present {
			t.Errorf("cap=%d stamped annotation=%q, want the annotation absent", cap, v)
		}
	}
}

// TestStampMaxPodUptimeToleratesMissingPod verifies a stamp on a pod the
// apiserver no longer knows is a no-op rather than an error: a pod already gone
// (a concurrent retirement) needs no cap.
// spec: 4.6.3 (nothing to stamp for a gone pod)
func TestStampMaxPodUptimeToleratesMissingPod_spec_4_6(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	if err := podclaim.StampMaxPodUptime(context.Background(), cl, testNS, "ghost", 3600); err != nil {
		t.Fatalf("StampMaxPodUptime on a missing pod: %v, want nil (NotFound tolerated)", err)
	}
}

// TestStampMaxPodUptimePropagatesPatchError verifies a transient apiserver
// patch fault surfaces as a wrapped error rather than being swallowed, so the
// controller is never left without the cap it needs to drain an over-uptime
// pod (fail closed on the delivery seam).
// spec: 4.6.1 (uptime-cap delivery fails closed on a patch fault)
func TestStampMaxPodUptimePropagatesPatchError_spec_4_6(t *testing.T) {
	base := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(maxUptimePod("pod-1")).
		Build()
	cl := interceptor.NewClient(base, interceptor.Funcs{
		Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return errors.New("apiserver unreachable")
		},
	})
	if err := podclaim.StampMaxPodUptime(context.Background(), cl, testNS, "pod-1", 3600); err == nil {
		t.Error("StampMaxPodUptime patch error = nil, want non-nil")
	}
}
