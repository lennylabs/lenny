// SPDX-License-Identifier: MIT

package infra_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/preflight"
	"github.com/lennylabs/lenny/pkg/preflight/infra"
)

// spec: §24.2 line 47 — DSN resolution precedence is CLI flags, then
// environment variables, then the values file (highest wins).
func TestResolvePrecedence_FlagsOverEnvOverValues(t *testing.T) {
	flags := infra.Config{PostgresDSN: "pg-flag"}
	env := infra.Config{PostgresDSN: "pg-env", RedisDSN: "redis-env"}
	values := infra.Config{PostgresDSN: "pg-values", RedisDSN: "redis-values", MinIOEndpoint: "minio-values"}

	got := infra.Resolve(flags, env, values)
	if got.PostgresDSN != "pg-flag" {
		t.Errorf("PostgresDSN: want flag value, got %q", got.PostgresDSN)
	}
	if got.RedisDSN != "redis-env" {
		t.Errorf("RedisDSN: want env value (flag empty), got %q", got.RedisDSN)
	}
	if got.MinIOEndpoint != "minio-values" {
		t.Errorf("MinIOEndpoint: want values value (flag+env empty), got %q", got.MinIOEndpoint)
	}
}

// spec: §24.2 line 47 — MinIO access/secret/bucket each resolve
// independently so an operator can commit a values file without inline
// credentials and inject only the secrets at runtime.
func TestResolvePrecedence_MinIOFieldsIndependent(t *testing.T) {
	flags := infra.Config{MinIOAccessKey: "ak-flag"}
	env := infra.Config{MinIOSecretKey: "sk-env"}
	values := infra.Config{MinIOEndpoint: "ep-values", MinIOBucket: "bucket-values", MinIOAccessKey: "ak-values", MinIOSecretKey: "sk-values"}

	got := infra.Resolve(flags, env, values)
	if got.MinIOAccessKey != "ak-flag" {
		t.Errorf("access key: want flag, got %q", got.MinIOAccessKey)
	}
	if got.MinIOSecretKey != "sk-env" {
		t.Errorf("secret key: want env, got %q", got.MinIOSecretKey)
	}
	if got.MinIOEndpoint != "ep-values" || got.MinIOBucket != "bucket-values" {
		t.Errorf("endpoint/bucket: want values, got %q/%q", got.MinIOEndpoint, got.MinIOBucket)
	}
}

func TestConfigured(t *testing.T) {
	if (infra.Config{}).Configured() {
		t.Error("empty config reported configured")
	}
	if !(infra.Config{RedisDSN: "redis://x"}).Configured() {
		t.Error("redis-only config reported not configured")
	}
}

type fakePostgres struct {
	version string
	err     error
	gotDSN  string
}

func (f *fakePostgres) ProbePostgres(_ context.Context, dsn string) (string, error) {
	f.gotDSN = dsn
	return f.version, f.err
}

type fakeRedis struct{ err error }

func (f fakeRedis) ProbeRedis(context.Context, string) error { return f.err }

type fakeMinIO struct {
	err       error
	gotBucket string
	gotUseSSL bool
	gotEndpnt string
}

func (f *fakeMinIO) ProbeMinIO(_ context.Context, endpoint, _, _, bucket string, useSSL bool) error {
	f.gotEndpnt = endpoint
	f.gotBucket = bucket
	f.gotUseSSL = useSSL
	return f.err
}

func byName(report []preflight.CheckResult, name string) preflight.CheckResult {
	for _, r := range report {
		if r.Name == name {
			return r
		}
	}
	return preflight.CheckResult{Name: "MISSING:" + name}
}

// spec: §15.1 line 890 — the endpoint runs Postgres, Redis, and MinIO
// connectivity plus the schema-version read; all three pass when the
// probers succeed and the schema version is surfaced in the report.
func TestRun_AllReachable(t *testing.T) {
	pg := &fakePostgres{version: "116"}
	mio := &fakeMinIO{}
	cfg := infra.Config{
		PostgresDSN:   "postgres://pg",
		RedisDSN:      "redis://r",
		MinIOEndpoint: "minio:9000",
		MinIOBucket:   "lenny-artifacts",
		MinIOUseSSL:   true,
	}
	report := infra.Run(context.Background(), cfg, infra.Probers{Postgres: pg, Redis: fakeRedis{}, MinIO: mio})

	if preflight.Failed(report) {
		t.Fatalf("expected all checks to pass, report=%+v", report)
	}
	if pg.gotDSN != "postgres://pg" {
		t.Errorf("postgres prober got dsn %q", pg.gotDSN)
	}
	if r := byName(report, infra.CheckPostgres); !strings.Contains(r.Decision.Reason, "schema version: 116") {
		t.Errorf("postgres reason missing schema version: %q", r.Decision.Reason)
	}
	if !mio.gotUseSSL || mio.gotBucket != "lenny-artifacts" {
		t.Errorf("minio prober args: useSSL=%v bucket=%q", mio.gotUseSSL, mio.gotBucket)
	}
}

// spec: §15.1 line 890 — a backend that fails to dial is reported as a
// failed check (fail-closed), so the install wizard and the API caller
// can abort.
func TestRun_BackendUnreachableFails(t *testing.T) {
	cfg := infra.Config{PostgresDSN: "postgres://pg", RedisDSN: "redis://r", MinIOEndpoint: "m:9000"}
	report := infra.Run(context.Background(), cfg, infra.Probers{
		Postgres: &fakePostgres{err: errors.New("connection refused")},
		Redis:    fakeRedis{err: errors.New("timeout")},
		MinIO:    &fakeMinIO{err: errors.New("bucket missing")},
	})
	if !preflight.Failed(report) {
		t.Fatal("expected failure when backends unreachable")
	}
	if r := byName(report, infra.CheckPostgres); r.Decision.Passed || !strings.Contains(r.Decision.Reason, "POSTGRES_UNREACHABLE") {
		t.Errorf("postgres check: %+v", r.Decision)
	}
	if r := byName(report, infra.CheckRedis); r.Decision.Passed || !strings.Contains(r.Decision.Reason, "REDIS_UNREACHABLE") {
		t.Errorf("redis check: %+v", r.Decision)
	}
	if r := byName(report, infra.CheckMinIO); r.Decision.Passed || !strings.Contains(r.Decision.Reason, "MINIO_UNREACHABLE") {
		t.Errorf("minio check: %+v", r.Decision)
	}
}

// spec: §24.2 line 39 — a backend with no connection string is skipped
// (a partial pre-deployment topology must not fail closed on a backend
// the operator has not yet provisioned).
func TestRun_UnconfiguredBackendSkipped(t *testing.T) {
	report := infra.Run(context.Background(), infra.Config{RedisDSN: "redis://r"}, infra.Probers{Redis: fakeRedis{}})
	if preflight.Failed(report) {
		t.Fatalf("unconfigured backends should skip, not fail: %+v", report)
	}
	if r := byName(report, infra.CheckPostgres); !strings.Contains(r.Decision.Reason, "SKIPPED") {
		t.Errorf("postgres should be skipped: %q", r.Decision.Reason)
	}
	if r := byName(report, infra.CheckMinIO); !strings.Contains(r.Decision.Reason, "SKIPPED") {
		t.Errorf("minio should be skipped: %q", r.Decision.Reason)
	}
}

// A configured backend with no wired prober fails closed rather than
// silently passing — that combination is a wiring bug.
func TestRun_ConfiguredButUnwiredFailsClosed(t *testing.T) {
	report := infra.Run(context.Background(), infra.Config{PostgresDSN: "postgres://pg"}, infra.Probers{})
	if !preflight.Failed(report) {
		t.Fatal("configured-but-unwired prober should fail closed")
	}
	if r := byName(report, infra.CheckPostgres); !strings.Contains(r.Decision.Reason, "PREFLIGHT_PROBE_UNWIRED") {
		t.Errorf("want unwired reason, got %q", r.Decision.Reason)
	}
}

// A fresh database with no applied migrations passes connectivity and
// reports the un-migrated state rather than failing.
func TestRun_FreshDatabaseReportsNoSchema(t *testing.T) {
	report := infra.Run(context.Background(), infra.Config{PostgresDSN: "postgres://pg"},
		infra.Probers{Postgres: &fakePostgres{version: ""}})
	if preflight.Failed(report) {
		t.Fatalf("fresh database should pass connectivity: %+v", report)
	}
	if r := byName(report, infra.CheckPostgres); !strings.Contains(r.Decision.Reason, "fresh database") {
		t.Errorf("want fresh-database note, got %q", r.Decision.Reason)
	}
}
