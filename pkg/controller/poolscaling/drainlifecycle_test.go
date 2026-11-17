// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
)

// seedPodSet returns the test pool's PodSet with the given warm targets and
// status.readyCount, for pre-loading into the fake client.
func seedPodSet(minWarm, maxWarm, readyCount int32) *lennyv1.PodSet {
	return &lennyv1.PodSet{
		ObjectMeta: metav1.ObjectMeta{Name: testPool, Namespace: testNS},
		Spec:       lennyv1.PodSetSpec{RuntimeRef: "claude-code", MinWarm: minWarm, MaxWarm: maxWarm},
		Status:     lennyv1.PodSetStatus{ReadyCount: readyCount},
	}
}

func podSetExists(t *testing.T, c client.Client) bool {
	t.Helper()
	var p lennyv1.PodSet
	err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testPool}, &p)
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatalf("get PodSet: %v", err)
	}
	return true
}

// spec: §10.7 — a paused experiment's variant pool pins
// minWarm to 0 while leaving maxWarm at its ceiling so warm pods drain
// naturally and the CRD is retained.
func TestSyncForceZeroMinWarmPinsPausedVariantToZero(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.PodSet{}).Build()
	cfg := config()
	cfg.ForceZeroMinWarm = true
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// getPodSet fails the test when the PodSet is absent: the PodSet is
	// retained so the pool can be restored on re-activation.
	pool := getPodSet(t, c)
	if pool.Spec.MinWarm != 0 {
		t.Errorf("paused variant minWarm = %d, want 0", pool.Spec.MinWarm)
	}
	if pool.Spec.MaxWarm != 10 {
		t.Errorf("paused variant maxWarm = %d, want 10 (unchanged)", pool.Spec.MaxWarm)
	}
	if pool.Spec.RuntimeRef != "claude-code" {
		t.Errorf("paused variant runtimeRef = %q, want claude-code (pod settings kept)", pool.Spec.RuntimeRef)
	}
}

// spec: §10.7 — a concluded variant pool with warm pods still
// ready is driven to 0/0 (full drain) but not yet deleted.
func TestSyncDrainAndDeleteDrainsWhileReady(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(seedPodSet(3, 10, 4)).
		WithStatusSubresource(&lennyv1.PodSet{}).
		Build()
	cfg := config()
	cfg.DrainAndDelete = true
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !podSetExists(t, c) {
		t.Fatal("PodSet deleted while readyCount > 0; want retained for drain")
	}
	pool := getPodSet(t, c)
	if pool.Spec.MinWarm != 0 || pool.Spec.MaxWarm != 0 {
		t.Errorf("draining pool min/max = %d/%d, want 0/0", pool.Spec.MinWarm, pool.Spec.MaxWarm)
	}
}

// spec: §10.7 — once status.readyCount reaches 0 the
// PodSet is deleted, and a later Sync does not recreate it.
func TestSyncDrainAndDeleteDeletesWhenDrained(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(seedPodSet(0, 0, 0)).
		WithStatusSubresource(&lennyv1.PodSet{}).
		Build()
	cfg := config()
	cfg.DrainAndDelete = true
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if podSetExists(t, c) {
		t.Fatal("PodSet not deleted after readyCount reached 0")
	}
	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if podSetExists(t, c) {
		t.Fatal("a Sync after the drain recreated the deleted PodSet")
	}
}

// A concluded experiment whose variant pool was already reclaimed must
// not be recreated: drain-and-delete on an absent PodSet is a no-op.
func TestSyncDrainAndDeleteAbsentPoolIsNoOp(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&lennyv1.PodSet{}).
		Build()
	cfg := config()
	cfg.DrainAndDelete = true
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if podSetExists(t, c) {
		t.Fatal("drain-and-delete recreated an absent PodSet")
	}
}
