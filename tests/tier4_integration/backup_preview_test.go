//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the declared lenny_ops_endpoints suite
// (TESTING.md §12.4), the "backup/restore preview against the compose
// stack" surface. It boots the real cmd/lenny-ops binary against a live
// Postgres (with migrations applied) and Redis, seeds ops_backups rows
// directly, then drives GET /v1/admin/restore/safety-check and
// POST /v1/admin/restore/preview through the composition root and the
// Postgres-backed backup store. This walks the surface above the
// pkg/ops/opsserver httptest-with-stubs component tests, which never run
// the composition root against a real store.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §25.11 — "GET /v1/admin/restore/safety-check | Compare a backup
// against current state to estimate data loss. Params: ?backupId=" and
// "POST /v1/admin/restore/preview | Analyze restore impact without
// executing. Body: {"backupId": "..."}. Returns affected resources,
// version compatibility, and estimated downtime." The Safety Check
// contract: "safe: true is returned only when the backup is so recent
// (< 5 minutes old) or the platform has been idle (no recent writes)
// that no data loss occurs. In practice, most restores are safe: false
// and require explicit acknowledgeDataLoss: true."
//
// diagnosis: a failure means the cmd/lenny-ops composition root did not
// thread the live Postgres backup store through the §25.11 restore
// preview / safety-check endpoints. Either the endpoint did not read the
// seeded ops_backups row from the real store, the safety check did not
// compute the age-based safe verdict (a backup older than 5 minutes must
// be safe:false; a fresh backup safe:true), or the preview did not
// return the version-compatibility and computed-downtime analysis — any
// of which shows the restore-preview surface diverged from §25.11 when
// driven against a real store rather than an httptest stub.
func TestRestorePreviewAndSafetyCheckAgainstPostgresE2E(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	ctx := context.Background()

	// Seed ops_backups rows directly so the age of each backup is under the
	// test's control. The safety check's safe verdict turns on the backup's
	// completed_at relative to now (§25.11 5-minute window); an hour-old
	// backup must be unsafe and a just-completed backup safe.
	const (
		oldID   = "bkp-preview-old"
		freshID = "bkp-preview-fresh"
	)
	seed := func(id string, completedExpr string) {
		t.Helper()
		_, err := pg.Pool.Exec(ctx, `
			INSERT INTO ops_backups
				(id, type, status, started_at, completed_at, size_bytes,
				 started_by, job_id, platform_version, schema_version)
			VALUES
				($1, 'full', 'completed', now() - interval '1 hour', `+completedExpr+`,
				 6442450944, 'alice@acme.com', 'job-'||$1, '1.5.0', 42)`, id)
		if err != nil {
			t.Fatalf("seed ops_backups %s: %v", id, err)
		}
	}
	// A 6 GiB backup completed an hour ago: unsafe, and large enough that the
	// computed downtime exceeds the bare base term.
	seed(oldID, "now() - interval '1 hour'")
	// A backup completed just now: inside the 5-minute window, so safe.
	seed(freshID, "now()")

	ops := opsprocess.StartWith(
		t,
		"--postgres-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--redis-allow-insecure",
	)
	base := ops.BaseURL()
	client := http.DefaultClient

	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		// The dev / unauthenticated ops surface honours the X-Lenny-* headers
		// for identity and role; a restore operator presents platform-admin.
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

	// Safety check on the hour-old backup: the endpoint reads the seeded row
	// from the live Postgres store and reports the restore as unsafe because
	// the backup pre-dates the 5-minute window. §25.11 "In practice, most
	// restores are safe: false and require explicit acknowledgeDataLoss".
	status, out := do(http.MethodGet, "/v1/admin/restore/safety-check?backupId="+oldID, nil)
	if status != http.StatusOK {
		t.Fatalf("safety-check(%s): status = %d, want 200; body = %v", oldID, status, out)
	}
	if got := out["backupId"]; got != oldID {
		t.Errorf("safety-check(%s): backupId = %v, want %q", oldID, got, oldID)
	}
	if safe, _ := out["safe"].(bool); safe {
		t.Errorf("safety-check(%s): safe = true, want false for an hour-old backup (§25.11 5-minute window)", oldID)
	}
	if got, _ := out["recommendedAction"].(string); got != "review and acknowledgeDataLoss, then execute" {
		t.Errorf("safety-check(%s): recommendedAction = %q, want the acknowledge-data-loss guidance", oldID, got)
	}
	// The compatibility block is resolved from the stored row's versions,
	// proving the endpoint read the durable record rather than a stub.
	compat, _ := out["compatibility"].(map[string]any)
	if compat == nil {
		t.Fatalf("safety-check(%s): missing compatibility block; body = %v", oldID, out)
	}
	if v, _ := compat["backupSchemaVersion"].(float64); int(v) != 42 {
		t.Errorf("safety-check(%s): compatibility.backupSchemaVersion = %v, want 42 (from the seeded row)", oldID, compat["backupSchemaVersion"])
	}
	if v, _ := compat["backupPlatformVersion"].(string); v != "1.5.0" {
		t.Errorf("safety-check(%s): compatibility.backupPlatformVersion = %q, want \"1.5.0\" (from the seeded row)", oldID, v)
	}

	// Safety check on the fresh backup: inside the 5-minute window, so the
	// same endpoint reports the restore as safe with the safe-path guidance.
	status, out = do(http.MethodGet, "/v1/admin/restore/safety-check?backupId="+freshID, nil)
	if status != http.StatusOK {
		t.Fatalf("safety-check(%s): status = %d, want 200; body = %v", freshID, status, out)
	}
	if safe, _ := out["safe"].(bool); !safe {
		t.Errorf("safety-check(%s): safe = false, want true for a just-completed backup (§25.11 < 5 minutes old)", freshID)
	}
	if got, _ := out["recommendedAction"].(string); got != "restore is safe; execute" {
		t.Errorf("safety-check(%s): recommendedAction = %q, want the safe-restore guidance", freshID, got)
	}

	// Preview on the hour-old backup: analyze restore impact without
	// executing. §25.11 "Returns affected resources, version compatibility,
	// and estimated downtime."
	status, out = do(http.MethodPost, "/v1/admin/restore/preview", map[string]any{"backupId": oldID})
	if status != http.StatusOK {
		t.Fatalf("preview(%s): status = %d, want 200; body = %v", oldID, status, out)
	}
	if got := out["backupId"]; got != oldID {
		t.Errorf("preview(%s): backupId = %v, want %q", oldID, got, oldID)
	}
	if compatible, _ := out["compatible"].(bool); !compatible {
		t.Errorf("preview(%s): compatible = false, want true (backup version is not newer than current)", oldID)
	}
	if full, _ := out["requiresFullStop"].(bool); !full {
		t.Errorf("preview(%s): requiresFullStop = false, want true (a Postgres restore is a full-stop operation)", oldID)
	}
	affected := toStringSlice(out["affectedResources"])
	if !containsStr(affected, "postgres") {
		t.Errorf("preview(%s): affectedResources = %v, want it to include \"postgres\"", oldID, affected)
	}
	// The downtime is a computed ISO-8601 estimate scaled by backup size and
	// component count, not a bare constant. A 6 GiB backup exceeds the base
	// term, so the estimate is a non-zero PT duration.
	downtime, _ := out["estimatedDowntime"].(string)
	if !strings.HasPrefix(downtime, "PT") || downtime == "PT0S" {
		t.Errorf("preview(%s): estimatedDowntime = %q, want a non-zero ISO-8601 PT duration", oldID, downtime)
	}

	// Error path through the real store: an unknown backup id is BACKUP_NOT_
	// FOUND (§25.11 error codes), proving the endpoint queries the durable
	// store rather than fabricating a response.
	status, out = do(http.MethodGet, "/v1/admin/restore/safety-check?backupId=bkp-does-not-exist", nil)
	if status != http.StatusNotFound {
		t.Fatalf("safety-check(missing): status = %d, want 404; body = %v", status, out)
	}
	errBody, _ := out["error"].(map[string]any)
	if code, _ := errBody["code"].(string); code != "BACKUP_NOT_FOUND" {
		t.Errorf("safety-check(missing): error.code = %q, want BACKUP_NOT_FOUND", code)
	}

	// Note on the estimator seams: dataLossEstimate.mutationsSinceBackup and
	// preview.artifactReplicationLagSeconds serialize as 0 here because
	// cmd/lenny-ops wires no concrete DataLossEstimator or
	// ReplicationLagSource into the backup service even with Postgres
	// present. That production-wiring gap is tracked separately; this test
	// asserts the observable §25.11 contract the composition root does
	// implement (the endpoints, the store round trip, the age-based safe
	// verdict, and the computed compatibility and downtime).
}

// toStringSlice converts a decoded JSON array to a string slice.
func toStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// containsStr reports whether s contains want.
func containsStr(s []string, want string) bool {
	for _, e := range s {
		if e == want {
			return true
		}
	}
	return false
}
