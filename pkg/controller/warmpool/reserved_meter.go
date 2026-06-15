// SPDX-License-Identifier: MIT

package warmpool

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// reservedPods is the §16.1 lenny_warmpool_reserved_pods gauge: the
// instantaneous count of pods whose occupancy claim is in the §6.2
// reserved hold window (scrubbed, SDK-warm on preConnect pools, held for
// the pinned tenant until the claim-hold TTL expires), labeled by pool.
// Per §4.6.2 these pods are occupied: they are excluded from claimable
// idle inventory and counted occupied for scaling, so a long
// gateway.claimHoldTTLSeconds depresses apparent idle inventory. The
// §16.1 catalog declares the gauge but no component emitted it before
// the WarmPoolController; the controller is the sole writer and refreshes
// it on every reconcile from the reserved-phase pods it observes, so a
// controller restart re-establishes the series.
//
// spec: §16.1 (lenny_warmpool_reserved_pods gauge); §4.6.2 ("reserved
// pods count as occupied").
var reservedPods = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_warmpool_reserved_pods",
		Help: "Pods whose occupancy claim is in the reserved hold window (instantaneous, per pool).",
	}, []string{"pool"})
	if err != nil {
		panic(fmt.Sprintf("warmpool: build reserved-pods gauge: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, g)
	return g
}()

// setReservedPods publishes the pool's current reserved-pod count to the
// §16.1 lenny_warmpool_reserved_pods gauge. It runs once per reconcile
// (not only on a count change) so a controller restart re-establishes the
// series from the observed reserved-phase pods.
//
// spec: §16.1; §4.6.2.
func setReservedPods(pool string, count int) {
	reservedPods.WithLabelValues(pool).Set(float64(count))
}

// forgetReservedPods clears a deleted pool's reserved-pods gauge series so
// a removed pool does not leave a stale lenny_warmpool_reserved_pods
// series behind.
func forgetReservedPods(pool string) {
	reservedPods.DeleteLabelValues(pool)
}
