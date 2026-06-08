// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
)

// spec: §4.6.2 lines 558-560 — CRDGeneration reads the
// pool_config_generation and last-reconciled timestamp the
// PoolScalingController stamps on the pool's SandboxTemplate, backing the
// production sync-status endpoint outside the Postgres-only dev posture.
func TestCRDGenerationReadsAnnotations_spec_4_6_2(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "p",
			Namespace: testNS,
			Annotations: map[string]string{
				lennyv1.AnnotationConfigGeneration: "12",
				lennyv1.AnnotationLastReconciledAt: at.Format(time.RFC3339Nano),
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(tmpl).Build()
	l := podsession.PoolStatusLookup{Reader: c, Namespace: testNS}

	gen, lastAt, ok, err := l.CRDGeneration(context.Background(), "p")
	if err != nil || !ok {
		t.Fatalf("CRDGeneration: ok=%v err=%v", ok, err)
	}
	if gen != 12 {
		t.Errorf("generation = %d, want 12", gen)
	}
	if !lastAt.Equal(at) {
		t.Errorf("lastReconciledAt = %v, want %v", lastAt, at)
	}
}

// spec: §4.6.2 line 560 — a pool defined in Postgres but not yet
// reconciled into a SandboxTemplate reports ok=false so the handler
// renders the pending state rather than a misleading synced=false-at-0.
func TestCRDGenerationMissingTemplate_spec_4_6_2(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	l := podsession.PoolStatusLookup{Reader: c, Namespace: testNS}
	gen, lastAt, ok, err := l.CRDGeneration(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("CRDGeneration: %v", err)
	}
	if ok || gen != 0 || !lastAt.IsZero() {
		t.Errorf("missing template: ok=%v gen=%d lastAt=%v, want false/0/zero", ok, gen, lastAt)
	}
}

// spec: §4.6.2 line 558 — a SandboxTemplate the controller has not yet
// stamped (applied by hand, or a generation it has not observed) leaves
// generation 0 with ok=true, so the handler reports inSync=false against
// the Postgres counter.
func TestCRDGenerationUnstampedTemplate_spec_4_6_2(t *testing.T) {
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: testNS},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(tmpl).Build()
	l := podsession.PoolStatusLookup{Reader: c, Namespace: testNS}
	gen, lastAt, ok, err := l.CRDGeneration(context.Background(), "p")
	if err != nil || !ok {
		t.Fatalf("CRDGeneration: ok=%v err=%v", ok, err)
	}
	if gen != 0 || !lastAt.IsZero() {
		t.Errorf("unstamped template: gen=%d lastAt=%v, want 0/zero", gen, lastAt)
	}
}
