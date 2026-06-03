// SPDX-License-Identifier: MIT

package main

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// The §25.4 lines 2491-2497 self-health source gauges. Each reports the
// raw numeric input one of the §25.4 self-health checks derives its
// status from, so an operator can read the underlying value (not only
// the 0/1/2 status the lenny_ops_self_health_status{check} gauge
// encodes). Registered on the process default registry the §16.9
// lenny-ops /metrics exposition scrapes. A registration error leaves the
// variable nil so the sampler no-ops rather than panics.

var opsPostgresPoolActive = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_ops_postgres_pool_active",
		Help: "§25.4 active (acquired) connections in the lenny-ops platform Postgres pool.",
	}, nil)
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

var opsRedisConsumerLag = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_ops_redis_consumer_lag",
		Help: "§25.4 ops:events:stream consumer lag in events (0 when no consumer-lag source is wired).",
	}, nil)
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

var opsWebhookBacklog = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_ops_webhook_backlog",
		Help: "§25.4 pending webhook deliveries on the leader's delivery worker.",
	}, nil)
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

// sampleSelfHealthSourceGauges refreshes the §25.4 self-health source
// gauges from their live sources. The Postgres pool-active gauge reads
// the pgxpool stat's acquired-connection count (0 when no pool is
// wired); the Redis consumer-lag gauge reads the lag function the
// redis_consumer_lag self-health check uses (0 when nil — no consumer-lag
// source is wired yet); the webhook-backlog gauge reads the delivery
// worker's pending count. spec: §25.4 lines 2491-2497.
func sampleSelfHealthSourceGauges(pool *pgxpool.Pool, redisLag func() int, webhookBacklog func() int) {
	if opsPostgresPoolActive != nil && pool != nil {
		opsPostgresPoolActive.WithLabelValues().Set(float64(pool.Stat().AcquiredConns()))
	}
	if opsRedisConsumerLag != nil {
		lag := 0
		if redisLag != nil {
			lag = redisLag()
		}
		opsRedisConsumerLag.WithLabelValues().Set(float64(lag))
	}
	if opsWebhookBacklog != nil {
		backlog := 0
		if webhookBacklog != nil {
			backlog = webhookBacklog()
		}
		opsWebhookBacklog.WithLabelValues().Set(float64(backlog))
	}
}
