// SPDX-License-Identifier: MIT

package warmpool

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// reasonUptimeLimit is the sole reason the WarmPoolController-owned
// retirement counter carries. The §16.1 reason vocabulary
// (session_count_limit, uptime_limit, scrub_failure_limit) partitions by
// process: the gateway counter carries session_count_limit and
// scrub_failure_limit, and this controller counter carries uptime_limit,
// the level-triggered maxPodUptimeSeconds reclaim the controller originates
// with no gateway-side count.
//
// spec: spec/16 §16.1 (reason partitions by process across the two
// retirement counters).
const reasonUptimeLimit = "uptime_limit"

// controllerPodRetirement is the §16.1 lenny_controller_pod_retirement_total
// counter: retirements of a recycling session-mode pod initiated by the
// WarmPoolController's level-triggered maxPodUptimeSeconds drain, derived
// from the pod CreationTimestamp. It is registered against the
// controller-runtime metrics registry because the WarmPoolController and the
// gateway run as separate processes with no direct RPC, so the gateway's
// lenny_gateway_pod_retirement_total cannot count a controller-originated
// retirement. The gateway's occupancy-zero recycle disposition suppresses
// its own uptime_limit emission so an over-uptime pod that reaches occupancy
// zero is counted here alone. Labels: reason (frozen to uptime_limit for
// this counter), pool, runtime_class (all finite). A duplicate registration
// (two reconcilers in one process) is tolerated by metrics.MustRegister.
//
// spec: spec/16 §16.1 (lenny_controller_pod_retirement_total, controller-owned
// uptime_limit retirements, summed with the gateway counter via a recording
// rule); spec/05 §5.2 (the maxPodUptimeSeconds retirement is
// WarmPoolController-owned).
var controllerPodRetirement = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_controller_pod_retirement_total",
		Help: "Controller-initiated session-pool pod retirements by reason (level-triggered maxPodUptimeSeconds drain, §16.1).",
	}, []string{"reason", "pool", "runtime_class"})
	if err != nil {
		panic(fmt.Sprintf("warmpool: build controller-pod-retirement counter: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, c)
	return c
}()

// IncControllerUptimeRetirement increments the §16.1
// lenny_controller_pod_retirement_total counter with reason="uptime_limit"
// for the (pool, runtimeClass) tuple. It is the seam wired to
// PodReconciler.OnUptimeRetirement, invoked on the claimed→draining transition
// edge, so the counter records one increment per over-uptime pod rather than
// once per level-triggered reconcile.
//
// spec: spec/16 §16.1; spec/04 §4.6.1 (uptime drains are WarmPoolController-written).
func IncControllerUptimeRetirement(pool, runtimeClass string) {
	controllerPodRetirement.WithLabelValues(reasonUptimeLimit, pool, runtimeClass).Inc()
}
