// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"k8s.io/client-go/discovery"

	"github.com/lennylabs/lenny/pkg/ops/probe"
)

// Probe names §25.4 reports in the connectivity diagnostic and the
// readiness signal.
const (
	ProbePostgres   = "postgres"
	ProbeRedis      = "redis"
	ProbeK8sAPI     = "kubernetes"
	ProbeGateway    = "gateway"
	ProbeMinIO      = "minio"
	ProbePrometheus = "prometheus"
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

// MinIOProbe returns the §25.2 / §25.11 MinIO dependency probe: it GETs
// the MinIO liveness endpoint (`/minio/health/live`). §25.2 line 169
// lists MinIO among the dependencies lenny-ops connects to, and §25.11
// streams backup archives to it. An endpoint without a scheme is
// assumed plain HTTP. An empty endpoint yields a probe that reports
// MinIO not configured, keeping the connectivity report honest in a
// deployment without object storage.
func MinIOProbe(client *http.Client, endpoint string) probe.Func {
	return func(ctx context.Context) error {
		if endpoint == "" {
			return fmt.Errorf("minio endpoint is not configured")
		}
		return httpHealthGet(ctx, client, normalizeHTTPBase(endpoint)+"/minio/health/live")
	}
}

// PrometheusProbe returns the §25.2 / §25.16 Prometheus dependency
// probe: it GETs the Prometheus health endpoint (`/-/healthy`). §25.2
// line 169 lists Prometheus among the dependencies lenny-ops connects
// to, and §25.6 queries it for diagnostic time-series. An empty URL
// yields a probe that reports Prometheus not configured, the §25.16
// Minimal-block degraded posture.
func PrometheusProbe(client *http.Client, baseURL string) probe.Func {
	return func(ctx context.Context) error {
		if baseURL == "" {
			return fmt.Errorf("prometheus URL is not configured")
		}
		return httpHealthGet(ctx, client, strings.TrimRight(normalizeHTTPBase(baseURL), "/")+"/-/healthy")
	}
}

// normalizeHTTPBase prepends a plain-HTTP scheme to a bare host:port so
// http.NewRequest accepts it. A value that already carries a scheme is
// returned unchanged.
func normalizeHTTPBase(s string) string {
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	return "http://" + s
}

// httpHealthGet GETs url and treats any non-5xx response as reachable.
// A health endpoint that answers at all proves the dependency is up; a
// 4xx (wrong path on an older MinIO, auth-gated Prometheus) still
// confirms connectivity, which is what the §25.6 report measures.
func httpHealthGet(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("health endpoint returned %d", resp.StatusCode)
	}
	return nil
}
