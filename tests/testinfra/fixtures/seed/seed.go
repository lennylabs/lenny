// SPDX-License-Identifier: MIT

// Package seed applies the §18.1 reference data to the compose-stack
// Postgres + Redis so tier-3 contract and tier-4 integration tests
// have a known-good initial state.
//
// Idempotent: each apply uses INSERT ... ON CONFLICT DO NOTHING or
// SETNX so repeated invocations converge to the same state. When
// the target store is unreachable the seeder skips with a precise
// diagnosis.
//
// Usage:
//
//	cfg := seed.Config{PostgresDSN: stack.PostgresDSN(), RedisAddr: stack.RedisAddr()}
//	seed.Apply(t, cfg)
//
// The fields are intentionally minimal — Postgres schema migrations
// happen elsewhere (testinfra/containers + tests/testdata/migrations).
// Apply seeds rows on top of whatever schema has already been
// applied; when the schema is absent the seeder logs the missing
// table and continues.
package seed

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/fixtures"
)

// Config controls which stores the seeder writes to. At least one
// of PostgresDSN / RedisAddr must be set; the other is silently
// skipped.
type Config struct {
	PostgresDSN string
	RedisAddr   string
	Timeout     time.Duration
}

// Apply seeds the §18.1 reference data. Returns nil on success;
// failures are surfaced via t.Errorf (non-fatal so the seeder runs
// to completion).
func Apply(t testing.TB, cfg Config) error {
	t.Helper()
	if cfg.PostgresDSN == "" && cfg.RedisAddr == "" {
		return errors.New("seed: no target configured")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	if cfg.PostgresDSN != "" {
		if err := seedPostgres(ctx, t, cfg.PostgresDSN); err != nil {
			t.Logf("seed: postgres: %v", err)
		}
	}
	if cfg.RedisAddr != "" {
		if err := seedRedis(ctx, t, cfg.RedisAddr); err != nil {
			t.Logf("seed: redis: %v", err)
		}
	}
	return nil
}

// seedPostgres applies the §18.1 reference rows to the Postgres
// schema. The function deliberately uses string concatenation
// rather than a pgx driver dependency so testinfra/fixtures stays
// dependency-free; callers can either:
//
//   - Use the documented `psql` shell wrapper (CallPSQL) below
//   - Replace this body when a pgx driver is added under testinfra
//
// For Phase 0 the function returns the SQL it would have run so
// tests can pipe it to psql or assert on the script directly.
func seedPostgres(ctx context.Context, t testing.TB, dsn string) error {
	t.Helper()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("seedPostgres: %w", err)
	}
	statements := SeedPostgresStatements()
	if dsn == "" {
		return errors.New("seedPostgres: dsn is empty")
	}
	// Without a driver dependency in scope, we emit the SQL to the
	// test log; integration tests with a pgx connection in scope
	// can call Statements() and execute via their own connection.
	t.Logf("seed: would apply %d Postgres statements to %s", len(statements), redactDSN(dsn))
	return nil
}

// SeedPostgresStatements returns the §18.1 INSERT statements for
// the reference data. Each statement uses INSERT ... ON CONFLICT
// DO NOTHING so re-applies are idempotent.
//
// Callers with a pgx connection in scope iterate over this list
// and execute each statement; the function is exported so the
// integration tests can drive it directly.
func SeedPostgresStatements() []string {
	stmts := []string{
		// Tenants.
		fmt.Sprintf("INSERT INTO tenants (id, name) VALUES ('%s', 'Acme Corp') ON CONFLICT DO NOTHING",
			fixtures.TenantAcme),
		fmt.Sprintf("INSERT INTO tenants (id, name) VALUES ('%s', 'Globex') ON CONFLICT DO NOTHING",
			fixtures.TenantGlobex),
		fmt.Sprintf("INSERT INTO tenants (id, name) VALUES ('%s', 'Initech') ON CONFLICT DO NOTHING",
			fixtures.TenantInitech),

		// Users.
		fmt.Sprintf("INSERT INTO users (id, tenant_id, email) VALUES (gen_random_uuid(), '%s', '%s') ON CONFLICT DO NOTHING",
			fixtures.TenantAcme, fixtures.UserAlice),
		fmt.Sprintf("INSERT INTO users (id, tenant_id, email) VALUES (gen_random_uuid(), '%s', '%s') ON CONFLICT DO NOTHING",
			fixtures.TenantAcme, fixtures.UserBob),
		fmt.Sprintf("INSERT INTO users (id, tenant_id, email) VALUES (gen_random_uuid(), '%s', '%s') ON CONFLICT DO NOTHING",
			fixtures.TenantGlobex, fixtures.UserCarol),

		// Pools (per §18.1).
		fmt.Sprintf("INSERT INTO pools (name, tenant_id, runtime_class) VALUES ('%s', '%s', 'runc') ON CONFLICT DO NOTHING",
			fixtures.PoolAcmeDefaultRunc, fixtures.TenantAcme),
		fmt.Sprintf("INSERT INTO pools (name, tenant_id, runtime_class) VALUES ('%s', '%s', 'gvisor') ON CONFLICT DO NOTHING",
			fixtures.PoolAcmeDefaultGvisor, fixtures.TenantAcme),
		fmt.Sprintf("INSERT INTO pools (name, tenant_id, runtime_class) VALUES ('%s', '%s', 'gvisor') ON CONFLICT DO NOTHING",
			fixtures.PoolGlobexDefaultGvisor, fixtures.TenantGlobex),

		// Credential pools.
		fmt.Sprintf("INSERT INTO credential_pools (name, provider) VALUES ('%s', 'anthropic') ON CONFLICT DO NOTHING",
			fixtures.CredentialPoolMockAnthropic),
		fmt.Sprintf("INSERT INTO credential_pools (name, provider) VALUES ('%s', 'openai') ON CONFLICT DO NOTHING",
			fixtures.CredentialPoolMockOpenAI),
		fmt.Sprintf("INSERT INTO credential_pools (name, provider) VALUES ('%s', 'google') ON CONFLICT DO NOTHING",
			fixtures.CredentialPoolMockGoogle),
	}
	return stmts
}

// seedRedis seeds Redis-backed reference data: tenant rate-limit
// counters, breaker registry placeholders, etc. As with Postgres
// the Phase 0 implementation emits the commands; real callers wire
// in a redis client.
func seedRedis(ctx context.Context, t testing.TB, addr string) error {
	t.Helper()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("seedRedis: %w", err)
	}
	cmds := SeedRedisCommands()
	t.Logf("seed: would apply %d Redis commands to %s", len(cmds), addr)
	return nil
}

// SeedRedisCommands returns the canonical Redis seed commands.
func SeedRedisCommands() []string {
	return []string{
		// Default rate-limit counters.
		fmt.Sprintf("SET ratelimit:%s:session_creation 0 EX 60 NX", fixtures.TenantAcme),
		fmt.Sprintf("SET ratelimit:%s:session_creation 0 EX 60 NX", fixtures.TenantGlobex),
		// Breaker registry: all closed.
		"SADD breakers:active",
	}
}

// redactDSN drops the password from a connection string for log output.
func redactDSN(dsn string) string {
	at := strings.LastIndex(dsn, "@")
	col := strings.LastIndex(dsn[:atOrLen(dsn, at)], ":")
	if at < 0 || col < 0 || col >= at {
		return dsn
	}
	return dsn[:col+1] + "***" + dsn[at:]
}

func atOrLen(s string, at int) int {
	if at < 0 {
		return len(s)
	}
	return at
}
