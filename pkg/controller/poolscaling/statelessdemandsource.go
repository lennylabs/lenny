// SPDX-License-Identifier: MIT

// This file holds the production DemandSource for §5.2 service-mode
// pools. The §5.2 formula derives a service pool's demand from two
// metrics the gateway's tenant-affinity router emits:
//
//   - base_demand_p95  from  rate(lenny_service_requests_total[5m])
//   - burst_p99_claims from  max_over_time(lenny_service_concurrent_active[5m])
//
// The PoolScalingController runs in its own process, separate from the
// gateway replicas that emit these metrics, so — like the §6.1 demotion
// source — it queries a Prometheus-compatible backend for the rolling
// values rather than maintaining the window itself.
//
// The two service-mode metrics are emitted per gateway replica, so the
// queries sum across replicas to recover the pool-wide signal. A pool
// that never emitted a service request series reports Observed=false,
// which holds it at its bootstrap minWarm; this is also the path every
// non-service (session-mode) pool takes, because those pools never emit
// lenny_service_requests_total. The source is therefore safe to wire as
// the sole DemandSource: it activates demand-driven scaling for service
// pools only and leaves every other pool in bootstrap mode until a
// claim-rate DemandSource is wired separately.
//
// spec: §5.2.

package poolscaling

import (
	"context"
	"fmt"
)

// PrometheusStatelessDemandSource is the production DemandSource for
// service-mode pools. It queries a Prometheus-compatible backend for the
// §5.2 service-mode demand signals.
type PrometheusStatelessDemandSource struct {
	// Querier runs the instant queries. Required.
	Querier PromQLQuerier
}

var _ DemandSource = (*PrometheusStatelessDemandSource)(nil)

// PoolDemand computes the §5.2 service-mode demand for poolName. A pool
// with no lenny_service_requests_total series reports Observed=false
// (bootstrap mode); a query error fails the reconcile so a transient
// Prometheus outage does not silently collapse a pool to bootstrap
// sizing.
func (s *PrometheusStatelessDemandSource) PoolDemand(ctx context.Context, poolName string) (Demand, error) {
	matcher := fmt.Sprintf("{pool=%q}", escapePromLabel(poolName))

	// Presence: count of service request series for the pool.
	// Query returns 0 for an empty result vector, which the rate query
	// cannot distinguish from a genuinely-zero rate, so this separate
	// count decides Observed.
	present, err := s.Querier.Query(ctx,
		fmt.Sprintf("count(lenny_service_requests_total%s)", matcher))
	if err != nil {
		return Demand{}, fmt.Errorf("query service-mode presence: %w", err)
	}
	if present < 1 {
		// Not a service-mode pool (or never served a request) — stay at
		// bootstrap minWarm.
		return Demand{}, nil
	}

	base, err := s.Querier.Query(ctx,
		fmt.Sprintf("sum(rate(lenny_service_requests_total%s[5m]))", matcher))
	if err != nil {
		return Demand{}, fmt.Errorf("query service-mode base demand: %w", err)
	}
	burst, err := s.Querier.Query(ctx,
		fmt.Sprintf("sum(max_over_time(lenny_service_concurrent_active%s[5m]))", matcher))
	if err != nil {
		return Demand{}, fmt.Errorf("query service-mode burst demand: %w", err)
	}
	if base < 0 {
		base = 0
	}
	if burst < 0 {
		burst = 0
	}
	return Demand{
		BaseDemandP95:  base,
		BurstP99Claims: burst,
		Observed:       true,
	}, nil
}
