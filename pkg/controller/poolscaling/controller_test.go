// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
)

const (
	testNS   = "lenny-agents"
	testPool = "claude-worker-small"
)

type fakeSource struct {
	configs []poolscaling.PoolConfig
	err     error
}

func (f *fakeSource) ListPoolConfigs(context.Context) ([]poolscaling.PoolConfig, error) {
	return f.configs, f.err
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func config() poolscaling.PoolConfig {
	return poolscaling.PoolConfig{
		Name:      testPool,
		Namespace: testNS,
		Template: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-code",
			IsolationProfile: "sandboxed",
			ResourceClass:    "medium",
			ExecutionMode:    "session",
		},
		MinWarm: 3,
		MaxWarm: 10,
	}
}

func syncOnce(t *testing.T, c client.Client, src *fakeSource) error {
	t.Helper()
	r := &poolscaling.Reconciler{Client: c, Source: src}
	return r.Sync(context.Background())
}

func getTemplate(t *testing.T, c client.Client) lennyv1.SandboxTemplate {
	t.Helper()
	var tm lennyv1.SandboxTemplate
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: testNS, Name: testPool}, &tm); err != nil {
		t.Fatalf("get template: %v", err)
	}
	return tm
}

func getWarmPool(t *testing.T, c client.Client) lennyv1.SandboxWarmPool {
	t.Helper()
	var p lennyv1.SandboxWarmPool
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: testNS, Name: testPool}, &p); err != nil {
		t.Fatalf("get warm pool: %v", err)
	}
	return p
}

func TestSyncCreatesTheCRDPair(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	tmpl := getTemplate(t, c)
	if tmpl.Spec.RuntimeRef != "claude-code" || tmpl.Spec.IsolationProfile != "sandboxed" {
		t.Errorf("template spec = %+v, want runtimeRef claude-code / isolation sandboxed", tmpl.Spec)
	}

	pool := getWarmPool(t, c)
	if pool.Spec.TemplateRef != testPool {
		t.Errorf("warm pool templateRef = %q, want %q", pool.Spec.TemplateRef, testPool)
	}
	if pool.Spec.MinWarm != 3 || pool.Spec.MaxWarm != 10 {
		t.Errorf("warm pool min/max = %d/%d, want 3/10", pool.Spec.MinWarm, pool.Spec.MaxWarm)
	}
}

func TestSyncUpdatesAnExistingPool(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	// The admin API raised minWarm and switched the runtime image line.
	changed := config()
	changed.MinWarm = 7
	changed.Template.RuntimeRef = "claude-code-next"
	src.configs = []poolscaling.PoolConfig{changed}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	if got := getWarmPool(t, c).Spec.MinWarm; got != 7 {
		t.Errorf("warm pool minWarm = %d after re-sync, want 7", got)
	}
	if got := getTemplate(t, c).Spec.RuntimeRef; got != "claude-code-next" {
		t.Errorf("template runtimeRef = %q after re-sync, want claude-code-next", got)
	}
}

func TestSyncCorrectsManualSpecDrift(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	// Simulate an operator hand-editing the derived CRD.
	pool := getWarmPool(t, c)
	pool.Spec.MinWarm = 999
	if err := c.Update(context.Background(), &pool); err != nil {
		t.Fatalf("manual update: %v", err)
	}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if got := getWarmPool(t, c).Spec.MinWarm; got != 3 {
		t.Errorf("warm pool minWarm = %d, want 3 (drift not corrected)", got)
	}
}

func TestSyncHandlesMultiplePools(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()

	a := config()
	a.Name = "pool-a"
	b := config()
	b.Name = "pool-b"
	b.MinWarm = 5
	src := &fakeSource{configs: []poolscaling.PoolConfig{a, b}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	var pools lennyv1.SandboxWarmPoolList
	if err := c.List(context.Background(), &pools, client.InNamespace(testNS)); err != nil {
		t.Fatalf("list warm pools: %v", err)
	}
	if len(pools.Items) != 2 {
		t.Fatalf("synced %d warm pools, want 2", len(pools.Items))
	}
}

func TestSyncPropagatesSourceError(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{err: errors.New("postgres unreachable")}

	if err := syncOnce(t, c, src); err == nil {
		t.Fatal("Sync should return an error when the pool config source fails")
	}
}
