// SPDX-License-Identifier: MIT

package sandbox

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/lifecycle"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// defaultSDKConnectTimeout is the §6.1 line 69 sdkConnectTimeoutSeconds
// default applied when a preConnect pool's ScalePolicy leaves it unset.
const defaultSDKConnectTimeout = 60 * time.Second

// sdkConnectTimeoutTotal is the §6.1 line 69 watchdog counter: it
// increments when the WarmPoolController retires a pod that hung in
// sdk_connecting past sdkConnectTimeoutSeconds. The §16.5 SDKConnectTimeout
// alert reads its rate. Labeled by pool per the §16.1 catalog.
var sdkConnectTimeoutTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_warmpool_sdk_connect_timeout_total",
		Help: "SDK-warm handshake watchdog timeouts (sdk_connecting → failed).",
	}, []string{"pool"})
	if err != nil {
		panic(fmt.Sprintf("sandbox: build sdk-connect-timeout counter: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, c)
	return c
}()

// sdkWarmConfig is the resolved §6.1 SDK-warm configuration for one
// Sandbox: whether its runtime is preConnect-capable, whether the §6.1
// circuit breaker has disabled SDK-warm for the pool, and the watchdog
// budget.
type sdkWarmConfig struct {
	// preConnect is the runtime's capabilities.preConnect flag.
	preConnect bool
	// disabled is the pool's spec.sdkWarmDisabled circuit-breaker flag.
	disabled bool
	// connectTimeout is the resolved sdkConnectTimeoutSeconds budget.
	connectTimeout time.Duration
}

// sdkWarmActive reports whether the warm path should route through
// sdk_connecting: the runtime declares preConnect and the circuit breaker
// has not disabled SDK-warm for the pool (§6.1 line 50).
func (c sdkWarmConfig) sdkWarmActive() bool {
	return c.preConnect && !c.disabled
}

// resolveSDKWarm best-effort resolves the §6.1 SDK-warm configuration for
// a Sandbox in-cluster: the runtime's capabilities.preConnect (Runtime CRD
// by spec.runtimeRef), the pool's spec.sdkWarmDisabled circuit-breaker
// flag, and the sdkConnectTimeoutSeconds watchdog budget (SandboxWarmPool
// by spec.poolRef). Any lookup miss yields preConnect=false so the pod
// warms straight to pod-warm idle rather than blocking on a stuck
// sdk_connecting phase. spec: §6.1 lines 30-69; §5.1 capabilities.preConnect.
func (r *Reconciler) resolveSDKWarm(ctx context.Context, sb *lennyv1.Sandbox) sdkWarmConfig {
	cfg := sdkWarmConfig{connectTimeout: defaultSDKConnectTimeout}

	if sb.Spec.RuntimeRef != "" {
		var rt lennyv1.Runtime
		if err := r.Client.Get(ctx, client.ObjectKey{Name: sb.Spec.RuntimeRef}, &rt); err == nil {
			cfg.preConnect = rt.Spec.Capabilities != nil && rt.Spec.Capabilities.PreConnect
		} else if !apierrors.IsNotFound(err) {
			// A transient lookup error is treated as not-preConnect so a
			// flaky read never strands a pod in sdk_connecting.
			cfg.preConnect = false
		}
	}
	if !cfg.preConnect {
		return cfg
	}

	if sb.Spec.PoolRef != "" {
		var pool lennyv1.SandboxWarmPool
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: sb.Namespace, Name: sb.Spec.PoolRef}, &pool); err == nil {
			cfg.disabled = pool.Spec.SDKWarmDisabled
			if pool.Spec.ScalePolicy != nil && pool.Spec.ScalePolicy.SDKConnectTimeoutSeconds > 0 {
				cfg.connectTimeout = time.Duration(pool.Spec.ScalePolicy.SDKConnectTimeoutSeconds) * time.Second
			}
		}
	}
	return cfg
}

// sdkWarmInputs builds the planner input for a preConnect Sandbox: the
// current phase, the observed pod, and the §6.1 watchdog clock re-anchored
// per edge. On the warm-fill edge (warming → sdk_connecting) the clock
// measures from pod start. On the recycle re-warm edge (a recycling claim
// carrying rewarmStartedAt, which the OccupancyReconciler projects to
// sdk_connecting) it measures from the rewarmStartedAt stamp so neither the
// prior occupancy episode nor the whole-pod scrub counts against the
// re-warm budget. The recycle edge also flips the success terminus from
// idle to reserved (the claim projection owns the reserved write).
// spec: §6.1 (watchdog clock per edge, reserved terminus), §3.3 (the
// watchdog measures only the re-warm leg).
func sdkWarmInputs(sb *lennyv1.Sandbox, obs lifecycle.PodObservation, pod *corev1.Pod, cfg sdkWarmConfig, rewarm *time.Time, now time.Time) lifecycle.SDKWarmInputs {
	elapsed := podRunningFor(pod, now)
	recycle := rewarm != nil
	if recycle {
		elapsed = rewarmElapsed(*rewarm, now)
	}
	return lifecycle.SDKWarmInputs{
		Phase:             state.State(sb.Status.Phase),
		Pod:               obs,
		SDKConnectElapsed: elapsed,
		SDKConnectTimeout: cfg.connectTimeout,
		Recycle:           recycle,
	}
}

// observeRewarm reads the per-pod SandboxClaim (claim-<podName>, with the
// pod named after the Sandbox) and returns the §6.1 re-warm-start stamp
// when the Sandbox sits on the recycle re-warm edge: the claim is in the
// recycling binding state and carries a rewarmStartedAt stamp. Any other
// state (no claim, a bound/reserved/terminal claim, or a recycling claim
// still in the whole-pod scrub leg with no stamp) yields nil, so the
// watchdog clock falls back to pod start for the warm-fill edge. A claim
// lookup error other than NotFound is returned: the reconciler treats it as
// a transient read failure and retries rather than mis-anchoring the clock.
// spec: §6.1 (re-warm-start anchor), §6.2 (recycle edges), §4.6.3 (claim
// binding state).
func (r *Reconciler) observeRewarm(ctx context.Context, sb *lennyv1.Sandbox) (*time.Time, error) {
	var cl lennyv1.SandboxClaim
	key := client.ObjectKey{Namespace: sb.Namespace, Name: sdkWarmClaimName(sb.Name)}
	if err := r.Client.Get(ctx, key, &cl); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get sandbox claim for %s: %w", sb.Name, err)
	}
	// The re-warm leg is the recycling binding state with a stamp; a
	// recycling claim without a stamp is still in the whole-pod scrub leg
	// (which projects claimed, bounded by the scrub-report timeout, not the
	// sdk_connecting watchdog). spec: §6.1, §3.3.
	if cl.Status.Phase != string(claimstate.Recycling) || cl.Status.RewarmStartedAt == nil {
		return nil, nil
	}
	t := cl.Status.RewarmStartedAt.Time
	return &t, nil
}

// rewarmElapsed is the §6.1 recycle re-warm watchdog clock: the time
// elapsed since the gateway stamped rewarmStartedAt when the recycle
// disposition began the SDK re-warm. A stamp in the future (clock skew)
// yields zero so the watchdog stays dormant rather than firing spuriously.
// spec: §6.1 (re-warm-start anchor), §3.3.
func rewarmElapsed(rewarmStartedAt, now time.Time) time.Duration {
	d := now.Sub(rewarmStartedAt)
	if d < 0 {
		return 0
	}
	return d
}

// sdkWarmClaimName is the deterministic SandboxClaim name for a pod
// (claim-<podName>); the Sandbox-to-Pod reconciler names the pod after the
// Sandbox, so the claim name keys off the Sandbox name. It mirrors the
// gateway's podclaim.claimName and the WarmPoolController occupancy arm so
// all three resolve the same per-pod claim. It is duplicated rather than
// imported to avoid an import cycle, matching the occupancy arm.
func sdkWarmClaimName(podName string) string {
	return "claim-" + podName
}

// podRunningFor returns how long the pod has been running, used as the
// §6.1 line 69 sdk_connecting watchdog clock (SDK pre-connect begins when
// the pod starts running). A pod with no StartTime yet returns zero, which
// disables the watchdog until the kubelet records the start.
func podRunningFor(pod *corev1.Pod, now time.Time) time.Duration {
	if pod == nil || pod.Status.StartTime == nil {
		return 0
	}
	d := now.Sub(pod.Status.StartTime.Time)
	if d < 0 {
		return 0
	}
	return d
}
