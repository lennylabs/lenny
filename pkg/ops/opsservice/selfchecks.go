// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"k8s.io/client-go/discovery"
)

// §25.4 self-health check names.
const (
	CheckPostgresPool   = "postgres_pool"
	CheckRedisLag       = "redis_consumer_lag"
	CheckWebhookBacklog = "webhook_backlog"
	CheckK8sAPI         = "k8s_api"
	CheckMemoryPressure = "memory_pressure"
)

// §25.4 self-health thresholds. The table in §25.4 "Self-Health
// Checks" fixes each degraded/unhealthy boundary.
const (
	pgPoolDegradedFraction      = 0.80 // active connections > 80% of pool
	webhookBacklogDegraded      = 100  // pending deliveries > 100
	webhookBacklogUnhealthy     = 500  // pending deliveries > 500
	k8sLatencyDegradedThreshold = 2 * time.Second
	memoryDegradedFraction      = 0.80 // RSS > 80% of limit
	memoryUnhealthyFraction     = 0.95 // RSS > 95% of limit
	redisLagDegradedThreshold   = 1000 // stream lag > 1000 events
	redisLagUnhealthyThreshold  = 5000 // stream lag > 5000 events
)

// PostgresPoolCheck returns the §25.4 postgres_pool self-health check.
// It is degraded when active connections exceed 80% of the pool size
// and unhealthy when a connection error is observed. A nil pool
// reports unhealthy, matching §25.4's treatment of a missing required
// dependency.
func PostgresPoolCheck(pool *pgxpool.Pool) SelfCheck {
	return func() CheckResult {
		res := CheckResult{Name: CheckPostgresPool}
		if pool == nil {
			res.Status, res.Detail = StatusUnhealthy, "postgres is not configured"
			return res
		}
		stat := pool.Stat()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			res.Status = StatusUnhealthy
			res.Detail = fmt.Sprintf("postgres connection error: %v", err)
			return res
		}
		total := stat.MaxConns()
		if total > 0 && float64(stat.AcquiredConns()) > pgPoolDegradedFraction*float64(total) {
			res.Status = StatusDegraded
			res.Detail = fmt.Sprintf("%d of %d connections in use", stat.AcquiredConns(), total)
			return res
		}
		res.Status = StatusHealthy
		return res
	}
}

// RedisLagCheck returns the §25.4 redis_consumer_lag self-health check.
// lag supplies the current ops:events:stream consumer lag in events;
// the check is degraded above 1000 and unhealthy above 5000 or when
// the consumer is disconnected. A nil client reports unhealthy
// (consumer disconnected).
func RedisLagCheck(client redis.UniversalClient, lag func() int) SelfCheck {
	return func() CheckResult {
		res := CheckResult{Name: CheckRedisLag}
		if client == nil {
			res.Status, res.Detail = StatusUnhealthy, "redis consumer is not connected"
			return res
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.Ping(ctx).Err(); err != nil {
			res.Status = StatusUnhealthy
			res.Detail = fmt.Sprintf("redis consumer disconnected: %v", err)
			return res
		}
		behind := 0
		if lag != nil {
			behind = lag()
		}
		switch {
		case behind > redisLagUnhealthyThreshold:
			res.Status = StatusUnhealthy
			res.Detail = fmt.Sprintf("stream lag %d events", behind)
		case behind > redisLagDegradedThreshold:
			res.Status = StatusDegraded
			res.Detail = fmt.Sprintf("stream lag %d events", behind)
		default:
			res.Status = StatusHealthy
		}
		return res
	}
}

// WebhookBacklogCheck returns the §25.4 webhook_backlog self-health
// check. backlog supplies the current pending-delivery count; the
// check is degraded above 100 and unhealthy above 500. The webhook
// delivery worker's Backlog method is the production source.
func WebhookBacklogCheck(backlog func() int) SelfCheck {
	return func() CheckResult {
		res := CheckResult{Name: CheckWebhookBacklog}
		pending := 0
		if backlog != nil {
			pending = backlog()
		}
		switch {
		case pending > webhookBacklogUnhealthy:
			res.Status = StatusUnhealthy
			res.Detail = fmt.Sprintf("%d pending deliveries", pending)
		case pending > webhookBacklogDegraded:
			res.Status = StatusDegraded
			res.Detail = fmt.Sprintf("%d pending deliveries", pending)
		default:
			res.Status = StatusHealthy
		}
		return res
	}
}

// K8sAPICheck returns the §25.4 k8s_api self-health check. It is
// degraded when an API server version query takes longer than 2s and
// unhealthy when the API is unreachable.
func K8sAPICheck(disc discovery.DiscoveryInterface) SelfCheck {
	return func() CheckResult {
		res := CheckResult{Name: CheckK8sAPI}
		if disc == nil {
			res.Status, res.Detail = StatusUnhealthy, "kubernetes API client is not configured"
			return res
		}
		start := time.Now()
		if _, err := disc.ServerVersion(); err != nil {
			res.Status = StatusUnhealthy
			res.Detail = fmt.Sprintf("kubernetes API unreachable: %v", err)
			return res
		}
		if elapsed := time.Since(start); elapsed > k8sLatencyDegradedThreshold {
			res.Status = StatusDegraded
			res.Detail = fmt.Sprintf("kubernetes API latency %s", elapsed.Round(time.Millisecond))
			return res
		}
		res.Status = StatusHealthy
		return res
	}
}

// MemoryPressureCheck returns the §25.4 memory_pressure self-health
// check. limitBytes is the container memory limit; the check is
// degraded when the process RSS exceeds 80% of it and unhealthy above
// 95%. A zero or negative limit disables the check (it reports
// healthy), since RSS cannot be compared to an unknown limit.
func MemoryPressureCheck(limitBytes int64) SelfCheck {
	return func() CheckResult {
		res := CheckResult{Name: CheckMemoryPressure, Status: StatusHealthy}
		if limitBytes <= 0 {
			return res
		}
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		// Go's runtime does not expose RSS directly; HeapSys plus the
		// non-heap system reservations is the closest in-process proxy
		// for resident memory without reading /proc.
		rss := int64(m.Sys)
		fraction := float64(rss) / float64(limitBytes)
		switch {
		case fraction > memoryUnhealthyFraction:
			res.Status = StatusUnhealthy
			res.Detail = fmt.Sprintf("memory %d%% of limit", int(fraction*100))
		case fraction > memoryDegradedFraction:
			res.Status = StatusDegraded
			res.Detail = fmt.Sprintf("memory %d%% of limit", int(fraction*100))
		}
		return res
	}
}
