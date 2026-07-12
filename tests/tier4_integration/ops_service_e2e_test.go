//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test: the real cmd/lenny-ops binary running against
// a Postgres and a Redis container. It walks a DevOps-agent journey
// across the §25.4 operability surface — GET /v1/admin/me discovery, the
// /v1/admin/me/operations inventory alias, then acquiring and releasing a
// remediation lock — and proves the remediation-lock endpoints drive the
// real Postgres Tier-1 store rather than a stub: the acquired lock lands
// in ops_remediation_locks and the release removes it, verified by
// querying the database directly. This exercises the surface above the
// httptest-with-stubs component tests, which never run the composition
// root against a live store.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// TestOpsServiceRemediationLockPersistenceE2E boots cmd/lenny-ops against
// real Postgres and Redis and walks the §25.4 watchdog journey, asserting
// that a remediation lock acquired through POST /v1/admin/remediation-locks
// persists to the Postgres Tier-1 store and that a DELETE releases it.
//
// spec: §25.4 (the mandatory lenny-ops service hosts the remediation
// surface; Tier 1 Postgres acquire is a distributed mutex over the shared
// ops_remediation_locks table; a released lock leaves no row).
// diagnosis: a failure means the cmd/lenny-ops composition root did not
// thread the live Postgres Tier-1 store through to the remediation-lock
// surface. Either POST /v1/admin/remediation-locks did not persist a row
// to ops_remediation_locks, a duplicate acquire did not return the 409
// distributed-mutex conflict, or DELETE did not remove the row — any of
// which shows the §25.4 remediation endpoints diverged from the spec when
// driven against a real store rather than an httptest stub.
func TestOpsServiceRemediationLockPersistenceE2E(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	// §25.16: the deployment tier and stable installation UUID the /me
	// discovery block surfaces. They flow in through flags and must echo
	// back unchanged, proving the composition root threaded them to the
	// live surface.
	const installationID = "11111111-2222-3333-4444-555555555555"
	const tier = "tier2"
	ops := opsprocess.StartWith(t,
		"--postgres-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--redis-allow-insecure",
		"--installation-id="+installationID,
		"--platform-tier="+tier,
	)
	base := ops.BaseURL()
	client := http.DefaultClient
	ctx := context.Background()

	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		// The dev / unauthenticated surface honours the X-Lenny-* headers
		// for identity and role; a watchdog agent presents platform-admin.
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out
	}

	lockRows := func(id string) int {
		t.Helper()
		var n int
		if err := pg.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM ops_remediation_locks WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatalf("db query ops_remediation_locks: %v", err)
		}
		return n
	}

	// ---- discovery: GET /v1/admin/me returns the platform context ----
	code, me := do(http.MethodGet, "/v1/admin/me", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/me: status %d (%v)", code, me)
	}
	platform, _ := me["platform"].(map[string]any)
	if platform == nil {
		t.Fatalf("GET /v1/admin/me: no platform block in %v", me)
	}
	if got, _ := platform["installationId"].(string); got != installationID {
		t.Errorf("platform.installationId = %q, want %q", got, installationID)
	}
	if got, _ := platform["tier"].(string); got != tier {
		t.Errorf("platform.tier = %q, want %q", got, tier)
	}
	if got, _ := platform["version"].(string); got == "" {
		t.Error("platform.version is empty; the /me block did not surface the build version")
	}
	// The lock-memory-tier capability reflects the wired coordination gate.
	caps, _ := me["capabilities"].(map[string]any)
	if caps == nil {
		t.Fatalf("GET /v1/admin/me: no capabilities block in %v", me)
	}
	if got, _ := caps["lockMemoryTier"].(string); got != "single-replica-only" {
		t.Errorf("capabilities.lockMemoryTier = %q, want single-replica-only", got)
	}
	// §25.4: the discovery block hands off to the caller's operations alias.
	links, _ := me["links"].(map[string]any)
	if got, _ := links["myOperations"].(string); got != "/v1/admin/me/operations" {
		t.Errorf("links.myOperations = %q, want /v1/admin/me/operations", got)
	}

	// ---- inventory: the caller's in-flight operations (empty so far) ----
	code, opsPage := do(http.MethodGet, "/v1/admin/me/operations", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/me/operations: status %d (%v)", code, opsPage)
	}
	if _, ok := opsPage["operations"].([]any); !ok {
		t.Errorf("GET /v1/admin/me/operations: body has no operations array: %v", opsPage)
	}

	// ---- acquire: POST /v1/admin/remediation-locks lands in Postgres ----
	code, lock := do(http.MethodPost, "/v1/admin/remediation-locks", map[string]any{
		"scope":      "pool:acme-default",
		"operation":  "scale",
		"ttlSeconds": 300,
	})
	if code != http.StatusCreated {
		t.Fatalf("POST /v1/admin/remediation-locks: status %d (%v)", code, lock)
	}
	lockID, _ := lock["id"].(string)
	if lockID == "" {
		t.Fatal("acquired lock has no id")
	}
	// §25.4 Tier 1: with Postgres reachable the acquire is served by the
	// Postgres store, not the in-memory fallback.
	if store, _ := lock["lockStore"].(string); store != "postgres" {
		t.Errorf("lock.lockStore = %q, want postgres (Tier 1 with Postgres reachable)", store)
	}
	if n := lockRows(lockID); n != 1 {
		t.Fatalf("ops_remediation_locks row count for %s = %d, want 1 (acquire did not persist to Postgres)", lockID, n)
	}

	// ---- read back: GET the lock through the live surface ----
	code, got := do(http.MethodGet, "/v1/admin/remediation-locks/"+lockID, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/remediation-locks/%s: status %d", lockID, code)
	}
	if got["id"] != lockID {
		t.Errorf("GET lock id = %v, want %s", got["id"], lockID)
	}
	if got["scope"] != "pool:acme-default" {
		t.Errorf("GET lock scope = %v, want pool:acme-default", got["scope"])
	}

	// ---- conflict: a second acquire on the held scope is rejected ----
	code, conflict := do(http.MethodPost, "/v1/admin/remediation-locks", map[string]any{
		"scope":     "pool:acme-default",
		"operation": "scale",
	})
	if code != http.StatusConflict {
		t.Errorf("second acquire on held scope: status %d, want 409 (Tier 1 distributed mutex); body %v", code, conflict)
	}
	if n := lockRows(lockID); n != 1 {
		t.Errorf("ops_remediation_locks row count after conflicting acquire = %d, want 1", n)
	}

	// ---- release: DELETE removes the durable row ----
	code, _ = do(http.MethodDelete, "/v1/admin/remediation-locks/"+lockID, nil)
	if code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/admin/remediation-locks/%s: status %d, want 204", lockID, code)
	}
	if n := lockRows(lockID); n != 0 {
		t.Errorf("ops_remediation_locks row count after release = %d, want 0 (release did not delete the row)", n)
	}
}
