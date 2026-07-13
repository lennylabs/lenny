//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test: the real lenny-ctl command dispatch driving the
// §25.14 operability command groups against the real cmd/lenny-ops binary
// backed by a Postgres and a Redis container. Every other operability test
// issues raw HTTP against the ops surface; this one exercises the whole
// lenny-ctl path — global-flag parsing, the §25.14 command dispatch, the
// §24.16 --ops-server routing, and the ctl.Client's dev-header propagation
// and JSON decode — end to end against a live ops process and store. A
// mutation issued through lenny-ctl (escalations create/resolve) is
// cross-checked against the Postgres ops_escalations table so a passing
// test proves the CLI-driven request round-tripped through the real store
// rather than an httptest stub.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/ctlcli"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// TestLennyCtlOperabilityCommandsE2E boots cmd/lenny-ops against real
// Postgres and Redis and drives the §25.14 lenny-ctl operability command
// groups against it: `runbooks list`, `diagnose connectivity`,
// `escalations create` / `escalations resolve`, and `backup list`. Each
// command is invoked through ctlcli.Run with the §24.16 --ops-server flag
// pointing at the live ops process and the dev-header identity the
// unauthenticated ops surface honours. The test asserts the CLI exits 0,
// emits the JSON body the §25.14 API mapping documents for each command,
// and — for the escalations mutation — that the resolve landed in the
// Postgres ops_escalations table.
//
// spec: §25.14 lenny-ctl Extensions — the command→API mappings: "lenny-ctl
// runbooks list | GET /v1/admin/runbooks", "lenny-ctl diagnose connectivity
// | GET /v1/admin/diagnostics/connectivity", "lenny-ctl escalations create
// --severity <sev> --summary <text> | POST /v1/admin/escalations",
// "lenny-ctl escalations resolve <id> | PUT /v1/admin/escalations/{id} |
// Mark as resolved", "lenny-ctl backup list | GET /v1/admin/backups"; and
// the Routing rule "events, diagnose, runbooks, upgrade, drift, backup,
// locks, escalations, logs, mcp-management → lenny-ops → --ops-server flag".
// §25.4 The lenny-ops Service — an escalation resolved through the surface
// updates its durable Postgres record.
// diagnosis: a failure means the lenny-ctl operability path diverged from
// §25.14 when driven against a real lenny-ops process. Either the §24.16
// --ops-server routing did not reach the ops surface, the dev-header
// identity did not propagate so a platform-admin endpoint rejected the
// call, a command's documented API mapping is wrong (wrong method, path, or
// body), the response did not decode into the CLI's JSON output, or the
// escalations resolve issued through the CLI did not round-trip to the
// Postgres ops_escalations table — any of which is invisible to the
// httptest-with-stubs component tests that never run the CLI against a live
// store.
func TestLennyCtlOperabilityCommandsE2E(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	ops := opsprocess.StartWith(t,
		"--postgres-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--redis-allow-insecure",
	)
	ctx := context.Background()

	// runCtl drives lenny-ctl exactly as a shell invocation would: the
	// §24.16 global flags select the live ops process via --ops-server and
	// present the platform-admin dev identity the unauthenticated ops
	// surface honours, then the command and its arguments follow. It
	// returns the exit code and the captured stdout.
	runCtl := func(args ...string) (int, []byte) {
		t.Helper()
		full := append([]string{
			"--ops-server", ops.BaseURL(),
			"--dev-tenant", "acme",
			"--dev-roles", "platform-admin",
		}, args...)
		var stdout, stderr bytes.Buffer
		code := ctlcli.Run(full, &stdout, &stderr, "test")
		if stderr.Len() > 0 {
			t.Logf("lenny-ctl %v stderr: %s", args, stderr.String())
		}
		return code, stdout.Bytes()
	}

	// decode parses the CLI's JSON stdout into a top-level object, failing
	// the test when the command did not emit a decodable body.
	decode := func(cmd string, out []byte) map[string]any {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("lenny-ctl %s: stdout is not a JSON object (%v): %s", cmd, err, out)
		}
		return m
	}

	// ---- runbooks list: GET /v1/admin/runbooks through lenny-ctl ----
	// §25.14: `runbooks list` maps to GET /v1/admin/runbooks. The ops
	// process indexes docs/runbooks from its repo-root working directory,
	// so the CLI surfaces a populated runbooks array.
	code, out := runCtl("runbooks", "list")
	if code != 0 {
		t.Fatalf("lenny-ctl runbooks list: exit %d, want 0; output %s", code, out)
	}
	runbooksResp := decode("runbooks list", out)
	books, ok := runbooksResp["runbooks"].([]any)
	if !ok {
		t.Fatalf("lenny-ctl runbooks list: response has no runbooks array: %v", runbooksResp)
	}
	if len(books) == 0 {
		t.Errorf("lenny-ctl runbooks list: runbooks array is empty; the CLI did not surface the ops runbook index")
	}

	// ---- diagnose connectivity: GET /v1/admin/diagnostics/connectivity ----
	// §25.14: `diagnose connectivity` maps to the §25.6 connectivity
	// report. The CLI surfaces the healthy flag and dependencies array.
	code, out = runCtl("diagnose", "connectivity")
	if code != 0 {
		t.Fatalf("lenny-ctl diagnose connectivity: exit %d, want 0; output %s", code, out)
	}
	conn := decode("diagnose connectivity", out)
	if _, ok := conn["healthy"].(bool); !ok {
		t.Errorf("lenny-ctl diagnose connectivity: response has no healthy flag: %v", conn)
	}
	if _, ok := conn["dependencies"].([]any); !ok {
		t.Errorf("lenny-ctl diagnose connectivity: response has no dependencies array: %v", conn)
	}

	// ---- escalations create: POST /v1/admin/escalations through the CLI ----
	// §25.14: `escalations create --severity --summary` maps to POST
	// /v1/admin/escalations. The created escalation carries a server id and
	// opens in the open state.
	code, out = runCtl("escalations", "create",
		"--severity", "critical",
		"--summary", "warm pool exhausted; scale-up failed")
	if code != 0 {
		t.Fatalf("lenny-ctl escalations create: exit %d, want 0; output %s", code, out)
	}
	created := decode("escalations create", out)
	escID, _ := created["id"].(string)
	if escID == "" {
		t.Fatalf("lenny-ctl escalations create: response carried no id: %v", created)
	}
	if got, _ := created["status"].(string); got != "open" {
		t.Errorf("lenny-ctl escalations create: status = %q, want open", got)
	}

	// escalationStatus reads the persisted status straight from the Tier-1
	// Postgres store so the round-trip assertion confirms the CLI-driven
	// mutation reached the real store, not an in-memory buffer.
	escalationStatus := func(id string) (string, bool) {
		t.Helper()
		var status string
		if err := pg.Pool.QueryRow(ctx,
			`SELECT status FROM ops_escalations WHERE id = $1`, id).Scan(&status); err != nil {
			return "", false
		}
		return status, true
	}
	if status, present := escalationStatus(escID); !present {
		t.Fatalf("escalation %s created through lenny-ctl not found in ops_escalations (create did not persist to Postgres)", escID)
	} else if status != "open" {
		t.Errorf("persisted escalation %s status = %q, want open", escID, status)
	}

	// ---- escalations resolve: PUT /v1/admin/escalations/{id} through CLI --
	// §25.14: `escalations resolve <id>` marks the escalation resolved. The
	// mutation must round-trip to the Postgres store.
	code, out = runCtl("escalations", "resolve", escID)
	if code != 0 {
		t.Fatalf("lenny-ctl escalations resolve %s: exit %d, want 0; output %s", escID, code, out)
	}
	resolved := decode("escalations resolve", out)
	if got, _ := resolved["status"].(string); got != "resolved" {
		t.Errorf("lenny-ctl escalations resolve: status = %q, want resolved", got)
	}
	if status, present := escalationStatus(escID); !present {
		t.Errorf("escalation %s vanished from ops_escalations after a CLI resolve", escID)
	} else if status != "resolved" {
		t.Errorf("persisted escalation %s status = %q after a CLI resolve, want resolved (the resolve did not round-trip through Postgres)", escID, status)
	}

	// ---- backup list: GET /v1/admin/backups through lenny-ctl ----
	// §25.14: `backup list` maps to GET /v1/admin/backups. The CLI surfaces
	// the §25.11 BackupPage envelope (the backups page and its hasMore
	// cursor flag); on a fresh store the backups field is present but empty.
	code, out = runCtl("backup", "list")
	if code != 0 {
		t.Fatalf("lenny-ctl backup list: exit %d, want 0; output %s", code, out)
	}
	backups := decode("backup list", out)
	if _, present := backups["backups"]; !present {
		t.Errorf("lenny-ctl backup list: response has no backups field: %v", backups)
	}
	if _, ok := backups["hasMore"].(bool); !ok {
		t.Errorf("lenny-ctl backup list: response has no hasMore field (the §25.11 BackupPage envelope): %v", backups)
	}
}
