//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.11 restore/resume lock-ownership
// semantics against the real §25.4 remediation-lock store (the Postgres
// Tier 1 store, migration 0121) and two distinct operator identities. It
// boots the real cmd/lenny-ops binary, drives a restore through failure
// and a lock steal between two operators, and asserts resume is gated
// on current lock ownership rather than on the lock merely being held
// by someone. The pkg/ops/backup and pkg/ops/opsserver unit tests cover
// the held-lock and released-lock cases over the in-memory
// backup.MemLocker fake; this test is the only one that drives a steal
// through the real §25.4 coordination service and two caller identities.
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

// spec: §25.11 (Restore Failure and Recovery, "Resume") — "Lock
// semantics: resume requires the caller to be the current acquiredBy of
// the restore:platform remediation lock. If the lock has been stolen by
// another operator (Section 25.4 Stealing), the new holder is now
// acquiredBy and may resume; the original caller must steal it back to
// regain control." Confirmed by the §25.11 error-code table: "409 |
// restore/resume called but the caller does not hold the
// restore:platform lock; re-acquire and retry" (RESTORE_LOCK_REQUIRED).
//
// diagnosis: a failure means resume authorizes on whether the
// restore:platform lock is held by anyone, not on whether the caller is
// its current acquiredBy. After bob steals the lock alice held, alice's
// resume attempt must fail with 409 RESTORE_LOCK_REQUIRED even though a
// lock is still held (by bob); it must succeed again only once alice has
// stolen the lock back and is acquiredBy once more.
func TestRestoreResumeAfterLockStealRequiresCurrentHolder(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	ctx := context.Background()

	// Seed a completed backup directly so ExecuteRestore has something to
	// restore. platformVersion/schemaVersion match cmd/lenny-ops's
	// embedded build version so the restore's version-compatibility gate
	// passes, matching the convention in
	// TestRestorePreviewAndSafetyCheckAgainstPostgresE2E.
	const backupID = "bkp-resume-lock"
	if _, err := pg.Pool.Exec(ctx, `
		INSERT INTO ops_backups
			(id, type, status, started_at, completed_at, size_bytes,
			 started_by, job_id, platform_version, schema_version)
		VALUES
			($1, 'full', 'completed', now() - interval '1 hour', now() - interval '1 hour',
			 1024, 'alice@acme.com', 'job-'||$1, '1.5.0', 42)`, backupID); err != nil {
		t.Fatalf("seed ops_backups: %v", err)
	}

	ops := opsprocess.StartWith(
		t,
		"--postgres-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--redis-allow-insecure",
	)
	base := ops.BaseURL()
	client := http.DefaultClient

	do := func(caller, method, path string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal request body: %v", err)
			}
			reader = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, base+path, reader)
		if err != nil {
			t.Fatalf("build request %s %s: %v", method, path, err)
		}
		req.Header.Set("Content-Type", "application/json")
		// opsprocess.StartWith supplies no --bearer-trust-hmac-key-file,
		// so lenny-ops wires no AuthConfig and the operability surface
		// falls back to the header-trust identity: callerIdentity reads
		// X-Lenny-Caller (not X-Lenny-User-ID, which only feeds a
		// verified-principal Subject when an AuthConfig is wired) and
		// callerRole defaults to platform-admin absent an X-Lenny-Role
		// override. alice and bob are two distinct platform-admin
		// operators contending for the same restore:platform lock.
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-Caller", caller)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("decode %s %s response %q: %v", method, path, raw, err)
			}
		}
		return resp.StatusCode, out
	}

	restoreLockID := func(caller string) string {
		t.Helper()
		status, out := do(caller, http.MethodGet, "/v1/admin/remediation-locks", nil)
		if status != http.StatusOK {
			t.Fatalf("list locks: status = %d, want 200; body = %v", status, out)
		}
		locks, _ := out["locks"].([]any)
		for _, l := range locks {
			lock, _ := l.(map[string]any)
			if lock["scope"] == "restore:platform" {
				id, _ := lock["id"].(string)
				return id
			}
		}
		t.Fatalf("no restore:platform lock found in %v", out)
		return ""
	}

	// alice executes the restore: this acquires the restore:platform
	// remediation lock as alice (§25.11 step 2) and creates the
	// ops_restore_state row.
	status, out := do("alice@acme.com", http.MethodPost, "/v1/admin/restore/execute", map[string]any{
		"backupId": backupID, "confirm": true, "acknowledgeDataLoss": true,
	})
	if status != http.StatusAccepted {
		t.Fatalf("restore/execute (alice): status = %d, want 202; body = %v", status, out)
	}
	restoreID, _ := out["restoreId"].(string)
	if restoreID == "" {
		t.Fatalf("restore/execute (alice): no restoreId in response: %v", out)
	}

	// Drive the restore to a failed state directly against the durable
	// row, as the completion reconciler would after a real shard
	// failure. Outside a real Kubernetes cluster lenny-ops falls back to
	// the in-memory FakeLauncher, which never itself fails or completes
	// a Job, so this seeds the "restore that fails" precondition the
	// §25.11 Restore Failure and Recovery section describes: the lock
	// stays held after a failure and only an explicit release or steal
	// changes its holder.
	if _, err := pg.Pool.Exec(ctx,
		`UPDATE ops_restore_state SET status = 'failed', failed_shard = 'shard-0', error = 'pg_restore exited 1' WHERE id = $1`,
		restoreID); err != nil {
		t.Fatalf("mark restore failed: %v", err)
	}

	// bob steals the restore:platform lock alice is holding (§25.4
	// Stealing, an audited action).
	lockID := restoreLockID("bob@acme.com")
	status, out = do("bob@acme.com", http.MethodPost, "/v1/admin/remediation-locks/"+lockID+"/steal",
		map[string]any{"confirm": true, "reason": "alice is unreachable; recovering the restore"})
	if status != http.StatusOK {
		t.Fatalf("bob steal: status = %d, want 200; body = %v", status, out)
	}
	if got, _ := out["acquiredBy"].(string); got != "bob@acme.com" {
		t.Fatalf("bob steal: acquiredBy = %q, want bob@acme.com", got)
	}

	// bob is now acquiredBy and may resume.
	status, out = do("bob@acme.com", http.MethodPost, "/v1/admin/restore/resume?restoreId="+restoreID, nil)
	if status != http.StatusAccepted {
		t.Fatalf("bob resume after steal: status = %d, want 202; body = %v", status, out)
	}

	// alice, no longer acquiredBy, must fail with the documented
	// RESTORE_LOCK_REQUIRED conflict — the lock is held (by bob), so a
	// check that only asks "is the lock held" would wrongly let her
	// through.
	status, out = do("alice@acme.com", http.MethodPost, "/v1/admin/restore/resume?restoreId="+restoreID, nil)
	if status != http.StatusConflict {
		t.Fatalf("alice resume after bob's steal: status = %d, want 409; body = %v", status, out)
	}
	errBody, _ := out["error"].(map[string]any)
	if code, _ := errBody["code"].(string); code != "RESTORE_LOCK_REQUIRED" {
		t.Errorf("alice resume after bob's steal: error.code = %q, want RESTORE_LOCK_REQUIRED", code)
	}

	// alice steals the lock back (§25.11: "the original caller must
	// steal it back to regain control") and may then resume again.
	lockID = restoreLockID("alice@acme.com")
	status, out = do("alice@acme.com", http.MethodPost, "/v1/admin/remediation-locks/"+lockID+"/steal",
		map[string]any{"confirm": true, "reason": "regaining control of the restore"})
	if status != http.StatusOK {
		t.Fatalf("alice steal back: status = %d, want 200; body = %v", status, out)
	}
	if got, _ := out["acquiredBy"].(string); got != "alice@acme.com" {
		t.Fatalf("alice steal back: acquiredBy = %q, want alice@acme.com", got)
	}

	status, out = do("alice@acme.com", http.MethodPost, "/v1/admin/restore/resume?restoreId="+restoreID, nil)
	if status != http.StatusAccepted {
		t.Fatalf("alice resume after stealing back: status = %d, want 202; body = %v", status, out)
	}
}
