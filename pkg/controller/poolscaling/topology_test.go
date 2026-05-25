// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
)

// spec: §5.2 lines 631-634 — the PoolScalingController owns
// SandboxTemplate.spec and writes the soft zone/node spread defaults
// when the pool definition carries none.
func TestSyncWritesTopologyDefaults_spec_5_2_631(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got := getTemplate(t, c).Spec.TopologySpreadConstraints
	if len(got) != 2 {
		t.Fatalf("topology constraints = %d, want 2 (zone + node)", len(got))
	}
	keys := map[string]corev1.TopologySpreadConstraint{}
	for _, c := range got {
		keys[c.TopologyKey] = c
	}
	for _, key := range []string{"topology.kubernetes.io/zone", "kubernetes.io/hostname"} {
		cstr, ok := keys[key]
		if !ok {
			t.Fatalf("missing default constraint for topologyKey %q", key)
		}
		if cstr.MaxSkew != 1 {
			t.Errorf("%s maxSkew = %d, want 1", key, cstr.MaxSkew)
		}
		if cstr.WhenUnsatisfiable != corev1.ScheduleAnyway {
			t.Errorf("%s whenUnsatisfiable = %q, want ScheduleAnyway", key, cstr.WhenUnsatisfiable)
		}
		// The selector scopes skew to the pool's own pods.
		if cstr.LabelSelector == nil || cstr.LabelSelector.MatchLabels["lenny.dev/pool"] != testPool {
			t.Errorf("%s selector = %+v, want match on lenny.dev/pool=%s", key, cstr.LabelSelector, testPool)
		}
	}
}

// spec: §5.2 line 636 — deployers can override the defaults per pool;
// when the definition carries constraints the controller writes them
// verbatim rather than replacing them with the defaults.
func TestSyncPreservesTopologyOverride_spec_5_2_636(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()

	cfg := config()
	cfg.Template.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
		MaxSkew:           1,
		TopologyKey:       "topology.kubernetes.io/zone",
		WhenUnsatisfiable: corev1.DoNotSchedule,
		LabelSelector:     &metav1.LabelSelector{MatchLabels: map[string]string{"app": "custom"}},
	}}
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got := getTemplate(t, c).Spec.TopologySpreadConstraints
	if len(got) != 1 {
		t.Fatalf("topology constraints = %d, want 1 (the override, not the 2 defaults)", len(got))
	}
	if got[0].WhenUnsatisfiable != corev1.DoNotSchedule {
		t.Errorf("override whenUnsatisfiable = %q, want DoNotSchedule (strict spread)", got[0].WhenUnsatisfiable)
	}
	if got[0].LabelSelector.MatchLabels["app"] != "custom" {
		t.Errorf("override selector lost: %+v", got[0].LabelSelector)
	}
}

// A re-sync over a pool whose stored template already carries the
// defaults must not append a second copy: the §5.2 defaults seed an
// empty list only, and the controller rewrites the spec wholesale each
// pass, so the count stays at 2.
func TestSyncTopologyDefaultsAreIdempotent_spec_5_2_631(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	if got := getTemplate(t, c).Spec.TopologySpreadConstraints; len(got) != 2 {
		t.Fatalf("after re-sync topology constraints = %d, want 2", len(got))
	}
	// The source PoolConfig is never mutated by the sync (the defaults
	// are applied to a copy), so a follow-up read of the config still
	// shows no constraints.
	cfgs, _ := src.ListPoolConfigs(context.Background())
	if len(cfgs[0].Template.TopologySpreadConstraints) != 0 {
		t.Errorf("source PoolConfig was mutated: %+v", cfgs[0].Template.TopologySpreadConstraints)
	}
}
