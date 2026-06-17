// SPDX-License-Identifier: MIT

// This file carries no build tag (like nullable_columns_test.go) so it
// runs in the default tier-1 build: a pure static scan of the production
// migration SQL that needs no Postgres container.
package migrations_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dropColumnRe and dropColumnIfExistsRe count the DROP COLUMN clauses in a
// normalized statement so a bare (non-idempotent) drop is distinguished
// from a guarded `DROP COLUMN IF EXISTS`.
var (
	dropColumnRe         = regexp.MustCompile(`(?i)drop\s+column`)
	dropColumnIfExistsRe = regexp.MustCompile(`(?i)drop\s+column\s+if\s+exists`)
	doBlockRe            = regexp.MustCompile(`(?i)do\s+\$\$`)
	gateIndexRe          = regexp.MustCompile(`(?i)gate-index:`)
)

// phase3Violations mirrors scripts/lint-migrations.sh Pass 5: a Phase 3
// migration (an up-migration that DROPs a column) must drop idempotently and
// front the DDL with a PL/pgSQL DO $$ preflight gate. The scan keeps line
// comments only for the gate-index marker; DROP/DO detection runs against the
// comment-stripped body so a commented-out DROP does not count.
//
// The gate requirement is unconditional: §10.5 line 417 mandates the DO $$
// preflight block at the top of every Phase 3 up-migration regardless of the
// expected row count. A migration that omits the gate trips the guard.
//
// spec: §10.5 line 417 (DO $$ preflight gate) + line 430 (DROP COLUMN IF
// EXISTS idempotency).
type phase3Violations struct {
	isPhase3       bool
	bareDrop       bool // a DROP COLUMN without IF EXISTS
	missingGate    bool // no DO $$ block fronts the drop
	missingGateIdx bool // no -- gate-index: comment (advisory only)
}

func scanPhase3(src string) phase3Violations {
	var body strings.Builder
	for _, line := range strings.Split(src, "\n") {
		body.WriteString(lineComment.ReplaceAllString(line, ""))
		body.WriteByte(' ')
	}
	stripped := body.String()
	total := len(dropColumnRe.FindAllString(stripped, -1))
	if total == 0 {
		return phase3Violations{}
	}
	guarded := len(dropColumnIfExistsRe.FindAllString(stripped, -1))
	return phase3Violations{
		isPhase3:       true,
		bareDrop:       total > guarded,
		missingGate:    !doBlockRe.MatchString(stripped),
		missingGateIdx: !gateIndexRe.MatchString(src), // gate-index lives in a comment
	}
}

// TestPhase3MigrationsAreGuarded_spec_10_5_417 asserts that every Phase 3
// contract migration in the tree drops columns idempotently and carries the
// preflight gate, keeping later DROP COLUMN authors from shipping a contract
// migration that re-runs unsafely or skips the un-migrated-rows gate. The tree
// has three Phase 3 (DROP COLUMN) migrations, each fronted by a DO $$ gate that
// counts un-migrated rows: 0118 (ops_event_subscriptions secret/types reshape),
// 0129 (credential_leases lease_token envelope conversion), and the §5.2
// mode-collapse 0167 (sandbox_warm_pools.concurrency_style). §10.5 line 417's
// gate requirement is unconditional: it applies to every migration that DROPs a
// column regardless of the expected row count.
//
// diagnosis: a failure means a Phase 3 contract migration (one that DROPs a
// column) either drops without IF EXISTS or omits the DO $$ preflight gate, so
// a re-run would fail or the §10.5 un-migrated-rows guard is absent and the
// contract could run before its data migration.
//
// spec: §10.5 line 417 / line 430.
func TestPhase3MigrationsAreGuarded_spec_10_5_417(t *testing.T) {
	t.Parallel()
	migrationsDir := filepath.Join(repoRootFromCaller(t), "migrations")
	ups, err := filepath.Glob(filepath.Join(migrationsDir, "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(ups) == 0 {
		t.Fatalf("no up-migrations found under %s", migrationsDir)
	}
	for _, up := range ups {
		b, err := os.ReadFile(up)
		if err != nil {
			t.Fatalf("read %s: %v", up, err)
		}
		v := scanPhase3(string(b))
		if !v.isPhase3 {
			continue
		}
		if v.bareDrop {
			t.Errorf("%s drops a column without IF EXISTS (§10.5 line 430: Phase 3 DROP COLUMN must be idempotent)", filepath.Base(up))
		}
		if v.missingGate {
			t.Errorf("%s is a Phase 3 migration with no DO $$ preflight gate (§10.5 line 417: front the DROP with a DO block that RAISE EXCEPTIONs on un-migrated rows)", filepath.Base(up))
		}
	}
}

// TestScanPhase3_spec_10_5_417 exercises the scanner against the boundary
// cases the §10.5 Phase 3 rules turn on.
//
// spec: §10.5 lines 417, 430.
// diagnosis: a failure means the Phase 3 scanner misclassifies a
// boundary case, so the CI gate would miss a bare DROP COLUMN or a
// Phase 3 migration with no preflight gate, or falsely flag a safe one.
func TestScanPhase3_spec_10_5_417(t *testing.T) {
	t.Parallel()
	const goodGate = `-- gate-index: idx_sessions_legacy_token_partial
DO $$
DECLARE remaining bigint;
BEGIN
  SELECT COUNT(*) INTO remaining FROM sessions WHERE legacy_token IS NOT NULL;
  IF remaining > 0 THEN
    RAISE EXCEPTION 'Phase 3 gate failed: % rows remain', remaining;
  END IF;
END $$;
ALTER TABLE sessions DROP COLUMN IF EXISTS legacy_token;`

	cases := []struct {
		name                                      string
		sql                                       string
		isPhase3, bareDrop, missGate, missGateIdx bool
	}{
		{"not a phase 3 migration", `ALTER TABLE sessions ADD COLUMN note TEXT;`, false, false, false, false},
		{"fully guarded phase 3", goodGate, true, false, false, false},
		{"bare drop no gate", `ALTER TABLE sessions DROP COLUMN legacy_token;`, true, true, true, true},
		{"idempotent drop but no gate", `ALTER TABLE sessions DROP COLUMN IF EXISTS legacy_token;`, true, false, true, true},
		{"gated but non-idempotent drop", "DO $$ BEGIN END $$;\nALTER TABLE sessions DROP COLUMN legacy_token;", true, true, false, true},
		{"commented drop is not phase 3", "-- ALTER TABLE sessions DROP COLUMN legacy_token;\nALTER TABLE sessions ADD COLUMN note TEXT;", false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := scanPhase3(tc.sql)
			if v.isPhase3 != tc.isPhase3 || v.bareDrop != tc.bareDrop || v.missingGate != tc.missGate || v.missingGateIdx != tc.missGateIdx {
				t.Errorf("scanPhase3(%q) = %+v, want {isPhase3:%v bareDrop:%v missingGate:%v missingGateIdx:%v}",
					tc.sql, v, tc.isPhase3, tc.bareDrop, tc.missGate, tc.missGateIdx)
			}
		})
	}
}
