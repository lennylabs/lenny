// SPDX-License-Identifier: MIT

// Package infra implements the §24.2 / §15.1 line 890 infrastructure
// connectivity preflight: active outbound probes against Postgres,
// Redis, and MinIO plus a best-effort schema-version read. The same
// orchestration backs the standalone `lenny-ctl preflight` CLI and the
// API-backed `POST /v1/admin/preflight` gateway endpoint, so the two
// modes run identical checks.
//
// The orchestration is a pure function over a Probers seam, so it is
// unit-testable with fakes; the real pgx/go-redis/minio-go dialers live
// in dial.go and are wired only by the binaries.
//
// spec: §24.2 lines 39-47; §15.1 line 890.
package infra

import (
	"context"
	"fmt"
	"strings"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// Config is the resolved set of backend connection settings the infra
// preflight probes. An empty connection field skips that backend's
// probe (reported as a passing "not configured" result so the report
// stays transparent and pre-deployment runs against partial topologies
// do not fail closed on an absent BYO backend).
//
// spec: §24.2 line 47 (DSN resolution precedence).
type Config struct {
	// PostgresDSN is the libpq/pgx connection string for the session
	// truth store. Empty skips the Postgres probe.
	PostgresDSN string
	// RedisDSN is the redis:// or rediss:// connection URL. Empty skips
	// the Redis probe.
	RedisDSN string
	// MinIOEndpoint is the object-store host:port. Empty skips the MinIO
	// probe.
	MinIOEndpoint string
	// MinIOAccessKey / MinIOSecretKey authenticate the MinIO probe.
	MinIOAccessKey string
	MinIOSecretKey string
	// MinIOBucket, when set, is checked for existence; empty checks
	// only connectivity and credentials.
	MinIOBucket string
	// MinIOUseSSL selects HTTPS for the MinIO probe. The caller sets it
	// after Resolve (Resolve merges only the string fields).
	MinIOUseSSL bool
}

// Resolve merges the supplied Configs field-by-field, taking the first
// non-empty value for each string field. Pass the sources in
// descending precedence: the §24.2 line 47 order is CLI flags, then
// environment variables, then the `--config` values file. MinIOUseSSL
// is not merged here; the caller sets it on the result (it defaults to
// true everywhere except §17.4 Embedded Mode).
//
// spec: §24.2 line 47.
func Resolve(sources ...Config) Config {
	var out Config
	first := func(dst *string, vals ...string) {
		if *dst != "" {
			return
		}
		for _, v := range vals {
			if v != "" {
				*dst = v
				return
			}
		}
	}
	for _, s := range sources {
		first(&out.PostgresDSN, s.PostgresDSN)
		first(&out.RedisDSN, s.RedisDSN)
		first(&out.MinIOEndpoint, s.MinIOEndpoint)
		first(&out.MinIOAccessKey, s.MinIOAccessKey)
		first(&out.MinIOSecretKey, s.MinIOSecretKey)
		first(&out.MinIOBucket, s.MinIOBucket)
	}
	return out
}

// Configured reports whether at least one backend connection field is
// set, so the caller can warn when a standalone preflight has nothing
// to probe.
func (c Config) Configured() bool {
	return c.PostgresDSN != "" || c.RedisDSN != "" || c.MinIOEndpoint != ""
}

// PostgresProber runs an active outbound Postgres connectivity probe
// and returns the applied schema-migration version (empty on a fresh
// database). It is the seam the real pgx dialer and test fakes satisfy.
type PostgresProber interface {
	ProbePostgres(ctx context.Context, dsn string) (schemaVersion string, err error)
}

// RedisProber runs an active outbound Redis PING.
type RedisProber interface {
	ProbeRedis(ctx context.Context, dsn string) error
}

// MinIOProber runs an active outbound MinIO connectivity-and-credential
// probe. A non-empty bucket is checked for existence.
type MinIOProber interface {
	ProbeMinIO(ctx context.Context, endpoint, accessKey, secretKey, bucket string, useSSL bool) error
}

// Probers bundles the three backend seams. A nil prober for a
// configured backend fails that check closed (a misconfiguration that
// must not pass silently).
type Probers struct {
	Postgres PostgresProber
	Redis    RedisProber
	MinIO    MinIOProber
}

// Check names, stable so the CLI, the gateway endpoint, and runbooks
// reference the same identifiers.
const (
	CheckPostgres = "postgres-connectivity"
	CheckRedis    = "redis-connectivity"
	CheckMinIO    = "minio-connectivity"
)

// Run probes each configured backend and returns one CheckResult per
// backend. Backends with no connection string yield a passing
// "not configured" result; a configured backend with no wired prober
// fails closed. The result list is uniform with pkg/preflight so the
// CLI and the gateway endpoint render both the admission-plane checks
// and the infra checks through one reporter.
//
// spec: §24.2 lines 39-47; §15.1 line 890.
func Run(ctx context.Context, cfg Config, p Probers) []preflight.CheckResult {
	report := make([]preflight.CheckResult, 0, 3)

	// Postgres connectivity + schema-version read.
	switch {
	case cfg.PostgresDSN == "":
		report = append(report, pass(CheckPostgres, "SKIPPED: no Postgres DSN configured"))
	case p.Postgres == nil:
		report = append(report, fail(CheckPostgres, "PREFLIGHT_PROBE_UNWIRED: Postgres DSN configured but no prober wired"))
	default:
		version, err := p.Postgres.ProbePostgres(ctx, cfg.PostgresDSN)
		if err != nil {
			report = append(report, fail(CheckPostgres, "POSTGRES_UNREACHABLE: "+err.Error()))
		} else {
			report = append(report, pass(CheckPostgres, "Postgres reachable; "+schemaVersionNote(version)))
		}
	}

	// Redis connectivity.
	switch {
	case cfg.RedisDSN == "":
		report = append(report, pass(CheckRedis, "SKIPPED: no Redis DSN configured"))
	case p.Redis == nil:
		report = append(report, fail(CheckRedis, "PREFLIGHT_PROBE_UNWIRED: Redis DSN configured but no prober wired"))
	default:
		if err := p.Redis.ProbeRedis(ctx, cfg.RedisDSN); err != nil {
			report = append(report, fail(CheckRedis, "REDIS_UNREACHABLE: "+err.Error()))
		} else {
			report = append(report, pass(CheckRedis, "Redis reachable"))
		}
	}

	// MinIO connectivity + optional bucket existence.
	switch {
	case cfg.MinIOEndpoint == "":
		report = append(report, pass(CheckMinIO, "SKIPPED: no MinIO endpoint configured"))
	case p.MinIO == nil:
		report = append(report, fail(CheckMinIO, "PREFLIGHT_PROBE_UNWIRED: MinIO endpoint configured but no prober wired"))
	default:
		err := p.MinIO.ProbeMinIO(ctx, cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOBucket, cfg.MinIOUseSSL)
		if err != nil {
			report = append(report, fail(CheckMinIO, "MINIO_UNREACHABLE: "+err.Error()))
		} else {
			report = append(report, pass(CheckMinIO, minioNote(cfg.MinIOBucket)))
		}
	}

	return report
}

// schemaVersionNote renders the schema-version portion of the Postgres
// probe reason. An empty version is a fresh database the migrate Job has
// not yet run against, which is the expected pre-install state.
func schemaVersionNote(version string) string {
	if strings.TrimSpace(version) == "" {
		return "schema version: none (fresh database, run lenny-migrate)"
	}
	return "schema version: " + version
}

// minioNote renders the MinIO probe reason, naming the verified bucket
// when one was configured.
func minioNote(bucket string) string {
	if bucket == "" {
		return "MinIO reachable; credentials valid"
	}
	return fmt.Sprintf("MinIO reachable; bucket %q present", bucket)
}

func pass(name, reason string) preflight.CheckResult {
	return preflight.CheckResult{Name: name, Decision: preflight.Decision{Passed: true, Reason: reason}}
}

func fail(name, reason string) preflight.CheckResult {
	return preflight.CheckResult{Name: name, Decision: preflight.Decision{Passed: false, Reason: reason}}
}
