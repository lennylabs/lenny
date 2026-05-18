// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"k8s.io/client-go/discovery"

	"github.com/lennylabs/lenny/pkg/ops/probe"
)

// Probe names §25.4 reports in the connectivity diagnostic and the
// readiness signal.
const (
	ProbePostgres = "postgres"
	ProbeRedis    = "redis"
	ProbeK8sAPI   = "kubernetes"
	ProbeGateway  = "gateway"
)

// PostgresProbe returns the §25.4 Postgres dependency probe: it pings
// the connection pool. A nil pool yields a probe that reports Postgres
// not configured, which keeps the readiness report honest in a
// deployment running without Postgres.
func PostgresProbe(pool *pgxpool.Pool) probe.Func {
	return func(ctx context.Context) error {
		if pool == nil {
			return fmt.Errorf("postgres is not configured")
		}
		return pool.Ping(ctx)
	}
}

// RedisProbe returns the §25.4 Redis dependency probe: it issues a
// PING. A nil client yields a probe that reports Redis not configured.
func RedisProbe(client redis.UniversalClient) probe.Func {
	return func(ctx context.Context) error {
		if client == nil {
			return fmt.Errorf("redis is not configured")
		}
		return client.Ping(ctx).Err()
	}
}

// K8sAPIProbe returns the §25.4 Kubernetes API dependency probe: it
// queries the API server version. §25.4 lists the K8s API as a
// required dependency, so a nil client yields a probe that fails.
func K8sAPIProbe(disc discovery.DiscoveryInterface) probe.Func {
	return func(context.Context) error {
		if disc == nil {
			return fmt.Errorf("kubernetes API client is not configured")
		}
		_, err := disc.ServerVersion()
		return err
	}
}

// GatewayProbe returns the §25.4 gateway-admin-API dependency probe: it
// GETs the gateway health endpoint. §25.4 lists the gateway admin API
// as a required dependency. An empty URL yields a probe that reports
// the gateway not configured.
func GatewayProbe(client *http.Client, healthURL string) probe.Func {
	return func(ctx context.Context) error {
		if healthURL == "" {
			return fmt.Errorf("gateway admin API URL is not configured")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			return fmt.Errorf("gateway health returned %d", resp.StatusCode)
		}
		return nil
	}
}
