// SPDX-License-Identifier: MIT

package warmpool

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// poolWarmingUp is the alert-support gauge backing the WarmPoolBootstrapping
// rule. It is 1 while a pool's PoolWarmingUp condition is True (minWarm > 0,
// no idle pods, at least one pod still warming) and 0 otherwise. The
// WarmPoolController is the sole writer; it sets the gauge in the same
// reconcile step that writes the PoolWarmingUp condition, so the alert's
// `lenny_pool_warming_up == 1` expression has a live producer. Without this
// gauge the shipped alert rule references a metric no component emits and can
// never fire.
//
// spec: §5.2 line 627 (WarmPoolBootstrapping alert fires when
// PoolWarmingUp = True for more than warmupDeadlineSeconds); §16.5 line 526.
var poolWarmingUp = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_pool_warming_up",
		Help: "1 while a warm pool's PoolWarmingUp condition is True, 0 otherwise.",
	}, []string{"pool"})
	if err != nil {
		panic(fmt.Sprintf("warmpool: build pool-warming-up gauge: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, g)
	return g
}()

// setPoolWarmingUp publishes the warming gauge for a pool from the
// PoolWarmingUp condition status the reconcile derived. It runs on every
// reconcile (not only on a condition change) so a controller restart
// re-establishes the series.
//
// spec: §5.2 line 627.
func setPoolWarmingUp(pool string, warming bool) {
	v := 0.0
	if warming {
		v = 1.0
	}
	poolWarmingUp.WithLabelValues(pool).Set(v)
}

// forgetPoolWarmingUp clears a deleted pool's warming gauge series so a
// removed pool does not leave a stale `lenny_pool_warming_up` series behind.
func forgetPoolWarmingUp(pool string) {
	poolWarmingUp.DeleteLabelValues(pool)
}
