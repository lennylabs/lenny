// SPDX-License-Identifier: MIT

package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/lifecycle"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
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

// spec: §6.1 (re-warm-start anchor), §3.3 — rewarmElapsed is the recycle
// re-warm watchdog clock measured from the rewarmStartedAt stamp; a
// future stamp (clock skew) yields zero so the watchdog stays dormant.
func TestRewarmElapsed(t *testing.T) {
	now := time.Now()
	if d := rewarmElapsed(now.Add(-20*time.Second), now); d < 19*time.Second || d > 21*time.Second {
		t.Fatalf("rewarmElapsed = %v, want ~20s", d)
	}
	if d := rewarmElapsed(now.Add(10*time.Second), now); d != 0 {
		t.Fatalf("rewarmElapsed with future stamp = %v, want 0", d)
	}
}

// spec: §6.1 (watchdog clock per edge, reserved terminus), §3.3 —
// sdkWarmInputs re-anchors the watchdog clock and selects the terminus per
// edge: pod start on the warm-fill edge (no rewarm stamp), the
// rewarmStartedAt stamp on the recycle re-warm edge, with the recycle flag
// flipping the success terminus to reserved.
func TestSDKWarmInputsClockReanchorPerEdge(t *testing.T) {
	now := time.Now()
	started := metav1.NewTime(now.Add(-50 * time.Second))
	pod := &corev1.Pod{Status: corev1.PodStatus{StartTime: &started}}
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb", Namespace: "lenny-agents"},
		Status:     lennyv1.SandboxStatus{Phase: string(state.SDKConnecting)},
	}
	cfg := sdkWarmConfig{preConnect: true, connectTimeout: 60 * time.Second}

	t.Run("warm-fill edge anchors at pod start and uses the idle terminus", func(t *testing.T) {
		in := sdkWarmInputs(sb, lifecycle.PodNotReady, pod, cfg, nil, now)
		if in.Recycle {
			t.Fatalf("warm-fill edge must not set Recycle")
		}
		// The clock is the pod's running time (~50s), not the recycle stamp.
		if in.SDKConnectElapsed < 49*time.Second || in.SDKConnectElapsed > 51*time.Second {
			t.Fatalf("SDKConnectElapsed = %v, want ~50s (pod start)", in.SDKConnectElapsed)
		}
	})

	t.Run("recycle re-warm edge anchors at rewarmStartedAt and sets Recycle", func(t *testing.T) {
		// The re-warm started 10s ago; the prior episode's pod-start clock
		// (~50s, over the 60s budget no — but distinct) must not be used.
		rewarm := now.Add(-10 * time.Second)
		in := sdkWarmInputs(sb, lifecycle.PodNotReady, pod, cfg, &rewarm, now)
		if !in.Recycle {
			t.Fatalf("recycle edge must set Recycle")
		}
		if in.SDKConnectElapsed < 9*time.Second || in.SDKConnectElapsed > 11*time.Second {
			t.Fatalf("SDKConnectElapsed = %v, want ~10s (rewarmStartedAt), not the pod-start clock", in.SDKConnectElapsed)
		}
		// The re-warm leg must not time out on the prior episode's elapsed
		// time: 10s is within the 60s budget even though the pod has been
		// running ~50s.
		if in.TimedOut() {
			t.Fatalf("re-warm within budget must not time out (prior episode must not count)")
		}
	})
}

// spec: §6.1 (re-warm-start anchor), §6.2 (recycle edges), §4.6.3 (claim
// binding state) — observeRewarm returns the stamp only on the recycle
// re-warm leg (a recycling claim carrying rewarmStartedAt); every other
// claim state yields nil so the clock falls back to pod start, and a
// non-NotFound read error is surfaced rather than mis-anchoring the clock.
func TestObserveRewarm(t *testing.T) {
	scheme := sdkWarmScheme(t)
	const ns = "lenny-agents"
	sb := &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sbx-1", Namespace: ns}}
	// metav1.Time serializes at second precision; build a second-aligned
	// stamp so the round-trip through the store is exact.
	stamp := metav1.NewTime(time.Now().Add(-5 * time.Second).Truncate(time.Second))

	claim := func(phase claimstate.State, rewarm *metav1.Time) *lennyv1.SandboxClaim {
		return &lennyv1.SandboxClaim{
			ObjectMeta: metav1.ObjectMeta{Name: sdkWarmClaimName(sb.Name), Namespace: ns},
			Spec:       lennyv1.SandboxClaimSpec{SandboxRef: sb.Name},
			Status:     lennyv1.SandboxClaimStatus{Phase: string(phase), RewarmStartedAt: rewarm},
		}
	}

	t.Run("recycling claim with stamp yields the re-warm anchor", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(claim(claimstate.Recycling, &stamp)).Build()
		r := &Reconciler{Client: cl, Scheme: scheme}
		got, err := r.observeRewarm(context.Background(), sb)
		if err != nil {
			t.Fatalf("observeRewarm: %v", err)
		}
		if got == nil || !got.Equal(stamp.Time) {
			t.Fatalf("observeRewarm = %v, want %v", got, stamp.Time)
		}
	})

	t.Run("no claim is the warm-fill edge (nil, no error)", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		r := &Reconciler{Client: cl, Scheme: scheme}
		got, err := r.observeRewarm(context.Background(), sb)
		if err != nil || got != nil {
			t.Fatalf("observeRewarm = (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("recycling claim without a stamp is the scrub leg (nil)", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(claim(claimstate.Recycling, nil)).Build()
		r := &Reconciler{Client: cl, Scheme: scheme}
		got, err := r.observeRewarm(context.Background(), sb)
		if err != nil || got != nil {
			t.Fatalf("observeRewarm (recycling, no stamp) = (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("bound claim with a stamp is not the re-warm leg (nil)", func(t *testing.T) {
		// A stamp on a non-recycling binding state must not arm the re-warm
		// clock; only the recycling leg projects sdk_connecting.
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(claim(claimstate.Bound, &stamp)).Build()
		r := &Reconciler{Client: cl, Scheme: scheme}
		got, err := r.observeRewarm(context.Background(), sb)
		if err != nil || got != nil {
			t.Fatalf("observeRewarm (bound, stamp) = (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("a non-NotFound read error is surfaced", func(t *testing.T) {
		wantErr := errors.New("apiserver down")
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(_ context.Context, _ client.WithWatch, key client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
					if key.Name == sdkWarmClaimName(sb.Name) {
						return wantErr
					}
					return apierrors.NewNotFound(lennyv1.GroupVersion.WithResource("sandboxclaims").GroupResource(), key.Name)
				},
			}).Build()
		r := &Reconciler{Client: cl, Scheme: scheme}
		if _, err := r.observeRewarm(context.Background(), sb); err == nil || !errors.Is(err, wantErr) {
			t.Fatalf("observeRewarm error = %v, want wrap of %v", err, wantErr)
		}
	})
}
