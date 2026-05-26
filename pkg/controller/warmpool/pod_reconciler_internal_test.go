// SPDX-License-Identifier: MIT

package warmpool

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestHostSchedulable_spec_4_6 covers the §4.6.1 host-node
// schedulability decision: a node is schedulable only when
// .spec.unschedulable is false and it carries no
// node.kubernetes.io/unschedulable taint.
func TestHostSchedulable_spec_4_6(t *testing.T) {
	unschedulableTaint := corev1.Taint{Key: corev1.TaintNodeUnschedulable, Effect: corev1.TaintEffectNoSchedule}
	otherTaint := corev1.Taint{Key: "example.com/dedicated", Effect: corev1.TaintEffectNoSchedule}
	cases := []struct {
		name string
		node corev1.Node
		want bool
	}{
		{
			name: "plain node is schedulable",
			node: corev1.Node{},
			want: true,
		},
		{
			name: "unrelated taint does not block",
			node: corev1.Node{Spec: corev1.NodeSpec{Taints: []corev1.Taint{otherTaint}}},
			want: true,
		},
		{
			name: "cordon field blocks",
			node: corev1.Node{Spec: corev1.NodeSpec{Unschedulable: true}},
			want: false,
		},
		{
			name: "unschedulable taint blocks",
			node: corev1.Node{Spec: corev1.NodeSpec{Taints: []corev1.Taint{unschedulableTaint}}},
			want: false,
		},
		{
			name: "cordon field and taint both block",
			node: corev1.Node{Spec: corev1.NodeSpec{Unschedulable: true, Taints: []corev1.Taint{unschedulableTaint}}},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostSchedulable(&tc.node); got != tc.want {
				t.Fatalf("hostSchedulable=%v, want %v", got, tc.want)
			}
		})
	}
}

// TestCertExpiry covers the §4.6.1 / §10.3 cert-expiry resolution: an
// explicit RFC3339 lenny.dev/cert-not-after annotation wins; otherwise
// the expiry is pod-creation-time + the configured TTL; an unparseable
// annotation falls back to the TTL path; a zero creation timestamp with
// no annotation yields no expiry.
func TestCertExpiry(t *testing.T) {
	created := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	annotated := time.Date(2026, 5, 25, 18, 30, 0, 0, time.UTC)
	r := &PodReconciler{CertTTL: 4 * time.Hour}

	t.Run("annotation wins over creation+ttl", func(t *testing.T) {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.NewTime(created),
			Annotations:       map[string]string{AnnotationCertNotAfter: annotated.Format(time.RFC3339)},
		}}
		got, ok := r.certExpiry(pod)
		if !ok || !got.Equal(annotated) {
			t.Fatalf("certExpiry=%v ok=%v, want %v true", got, ok, annotated)
		}
	})

	t.Run("no annotation falls back to creation+ttl", func(t *testing.T) {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(created)}}
		got, ok := r.certExpiry(pod)
		want := created.Add(4 * time.Hour)
		if !ok || !got.Equal(want) {
			t.Fatalf("certExpiry=%v ok=%v, want %v true", got, ok, want)
		}
	})

	t.Run("unparseable annotation falls back to creation+ttl", func(t *testing.T) {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.NewTime(created),
			Annotations:       map[string]string{AnnotationCertNotAfter: "not-a-timestamp"},
		}}
		got, ok := r.certExpiry(pod)
		want := created.Add(4 * time.Hour)
		if !ok || !got.Equal(want) {
			t.Fatalf("certExpiry=%v ok=%v, want %v true", got, ok, want)
		}
	})

	t.Run("zero creation and no annotation yields no expiry", func(t *testing.T) {
		pod := &corev1.Pod{}
		if _, ok := r.certExpiry(pod); ok {
			t.Fatal("certExpiry ok=true for a pod with no creation time and no annotation")
		}
	})
}

// TestCertDefaults verifies the spec-default TTL and threshold apply when
// the reconciler fields are zero.
func TestCertDefaults(t *testing.T) {
	r := &PodReconciler{}
	if r.certTTL() != defaultCertTTL {
		t.Fatalf("certTTL default=%v, want %v", r.certTTL(), defaultCertTTL)
	}
	if r.certExpiryThreshold() != defaultCertExpiryThreshold {
		t.Fatalf("certExpiryThreshold default=%v, want %v", r.certExpiryThreshold(), defaultCertExpiryThreshold)
	}
}
