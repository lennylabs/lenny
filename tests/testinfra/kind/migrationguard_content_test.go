// SPDX-License-Identifier: MIT

package kind

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// contentGuardStub is a kubectl stub for the content check and the
// recorder. It answers the two schema_migrations reads, the OID read,
// the record read, and the record write, and logs every call. The
// record write pipes `create --dry-run` into `apply -f -`; the stub's
// create prints its arguments and its apply copies stdin to
// $STUB_APPLIED, so a case reads back the manifest the recorder wrote.
const contentGuardStub = `#!/usr/bin/env bash
echo "$*" >> "${STUB_LOG}"
case "$*" in
  *to_regclass*) printf 't\n' ;;
  *"SELECT version, dirty FROM schema_migrations"*) printf '%s\n' "${STUB_ROW}" ;;
  *"SELECT oid FROM pg_database"*)
    [[ "${STUB_FAIL:-}" == "oid" ]] && { echo "stub kubectl: exec failed" >&2; exit 1; }
    printf '%s\n' "${STUB_OID}" ;;
  *"get configmap lenny-e2e-migrations"*)
    [[ "${STUB_FAIL:-}" == "record" ]] && { echo "stub kubectl: get failed" >&2; exit 1; }
    if [[ -n "${STUB_RECORD_OID}" ]]; then printf '%s\n%s' "${STUB_RECORD_OID}" "${STUB_RECORD}"; fi ;;
  *"create configmap lenny-e2e-migrations"*) printf '%s\n' "$*" ;;
  *"apply -f -"*)
    [[ "${STUB_FAIL:-}" == "apply" ]] && { echo "stub kubectl: apply failed" >&2; exit 1; }
    cat > "${STUB_APPLIED}" ;;
  *"delete configmap lenny-e2e-migrations"*) : > "${STUB_DELETED}" ;;
  *) echo "stub kubectl: unexpected $*" >&2; exit 3 ;;
esac
`

// contentGuardFiles is the branch's migrations directory: each name
// maps to the content written for it.
var contentGuardFiles = map[string]string{
	"0009_early.up.sql":      "CREATE TABLE early ();\n",
	"0009_early.down.sql":    "DROP TABLE early;\n",
	"0191_previous.up.sql":   "CREATE TABLE previous ();\n",
	"0191_previous.down.sql": "DROP TABLE previous;\n",
	"0192_latest.up.sql":     "CREATE TABLE latest ();\n",
	"0192_latest.down.sql":   "DROP TABLE latest;\n",
}

// recordLine returns the manifest line the recorder writes for one
// branch file: the version without padding, the sha256, and the name.
func recordLine(version, name, content string) string {
	sum := sha256.Sum256([]byte(content))
	return version + " " + hex.EncodeToString(sum[:]) + " " + name
}

var (
	line9   = recordLine("9", "0009_early.up.sql", contentGuardFiles["0009_early.up.sql"])
	line191 = recordLine("191", "0191_previous.up.sql", contentGuardFiles["0191_previous.up.sql"])
	line192 = recordLine("192", "0192_latest.up.sql", contentGuardFiles["0192_latest.up.sql"])
	// other192 is the record another worktree leaves when it applied a
	// different migration at 192.
	other192 = recordLine("192", "0192_other.up.sql", "CREATE TABLE other ();\n")
)

// contentGuardRun is one invocation of the guard, optionally followed
// by the recorder, against the stub.
type contentGuardRun struct {
	row        string
	oid        string
	recordOID  string
	record     []string
	fail       string
	override   bool
	thenRecord bool // run record_migration_manifest after the guard
}

// contentGuardResult is what a run produced.
type contentGuardResult struct {
	out     string
	passed  bool
	err     error
	applied string
	deleted bool
	calls   []string
}

func runContentGuard(t *testing.T, run contentGuardRun) contentGuardResult {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash not on PATH: %v", err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	mig := filepath.Join(dir, "repo", "migrations")
	for _, d := range []string{bin, mig} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range contentGuardFiles {
		if err := os.WriteFile(filepath.Join(mig, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(contentGuardStub), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "set -euo pipefail\n" +
		"log() { printf '==> %s\\n' \"$*\"; }\n" +
		"KCTX=kind-lenny-e2e\n" +
		"kc() { kubectl --context \"${KCTX}\" \"$@\"; }\n" +
		"REPO_ROOT=" + filepath.Join(dir, "repo") + "\n" +
		installMigrationGuard(t) +
		installScriptSection(t, "record_migration_manifest() {", "# end record_migration_manifest") +
		"\nmigration_guard\necho GUARD-PASSED\n"
	if run.thenRecord {
		script += "record_migration_manifest\necho RECORDED\n"
	}
	calls := filepath.Join(dir, "calls")
	applied := filepath.Join(dir, "applied")
	deleted := filepath.Join(dir, "deleted")
	env := []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"STUB_LOG=" + calls,
		"STUB_APPLIED=" + applied,
		"STUB_DELETED=" + deleted,
		"STUB_ROW=" + run.row,
		"STUB_OID=" + run.oid,
		"STUB_RECORD_OID=" + run.recordOID,
		"STUB_RECORD=" + strings.Join(run.record, "\n"),
		"STUB_FAIL=" + run.fail,
	}
	if run.override {
		env = append(env, "LENNY_SKIP_MIGRATION_GUARD=1")
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(filteredEnv("LENNY_SKIP_MIGRATION_GUARD"), env...)
	out, err := cmd.CombinedOutput()
	res := contentGuardResult{out: string(out), err: err, passed: strings.Contains(string(out), "GUARD-PASSED")}
	if b, rerr := os.ReadFile(applied); rerr == nil {
		res.applied = string(b)
	}
	if _, serr := os.Stat(deleted); serr == nil {
		res.deleted = true
	}
	if b, rerr := os.ReadFile(calls); rerr == nil {
		res.calls = strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	return res
}

// TestInstallMigrationGuardRefusesASameNumberContentMismatch runs the
// guard against a recorded manifest. A recorded migration at or below the
// database version whose file name or sha256 differs from this branch's
// file of the same number is refused, because golang-migrate records the
// version alone and `lenny-migrate up` would skip this branch's file
// (T-17.4.31). Entries above the database version, a record of another
// database, and a missing record do not refuse. A record or OID that
// cannot be read refuses, and LENNY_SKIP_MIGRATION_GUARD=1 overrides each
// refusal. Every database read stays read-only.
//
// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
func TestInstallMigrationGuardRefusesASameNumberContentMismatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		run     contentGuardRun
		wantOK  bool
		wantMsg []string
	}{
		{name: "record matches the branch", run: contentGuardRun{
			row: "192|f", oid: "16384", recordOID: "16384", record: []string{line9, line191, line192},
		}, wantOK: true, wantMsg: []string{"database at migration 192, branch highest 192"}},
		{name: "same number, different content at the database version", run: contentGuardRun{
			row: "192|f", oid: "16384", recordOID: "16384", record: []string{line9, line191, other192},
		}, wantOK: false, wantMsg: []string{
			"migration 192 was applied from 0192_other.up.sql",
			"this branch carries 0192_latest.up.sql",
			"Renumber this branch's migration",
			"Shared Kind cluster database",
			"LENNY_SKIP_MIGRATION_GUARD=1 overrides the check",
		}},
		{name: "same name, different content", run: contentGuardRun{
			row: "192|f", oid: "16384", recordOID: "16384",
			record: []string{recordLine("192", "0192_latest.up.sql", "ALTER TABLE x ADD y int;\n")},
		}, wantOK: false, wantMsg: []string{"migration 192 was applied from 0192_latest.up.sql (sha256"}},
		{name: "collision below the database version", run: contentGuardRun{
			row: "192|f", oid: "16384", recordOID: "16384",
			record: []string{recordLine("191", "0191_other.up.sql", "x"), line192},
		}, wantOK: false, wantMsg: []string{"migration 191 was applied from 0191_other.up.sql"}},
		{name: "recorded number the branch lacks", run: contentGuardRun{
			row: "192|f", oid: "16384", recordOID: "16384",
			record: []string{recordLine("150", "0150_elsewhere.up.sql", "x"), line192},
		}, wantOK: false, wantMsg: []string{"migration 150 was applied from 0150_elsewhere.up.sql, which this branch does not carry"}},
		{name: "collision above a stepped-back database", run: contentGuardRun{
			row: "191|f", oid: "16384", recordOID: "16384", record: []string{line9, line191, other192},
		}, wantOK: true, wantMsg: []string{"database at migration 191, branch highest 192"}},
		{name: "record of a recreated database is ignored", run: contentGuardRun{
			row: "192|f", oid: "20000", recordOID: "16384", record: []string{other192},
		}, wantOK: true, wantMsg: []string{"describes database OID 16384, not 20000", "migration content is unchecked"}},
		{name: "no record", run: contentGuardRun{row: "192|f", oid: "16384"}, wantOK: true, wantMsg: []string{
			"no lenny-e2e-migrations record",
		}},
		{name: "unreadable record", run: contentGuardRun{row: "192|f", oid: "16384", fail: "record"}, wantOK: false, wantMsg: []string{
			"cannot read the lenny-e2e-migrations record or the database OID",
		}},
		{name: "unreadable OID", run: contentGuardRun{
			row: "192|f", recordOID: "16384", record: []string{line192}, fail: "oid",
		}, wantOK: false, wantMsg: []string{"cannot read the lenny-e2e-migrations record or the database OID"}},
		{name: "mismatch with override", run: contentGuardRun{
			row: "192|f", oid: "16384", recordOID: "16384", record: []string{other192}, override: true,
		}, wantOK: true, wantMsg: []string{"migration 192 was applied from 0192_other.up.sql", "proceeding because LENNY_SKIP_MIGRATION_GUARD=1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runContentGuard(t, tc.run)
			if tc.wantOK && (res.err != nil || !res.passed) {
				t.Fatalf("guard refused, want proceed: %v\n%s", res.err, res.out)
			}
			if !tc.wantOK && (res.err == nil || res.passed) {
				t.Fatalf("guard proceeded, want refusal\n%s", res.out)
			}
			for _, m := range tc.wantMsg {
				if !strings.Contains(res.out, m) {
					t.Errorf("output lacks %q:\n%s", m, res.out)
				}
			}
			for _, call := range res.calls {
				if strings.Contains(call, " exec deploy/lenny-postgres ") && !strings.Contains(call, "default_transaction_read_only=on") {
					t.Errorf("kubectl call %q does not run psql read-only", call)
				}
				if strings.Contains(call, " apply ") || strings.Contains(call, " create ") || strings.Contains(call, " delete ") {
					t.Errorf("the guard wrote to the cluster: %q", call)
				}
			}
		})
	}
}

// TestInstallMigrationRecordCarriesVerifiedAndAppliedEntries runs the
// guard and then the recorder, as install.sh does after a successful
// migrate Job. The record carries the database OID, the recorded entries
// the guard verified at or below the pre-migrate version, and this
// branch's files above it. An overridden guard and a failed write delete
// the record instead of leaving one that names content the database may
// not carry. The recorder writes a ConfigMap and never the database.
//
// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
func TestInstallMigrationRecordCarriesVerifiedAndAppliedEntries(t *testing.T) {
	for _, tc := range []struct {
		name        string
		run         contentGuardRun
		wantLines   []string
		wantAbsent  []string
		wantDeleted bool
	}{
		{name: "upgrade from a recorded database", run: contentGuardRun{
			row: "191|f", oid: "16384", recordOID: "16384", record: []string{line9, line191},
		}, wantLines: []string{"--from-literal=databaseOID=16384", line9, line191, line192}},
		{name: "a stepped-back database drops the entry above it", run: contentGuardRun{
			row: "191|f", oid: "16384", recordOID: "16384", record: []string{line9, line191, other192},
		}, wantLines: []string{line9, line191, line192}, wantAbsent: []string{other192}},
		{name: "unrecorded database records only what this install applied", run: contentGuardRun{
			row: "191|f", oid: "16384",
		}, wantLines: []string{line192}, wantAbsent: []string{line9, line191}},
		{name: "record of a recreated database is replaced", run: contentGuardRun{
			row: "191|f", oid: "20000", recordOID: "16384", record: []string{line9, line191},
		}, wantLines: []string{"--from-literal=databaseOID=20000", line192}, wantAbsent: []string{line9, line191}},
		{name: "override deletes the record", run: contentGuardRun{
			row: "192|f", oid: "16384", recordOID: "16384", record: []string{other192}, override: true,
		}, wantDeleted: true},
		{name: "a failed write deletes the record", run: contentGuardRun{
			row: "191|f", oid: "16384", recordOID: "16384", record: []string{line9, line191}, fail: "apply",
		}, wantDeleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.run.thenRecord = true
			res := runContentGuard(t, tc.run)
			if res.err != nil || !strings.Contains(res.out, "RECORDED") {
				t.Fatalf("guard or recorder failed: %v\n%s", res.err, res.out)
			}
			if res.deleted != tc.wantDeleted {
				t.Errorf("record deleted = %v, want %v\n%s", res.deleted, tc.wantDeleted, res.out)
			}
			if tc.wantDeleted {
				return
			}
			for _, l := range tc.wantLines {
				if !strings.Contains(res.applied, l) {
					t.Errorf("record lacks %q:\n%s", l, res.applied)
				}
			}
			for _, l := range tc.wantAbsent {
				if strings.Contains(res.applied, l) {
					t.Errorf("record carries %q:\n%s", l, res.applied)
				}
			}
			for _, call := range res.calls {
				if strings.Contains(call, " exec deploy/lenny-postgres ") && !strings.Contains(call, "default_transaction_read_only=on") {
					t.Errorf("kubectl call %q does not run psql read-only", call)
				}
			}
		})
	}
}

// TestInstallRecordsTheMigrationManifestAfterTheMigrateJob pins the
// recorder's position: install.sh records the manifest only after the
// migrate Job completed, so a failed migrate leaves the previous record.
//
// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
func TestInstallRecordsTheMigrationManifestAfterTheMigrateJob(t *testing.T) {
	b, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	s := string(b)
	done := strings.Index(s, `log "schema migration completed"`)
	call := strings.Index(s, "\nrecord_migration_manifest\n")
	if done < 0 || call < 0 || call < done {
		t.Fatalf("record_migration_manifest call at %d must follow the migrate Job's completion at %d", call, done)
	}
}
