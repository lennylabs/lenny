// SPDX-License-Identifier: MIT

package sandbox

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

func sdkWarmScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("add lennyv1 scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1 scheme: %v", err)
	}
	return s
}

// spec: §6.1 lines 30-69 — resolveSDKWarm reads capabilities.preConnect
// from the Runtime CRD, the circuit-breaker flag and watchdog budget from
// the SandboxWarmPool, and fails safe to pod-warm on any miss.
func TestResolveSDKWarm_spec_6_1(t *testing.T) {
	scheme := sdkWarmScheme(t)
	timeout := int64(45)

	preConnectRuntime := &lennyv1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: "claude"},
		Spec:       lennyv1.RuntimeSpec{Capabilities: &lennyv1.RuntimeCapabilitiesCRD{PreConnect: true}},
	}
	podWarmRuntime := &lennyv1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: "echo"},
		Spec:       lennyv1.RuntimeSpec{Capabilities: &lennyv1.RuntimeCapabilitiesCRD{PreConnect: false}},
	}
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "pool-a", Namespace: "lenny-agents"},
		Spec: lennyv1.SandboxWarmPoolSpec{
			ScalePolicy: &lennyv1.ScalePolicy{SDKConnectTimeoutSeconds: timeout},
		},
	}
	disabledPool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "pool-disabled", Namespace: "lenny-agents"},
		Spec:       lennyv1.SandboxWarmPoolSpec{SDKWarmDisabled: true},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(preConnectRuntime, podWarmRuntime, pool, disabledPool).Build()
	r := &Reconciler{Client: cl, Scheme: scheme}

	sb := func(rtRef, poolRef string) *lennyv1.Sandbox {
		return &lennyv1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{Name: "sb", Namespace: "lenny-agents"},
			Spec:       lennyv1.SandboxSpec{RuntimeRef: rtRef, PoolRef: poolRef},
		}
	}

	t.Run("preConnect runtime with pool timeout is active", func(t *testing.T) {
		cfg := r.resolveSDKWarm(context.Background(), sb("claude", "pool-a"))
		if !cfg.sdkWarmActive() {
			t.Fatalf("expected sdkWarmActive, got %+v", cfg)
		}
		if cfg.connectTimeout != time.Duration(timeout)*time.Second {
			t.Fatalf("connectTimeout = %v, want %v", cfg.connectTimeout, time.Duration(timeout)*time.Second)
		}
	})

	t.Run("pod-warm runtime is not active and uses no SDK-warm path", func(t *testing.T) {
		cfg := r.resolveSDKWarm(context.Background(), sb("echo", "pool-a"))
		if cfg.preConnect || cfg.sdkWarmActive() {
			t.Fatalf("expected pod-warm, got %+v", cfg)
		}
	})

	t.Run("circuit-breaker disabled pool is not active", func(t *testing.T) {
		cfg := r.resolveSDKWarm(context.Background(), sb("claude", "pool-disabled"))
		if !cfg.preConnect {
			t.Fatalf("expected preConnect true")
		}
		if cfg.sdkWarmActive() {
			t.Fatalf("disabled pool must not be active, got %+v", cfg)
		}
	})

	t.Run("missing runtime fails safe to pod-warm", func(t *testing.T) {
		cfg := r.resolveSDKWarm(context.Background(), sb("ghost", "pool-a"))
		if cfg.preConnect || cfg.sdkWarmActive() {
			t.Fatalf("missing runtime must be pod-warm, got %+v", cfg)
		}
	})

	t.Run("default timeout when pool omits it", func(t *testing.T) {
		cl2 := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(preConnectRuntime,
				&lennyv1.SandboxWarmPool{ObjectMeta: metav1.ObjectMeta{Name: "bare", Namespace: "lenny-agents"}}).Build()
		r2 := &Reconciler{Client: cl2, Scheme: scheme}
		cfg := r2.resolveSDKWarm(context.Background(), sb("claude", "bare"))
		if cfg.connectTimeout != defaultSDKConnectTimeout {
			t.Fatalf("connectTimeout = %v, want default %v", cfg.connectTimeout, defaultSDKConnectTimeout)
		}
	})
}

// spec: §6.1 line 69 — podRunningFor is the watchdog clock; an unstarted
// pod yields zero so the watchdog stays dormant.
func TestPodRunningFor(t *testing.T) {
	now := time.Now()
	started := metav1.NewTime(now.Add(-30 * time.Second))
	running := &corev1.Pod{Status: corev1.PodStatus{StartTime: &started}}
	if d := podRunningFor(running, now); d < 29*time.Second || d > 31*time.Second {
		t.Fatalf("podRunningFor = %v, want ~30s", d)
	}
	if d := podRunningFor(&corev1.Pod{}, now); d != 0 {
		t.Fatalf("podRunningFor with no StartTime = %v, want 0", d)
	}
	if d := podRunningFor(nil, now); d != 0 {
		t.Fatalf("podRunningFor(nil) = %v, want 0", d)
	}
}
