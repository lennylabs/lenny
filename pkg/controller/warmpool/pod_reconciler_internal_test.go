// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
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
	if r.certIssuanceGrace() != defaultCertIssuanceGrace {
		t.Fatalf("certIssuanceGrace default=%v, want %v", r.certIssuanceGrace(), defaultCertIssuanceGrace)
	}
}

// fakeCertMetric records the §10.3 lenny_cert_expiry_seconds gauge
// Set/Clear calls so the unit tests can assert the reconciler's emission
// without touching the global Prometheus registry.
type fakeCertMetric struct {
	set   map[string]float64
	clear map[string]int
}

func newFakeCertMetric() *fakeCertMetric {
	return &fakeCertMetric{set: map[string]float64{}, clear: map[string]int{}}
}

func (f *fakeCertMetric) Set(namespace, pod string, seconds float64) {
	f.set[namespace+"/"+pod] = seconds
}

func (f *fakeCertMetric) Clear(namespace, pod string) {
	f.clear[namespace+"/"+pod]++
}

// TestPublishCertExpiry_spec_10_3 verifies the §10.3 line 342/343
// lenny_cert_expiry_seconds gauge carries the remaining certificate
// validity in seconds and emits no sample when no expiry is derivable.
func TestPublishCertExpiry_spec_10_3(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("emits remaining validity from annotation", func(t *testing.T) {
		m := newFakeCertMetric()
		r := &PodReconciler{CertMetrics: m, Now: func() time.Time { return now }}
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Namespace:   "lenny-agents",
			Name:        "pod-a",
			Annotations: map[string]string{AnnotationCertNotAfter: now.Add(45 * time.Minute).Format(time.RFC3339)},
		}}
		r.publishCertExpiry(pod)
		if got := m.set["lenny-agents/pod-a"]; got != (45 * time.Minute).Seconds() {
			t.Fatalf("gauge=%v, want %v", got, (45 * time.Minute).Seconds())
		}
	})

	t.Run("no expiry derivable emits nothing", func(t *testing.T) {
		m := newFakeCertMetric()
		r := &PodReconciler{CertMetrics: m, Now: func() time.Time { return now }}
		r.publishCertExpiry(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "lenny-agents", Name: "pod-b"}})
		if len(m.set) != 0 {
			t.Fatalf("gauge set for a pod with no derivable expiry: %v", m.set)
		}
	})
}

// TestCertIssued_spec_10_3 covers the §10.3 line 342 "valid certificate"
// predicate the issuance check keys on: only a present, parseable,
// not-yet-expired annotation counts as issued — there is no creation-time
// fallback (its absence is the no-cert signal).
func TestCertIssued_spec_10_3(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	r := &PodReconciler{Now: func() time.Time { return now }}
	cases := []struct {
		name string
		ann  string
		want bool
	}{
		{"absent", "", false},
		{"malformed", "not-a-timestamp", false},
		{"expired", now.Add(-time.Minute).Format(time.RFC3339), false},
		{"valid future", now.Add(time.Hour).Format(time.RFC3339), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pod := &corev1.Pod{}
			if tc.ann != "" {
				pod.Annotations = map[string]string{AnnotationCertNotAfter: tc.ann}
			}
			if got := r.certIssued(pod); got != tc.want {
				t.Fatalf("certIssued=%v, want %v", got, tc.want)
			}
		})
	}
}

// TestReconcileCertIssuance_NonDrainPaths_spec_10_3 covers every §10.3
// line 342 issuance-grace branch that does not reach the drain (which
// needs a client): disabled by default, a non-pre-idle pod, a pod with a
// valid cert, and a pod still inside the grace window. A nil client is
// safe on all of these because none patches the Sandbox.
func TestReconcileCertIssuance_NonDrainPaths_spec_10_3(t *testing.T) {
	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	preIdle := func(ann map[string]string) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Namespace:         "lenny-agents",
			Name:              "pod-pre",
			CreationTimestamp: metav1.NewTime(created),
			Labels:            map[string]string{LabelManaged: "true", state.LabelState: "warming"},
			Annotations:       ann,
		}}
	}

	t.Run("disabled by default", func(t *testing.T) {
		r := &PodReconciler{Now: func() time.Time { return created.Add(10 * time.Minute) }}
		// RequireCertIssuance is false: no cert, well past grace, nil client.
		rq, replaced, err := r.reconcileCertIssuance(context.Background(), preIdle(nil))
		if err != nil || replaced || rq != 0 {
			t.Fatalf("got rq=%v replaced=%v err=%v, want 0 false nil", rq, replaced, err)
		}
	})

	t.Run("pod with valid cert is left alone", func(t *testing.T) {
		r := &PodReconciler{RequireCertIssuance: true, Now: func() time.Time { return created.Add(10 * time.Minute) }}
		pod := preIdle(map[string]string{AnnotationCertNotAfter: created.Add(4 * time.Hour).Format(time.RFC3339)})
		rq, replaced, err := r.reconcileCertIssuance(context.Background(), pod)
		if err != nil || replaced || rq != 0 {
			t.Fatalf("got rq=%v replaced=%v err=%v, want 0 false nil", rq, replaced, err)
		}
	})

	t.Run("inside grace requeues without draining", func(t *testing.T) {
		r := &PodReconciler{RequireCertIssuance: true, CertIssuanceGrace: 60 * time.Second,
			Now: func() time.Time { return created.Add(20 * time.Second) }}
		rq, replaced, err := r.reconcileCertIssuance(context.Background(), preIdle(nil))
		if err != nil || replaced {
			t.Fatalf("got replaced=%v err=%v, want false nil", replaced, err)
		}
		if rq != 40*time.Second {
			t.Fatalf("requeue=%v, want 40s (grace - age)", rq)
		}
	})

	t.Run("non-pre-idle pod is skipped", func(t *testing.T) {
		r := &PodReconciler{RequireCertIssuance: true, Now: func() time.Time { return created.Add(10 * time.Minute) }}
		pod := preIdle(nil)
		pod.Labels[state.LabelState] = "idle"
		rq, replaced, err := r.reconcileCertIssuance(context.Background(), pod)
		if err != nil || replaced || rq != 0 {
			t.Fatalf("got rq=%v replaced=%v err=%v, want 0 false nil", rq, replaced, err)
		}
	})
}
