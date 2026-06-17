// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
)

// scrubWarningPod builds an agent Pod for the StampScrubWarning tests.
func scrubWarningPod(name string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS}}
}

// TestStampScrubWarningStampsAnnotation verifies StampScrubWarning patches the
// lenny.dev/scrub-warning annotation onto the agent Pod with the RFC3339Nano
// stamp instant, the §5.2 warn-policy residual-state marker.
// spec: 5.2 (warn policy returns the pod with a scrub_warning annotation), 6.2 (annotation persists through re-warm)
func TestStampScrubWarningStampsAnnotation_spec_5_2(t *testing.T) {
	cl := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(scrubWarningPod("pod-1")).
		Build()
	stampedAt := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	if err := podclaim.StampScrubWarning(context.Background(), cl, testNS, "pod-1", stampedAt); err != nil {
		t.Fatalf("StampScrubWarning: %v", err)
	}
	var got corev1.Pod
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "pod-1"}, &got); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if v := got.Annotations[lennyv1.AnnotationScrubWarning]; v != stampedAt.Format(time.RFC3339Nano) {
		t.Errorf("scrub-warning annotation = %q, want %q", v, stampedAt.Format(time.RFC3339Nano))
	}
}

// TestStampScrubWarningTolerLatesMissingPod verifies a stamp on a pod the
// apiserver no longer knows is a no-op rather than an error: a pod already gone
// (a concurrent retirement) needs no residual-state marker.
// spec: 5.2 (scrub_warning marker), 3.4 (nothing to mark for a gone pod)
func TestStampScrubWarningToleratesMissingPod_spec_3_4(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	if err := podclaim.StampScrubWarning(context.Background(), cl, testNS, "ghost", time.Unix(0, 0)); err != nil {
		t.Fatalf("StampScrubWarning on a missing pod: %v, want nil (NotFound tolerated)", err)
	}
}

// TestStampScrubWarningPropagatesPatchError verifies a transient apiserver
// patch fault surfaces as a wrapped error rather than being swallowed, so a
// warn-policy pod never re-enters the pool with its marker silently dropped.
// spec: 5.2 (warn-policy marker fails closed on a patch fault)
func TestStampScrubWarningPropagatesPatchError_spec_5_2(t *testing.T) {
	base := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(scrubWarningPod("pod-1")).
		Build()
	cl := interceptor.NewClient(base, interceptor.Funcs{
		Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return errors.New("apiserver unreachable")
		},
	})
	if err := podclaim.StampScrubWarning(context.Background(), cl, testNS, "pod-1", time.Unix(0, 0)); err == nil {
		t.Error("StampScrubWarning patch error = nil, want non-nil")
	}
}
