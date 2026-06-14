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
	// phase3NotRequiredRe matches the author's machine-readable assertion
	// that a DROP COLUMN is not a Phase 3 contract drop because the table
	// carried no rows under any prior release (an empty pre-deployment table
	// reshaped within the same unreleased line). §10.5's preflight gate
	// exists for "every record that could have been written under the old
	// schema"; a table that has never held data in any deployment has no such
	// record, so the un-migrated-rows gate has no premise. The marker is
	// required to opt out: an unmarked DROP COLUMN still trips the guard, so
	// the default remains fail-closed.
	phase3NotRequiredRe = regexp.MustCompile(`(?i)phase3:\s*not-required`)
)

// phase3Violations mirrors scripts/lint-migrations.sh Pass 5: a Phase 3
// migration (an up-migration that DROPs a column from a table that held data
// under a prior release) must drop idempotently and front the DDL with a
// PL/pgSQL DO $$ preflight gate. The scan keeps line comments only for the
// gate-index and phase3 markers; DROP/DO detection runs against the
// comment-stripped body so a commented-out DROP does not count.
//
// A migration may declare its DROP COLUMN out of scope with a
// `-- phase3: not-required` marker when the table is empty pre-deployment
// (reshaped within the same unreleased line, with no rows in any
// deployment). §10.5's gate counts un-migrated rows; a table that never held
// data has none, so the gate's premise does not apply. The marker is
// required to opt out, so an author who omits both the gate and the marker
// still trips the guard (fail-closed default).
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
	// An empty pre-deployment table reshape opts its DROP COLUMN out of the
	// Phase 3 contract discipline with the `-- phase3: not-required` marker.
	// The marker lives in a comment, so it is read from the raw source.
	if phase3NotRequiredRe.MatchString(src) {
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
// preflight gate. Migration 0167 (the §5.2 mode collapse) drops
// sandbox_warm_pools.concurrency_style behind a DO $$ gate keyed on the
// out-of-vocabulary value set, so this guard exercises a live Phase 3 file
// and keeps later DROP COLUMN authors from shipping a contract migration
// that re-runs unsafely or skips the un-migrated-rows gate. A migration that
// reshapes an empty pre-deployment table (0118, 0129) drops columns that
// never held data in any deployment; it declares itself out of scope with a
// `-- phase3: not-required` marker and is exempt, because §10.5's gate has no
// un-migrated rows to count there.
//
// diagnosis: a failure means a Phase 3 contract migration (one that DROPs a
// column from a table that held data under a prior release and does not carry
// the `-- phase3: not-required` empty-table marker) either drops without IF
// EXISTS or omits the DO $$ preflight gate, so a re-run would fail or the
// §10.5 un-migrated-rows guard is absent and the contract could run before
// its data migration.
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
		// An empty pre-deployment table reshape opts out of the contract
		// discipline with the marker; an ungated bare DROP is then exempt.
		{"empty-table marker exempts bare drop", "-- phase3: not-required (table empty pre-deployment)\nALTER TABLE ops_event_subscriptions DROP COLUMN secret, DROP COLUMN types;", false, false, false, false},
		// The marker only matters when a DROP COLUMN is present; on a
		// non-dropping migration it is irrelevant and the scan still reports
		// "not phase 3".
		{"marker without a drop is still not phase 3", "-- phase3: not-required\nALTER TABLE sessions ADD COLUMN note TEXT;", false, false, false, false},
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
