//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §25.4 lenny-ops idempotency contract exercised
// end-to-end through the full opsserver HTTP surface against the durable
// Postgres-backed ops_idempotency_keys table (migration 0116), rather
// than the in-process memory store. It covers the two behaviors the
// unit tests can only assert against an in-memory Store:
//
//   - The fail-closed Postgres-outage path: when the durable store is
//     unreachable, a required-key endpoint returns 503
//     IDEMPOTENCY_STORE_UNAVAILABLE at Tier 2/3, while an optional
//     endpoint (and a required endpoint at Tier 1) still proceeds.
//   - The tier-dependent required-key posture: with the same durable
//     store, a required-key endpoint rejects a missing key with 400
//     IDEMPOTENCY_KEY_REQUIRED at Tier 2/3 and accepts it at Tier 1.
//
// The outage is simulated by closing the pgxpool the store reads from,
// which surfaces as opsidem.ErrStoreUnavailable the same way a real
// Postgres connection loss does.
package opsidem_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/opsidem"
	"github.com/lennylabs/lenny/pkg/ops/opsidem/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// startIdemPostgres brings up a Postgres container with the production
// migrations (so ops_idempotency_keys from migration 0116 exists) and
// returns a dedicated pool the caller owns and can close to simulate a
// Postgres outage independently of the container's own pool.
func startIdemPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN)
	if err != nil {
		t.Fatalf("dedicated pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// idemServer wires an opsserver with a §25.11 backup service and the
// §25.4 idempotency middleware backed by the durable pgstore over pool,
// in the given Tier posture (production reports Tier 2/3).
func idemServer(t *testing.T, pool *pgxpool.Pool, production bool) *opsserver.Server {
	t.Helper()
	svc, err := backup.NewService(backup.Config{
		Store:           backup.NewMemStore(),
		Launcher:        backup.NewFakeLauncher(),
		Locker:          backup.NewMemLocker(),
		PlatformVersion: "1.5.0",
		SchemaVersion:   42,
		Now:             func() time.Time { return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return opsserver.New(opsserver.Options{
		Backups:     svc,
		Production:  production,
		Idempotency: pgstore.New(pool),
	})
}

func postIdem(srv *opsserver.Server, path, key, caller, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(opsidem.HeaderName, key)
	}
	if caller != "" {
		req.Header.Set("X-Lenny-Caller", caller)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

const fullBackupBody = `{"type":"full","confirm":true}`

// spec: §25.4 lines 2037-2042 — "The following endpoints **require**
// `Idempotency-Key` at Tier 2/3 and return `400 IDEMPOTENCY_KEY_REQUIRED`
// when omitted: ... `POST /v1/admin/backups` for `type: "full"`. At Tier
// 1 (dev), the key is optional on these endpoints." Verified against the
// durable ops_idempotency_keys table, not the in-process memory store.
//
// diagnosis: the §25.4 tier-dependent required-key posture regressed at
// the HTTP surface when backed by the real Postgres store. At Tier 2/3 a
// full backup without an Idempotency-Key must be rejected with 400
// IDEMPOTENCY_KEY_REQUIRED before the handler runs; at Tier 1 the same
// request must be accepted.
func TestOpsIdempotencyTierRequiredKey_spec_25_4(t *testing.T) {
	t.Parallel()
	pool := startIdemPostgres(t)

	t.Run("tier2 rejects a required-key full backup with no key", func(t *testing.T) {
		srv := idemServer(t, pool, true)
		rec := postIdem(srv, "/v1/admin/backups", "", "alice", fullBackupBody)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "IDEMPOTENCY_KEY_REQUIRED") {
			t.Errorf("body missing IDEMPOTENCY_KEY_REQUIRED: %s", rec.Body.String())
		}
	})

	t.Run("tier2 accepts a full backup with a key and persists the record", func(t *testing.T) {
		srv := idemServer(t, pool, true)
		first := postIdem(srv, "/v1/admin/backups", "durable-1", "alice", fullBackupBody)
		if first.Code != http.StatusAccepted {
			t.Fatalf("first status = %d, want 202; body=%s", first.Code, first.Body.String())
		}
		// The record is durable: a replay returns the cached response and
		// does not re-execute (X-Lenny-Idempotent-Replay marks the cache hit).
		second := postIdem(srv, "/v1/admin/backups", "durable-1", "alice", fullBackupBody)
		if second.Code != http.StatusAccepted {
			t.Fatalf("replay status = %d, want 202; body=%s", second.Code, second.Body.String())
		}
		if second.Header().Get("X-Lenny-Idempotent-Replay") != "true" {
			t.Errorf("replay missing X-Lenny-Idempotent-Replay header")
		}
		if first.Body.String() != second.Body.String() {
			t.Errorf("replay body differs from original:\n first=%s\nsecond=%s", first.Body.String(), second.Body.String())
		}
	})

	t.Run("tier1 accepts a full backup with no key", func(t *testing.T) {
		srv := idemServer(t, pool, false)
		rec := postIdem(srv, "/v1/admin/backups", "", "alice", fullBackupBody)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
	})
}

// spec: §25.4 lines 2048-2059 — during a Postgres outage, "Endpoints
// requiring idempotency keys return `503 IDEMPOTENCY_STORE_UNAVAILABLE`"
// because "silently proceeding without it would violate the contract",
// while "Endpoints that accept optional idempotency keys proceed without
// tracking". Verified against the durable pgstore by closing its pool.
//
// diagnosis: the §25.4 fail-closed Postgres-outage path regressed. When
// the durable idempotency store is unreachable, a required-key endpoint
// at Tier 2/3 must fail closed with 503 IDEMPOTENCY_STORE_UNAVAILABLE
// rather than executing the mutation untracked; an optional endpoint (or
// a required endpoint at Tier 1) must still proceed.
func TestOpsIdempotencyStoreOutage_spec_25_4(t *testing.T) {
	// Not parallel: this test closes the pool it shares with its server,
	// so it owns a private container/pool for the whole test.
	pool := startIdemPostgres(t)

	// Confirm the store is healthy before the outage: a keyed full backup
	// at Tier 2 succeeds and persists.
	warmup := idemServer(t, pool, true)
	if rec := postIdem(warmup, "/v1/admin/backups", "pre-outage", "alice", fullBackupBody); rec.Code != http.StatusAccepted {
		t.Fatalf("pre-outage full backup status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	// Induce the outage: closing the pool makes every subsequent store
	// operation fail as opsidem.ErrStoreUnavailable, the same class a real
	// Postgres connection loss produces.
	pool.Close()

	t.Run("tier2 required-key endpoint fails closed with 503", func(t *testing.T) {
		srv := idemServer(t, pool, true)
		rec := postIdem(srv, "/v1/admin/backups", "outage-1", "alice", fullBackupBody)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "IDEMPOTENCY_STORE_UNAVAILABLE") {
			t.Errorf("body missing IDEMPOTENCY_STORE_UNAVAILABLE: %s", rec.Body.String())
		}
	})

	t.Run("tier2 optional endpoint proceeds during the outage", func(t *testing.T) {
		srv := idemServer(t, pool, true)
		// A non-full backup is not a required-key endpoint, so it proceeds
		// (untracked) rather than failing closed.
		rec := postIdem(srv, "/v1/admin/backups", "outage-2", "alice", `{"type":"postgres"}`)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("optional endpoint status = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("tier1 required-key endpoint proceeds during the outage", func(t *testing.T) {
		srv := idemServer(t, pool, false)
		// At Tier 1 the key is optional on required-classified endpoints,
		// so the fail-closed 503 does not apply and the mutation proceeds.
		rec := postIdem(srv, "/v1/admin/backups", "outage-3", "alice", fullBackupBody)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("tier1 status = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
	})
}
