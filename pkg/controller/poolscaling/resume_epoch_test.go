// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
)

// denyingClient builds a fake client whose Create always returns a
// validator Forbidden, so a pool reaches the stuck state on the first
// Sync with a retry ceiling of 1.
func denyingClient(t *testing.T) client.WithWatch {
	t.Helper()
	scheme := newScheme(t)
	return fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&lennyv1.SandboxWarmPool{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
				return apierrors.NewForbidden(
					schema.GroupResource{Group: "lenny.dev", Resource: "sandboxwarmpools"},
					obj.GetName(), errors.New("validator rejected"),
				)
			},
		}).Build()
}

// spec: §4.6.2 item 3 condition (c) — an advance of the pool's
// reconciliation_resume_epoch between Syncs (the cross-process resume
// the gateway records in Postgres) clears the stuck pool's in-memory
// admission-denial backoff so it is retried on the next tick.
func TestSyncResumeEpochAdvanceClearsStuckPool_spec_4_6_2(t *testing.T) {
	c := denyingClient(t)
	cfg := config()
	cfg.ResumeEpoch = 4
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	now := time.Unix(1000, 0)
	r := &poolscaling.Reconciler{
		Client: c, Source: src,
		AdmissionDeniedRetryCeiling: 1,
		Now:                         func() time.Time { return now },
	}

	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	if got := r.StuckPools(); len(got) != 1 {
		t.Fatalf("StuckPools after first sync = %v, want one stuck pool", got)
	}

	// No epoch change on the next tick: the pool stays stuck.
	now = now.Add(5 * time.Minute)
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if got := r.StuckPools(); len(got) != 1 {
		t.Fatalf("StuckPools with no resume = %v, want still stuck", got)
	}

	// Operator resumes through the gateway: the epoch advances. The next
	// Sync clears the denial backoff before re-applying. The apply still
	// fails (validator still denies), so the pool re-enters the stuck
	// state from a fresh count — but the consecutive counter was reset,
	// proving the resume took effect.
	cfg.ResumeEpoch = 5
	src.configs = []poolscaling.PoolConfig{cfg}
	if got := r.ConsecutiveAdmissionDenials(testNS, testPool); got == 0 {
		t.Fatalf("precondition: expected a non-zero denial count, got 0")
	}
	now = now.Add(5 * time.Minute)
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync 3: %v", err)
	}
	// The resume cleared the counter; the re-apply this tick denied once,
	// so the count is exactly 1 (not 2+), confirming the reset happened
	// before the apply.
	if got := r.ConsecutiveAdmissionDenials(testNS, testPool); got != 1 {
		t.Errorf("consecutive denials after resume = %d, want 1 (counter reset then one fresh denial)", got)
	}
}

// spec: §4.6.2 item 3 condition (c) — a non-zero resume epoch observed
// for the first time (a freshly-started leader reading a pool that was
// resumed before the restart) does not spuriously clear: there is no
// denial state on a fresh leader, and the strictly-increasing rule means
// only an advance after first observation triggers a resume.
func TestSyncResumeEpochFirstObservationDoesNotClear_spec_4_6_2(t *testing.T) {
	c := denyingClient(t)
	cfg := config()
	cfg.ResumeEpoch = 9 // already non-zero at startup
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	now := time.Unix(2000, 0)
	r := &poolscaling.Reconciler{
		Client: c, Source: src,
		AdmissionDeniedRetryCeiling: 2,
		Now:                         func() time.Time { return now },
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	// One denial recorded this tick. A spurious resume would have reset
	// it to 0 before the apply, leaving it at 1 either way — so to
	// distinguish, run a second tick with the SAME epoch: a correct
	// implementation accumulates to 2, a buggy one that re-clears on the
	// unchanged epoch would stay at 1.
	now = now.Add(5 * time.Minute)
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if got := r.ConsecutiveAdmissionDenials(testNS, testPool); got != 2 {
		t.Errorf("consecutive denials over two ticks with unchanged epoch = %d, want 2", got)
	}
}

// spec: §4.6.2 lines 558, 560 — the reconcile timestamp annotation is
// stamped alongside the generation when the generation changes, and is
// not rewritten when the generation is unchanged (so a steady-state pool
// does not churn the CRD every tick).
func TestSyncStampsLastReconciledAtOnGenerationChange_spec_4_6_2(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	cfg := config()
	cfg.Generation = 3
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	t0 := time.Unix(5000, 0).UTC()
	r := &poolscaling.Reconciler{Client: c, Source: src, Now: func() time.Time { return t0 }}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	tmpl := getTemplate(t, c)
	first := tmpl.Annotations[lennyv1.AnnotationLastReconciledAt]
	if first == "" {
		t.Fatalf("last-reconciled-at not stamped on first reconcile: %v", tmpl.Annotations)
	}
	if _, err := time.Parse(time.RFC3339Nano, first); err != nil {
		t.Errorf("last-reconciled-at %q is not RFC3339Nano: %v", first, err)
	}

	// Same generation, later clock: the annotation must NOT advance (no
	// CRD rewrite for an unchanged pool).
	r2 := &poolscaling.Reconciler{Client: c, Source: src, Now: func() time.Time { return t0.Add(time.Hour) }}
	if err := r2.Sync(context.Background()); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if got := getTemplate(t, c).Annotations[lennyv1.AnnotationLastReconciledAt]; got != first {
		t.Errorf("last-reconciled-at changed on unchanged generation: %q -> %q", first, got)
	}

	// Generation bump: the annotation advances to the new clock.
	cfg.Generation = 4
	src.configs = []poolscaling.PoolConfig{cfg}
	t1 := t0.Add(2 * time.Hour)
	r3 := &poolscaling.Reconciler{Client: c, Source: src, Now: func() time.Time { return t1 }}
	if err := r3.Sync(context.Background()); err != nil {
		t.Fatalf("sync 3: %v", err)
	}
	got := getTemplate(t, c).Annotations[lennyv1.AnnotationLastReconciledAt]
	if got == first {
		t.Errorf("last-reconciled-at did not advance on generation change (still %q)", got)
	}
	if want := t1.Format(time.RFC3339Nano); got != want {
		t.Errorf("last-reconciled-at = %q, want %q", got, want)
	}
}
