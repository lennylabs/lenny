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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/health"
)

// probeTimeout bounds a health ping so a hung backend cannot stall the
// §25.3 health request path.
const probeTimeout = 2 * time.Second

// Postgres returns a health.Checker that pings the Postgres pool.
func Postgres(pool *pgxpool.Pool, name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(ctx context.Context) health.Component {
			pingCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			if err := pool.Ping(pingCtx); err != nil {
				return health.Component{
					Name:            name,
					Status:          health.StatusUnhealthy,
					Detail:          "postgres ping failed: " + err.Error(),
					SuggestedAction: "verify Postgres reachability, credentials, and the connection pool; the gateway rejects session writes until it recovers",
					RunbookRef:      "postgres-failover",
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
					Name:            name,
					Status:          health.StatusUnhealthy,
					Detail:          "redis ping failed: " + err.Error(),
					SuggestedAction: "verify Redis reachability; circuit-breaker and coordination state degrade to per-replica behaviour until it recovers",
					RunbookRef:      "redis-failure",
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
