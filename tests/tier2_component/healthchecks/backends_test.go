//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §25.3 backend health checkers
// (pkg/gateway/health/backends), exercised against real Postgres and
// Redis containers and against unreachable backends.
package backends_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/health"
	"github.com/lennylabs/lenny/pkg/gateway/health/backends"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// spec: 25.3
// diagnosis: the Postgres backend health checker in
// pkg/gateway/health/backends did not behave as specified. It must
// report healthy against a live database and unhealthy against an
// unreachable one, stamping the §25.3 issue code the aggregator's
// catalog resolves into the structured suggestedAction and runbook.
func TestPostgresChecker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("reports healthy against a live database", func(t *testing.T) {
		pg := containers.StartPostgres(t, containers.PostgresOptions{})
		comp := backends.Postgres(pg.Pool, "postgres").Check(ctx)
		if comp.Name != "postgres" {
			t.Errorf("Name = %q, want postgres", comp.Name)
		}
		if comp.Status != health.StatusHealthy {
			t.Errorf("Status = %q, want healthy (detail: %s)", comp.Status, comp.Detail)
		}
	})

	t.Run("reports unhealthy when the database is unreachable", func(t *testing.T) {
		// A syntactically valid DSN pointing at a closed port: the
		// pool builds lazily and the ping fails fast.
		pool, err := pgxpool.New(ctx, "postgres://lenny@127.0.0.1:1/none?sslmode=disable")
		if err != nil {
			t.Fatalf("pgxpool.New: %v", err)
		}
		defer pool.Close()
		comp := backends.Postgres(pool, "postgres").Check(ctx)
		if comp.Status != health.StatusUnhealthy {
			t.Errorf("Status = %q, want unhealthy", comp.Status)
		}
		// The checker stamps the Issue code; the aggregator's catalog
		// resolves the structured suggestedAction and Path B runbook from
		// it. spec: §25.3 lines 459-501; §25.7 line 3234.
		if comp.Issue == "" {
			t.Errorf("unhealthy component missing §25.3 issue code: %+v", comp)
		}
		if single, _ := health.ActionsForIssue(comp.Issue, comp.Name); single == nil || single.Runbook == "" {
			t.Errorf("issue %q does not resolve to a §25.3 hint with a runbook", comp.Issue)
		}
	})
}

// spec: 25.3
// diagnosis: the Redis backend health checker in
// pkg/gateway/health/backends did not behave as specified. It must
// report healthy against a live Redis and unhealthy against an
// unreachable one, stamping the §25.3 issue code the aggregator's
// catalog resolves into the structured suggestedAction and runbook.
func TestRedisChecker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("reports healthy against a live Redis", func(t *testing.T) {
		rd := containers.StartRedis(t, containers.RedisOptions{})
		comp := backends.Redis(rd.Client, "redis").Check(ctx)
		if comp.Name != "redis" {
			t.Errorf("Name = %q, want redis", comp.Name)
		}
		if comp.Status != health.StatusHealthy {
			t.Errorf("Status = %q, want healthy (detail: %s)", comp.Status, comp.Detail)
		}
	})

	t.Run("reports unhealthy when Redis is unreachable", func(t *testing.T) {
		client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
		defer func() { _ = client.Close() }()
		comp := backends.Redis(client, "redis").Check(ctx)
		if comp.Status != health.StatusUnhealthy {
			t.Errorf("Status = %q, want unhealthy", comp.Status)
		}
		// The checker stamps the Issue code; the aggregator's catalog
		// resolves the structured suggestedAction and Path B runbook from
		// it. spec: §25.3 lines 459-501; §25.7 line 3234.
		if comp.Issue == "" {
			t.Errorf("unhealthy component missing §25.3 issue code: %+v", comp)
		}
		if single, _ := health.ActionsForIssue(comp.Issue, comp.Name); single == nil || single.Runbook == "" {
			t.Errorf("issue %q does not resolve to a §25.3 hint with a runbook", comp.Issue)
		}
	})
}
