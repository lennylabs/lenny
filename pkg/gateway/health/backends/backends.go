// SPDX-License-Identifier: MIT

// Package backends provides §25.3 health.Checkers for the gateway's
// external backing services. Each checker pings its backend within a
// bounded timeout and reports a healthy or unhealthy Component with
// the §25.3 operability hints (a suggested action and a runbook
// reference) an AI-DevOps agent can act on.
//
// The dependency-heavy backend probes live here, in a subpackage, so
// the parent health package stays free of the pgx and Redis imports.
package backends

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/health"
)

// probeTimeout bounds a health ping so a hung backend cannot stall the
// §25.3 health request path.
const probeTimeout = 2 * time.Second

// breakerCacheStaleAfter is the §11.7 staleness budget for the
// circuit-breaker cache: a snapshot older than this is reported
// degraded because the cache has stopped reconciling with Redis.
const breakerCacheStaleAfter = 5 * time.Second

// Postgres returns a health.Checker that pings the Postgres pool.
func Postgres(pool *pgxpool.Pool, name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(ctx context.Context) health.Component {
			pingCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			if err := pool.Ping(pingCtx); err != nil {
				return health.Component{
					Name:   name,
					Status: health.StatusUnhealthy,
					Detail: "postgres ping failed: " + err.Error(),
					// spec: §25.7 line 3226 — Issue selects the
					// Path B runbook through the §17.7 issueRunbooks
					// table.
					Issue:           "POSTGRES_UNREACHABLE",
					SuggestedAction: "verify Postgres reachability, credentials, and the connection pool; the gateway rejects session writes until it recovers",
					RunbookRef:      health.RunbookFor("postgres"),
				}
			}
			return health.Component{
				Name:   name,
				Status: health.StatusHealthy,
				Detail: "postgres reachable",
			}
		},
	}
}

// Redis returns a health.Checker that pings the Redis client.
func Redis(client redis.UniversalClient, name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(ctx context.Context) health.Component {
			pingCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			if err := client.Ping(pingCtx).Err(); err != nil {
				return health.Component{
					Name:   name,
					Status: health.StatusUnhealthy,
					Detail: "redis ping failed: " + err.Error(),
					// spec: §25.7 line 3227.
					Issue:           "REDIS_UNREACHABLE",
					SuggestedAction: "verify Redis reachability; circuit-breaker and coordination state degrade to per-replica behaviour until it recovers",
					RunbookRef:      health.RunbookFor("redis"),
				}
			}
			return health.Component{
				Name:   name,
				Status: health.StatusHealthy,
				Detail: "redis reachable",
			}
		},
	}
}

// BreakerCache is the freshness view of the §11.6 circuit-breaker
// cache. *cachingstore.Store satisfies it.
type BreakerCache interface {
	LastRefresh() time.Time
}

// CircuitBreakerCache returns a health.Checker that reports the
// freshness of the §11.6 circuit-breaker cache. When the cache has not
// reconciled with Redis within the §11.7 5-second budget the component
// is degraded rather than unhealthy: the gateway keeps admitting
// requests against the last known snapshot, which §11.7 states is
// strictly better than reporting nothing during a Redis outage.
func CircuitBreakerCache(cache BreakerCache, name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(context.Context) health.Component {
			last := cache.LastRefresh()
			if last.IsZero() {
				return health.Component{
					Name:            name,
					Status:          health.StatusDegraded,
					Detail:          "circuit-breaker cache has not completed its first refresh",
					SuggestedAction: "verify Redis reachability; the gateway admits requests against an empty breaker snapshot until the first refresh succeeds",
					RunbookRef:      health.RunbookFor("redis"),
				}
			}
			if age := time.Since(last); age > breakerCacheStaleAfter {
				return health.Component{
					Name:            name,
					Status:          health.StatusDegraded,
					Detail:          fmt.Sprintf("circuit-breaker cache is %s stale; serving the last known snapshot", age.Round(time.Second)),
					SuggestedAction: "verify Redis reachability; the gateway admits requests against a stale breaker snapshot until the cache refreshes",
					RunbookRef:      health.RunbookFor("redis"),
				}
			}
			return health.Component{
				Name:   name,
				Status: health.StatusHealthy,
				Detail: "circuit-breaker cache fresh",
			}
		},
	}
}

// SIEMForwarder is the §11.7 SIEM delivery view a health probe reads.
// *siem.Forwarder satisfies Healthy(); *siem.CountingMetrics satisfies
// FailureRate(). The probe stays free of the siem import by depending on
// these narrow readers.
type SIEMForwarder interface {
	// Healthy reports whether the most recent SIEM batch delivery
	// succeeded.
	Healthy() bool
}

// SIEMFailureRate reports the §11.7 SIEM delivery failure rate as a
// percentage so the probe can compare it against
// audit.siem.failureThresholdPercent.
type SIEMFailureRate interface {
	FailureRate() float64
}

// SIEM returns a §11.7 item 4 health.Checker for the SIEM forwarder. It
// reports degraded when the delivery failure rate exceeds
// thresholdPercent (default 5%) or the most recent batch delivery
// failed, matching the spec's "if the failure rate exceeds the
// configured threshold ... the /healthz endpoint reports degraded
// status". The status is StatusDegraded rather than StatusUnhealthy
// because audit rows continue to persist durably in Postgres during a
// SIEM outage — the external copy is impaired, not the audit pipeline.
//
// The gateway does not gate its liveness probe (/healthz) or readiness
// probe (/readyz) on this component: a SIEM outage is shared across
// every replica, so failing readiness would remove all replicas from
// the Service and turn an audit-integrity degradation into a full
// outage. The degraded verdict surfaces on the §25.3 health API and
// drives the §16.5 alert instead. spec: §11.7 item 4 line 372.
func SIEM(fwd SIEMForwarder, rate SIEMFailureRate, thresholdPercent float64, name string) health.Checker {
	if thresholdPercent <= 0 {
		thresholdPercent = 5
	}
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(context.Context) health.Component {
			fr := rate.FailureRate()
			if fr > thresholdPercent {
				return health.Component{
					Name:            name,
					Status:          health.StatusDegraded,
					Detail:          fmt.Sprintf("SIEM delivery failure rate %.1f%% exceeds the %.1f%% threshold; audit rows persist in Postgres but the external immutable copy is lagging", fr, thresholdPercent),
					Issue:           "AUDIT_SIEM_DELIVERY_DEGRADED",
					SuggestedAction: "verify SIEM endpoint reachability and credentials; the audit hash chain is durable in Postgres but the independent SIEM copy is incomplete until delivery recovers",
					RunbookRef:      health.RunbookFor("siem"),
				}
			}
			if !fwd.Healthy() {
				return health.Component{
					Name:            name,
					Status:          health.StatusDegraded,
					Detail:          "the most recent SIEM batch delivery failed",
					Issue:           "AUDIT_SIEM_DELIVERY_DEGRADED",
					SuggestedAction: "verify SIEM endpoint reachability and credentials; the audit hash chain is durable in Postgres but the independent SIEM copy is incomplete until delivery recovers",
					RunbookRef:      health.RunbookFor("siem"),
				}
			}
			return health.Component{
				Name:   name,
				Status: health.StatusHealthy,
				Detail: "SIEM delivery healthy",
			}
		},
	}
}
