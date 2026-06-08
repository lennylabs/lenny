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
// current phase, the observed pod, and the watchdog clock derived from the
// pod's running time.
func sdkWarmInputs(sb *lennyv1.Sandbox, obs lifecycle.PodObservation, pod *corev1.Pod, cfg sdkWarmConfig, now time.Time) lifecycle.SDKWarmInputs {
	return lifecycle.SDKWarmInputs{
		Phase:             state.State(sb.Status.Phase),
		Pod:               obs,
		SDKConnectElapsed: podRunningFor(pod, now),
		SDKConnectTimeout: cfg.connectTimeout,
	}
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
