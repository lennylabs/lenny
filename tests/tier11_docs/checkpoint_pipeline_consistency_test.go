// SPDX-License-Identifier: MIT

// Tier-11 spec/code-consistency checks for the gateway-driven checkpoint
// upload pipeline. These pin the agreements the pipeline depends on across the
// spec, the migration, and the Go emitters, so a later edit to one site cannot
// silently drift from the others:
//
//   - the §10.1 checkpoint_manifest column set matches migration 0178;
//   - the closed §10.1 manifest_reason enum, the §16.1
//     lenny_checkpoint_partial_total label domains (including recovered), and
//     the write-path and resume-path emitters all agree, with no site naming
//     the removed terminated_during_resume value and every emitted trigger a
//     member of checkpoint.AllTriggers();
//   - the §12.5 backstop sweep predicate reads identically at its two spec/12
//     occurrences (the backstop bullet and GC concurrency rule 6);
//   - the storage-counter rehydrate reservation-folding term reads identically
//     at its §11.2 occurrences and at §12.4;
//   - the reader-facing docs mirrors of the §13.1 Pod Security table
//     (architecture.md, security.md) and the concepts.md file-delivery prose
//     agree with the amended spec rows, so no docs page still asserts that
//     agent pods have no object-store path.
//
// The checks read repository state directly (no build tag, no infrastructure),
// the same posture as the other tier-11 doc checks, plus two lightweight enum
// packages so the code side of each agreement is the real symbol rather than a
// re-typed literal.
//
// spec: 10.1 (partial-manifest column set and manifest_reason enum), 11.2
// (storage-counter rehydrate), 12.5 (backstop sweep predicate), 13.1 (docs
// mirrors of the pod-to-object-store checkpoint path), 16.1
// (lenny_checkpoint_partial_total label domains).

package tier11_docs_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
)

// domainColumns are the checkpoint_manifest columns migration 0178 creates
// that the §10.1 line-141 manifest enumeration also names. tenant_id (the RLS
// tenant key) and created_at (a standard audit column) are infrastructure
// columns the §10.1 prose does not enumerate, so they are excluded from the
// bidirectional agreement.
var infraColumns = map[string]bool{
	"tenant_id":  true,
	"created_at": true,
}

// droppedColumn records a checkpoint_manifest column that migration 0178
// creates and a later migration drops. The record lets a reader of the
// exception resolve which change removed the column, by its migration number.
type droppedColumn struct {
	// dropMigration is the four-digit numeric prefix of the migration that
	// drops the column. It is always set: a record that names no migration
	// does not resolve to a change. While that migration is still unwritten
	// the record names the number allocated for it; once the tree carries a
	// migration with that prefix, that migration must drop the column, and no
	// other migration may drop it first.
	dropMigration string
	// reason states why the column is gone and names dropMigration, so the
	// record reads as a retirement record on its own.
	reason string
}

// droppedColumns are checkpoint_manifest columns migration 0178 creates and a
// later migration drops. Migration 0178's own `.up.sql` text keeps the CREATE
// TABLE line that first declared them, so they stay in the extracted set,
// while §10.1 no longer names them. Each entry names the migration that drops
// the column, the form tests/tier2_component/migrations/prod_columns_test.go
// already uses for the columns the prod chain retires: its 0040 entry records
// that migration 0167 drops sandbox_warm_pools.concurrency_style.
//
// The set drains rather than accumulates. Its two conditions are the two
// droppedColumnDrainError states: an entry naming a column migration 0178 does
// not create is dead, and an entry for a column §10.1 names again is a live
// column the agreement must cover. Both are read off the two sources the
// agreement itself reads, so a drop migration that has not landed yet cannot
// make the gate red before the change that writes it. droppedColumnRecordError
// holds the named number to the tree: the migration with that prefix, once it
// exists, must be the one that drops the column.
var droppedColumns = map[string]droppedColumn{
	// spec: §10.1 (the partial manifest, the supersede rule, and the
	// reassembly predicate are keyed on session_id alone), §12.5 (retention
	// and supersession operate on session_id)
	"slot_id": {
		dropMigration: "0180",
		reason:        "the manifest is scoped on session_id alone, so migration 0180 drops checkpoint_manifest.slot_id together with session_checkpoints.slot_id and re-keys the three indexes on session_id",
	},
}

// droppedColumnDrainError reports why a droppedColumns entry no longer stands,
// or nil while it does. An exception is suppressing the column agreement
// above, so it is held to exactly two conditions: the column is one migration
// 0178 creates, and §10.1 names it nowhere. Both are read off the two sources
// the agreement itself reads, so the entry is never coupled to a migration
// number that has not been allocated.
//
// spec: §10.1 (the manifest column enumeration)
func droppedColumnDrainError(col, reason string, createdByMigration, namedInSpec bool) error {
	if !createdByMigration {
		return fmt.Errorf("droppedColumns names %q (%s), which migration 0178 does not create", col, reason)
	}
	if namedInSpec {
		return fmt.Errorf("§10.1 names %q again, so it is a live manifest column: remove its droppedColumns entry (%s) and hold it to the agreement above", col, reason)
	}
	return nil
}

// migrationColumnRE matches a column definition line in migration 0178, e.g.
// `    manifest_reason                TEXT        NOT NULL DEFAULT 'in_progress',`.
var migrationColumnRE = regexp.MustCompile(`^\s+([a-z_]+)\s+(TEXT|UUID|BIGINT|INTEGER|BOOLEAN|TIMESTAMPTZ)\b`)

// spec: 10.1
// diagnosis: the §10.1 partial-manifest column enumeration and migration 0178
//
//	disagree on the checkpoint_manifest column set. §10.1.7 enumerates
//	the manifest columns in prose; migration 0178 CREATEs the table. A failure
//	here means one side added, renamed, or dropped a domain column the other
//	did not — for example the migration grows a column §10.1 never names, or
//	§10.1 promises a field the table has no column for — leaving the schema and
//	its normative description out of sync.
func TestCheckpointManifestColumnSetMatchesMigration0178(t *testing.T) {
	root := repoRoot(t)

	migration, err := os.ReadFile(filepath.Join(root, "migrations", "0178_checkpoint_manifest.up.sql"))
	if err != nil {
		t.Fatalf("read migration 0178: %v", err)
	}
	// Scope to the CREATE TABLE checkpoint_manifest body so a column named in a
	// DROP or index statement is not mistaken for a table column.
	body := string(migration)
	start := strings.Index(body, "CREATE TABLE checkpoint_manifest")
	if start < 0 {
		t.Fatal("migration 0178 has no CREATE TABLE checkpoint_manifest")
	}
	createBody := body[start:]
	if end := strings.Index(createBody, "\n);"); end >= 0 {
		createBody = createBody[:end]
	}

	var migrationCols []string
	for _, line := range strings.Split(createBody, "\n") {
		m := migrationColumnRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		migrationCols = append(migrationCols, m[1])
	}
	if len(migrationCols) == 0 {
		t.Fatal("extracted no columns from migration 0178 CREATE TABLE checkpoint_manifest (regex drift?)")
	}

	s101 := specSection(t, filepath.Join(root, "spec", "10_gateway-internals.md"), "### 10.1 ")

	for _, col := range migrationCols {
		if infraColumns[col] {
			continue
		}
		if _, dropped := droppedColumns[col]; dropped {
			continue
		}
		// §10.1 names some columns bare (`chunk_count`) and some with a
		// trailing type or value inside the code span (`reservation_released_at
		// TIMESTAMPTZ NULL`, `partial: true`), so accept the column name opening
		// a code span and closed by a backtick, space, or colon.
		if !columnNamedInCodeSpan(s101, col) {
			t.Errorf("migration 0178 column %q is not named in §10.1; the manifest column set and its normative enumeration must agree", col)
		}
	}

	// The exception list drains rather than accumulates. An entry naming a
	// column migration 0178 never creates is dead, and one §10.1 names again
	// is a live column the agreement above must cover.
	migrationSet := make(map[string]bool, len(migrationCols))
	for _, col := range migrationCols {
		migrationSet[col] = true
	}
	for col, entry := range droppedColumns {
		if err := droppedColumnDrainError(col, entry.reason, migrationSet[col], columnNamedInCodeSpan(s101, col)); err != nil {
			t.Error(err)
		}
	}
}

// spec: 10.1, 12.5
// diagnosis: the dropped-column exception's drain conditions have changed. The
//
//	§10.1 column-agreement check is suppressed for a column only on the
//	strength of a droppedColumns entry, so the entry is held to exactly two
//	conditions: the column is one migration 0178 creates, and §10.1 names it
//	nowhere. A failure here means one of those two stopped draining the entry,
//	or that a third condition was added and the exception now depends on
//	something other than the two sources the agreement itself reads.
func TestDroppedColumnExceptionDrainsOnItsTwoConditions(t *testing.T) {
	const (
		col    = "slot_id"
		reason = "the manifest is scoped on session_id alone"
	)
	cases := []struct {
		name         string
		createdBy178 bool
		namedInSpec  bool
		wantErr      bool
	}{
		{
			name:         "a column 0178 creates and §10.1 does not name is the exception standing",
			createdBy178: true,
		},
		{
			name:        "a column 0178 does not create is a dead entry",
			namedInSpec: false,
			wantErr:     true,
		},
		{
			name:         "a column §10.1 names again is a live column the agreement must cover",
			createdBy178: true,
			namedInSpec:  true,
			wantErr:      true,
		},
		{
			name:        "a column 0178 does not create and §10.1 names is still a dead entry",
			namedInSpec: true,
			wantErr:     true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := droppedColumnDrainError(col, reason, tc.createdBy178, tc.namedInSpec)
			if tc.wantErr && err == nil {
				t.Errorf("droppedColumnDrainError(%q, _, %v, %v) = nil, want an error", col, tc.createdBy178, tc.namedInSpec)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("droppedColumnDrainError(%q, _, %v, %v) = %v, want nil", col, tc.createdBy178, tc.namedInSpec, err)
			}
		})
	}
}

// manifestTable is the table the dropped-column exceptions above suppress the
// §10.1 column agreement for.
const manifestTable = "checkpoint_manifest"

// migrationTree is the set of migrations the record check resolves a
// retirement record against. It is injected so the check can be exercised
// against a synthetic tree as well as against migrations/.
type migrationTree struct {
	// byPrefix resolves a migration's up-SQL text by its four-digit numeric
	// prefix, reporting whether the tree carries a migration with that prefix.
	byPrefix func(prefix string) (body string, found bool, err error)
	// dropperOf reports the four-digit prefix of a migration whose up-SQL
	// drops col from table, and whether the tree carries one. It is what
	// catches a record whose named migration is still unwritten while some
	// other migration has already taken the drop.
	dropperOf func(table, col string) (prefix string, found bool, err error)
}

// repoMigrationTree reads migrations/*.up.sql from the repository tree rooted
// at root.
func repoMigrationTree(root string) migrationTree {
	dir := filepath.Join(root, "migrations")
	return migrationTree{
		byPrefix: func(prefix string) (string, bool, error) {
			matches, err := filepath.Glob(filepath.Join(dir, prefix+"_*.up.sql"))
			if err != nil {
				return "", false, fmt.Errorf("glob migration %s: %w", prefix, err)
			}
			if len(matches) == 0 {
				return "", false, nil
			}
			body, err := os.ReadFile(matches[0])
			if err != nil {
				return "", false, fmt.Errorf("read migration %s: %w", prefix, err)
			}
			return string(body), true, nil
		},
		dropperOf: func(table, col string) (string, bool, error) {
			matches, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
			if err != nil {
				return "", false, fmt.Errorf("glob migrations: %w", err)
			}
			sort.Strings(matches)
			for _, path := range matches {
				body, err := os.ReadFile(path)
				if err != nil {
					return "", false, fmt.Errorf("read migration %s: %w", filepath.Base(path), err)
				}
				if !migrationDropsColumn(string(body), table, col) {
					continue
				}
				prefix := filepath.Base(path)
				if len(prefix) < 4 {
					return "", false, fmt.Errorf("migration %s has no four-digit prefix", filepath.Base(path))
				}
				return prefix[:4], true, nil
			}
			return "", false, nil
		},
	}
}

// migrationDropsColumn reports whether an ALTER TABLE statement in body drops
// col from table.
func migrationDropsColumn(body, table, col string) bool {
	// The table may be written bare or schema-qualified ("public.<table>"),
	// and either spelling may be double-quoted, so a drop cannot be hidden from
	// the check by how the statement names its table.
	const qualifier = `(?:"?[a-z_]+"?\.)?"?\b`
	re := regexp.MustCompile(`(?is)ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?` + qualifier + regexp.QuoteMeta(table) +
		`"?\b[^;]*?DROP\s+COLUMN\s+(?:IF\s+EXISTS\s+)?"?` + regexp.QuoteMeta(col) + `"?\b`)
	return re.MatchString(body)
}

// droppedColumnRecordError reports why a droppedColumns entry's retirement
// record does not resolve to the change that drops the column, or nil while it
// does. The record always names a migration by number, and its reason repeats
// that number. The tree holds the number to one identity: while it carries no
// migration with that prefix the record stands, unless some other migration
// already drops the column, in which case the record names the wrong change;
// once a migration with that prefix exists, it must be the one that drops the
// column.
//
// spec: §10.1 (the manifest column enumeration)
func droppedColumnRecordError(table, col string, entry droppedColumn, tree migrationTree) error {
	if entry.reason == "" {
		return fmt.Errorf("droppedColumns[%q] states no reason", col)
	}
	if !migrationPrefixRE.MatchString(entry.dropMigration) {
		return fmt.Errorf("droppedColumns[%q].dropMigration = %q, want the four-digit prefix of the migration that drops %s.%s", col, entry.dropMigration, table, col)
	}
	if !strings.Contains(entry.reason, entry.dropMigration) {
		return fmt.Errorf("droppedColumns[%q].reason = %q, want prose naming migration %s, so the record reads as a retirement record on its own", col, entry.reason, entry.dropMigration)
	}
	body, found, err := tree.byPrefix(entry.dropMigration)
	if err != nil {
		return fmt.Errorf("resolve droppedColumns[%q].dropMigration: %w", col, err)
	}
	if found {
		if !migrationDropsColumn(body, table, col) {
			return fmt.Errorf("droppedColumns[%q] names migration %s, which drops no %s.%s: the record names the wrong change", col, entry.dropMigration, table, col)
		}
		return nil
	}
	// The named migration is still unwritten. The record stands on the number
	// allocated for it, and only while no other migration has taken the drop.
	dropper, found, err := tree.dropperOf(table, col)
	if err != nil {
		return fmt.Errorf("resolve the migration dropping %s.%s: %w", table, col, err)
	}
	if found {
		return fmt.Errorf("droppedColumns[%q] names migration %s, which the migration tree does not carry, while migration %s drops %s.%s: name the migration that drops the column", col, entry.dropMigration, dropper, table, col)
	}
	return nil
}

// migrationPrefixRE matches a migration's four-digit numeric prefix.
var migrationPrefixRE = regexp.MustCompile(`^[0-9]{4}$`)

// spec: 10.1, 12.5
// diagnosis: a dropped-column exception's retirement record no longer resolves
//
//	to the change that drops its column. The exception suppresses the §10.1
//	column agreement for a column migration 0178 still declares, so its record
//	must let a reader resolve the removal: either it names the drop in prose,
//	table-qualified, or it names a migration the tree carries that drops the
//	column. A failure here means an entry states no reason, names the drop
//	nowhere, or names a migration that is absent from the tree or drops
//	something else.
func TestDroppedColumnExceptionResolvesToTheChangeThatDropsIt(t *testing.T) {
	root := repoRoot(t)
	tree := repoMigrationTree(root)
	for col, entry := range droppedColumns {
		if err := droppedColumnRecordError(manifestTable, col, entry, tree); err != nil {
			t.Error(err)
		}
	}
}

// syntheticTree builds a migrationTree over an in-memory prefix-to-up-SQL map,
// so the record check can be exercised against a tree that does and does not
// carry the drop.
func syntheticTree(migrations map[string]string) migrationTree {
	return migrationTree{
		byPrefix: func(prefix string) (string, bool, error) {
			body, ok := migrations[prefix]
			return body, ok, nil
		},
		dropperOf: func(table, col string) (string, bool, error) {
			prefixes := make([]string, 0, len(migrations))
			for prefix := range migrations {
				prefixes = append(prefixes, prefix)
			}
			sort.Strings(prefixes)
			for _, prefix := range prefixes {
				if migrationDropsColumn(migrations[prefix], table, col) {
					return prefix, true, nil
				}
			}
			return "", false, nil
		},
	}
}

// spec: 10.1, 12.5
// diagnosis: the dropped-column record check no longer rejects a record whose
//
//	named migration is present in the tree but drops another column, or whose
//	named migration is still unwritten while a different migration already
//	drops the column. The check is what keeps the exception from asserting a
//	migration identity the tree contradicts, so a failure here means such a
//	record would now pass the gate above.
func TestDroppedColumnRecordRejectsAMigrationThatDoesNotDropTheColumn(t *testing.T) {
	tree := syntheticTree(map[string]string{
		"0181": "ALTER TABLE checkpoint_manifest DROP COLUMN IF EXISTS slot_id;",
		"0182": "ALTER TABLE checkpoint_manifest DROP COLUMN IF EXISTS chunk_count;",
	})
	cases := []struct {
		name    string
		entry   droppedColumn
		wantErr bool
	}{
		{
			name:  "a record naming a migration that drops the column stands",
			entry: droppedColumn{dropMigration: "0181", reason: "migration 0181 drops it"},
		},
		{
			name:    "a record naming no migration is rejected",
			entry:   droppedColumn{reason: "the change that re-keys persistence drops checkpoint_manifest.slot_id"},
			wantErr: true,
		},
		{
			name:    "a record naming an unwritten migration is rejected once another migration drops the column",
			entry:   droppedColumn{dropMigration: "0190", reason: "migration 0190 drops it"},
			wantErr: true,
		},
		{
			name:    "a record naming a migration that drops another column is rejected",
			entry:   droppedColumn{dropMigration: "0182", reason: "migration 0182 drops it"},
			wantErr: true,
		},
		{
			name:    "a record naming a migration its reason does not is rejected",
			entry:   droppedColumn{dropMigration: "0181", reason: "some later change drops it"},
			wantErr: true,
		},
		{
			name:    "a record whose migration prefix is not four digits is rejected",
			entry:   droppedColumn{dropMigration: "181", reason: "migration 181 drops it"},
			wantErr: true,
		},
		{
			name:    "a record with no reason is rejected",
			entry:   droppedColumn{dropMigration: "0181"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := droppedColumnRecordError(manifestTable, "slot_id", tc.entry, tree)
			if tc.wantErr && err == nil {
				t.Errorf("droppedColumnRecordError(%+v) = nil, want an error", tc.entry)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("droppedColumnRecordError(%+v) = %v, want nil", tc.entry, err)
			}
		})
	}
}

// spec: 10.1, 12.5
// diagnosis: the dropped-column record stopped standing on the migration
//
//	number allocated for a drop that is not written yet. The exception names
//	its drop migration by number from the change that re-keys the manifest on
//	session_id, before the migration writing that drop is on disk, so the check
//	must accept a named-but-unwritten migration and then hold that number to
//	the tree once a migration with the prefix exists. A failure here means
//	either the allocated number stopped being accepted, so the record falls
//	back to prose a reader cannot resolve to a change, or the number stopped
//	being checked once the migration landed.
func TestDroppedColumnRecordStandsOnTheAllocatedMigrationNumber(t *testing.T) {
	const created = "CREATE TABLE checkpoint_manifest (\n    slot_id UUID NOT NULL\n);"
	entry := droppedColumn{
		dropMigration: "0180",
		reason:        "migration 0180 drops checkpoint_manifest.slot_id",
	}

	before := syntheticTree(map[string]string{"0178": created})
	if err := droppedColumnRecordError(manifestTable, "slot_id", entry, before); err != nil {
		t.Errorf("record naming migration 0180 before it is written = %v, want nil", err)
	}

	after := syntheticTree(map[string]string{
		"0178": created,
		"0180": "ALTER TABLE checkpoint_manifest DROP COLUMN IF EXISTS slot_id;",
	})
	if err := droppedColumnRecordError(manifestTable, "slot_id", entry, after); err != nil {
		t.Errorf("record naming migration 0180 once it drops the column = %v, want nil", err)
	}

	elsewhere := syntheticTree(map[string]string{
		"0178": created,
		"0181": "ALTER TABLE public.checkpoint_manifest DROP COLUMN IF EXISTS slot_id;",
	})
	err := droppedColumnRecordError(manifestTable, "slot_id", entry, elsewhere)
	if err == nil {
		t.Fatal("record naming migration 0180 while migration 0181 drops the column = nil, want an error")
	}
	if !strings.Contains(err.Error(), "0181") {
		t.Errorf("error = %v, want it to name migration 0181, the change that drops the column", err)
	}
}

// spec: 10.1
// diagnosis: the drop-statement matcher no longer recognizes every spelling of
//
//	the ALTER TABLE that drops a column. dropperOf is the forcing function that
//	catches a retirement record naming the wrong migration, so a spelling it
//	misses (a schema-qualified or quoted table name) lets a record keep a
//	number the tree contradicts. A failure here means the matcher narrowed and
//	the forcing function can be bypassed by how the migration writes the table.
func TestMigrationDropMatcherRecognizesQualifiedTableNames(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{name: "bare table", sql: "ALTER TABLE checkpoint_manifest DROP COLUMN slot_id;", want: true},
		{name: "schema-qualified table", sql: "ALTER TABLE public.checkpoint_manifest DROP COLUMN IF EXISTS slot_id;", want: true},
		{name: "quoted table", sql: `ALTER TABLE "checkpoint_manifest" DROP COLUMN "slot_id";`, want: true},
		{name: "quoted schema-qualified table", sql: `ALTER TABLE "public"."checkpoint_manifest" DROP COLUMN slot_id;`, want: true},
		{name: "another table", sql: "ALTER TABLE session_checkpoints DROP COLUMN slot_id;", want: false},
		{name: "another column", sql: "ALTER TABLE checkpoint_manifest DROP COLUMN chunk_count;", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := migrationDropsColumn(tc.sql, manifestTable, "slot_id"); got != tc.want {
				t.Errorf("migrationDropsColumn(%q) = %v, want %v", tc.sql, got, tc.want)
			}
		})
	}
}

// columnNamedInCodeSpan reports whether col opens a markdown code span in body,
// closed by a backtick, a space (a trailing type), or a colon (a trailing
// value). It matches `col`, `col TYPE ...`, and `col: value` alike.
func columnNamedInCodeSpan(body, col string) bool {
	return regexp.MustCompile("`" + regexp.QuoteMeta(col) + "[` :]").MatchString(body)
}

// spec: 10.1, 16.1
// diagnosis: the §10.1 manifest_reason enum, the §16.1
//
//	lenny_checkpoint_partial_total label domains, and the Go emitters disagree.
//	The closed enum is in_progress, complete, timeout, stream_truncated,
//	superseded, quota_exceeded; the §16.1 partial-only manifest_reason sub-
//	domain drops the two partial = false values (in_progress, complete); the
//	recovered domain is true|false; the trigger domain is the §4.4 checkpoint
//	trigger enum. A failure here means a value drifted between the spec and the
//	partialmanifeststore / checkpoint code — for instance the removed
//	terminated_during_resume value reappeared, a partial reason is missing from
//	§16.1, or an emitted trigger is not a member of checkpoint.AllTriggers().
func TestManifestReasonAndPartialCounterDomainsAgree(t *testing.T) {
	root := repoRoot(t)

	// Code side: the closed enum from partialmanifeststore, and the partial-
	// only sub-domain the counter admits (the enum minus the two partial =
	// false values).
	fullEnum := []string{
		partialmanifeststore.ReasonInProgress,
		partialmanifeststore.ReasonComplete,
		partialmanifeststore.ReasonTimeout,
		partialmanifeststore.ReasonStreamTruncated,
		partialmanifeststore.ReasonSuperseded,
		partialmanifeststore.ReasonQuotaExceeded,
	}
	for _, r := range fullEnum {
		if !partialmanifeststore.IsValidReason(r) {
			t.Errorf("partialmanifeststore.IsValidReason rejects its own enum value %q", r)
		}
	}
	if partialmanifeststore.IsValidReason("terminated_during_resume") {
		t.Error("terminated_during_resume validates as a manifest_reason; it was removed from the closed enum as unsatisfiable")
	}
	partialReasons := []string{
		partialmanifeststore.ReasonTimeout,
		partialmanifeststore.ReasonStreamTruncated,
		partialmanifeststore.ReasonSuperseded,
		partialmanifeststore.ReasonQuotaExceeded,
	}

	// §10.1 enum enumeration: every full-enum value is named, and the removed
	// value is not a live member (the "removed from the enum" note aside).
	s101 := specSection(t, filepath.Join(root, "spec", "10_gateway-internals.md"), "### 10.1 ")
	enumLine := requireLine(t, s101, "a closed enum of the manifest's disposition")
	for _, r := range fullEnum {
		if !strings.Contains(enumLine, "`"+r+"`") {
			t.Errorf("§10.1 manifest_reason enum enumeration does not name the code value %q", r)
		}
	}
	if strings.Contains(enumLine, "`terminated_during_resume`") {
		t.Error("§10.1 manifest_reason enum enumeration still lists terminated_during_resume as a live value")
	}

	// §10.1 cleanup-paragraph cross-reference site: the second §10.1 mention of
	// lenny_checkpoint_partial_total (the Cleanup paragraph) carries only the
	// §16.1 cross-reference and re-enumerates none of the recovered /
	// manifest_reason / trigger domains, mirroring the enum-line check above so
	// both §10.1 cross-reference sites are pinned. A future edit that re-adds a
	// domain enumeration or the removed terminated_during_resume value here would
	// otherwise pass undetected and defeat the single-source invariant §16.1 owns.
	cleanupLine := requireLine(t, s101, "counter tracks partial checkpoint events")
	requireAllContain(t, "§10.1 cleanup-paragraph lenny_checkpoint_partial_total cross-reference", cleanupLine, []string{
		"the single source in [§16.1]",
	})
	requireNoneContain(t, "§10.1 cleanup-paragraph lenny_checkpoint_partial_total cross-reference", cleanupLine, []string{
		"terminated_during_resume",
		"`stream_truncated`",
		"`quota_exceeded`",
		"`pre_scale_down`",
		"`periodic`",
	})

	// §16.1 partial-total row: the recovered, manifest_reason, and trigger
	// domain declarations read exactly, and none names terminated_during_resume.
	s161 := specSection(t, filepath.Join(root, "spec", "16_observability.md"), "### 16.1 ")
	counterLine := requireLine(t, s161, "`lenny_checkpoint_partial_total`")
	requireAllContain(t, "§16.1 lenny_checkpoint_partial_total row", counterLine, []string{
		"`recovered`: `true` \\| `false`",
		"`manifest_reason`: `timeout` \\| `stream_truncated` \\| `superseded` \\| `quota_exceeded`",
		"`trigger`: `periodic` \\| `pre_scale_down` \\| `eviction`",
	})
	if strings.Contains(counterLine, "terminated_during_resume") {
		t.Error("§16.1 lenny_checkpoint_partial_total row names terminated_during_resume in a label domain")
	}

	// Code emitter registration: the counter carries exactly the pool,
	// recovered, manifest_reason, trigger labels §16.1 declares.
	emitter, err := os.ReadFile(filepath.Join(root, "pkg", "gateway", "metrics", "gatewaymetrics", "gatewaymetrics_podlifecycle.go"))
	if err != nil {
		t.Fatalf("read gatewaymetrics_podlifecycle.go: %v", err)
	}
	if !strings.Contains(string(emitter), `[]string{"pool", "recovered", "manifest_reason", "trigger"}`) {
		t.Error("the lenny_checkpoint_partial_total emitter does not register the {pool, recovered, manifest_reason, trigger} label set §16.1 declares")
	}

	// The partial-only sub-domain the counter admits matches the §16.1
	// manifest_reason declaration exactly.
	assertSetEqual(t, "partial manifest_reason sub-domain (code) vs §16.1",
		partialReasons, []string{"timeout", "stream_truncated", "superseded", "quota_exceeded"})

	// Every §16.1 trigger-domain value is a member of checkpoint.AllTriggers(),
	// and every trigger the code can emit is in the §16.1 domain.
	var codeTriggers []string
	for _, tr := range checkpoint.AllTriggers() {
		codeTriggers = append(codeTriggers, string(tr))
	}
	assertSetEqual(t, "checkpoint.AllTriggers() vs §16.1 trigger domain",
		codeTriggers, []string{"periodic", "pre_scale_down", "eviction"})
}

// spec: 12.5
// diagnosis: the §12.5 backstop sweep selection predicate reads differently at
//
//	its two spec/12 occurrences (the partial-manifest backstop bullet and GC
//	concurrency-model rule 6). Both sites quote the full selection predicate
//	verbatim so a reader of either learns the same query. A failure here means
//	one occurrence was edited without the other, so the backstop and its
//	idempotency argument no longer describe the same predicate.
func TestBackstopSweepPredicateReadsIdenticallyAcrossSpec12(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "spec", "12_storage-architecture.md"))
	if err != nil {
		t.Fatalf("read spec/12: %v", err)
	}
	const predicate = "partial = true AND (manifest_reason != 'in_progress' OR now() > checkpoint_timeout_at) AND (terminal_state OR created_at < now() - maxResumeWindowSeconds) AND deleted_at IS NULL"
	if n := strings.Count(string(body), predicate); n < 2 {
		t.Errorf("the §12.5 backstop sweep predicate appears %d time(s) in spec/12, want at least 2 (the backstop bullet and GC rule 6 must quote it identically); a differing count means one site drifted:\n  %s", n, predicate)
	}
}

// spec: 11.2, 12.4
// diagnosis: the storage-counter rehydrate reservation-folding term reads
//
//	differently across its spec sites. §11.2 (the relative-decrement rebuild-
//	safety argument and the GC-triggered decrement) and §12.4 (the Redis
//	failure-mode table) each fold outstanding checkpoint reservations into the
//	absolute artifact_store-sum rebuild with the identical term. A failure here
//	means one site was edited without the others, so the counter rehydrate no
//	longer folds reservations consistently and a relative decrement after a
//	rebuild could drop bytes belonging to the tenant's live artifacts.
func TestStorageRehydrateReservationTermReadsIdenticallyAcrossSpec(t *testing.T) {
	root := repoRoot(t)
	// The reservation-folding term uses a Unicode minus (U+2212), matching the
	// spec's arithmetic notation; keep the literal byte-for-byte.
	const term = "SUM(reserved_bytes − workspace_bytes_uploaded over checkpoint_manifest rows where deleted_at IS NULL AND reservation_released_at IS NULL)"

	s11, err := os.ReadFile(filepath.Join(root, "spec", "11_policy-and-controls.md"))
	if err != nil {
		t.Fatalf("read spec/11: %v", err)
	}
	if n := strings.Count(string(s11), term); n < 2 {
		t.Errorf("the reservation-folding rehydrate term appears %d time(s) in spec/11, want at least 2 (the rebuild-safety argument and the GC-triggered decrement must fold reservations identically):\n  %s", n, term)
	}

	s12, err := os.ReadFile(filepath.Join(root, "spec", "12_storage-architecture.md"))
	if err != nil {
		t.Fatalf("read spec/12: %v", err)
	}
	if n := strings.Count(string(s12), term); n < 1 {
		t.Errorf("the reservation-folding rehydrate term is absent from spec/12 §12.4; the failure-mode table must fold reservations with the same term §11.2 uses:\n  %s", term)
	}
}

// spec: 13.1
// diagnosis: the reader-facing docs mirrors of the §13.1 Pod Security control
//
//	table (architecture.md, security.md) or the concepts.md "Gateway-mediated
//	file delivery" prose have drifted from the amended §13.1 spec rows. §13.1
//	records that the agent pod PUTs checkpoint chunks to, and on resume GETs
//	them from, object storage against gateway-minted presigned capabilities —
//	the one exception to gateway-mediated file delivery. This check anchors on
//	the spec first (so a docs mirror is measured against the live §13.1 rows,
//	not a stale re-typed phrase) and then asserts each mirror agrees. A failure
//	means the §13.1 row that grants the presigned path was removed, or a docs
//	page still presents the pre-pipeline posture in which the agent pod has no
//	object-store path at all, leaving an operator with a control-table mirror
//	that contradicts the shipped presigned-capability grant.
func TestCheckpointDocsMirrorsAgreeWithSpec131(t *testing.T) {
	root := repoRoot(t)

	// Spec anchor: §13.1 grants the agent pod the presigned checkpoint-chunk
	// path. Assert the spec side first so the docs comparison below is bound to
	// a live row rather than passing vacuously when the row is gone.
	s131 := specSection(t, filepath.Join(root, "spec", "13_security-model.md"), "### 13.1 ")
	specTransfer := requireLine(t, s131, "| Checkpoint transfer |")
	requireAllContain(t, "§13.1 Checkpoint transfer row", specTransfer, []string{
		"`PUT`s checkpoint chunks",
		"gateway-minted presigned URLs",
	})

	// architecture.md and security.md §13.1 table mirrors: the File delivery row
	// names the checkpoint-chunk exception, matching the amended spec row.
	tableMirrors := []struct{ page, heading string }{
		{filepath.Join("docs", "getting-started", "architecture.md"), "Pod security settings"},
		{filepath.Join("docs", "operator-guide", "security.md"), "Security Context"},
	}
	for _, m := range tableMirrors {
		body := readDoc(t, filepath.Join(root, m.page))
		sec := section(body, m.heading)
		if sec == "" {
			t.Fatalf("%s: %q section not found (renamed or removed?)", m.page, m.heading)
		}
		delivery := requireLine(t, sec, "| File delivery |")
		requireAllContain(t, m.page+" §13.1 File delivery row", delivery, []string{
			"Checkpoint chunk objects are the one exception",
			"gateway-minted presigned capabilities",
		})
	}

	// concepts.md prose: the file-delivery section states the exception and does
	// not assert pods have no object-store path.
	concepts := readDoc(t, filepath.Join(root, "docs", "getting-started", "concepts.md"))
	delivery := section(concepts, "Gateway-mediated file delivery")
	if delivery == "" {
		t.Fatal("concepts.md: 'Gateway-mediated file delivery' section not found (renamed or removed?)")
	}
	requireAllContain(t, "concepts.md gateway-mediated file delivery prose", delivery, []string{
		"Checkpoint chunk objects are the one exception",
		"gateway-minted presigned capability",
	})
	requireNoneContain(t, "concepts.md gateway-mediated file delivery prose", delivery, []string{
		"no direct object store access from pods",
	})
}

// assertSetEqual fails when got and want are not the same set of strings,
// order-independent. It reports the symmetric difference so a drift names the
// offending value rather than a bare inequality.
func assertSetEqual(t *testing.T, label string, got, want []string) {
	t.Helper()
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if strings.Join(g, ",") != strings.Join(w, ",") {
		t.Errorf("%s: sets differ\n  got:  %v\n  want: %v", label, g, w)
	}
}
