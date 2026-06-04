// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
)

// seedWarmPool returns a SandboxWarmPool object with the given spec and
// status.readyCount, for pre-loading into the fake client.
func seedWarmPool(minWarm, maxWarm, readyCount int32) *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: testPool, Namespace: testNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: testPool, MinWarm: minWarm, MaxWarm: maxWarm},
		Status:     lennyv1.SandboxWarmPoolStatus{ReadyCount: readyCount},
	}
}

func warmPoolExists(t *testing.T, c client.Client) bool {
	t.Helper()
	var p lennyv1.SandboxWarmPool
	err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testPool}, &p)
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatalf("get warm pool: %v", err)
	}
	return true
}

// spec: §10.7 line 1102 — a paused experiment's variant pool pins
// minWarm to 0 while leaving maxWarm at its ceiling so warm pods drain
// naturally and the CRD is retained.
func TestSyncForceZeroMinWarmPinsPausedVariantToZero(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	cfg := config()
	cfg.ForceZeroMinWarm = true
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	pool := getWarmPool(t, c)
	if pool.Spec.MinWarm != 0 {
		t.Errorf("paused variant minWarm = %d, want 0", pool.Spec.MinWarm)
	}
	if pool.Spec.MaxWarm != 10 {
		t.Errorf("paused variant maxWarm = %d, want 10 (unchanged)", pool.Spec.MaxWarm)
	}
	// The SandboxTemplate is retained so the pool can be restored on
	// re-activation.
	_ = getTemplate(t, c)
}

// spec: §10.7 line 1104 — a concluded variant pool with warm pods still
// ready is driven to 0/0 (full drain) but not yet deleted.
func TestSyncDrainAndDeleteDrainsWhileReady(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(seedWarmPool(3, 10, 4)).
		WithStatusSubresource(&lennyv1.SandboxWarmPool{}).
		Build()
	cfg := config()
	cfg.DrainAndDelete = true
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !warmPoolExists(t, c) {
		t.Fatal("warm pool deleted while readyCount > 0; want retained for drain")
	}
	pool := getWarmPool(t, c)
	if pool.Spec.MinWarm != 0 || pool.Spec.MaxWarm != 0 {
		t.Errorf("draining pool min/max = %d/%d, want 0/0", pool.Spec.MinWarm, pool.Spec.MaxWarm)
	}
	// The SandboxTemplate is not deleted.
	_ = getTemplate(t, c)
}

// spec: §10.7 line 1104 — once status.readyCount reaches 0 the
// SandboxWarmPool CRD is deleted; the SandboxTemplate is retained.
func TestSyncDrainAndDeleteDeletesWhenDrained(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(seedWarmPool(0, 0, 0)).
		WithStatusSubresource(&lennyv1.SandboxWarmPool{}).
		Build()
	cfg := config()
	cfg.DrainAndDelete = true
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if warmPoolExists(t, c) {
		t.Fatal("warm pool not deleted after readyCount reached 0")
	}
	// SandboxTemplate retained per §10.7 line 1104.
	_ = getTemplate(t, c)
}

// A concluded experiment whose variant pool was already reclaimed must
// not be recreated: drain-and-delete on an absent pool is a no-op, and
// the SandboxTemplate is still reconciled.
func TestSyncDrainAndDeleteAbsentPoolIsNoOp(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&lennyv1.SandboxWarmPool{}).
		Build()
	cfg := config()
	cfg.DrainAndDelete = true
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if warmPoolExists(t, c) {
		t.Fatal("drain-and-delete recreated an absent SandboxWarmPool")
	}
	_ = getTemplate(t, c)
}
