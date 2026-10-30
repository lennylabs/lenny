// SPDX-License-Identifier: MIT

package kind

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// installMigrationGuard extracts the migration guard of install.sh, its
// globals and helpers included, from the begin marker through the end
// marker.
func installMigrationGuard(t *testing.T) string {
	t.Helper()
	return installScriptSection(t, "# begin migration_guard", "# end migration_guard")
}

// installScriptSection returns the part of install.sh from the line
// holding the begin marker up to the end marker.
func installScriptSection(t *testing.T, begin, end string) string {
	t.Helper()
	b, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	s := string(b)
	start := strings.Index(s, begin)
	stop := strings.Index(s, end)
	if start < 0 || stop < 0 || stop < start {
		t.Fatalf("install.sh: section %q..%q not found (start=%d end=%d)", begin, end, start, stop)
	}
	return s[start:stop]
}

// TestInstallMigrationGuardCallsPrecedeTheMigrateJob pins the guard's
// position: install.sh calls it after the data stores are Available and
// before it applies the lenny-e2e-migrate Job (T-17.4.29).
//
// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
func TestInstallMigrationGuardCallsPrecedeTheMigrateJob(t *testing.T) {
	b, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	s := string(b)
	wait := strings.Index(s, `for deploy in lenny-postgres lenny-redis lenny-minio; do`)
	call := strings.Index(s, "\nmigration_guard\n")
	job := strings.Index(s, "kc -n lenny-system delete job lenny-e2e-migrate")
	if wait < 0 || call < 0 || job < 0 {
		t.Fatalf("install.sh: wait=%d call=%d job=%d; a landmark is missing", wait, call, job)
	}
	if !(wait < call && call < job) {
		t.Fatalf("migration_guard call at %d must sit between the data-store wait (%d) and the migrate Job (%d)", call, wait, job)
	}
}

// TestInstallMigrationGuardRefusesADirtyOrAheadDatabase runs the
// install script's migration guard under bash against a stub kubectl
// that answers the guard's two psql reads, and a migrations directory
// whose highest migration is 0192. The guard proceeds on a fresh, empty,
// equal, or behind database and refuses a dirty or ahead one unless
// LENNY_SKIP_MIGRATION_GUARD=1. Every read the guard issues runs in a
// read-only transaction (T-17.4.29).
//
// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
func TestInstallMigrationGuardRefusesADirtyOrAheadDatabase(t *testing.T) {
	// bash is a hard requirement: install.sh is a bash script.
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash not on PATH: %v", err)
	}
	guard := installMigrationGuard(t)

	// The stub answers the existence probe with $STUB_EXISTS and the row
	// read with $STUB_ROW, and fails either read when $STUB_FAIL names it.
	stub := `#!/usr/bin/env bash
echo "$*" >> "${STUB_LOG}"
case "$*" in
  *to_regclass*)
    [[ "${STUB_FAIL:-}" == "exists" ]] && { echo "stub kubectl: exec failed" >&2; exit 1; }
    printf '%s\n' "${STUB_EXISTS}" ;;
  *"SELECT version, dirty FROM schema_migrations"*)
    [[ "${STUB_FAIL:-}" == "row" ]] && { echo "stub kubectl: exec failed" >&2; exit 1; }
    if [[ -n "${STUB_ROW}" ]]; then printf "%s\n" "${STUB_ROW}"; fi ;;
  *"get configmap lenny-e2e-migrations --ignore-not-found"*) ;;
  *) echo "stub kubectl: unexpected $*" >&2; exit 3 ;;
esac
`
	for _, tc := range []struct {
		name     string
		exists   string
		row      string
		fail     string
		override bool
		wantOK   bool
		wantMsg  []string
	}{
		{name: "fresh database", exists: "f", wantOK: true, wantMsg: []string{"database is fresh"}},
		{name: "empty table", exists: "t", row: "", wantOK: true, wantMsg: []string{"schema_migrations is empty"}},
		{name: "clean and equal", exists: "t", row: "192|f", wantOK: true, wantMsg: []string{"database at migration 192, branch highest 192"}},
		{name: "clean and behind", exists: "t", row: "190|f", wantOK: true, wantMsg: []string{"database at migration 190, branch highest 192"}},
		{name: "behind with leading zero padding in version", exists: "t", row: "0099|f", wantOK: true},
		{name: "clean and ahead", exists: "t", row: "193|f", wantOK: false, wantMsg: []string{
			"at migration 193, ahead of this branch's highest migration 192",
			"merge the integration branch",
			"lenny-migrate --break-glass --confirm goto 192",
			"Shared Kind cluster database",
			"LENNY_SKIP_MIGRATION_GUARD=1",
		}},
		{name: "dirty and equal", exists: "t", row: "192|t", wantOK: false, wantMsg: []string{
			"dirty at migration 192", "reset procedure", "Shared Kind cluster database", "Do not edit schema_migrations by hand",
		}},
		{name: "dirty and behind", exists: "t", row: "150|t", wantOK: false, wantMsg: []string{"dirty at migration 150"}},
		{name: "dirty and ahead reports dirty", exists: "t", row: "200|t", wantOK: false, wantMsg: []string{"dirty at migration 200"}},
		{name: "ahead with override", exists: "t", row: "193|f", override: true, wantOK: true, wantMsg: []string{
			"ahead of this branch's highest migration 192", "proceeding because LENNY_SKIP_MIGRATION_GUARD=1",
		}},
		{name: "dirty with override", exists: "t", row: "192|t", override: true, wantOK: true, wantMsg: []string{
			"dirty at migration 192", "proceeding because LENNY_SKIP_MIGRATION_GUARD=1",
		}},
		{name: "unreadable database", fail: "exists", wantOK: false, wantMsg: []string{"cannot read schema_migrations"}},
		{name: "unreadable database with override", fail: "exists", override: true, wantOK: true, wantMsg: []string{"proceeding because LENNY_SKIP_MIGRATION_GUARD=1"}},
		{name: "unreadable row", exists: "t", fail: "row", wantOK: false, wantMsg: []string{"cannot read the schema_migrations row"}},
		{name: "unreadable row with override", exists: "t", fail: "row", override: true, wantOK: true, wantMsg: []string{"proceeding because LENNY_SKIP_MIGRATION_GUARD=1"}},
		{name: "malformed row", exists: "t", row: "garbage", wantOK: false, wantMsg: []string{"unexpected schema_migrations row"}},
		{name: "malformed dirty column", exists: "t", row: "192|x", wantOK: false, wantMsg: []string{"unexpected schema_migrations row '192|x'"}},
		{name: "malformed row with override", exists: "t", row: "garbage", override: true, wantOK: true, wantMsg: []string{"proceeding because LENNY_SKIP_MIGRATION_GUARD=1"}},
		{name: "refusal names the override", exists: "t", fail: "row", wantOK: false, wantMsg: []string{"the install stopped before migrating", "LENNY_SKIP_MIGRATION_GUARD=1 overrides the check"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			mig := filepath.Join(dir, "repo", "migrations")
			for _, d := range []string{bin, mig} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, f := range []string{
				"0009_early.up.sql", "0009_early.down.sql",
				"0191_previous.up.sql", "0191_previous.down.sql",
				"0192_latest.up.sql", "0192_latest.down.sql",
				"embed.go", "0193_not_an_up_file.down.sql",
			} {
				if err := os.WriteFile(filepath.Join(mig, f), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			calls := filepath.Join(dir, "calls")
			script := "set -euo pipefail\n" +
				"log() { printf '==> %s\\n' \"$*\"; }\n" +
				"KCTX=kind-lenny-e2e\n" +
				"kc() { kubectl --context \"${KCTX}\" \"$@\"; }\n" +
				"REPO_ROOT=" + filepath.Join(dir, "repo") + "\n" +
				guard + "\nmigration_guard\necho GUARD-PASSED\n"
			env := []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"STUB_LOG=" + calls,
				"STUB_EXISTS=" + tc.exists,
				"STUB_ROW=" + tc.row,
				"STUB_FAIL=" + tc.fail,
			}
			if tc.override {
				env = append(env, "LENNY_SKIP_MIGRATION_GUARD=1")
			}
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(filteredEnv("LENNY_SKIP_MIGRATION_GUARD"), env...)
			out, err := cmd.CombinedOutput()
			passed := strings.Contains(string(out), "GUARD-PASSED")
			if tc.wantOK && (err != nil || !passed) {
				t.Fatalf("guard refused, want proceed: %v\n%s", err, out)
			}
			if !tc.wantOK && (err == nil || passed) {
				t.Fatalf("guard proceeded, want refusal\n%s", out)
			}
			for _, m := range tc.wantMsg {
				if !strings.Contains(string(out), m) {
					t.Errorf("output lacks %q:\n%s", m, out)
				}
			}
			log, err := os.ReadFile(calls)
			if err != nil {
				t.Fatalf("the guard issued no kubectl call: %v", err)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
				// The content check reads its record from a ConfigMap, a
				// read outside the database.
				if strings.HasPrefix(line, "--context kind-lenny-e2e -n lenny-system get configmap lenny-e2e-migrations ") {
					continue
				}
				if !strings.HasPrefix(line, "--context kind-lenny-e2e -n lenny-system exec deploy/lenny-postgres -- sh -c ") {
					t.Errorf("unexpected kubectl call %q", line)
				}
				if !strings.Contains(line, "default_transaction_read_only=on") {
					t.Errorf("kubectl call %q does not run psql read-only", line)
				}
			}
		})
	}
}

// filteredEnv returns the process environment without the named keys,
// so a caller's own override setting does not leak into a case.
func filteredEnv(keys ...string) []string {
	var out []string
	for _, kv := range os.Environ() {
		drop := false
		for _, k := range keys {
			if strings.HasPrefix(kv, k+"=") {
				drop = true
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}
