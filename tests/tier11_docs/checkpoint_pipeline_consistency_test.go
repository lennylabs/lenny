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
// creates and a later migration drops, so a reader can resolve the retirement
// to the change that performed it.
type droppedColumn struct {
	// table is the table the drop is asserted against. The record is scoped to
	// it so a drop of the same column name from a sibling table never
	// satisfies the record: session_checkpoints carries a slot_id of its own,
	// and only the checkpoint_manifest column is excepted here.
	table string
	// migration is the four-digit prefix of the migration that drops the
	// column. Every entry names one, so a reader resolves the retirement to a
	// change from the moment the exception is written rather than after the
	// drop lands. The name is a forward reference while that migration is
	// unwritten; the gate below holds it to the migration tree as soon as the
	// tree has one to check it against.
	migration string
	// reason states why the column is gone.
	reason string
}

// droppedColumns are checkpoint_manifest columns migration 0178 creates and a
// later migration drops, mapped to the table the drop is asserted against and
// the reason the column is gone. Migration 0178's own `.up.sql` text keeps the
// CREATE TABLE line that first declared them, so they stay in the extracted
// set, while §10.1 no longer names them. Each entry carries its table
// alongside the column, the form
// tests/tier2_component/migrations/prod_columns_test.go already uses for the
// columns the prod chain retires, so a column name is never resolved
// table-blind.
//
// Re-pointing the extraction away from migration 0178, to a post-drop schema,
// is rejected: that file is the gate's only statement of the created table, so
// every remaining manifest column would lose the source it agrees with §10.1
// against. The exception carries the retired column instead.
//
// The set drains rather than accumulates, on the conditions
// droppedColumnDrainError states: an entry recorded against a table other than
// checkpoint_manifest cannot except a manifest column, an entry naming a column
// migration 0178 does not create is dead, an entry for a column §10.1 names
// again is a live column the agreement must cover, an entry naming no drop
// migration is incomplete, and a named migration is held against the tree once
// the tree carries a drop of that table's column.
var droppedColumns = map[string]droppedColumn{
	// spec: §10.1 (the partial manifest, the supersede rule, and the
	// reassembly predicate are keyed on session_id alone), §12.5 (retention
	// and supersession operate on session_id)
	//
	// Migration 0180 is the migration that drops session_checkpoints.slot_id
	// and checkpoint_manifest.slot_id and re-keys the three checkpoint indexes
	// on session_id. It is named here before it is written, the way
	// tests/tier2_component/migrations/prod_columns_test.go names migration
	// 0167 as the drop of sandbox_warm_pools.concurrency_style, so the
	// exception identifies the change that retires the column rather than
	// leaving a reader to search the tree for it. 0180 is the next free prefix
	// under migrations/; should the drop land under another prefix, the drain
	// check below fails this entry and names the migration that actually
	// carries the drop.
	"slot_id": {
		table:     "checkpoint_manifest",
		migration: "0180",
		reason:    "the manifest, the supersede rule, and the reassembly predicate are keyed on session_id alone, so migration 0180 drops the column",
	},
}

// dropState is the migration-tree evidence for one droppedColumns entry.
type dropState struct {
	// exists reports whether the migration the entry names is in the tree.
	exists bool
	// carries reports whether that migration's `.up.sql` drops the entry's
	// table.column.
	carries bool
	// contradicted is the prefix of the migration that actually drops the
	// entry's table.column when that is not the migration the entry names. It
	// is empty when the named migration carries the drop, and while no
	// migration in the tree drops the column at all.
	contradicted string
}

// manifestTable is the table the §10.1 column agreement is stated over. A
// dropped-column exception only excepts a column of this table; an entry
// recorded against any other table cannot suppress the agreement, and fails
// the drain check below rather than shielding a manifest column.
const manifestTable = "checkpoint_manifest"

// suppressesManifestColumn reports whether set excepts manifestTable.col from
// the §10.1 column agreement. The lookup is table-scoped, so an entry recorded
// against a sibling table that carries a column of the same name (as
// session_checkpoints carries its own slot_id) never suppresses the agreement
// for the manifest column.
//
// spec: §10.1 (the manifest column enumeration)
func suppressesManifestColumn(set map[string]droppedColumn, col string) bool {
	d, ok := set[col]
	return ok && d.table == manifestTable
}

// droppedColumnDrainError reports why a droppedColumns entry no longer stands,
// or nil while it does. An exception is suppressing the column agreement
// above, so it is held to the conditions the droppedColumns comment states:
// the entry is recorded against checkpoint_manifest, the column is one
// migration 0178 creates, §10.1 names it nowhere, the entry names the drop
// migration, and that migration is the one the tree drops the column in once
// the tree drops it anywhere. While the drop is unwritten the named migration
// is a forward reference and the entry stands as the record of the change that
// will perform it.
//
// spec: §10.1 (the manifest column enumeration)
func droppedColumnDrainError(col string, d droppedColumn, createdByMigration, namedInSpec bool, drop dropState) error {
	switch {
	case d.table != manifestTable:
		return fmt.Errorf("droppedColumns[%q] is recorded against table %q, so it cannot except a %s column (%s)", col, d.table, manifestTable, d.reason)
	case !createdByMigration:
		return fmt.Errorf("droppedColumns names %q (%s), which migration 0178 does not create", col, d.reason)
	case namedInSpec:
		return fmt.Errorf("§10.1 names %q again, so it is a live manifest column: remove its droppedColumns entry (%s) and hold it to the agreement above", col, d.reason)
	case d.migration == "":
		return fmt.Errorf("droppedColumns[%q] names no drop migration, so the exception identifies no change as the one that retires %s.%s (%s)", col, manifestTable, col, d.reason)
	case drop.carries:
		return nil
	case drop.contradicted != "":
		return fmt.Errorf("droppedColumns names migration %s as the drop of %s.%s, but migration %s is the one that drops it (%s)", d.migration, d.table, col, drop.contradicted, d.reason)
	case drop.exists:
		return fmt.Errorf("droppedColumns names migration %s as the drop of %s.%s, but that migration's .up.sql drops no such column from that table", d.migration, d.table, col)
	}
	return nil
}

// alterTableRE splits a migration body at its ALTER TABLE statement heads.
var alterTableRE = regexp.MustCompile(`(?is)\bALTER\s+TABLE\s+`)

// dropColumnRE matches a column drop. It admits the optional IF EXISTS
// qualifier, which is the form every column drop in migrations/ is written
// with, and requires a word boundary so a longer column name with the same
// prefix does not match.
func dropColumnRE(column string) *regexp.Regexp {
	return regexp.MustCompile(`(?is)\bDROP\s+COLUMN\s+(?:IF\s+EXISTS\s+)?` + regexp.QuoteMeta(column) + `\b`)
}

// sqlDropsColumn reports whether body drops table.column. The drop must sit
// inside the ALTER TABLE statement that names table, so a drop of the same
// column name from a sibling table does not satisfy it.
func sqlDropsColumn(body, table, column string) bool {
	drop := dropColumnRE(column)
	head := regexp.MustCompile(`(?is)^(?:IF\s+EXISTS\s+)?(?:ONLY\s+)?` + regexp.QuoteMeta(table) + `\b`)
	statements := alterTableRE.Split(body, -1)
	for _, stmt := range statements[1:] {
		if end := strings.Index(stmt, ";"); end >= 0 {
			stmt = stmt[:end]
		}
		if head.MatchString(stmt) && drop.MatchString(stmt) {
			return true
		}
	}
	return false
}

// readMigration reads a migration file, failing the test when it cannot.
func readMigration(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// migrationDropsColumn reports whether the migration whose numeric prefix is
// `migration` exists under migrations/ and, when it does, whether its `.up.sql`
// drops table.column. An absent migration reports (false, false).
func migrationDropsColumn(t *testing.T, root, migration, table, column string) (exists, drops bool) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "migrations", migration+"_*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations/%s_*.up.sql: %v", migration, err)
	}
	if len(matches) == 0 {
		return false, false
	}
	for _, m := range matches {
		if sqlDropsColumn(readMigration(t, m), table, column) {
			return true, true
		}
	}
	return true, false
}

// findDropMigration returns the four-digit prefix of the first migration under
// migrations/ whose `.up.sql` drops table.column, and whether one exists. It
// resolves the drop by content, so a droppedColumns entry whose named
// migration turns out not to carry the drop is failed against the migration
// that does rather than against silence.
func findDropMigration(t *testing.T, root, table, column string) (string, bool) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations/*.up.sql: %v", err)
	}
	sort.Strings(matches)
	for _, m := range matches {
		if !sqlDropsColumn(readMigration(t, m), table, column) {
			continue
		}
		base := filepath.Base(m)
		if i := strings.Index(base, "_"); i > 0 {
			return base[:i], true
		}
		return base, true
	}
	return "", false
}

// dropStateFor gathers the migration-tree evidence for one droppedColumns
// entry: whether the migration it names is in the tree and drops the column,
// and, when it does not, which migration drops the column instead.
func dropStateFor(t *testing.T, root, col string, d droppedColumn) dropState {
	t.Helper()
	if d.migration == "" {
		return dropState{}
	}
	exists, carries := migrationDropsColumn(t, root, d.migration, d.table, col)
	if carries {
		return dropState{exists: true, carries: true}
	}
	contradicted, _ := findDropMigration(t, root, d.table, col)
	return dropState{exists: exists, contradicted: contradicted}
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
		if suppressesManifestColumn(droppedColumns, col) {
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

	// The exception list drains rather than accumulates. An entry recorded
	// against another table cannot except a manifest column, an entry naming a
	// column migration 0178 never creates is dead, one §10.1 names again is a
	// live column the agreement above must cover, an entry naming no migration
	// while the tree already drops the column must record that migration, and a
	// named migration must exist and carry the drop of that table's column.
	migrationSet := make(map[string]bool, len(migrationCols))
	for _, col := range migrationCols {
		migrationSet[col] = true
	}
	for col, dropped := range droppedColumns {
		drop := dropStateFor(t, root, col, dropped)
		if err := droppedColumnDrainError(col, dropped, migrationSet[col], columnNamedInCodeSpan(s101, col), drop); err != nil {
			t.Error(err)
		}
	}
}

// spec: 10.1, 12.5
// diagnosis: the dropped-column exception's drain conditions have changed. The
//
//	§10.1 column-agreement check is suppressed for a column only on the
//	strength of a droppedColumns entry, so the entry is held to its recorded
//	conditions: it is recorded against checkpoint_manifest, the column is one
//	migration 0178 creates, §10.1 names it nowhere, it names the drop
//	migration, and that migration is the one the tree drops the column in once
//	the tree drops it anywhere. A failure here means one of those stopped
//	draining the entry, so an exception can now outlive the retirement it
//	records or stand without identifying the change that performs it.
func TestDroppedColumnExceptionDrainsOnItsRecordedConditions(t *testing.T) {
	const col = "slot_id"
	named := droppedColumn{
		table:     "checkpoint_manifest",
		migration: "0181",
		reason:    "the manifest is scoped on session_id alone",
	}
	pending := droppedColumn{
		table:  "checkpoint_manifest",
		reason: "the manifest is scoped on session_id alone",
	}
	cases := []struct {
		name         string
		entry        droppedColumn
		createdBy178 bool
		namedInSpec  bool
		drop         dropState
		wantErr      bool
	}{
		{
			name:         "the entry names a migration the tree does not carry yet",
			entry:        named,
			createdBy178: true,
		},
		{
			name:         "the entry names a migration and the only drop is of another table's column of the same name",
			entry:        named,
			createdBy178: true,
			// dropStateFor resolves the drop table-scoped, so a drop of
			// session_checkpoints.slot_id contradicts nothing and the
			// checkpoint_manifest entry stands on its forward reference.
			drop: dropState{},
		},
		{
			name:         "the entry names a migration that exists and carries the drop",
			entry:        named,
			createdBy178: true,
			drop:         dropState{exists: true, carries: true},
		},
		{
			name:         "an entry naming no drop migration identifies no change",
			entry:        pending,
			createdBy178: true,
			wantErr:      true,
		},
		{
			name: "an entry recorded against a sibling table cannot except a manifest column",
			entry: droppedColumn{
				table:  "session_checkpoints",
				reason: "the manifest is scoped on session_id alone",
			},
			createdBy178: true,
			wantErr:      true,
		},
		{
			name: "an entry recorded against a sibling table is dead even when the named drop resolves",
			entry: droppedColumn{
				table:     "session_checkpoints",
				migration: "0181",
				reason:    "the manifest is scoped on session_id alone",
			},
			createdBy178: true,
			drop:         dropState{exists: true, carries: true},
			wantErr:      true,
		},
		{
			name:    "a column 0178 does not create is a dead entry",
			entry:   named,
			wantErr: true,
		},
		{
			name:         "a column §10.1 names again is a live column the agreement must cover",
			entry:        named,
			createdBy178: true,
			namedInSpec:  true,
			wantErr:      true,
		},
		{
			name:        "a column 0178 does not create and §10.1 names is still a dead entry",
			entry:       named,
			namedInSpec: true,
			wantErr:     true,
		},
		{
			name:         "another migration carries the drop the entry names",
			entry:        named,
			createdBy178: true,
			drop:         dropState{contradicted: "0182"},
			wantErr:      true,
		},
		{
			name:         "the named migration exists and drops no such column from that table",
			entry:        named,
			createdBy178: true,
			drop:         dropState{exists: true},
			wantErr:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := droppedColumnDrainError(col, tc.entry, tc.createdBy178, tc.namedInSpec, tc.drop)
			if tc.wantErr && err == nil {
				t.Errorf("droppedColumnDrainError(%q, %+v, %v, %v, %+v) = nil, want an error", col, tc.entry, tc.createdBy178, tc.namedInSpec, tc.drop)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("droppedColumnDrainError(%q, %+v, %v, %v, %+v) = %v, want nil", col, tc.entry, tc.createdBy178, tc.namedInSpec, tc.drop, err)
			}
		})
	}
}

// spec: 10.1, 12.5
// diagnosis: a dropped-column exception's record is malformed. The exception
//
//	suppresses the §10.1 column agreement for a column migration 0178 still
//	declares, so the record has to resolve: it names the table the drop is
//	asserted against, states why the column is gone, and names the drop
//	migration by its four-digit prefix. A failure here means an entry carries
//	no table or no reason, or names no migration or names one in a form the
//	drain check cannot resolve, so a reader cannot trace the exception to the
//	change that retires the column.
func TestDroppedColumnExceptionRecordResolves(t *testing.T) {
	migrationPrefix := regexp.MustCompile(`^[0-9]{4}$`)
	for col, dropped := range droppedColumns {
		if dropped.table == "" {
			t.Errorf("droppedColumns[%q] names no table, so its drop would be resolved table-blind", col)
		}
		if dropped.reason == "" {
			t.Errorf("droppedColumns[%q] states no reason", col)
		}
		// The migration field is a forward reference while the drop migration
		// is unwritten, and it is never absent: an exception that identifies no
		// change leaves a reader searching the tree for the retirement.
		if !migrationPrefix.MatchString(dropped.migration) {
			t.Errorf("droppedColumns[%q] names drop migration %q, want a four-digit migration prefix", col, dropped.migration)
		}
	}
}

// spec: 10.1, 12.5
// diagnosis: the §10.1 manifest column agreement is being suppressed
//
//	table-blind. A column is excepted from the agreement only by a
//	droppedColumns entry recorded against checkpoint_manifest;
//	session_checkpoints carries a slot_id of its own, so an entry recorded
//	against it must leave the manifest column held to the agreement. A failure
//	here means an entry for a sibling table's column now shields the manifest
//	column of the same name, which is the state the gate exists to catch.
func TestDroppedColumnSuppressionIsTableScoped(t *testing.T) {
	const col = "slot_id"
	cases := []struct {
		name string
		set  map[string]droppedColumn
		want bool
	}{
		{
			name: "an entry recorded against checkpoint_manifest excepts the manifest column",
			set:  map[string]droppedColumn{col: {table: "checkpoint_manifest", reason: "keyed on session_id alone"}},
			want: true,
		},
		{
			name: "an entry recorded against a sibling table does not except the manifest column",
			set:  map[string]droppedColumn{col: {table: "session_checkpoints", reason: "keyed on session_id alone"}},
		},
		{
			name: "an entry carrying no table does not except the manifest column",
			set:  map[string]droppedColumn{col: {reason: "keyed on session_id alone"}},
		},
		{
			name: "a column with no entry is not excepted",
			set:  map[string]droppedColumn{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := suppressesManifestColumn(tc.set, col); got != tc.want {
				t.Errorf("suppressesManifestColumn(%+v, %q) = %v, want %v", tc.set, col, got, tc.want)
			}
		})
	}

	// The shipped set only ever excepts manifest columns, so every entry in it
	// suppresses the agreement it is recorded for.
	for c, d := range droppedColumns {
		if !suppressesManifestColumn(droppedColumns, c) {
			t.Errorf("droppedColumns[%q] is recorded against table %q, so it excepts no checkpoint_manifest column", c, d.table)
		}
	}
}

// spec: 10.1
// diagnosis: the drop-statement matcher no longer recognizes the column drops
//
//	this repository writes, or resolves a column name without its table. Every
//	column drop under migrations/ is written `DROP COLUMN IF EXISTS <col>`, and
//	session_checkpoints carries a slot_id of its own alongside
//	checkpoint_manifest's. A failure here means the third drain condition either
//	fails a correct drop, turning the gate red when the retirement it records
//	completes, or accepts a drop of the same column name from a sibling table,
//	letting the exception outlive the column it excepts.
func TestMigrationDropMatcherIsTableScopedAndAdmitsIfExists(t *testing.T) {
	const (
		table = "checkpoint_manifest"
		col   = "slot_id"
	)
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{
			name: "the IF EXISTS form every migration in the tree uses",
			sql:  "ALTER TABLE checkpoint_manifest DROP COLUMN IF EXISTS slot_id;",
			want: true,
		},
		{
			name: "the bare DROP COLUMN form",
			sql:  "ALTER TABLE checkpoint_manifest DROP COLUMN slot_id;",
			want: true,
		},
		{
			name: "a lower-case statement",
			sql:  "alter table checkpoint_manifest drop column if exists slot_id;",
			want: true,
		},
		{
			name: "the drop of the sibling table's column of the same name",
			sql:  "ALTER TABLE session_checkpoints DROP COLUMN IF EXISTS slot_id;",
			want: false,
		},
		{
			name: "both tables dropped in one migration",
			sql: "ALTER TABLE session_checkpoints DROP COLUMN IF EXISTS slot_id;\n" +
				"ALTER TABLE checkpoint_manifest DROP COLUMN IF EXISTS slot_id;\n",
			want: true,
		},
		{
			name: "a drop of a longer column name sharing the prefix",
			sql:  "ALTER TABLE checkpoint_manifest DROP COLUMN IF EXISTS slot_id_legacy;",
			want: false,
		},
		{
			name: "an ADD of the column is not a drop",
			sql:  "ALTER TABLE checkpoint_manifest ADD COLUMN slot_id UUID;",
			want: false,
		},
		{
			name: "a drop naming the column outside the statement that names the table",
			sql: "ALTER TABLE checkpoint_manifest ADD COLUMN session_id UUID;\n" +
				"ALTER TABLE session_checkpoints DROP COLUMN IF EXISTS slot_id;\n",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sqlDropsColumn(tc.sql, table, col); got != tc.want {
				t.Errorf("sqlDropsColumn(%q, %q, %q) = %v, want %v", tc.sql, table, col, got, tc.want)
			}
		})
	}
}

// spec: 10.1
// diagnosis: the migration-tree resolution behind the drain conditions has
//
//	broken. migrationDropsColumn reads the migration a droppedColumns entry
//	names, and findDropMigration resolves a drop by content so a forward
//	reference is checked against the migration that lands the drop. A failure
//	here means the gate reads the tree
//	wrongly: it can miss a drop that landed, report one that did not, or
//	attribute a sibling table's drop to the excepted column.
func TestDropMigrationResolutionReadsTheMigrationTree(t *testing.T) {
	const (
		table = "checkpoint_manifest"
		col   = "slot_id"
	)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "migrations"), 0o755); err != nil {
		t.Fatalf("create migrations dir: %v", err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "migrations", name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("0177_unrelated.up.sql", "ALTER TABLE session_checkpoints DROP COLUMN IF EXISTS slot_id;\n")
	write("0181_manifest_session_scope.up.sql", "ALTER TABLE checkpoint_manifest DROP COLUMN IF EXISTS slot_id;\n")

	if exists, drops := migrationDropsColumn(t, root, "0181", table, col); !exists || !drops {
		t.Errorf("migrationDropsColumn(0181) = (%v, %v), want (true, true) for the IF EXISTS drop form", exists, drops)
	}
	if exists, drops := migrationDropsColumn(t, root, "0177", table, col); !exists || drops {
		t.Errorf("migrationDropsColumn(0177) = (%v, %v), want (true, false): it drops the sibling table's column", exists, drops)
	}
	if exists, drops := migrationDropsColumn(t, root, "0999", table, col); exists || drops {
		t.Errorf("migrationDropsColumn(0999) = (%v, %v), want (false, false) for an absent migration", exists, drops)
	}
	if got, found := findDropMigration(t, root, table, col); !found || got != "0181" {
		t.Errorf("findDropMigration = (%q, %v), want (\"0181\", true)", got, found)
	}
	if got, found := findDropMigration(t, root, "checkpoint_manifest", "chunk_count"); found {
		t.Errorf("findDropMigration for an undropped column = (%q, true), want no match", got)
	}

	// dropStateFor folds the two into the evidence the drain check reads: an
	// entry naming the migration that carries the drop resolves, and one naming
	// a different migration is contradicted by the migration that does.
	if got := dropStateFor(t, root, col, droppedColumn{table: table, migration: "0181"}); !got.exists || !got.carries || got.contradicted != "" {
		t.Errorf("dropStateFor(entry naming 0181) = %+v, want exists and carries with no contradiction", got)
	}
	if got := dropStateFor(t, root, col, droppedColumn{table: table, migration: "0177"}); got.carries || got.contradicted != "0181" {
		t.Errorf("dropStateFor(entry naming 0177) = %+v, want contradicted by 0181", got)
	}
	if got := dropStateFor(t, root, col, droppedColumn{table: table, migration: "0999"}); got.exists || got.carries || got.contradicted != "0181" {
		t.Errorf("dropStateFor(entry naming an absent migration) = %+v, want contradicted by 0181", got)
	}
	if got := dropStateFor(t, root, "chunk_count", droppedColumn{table: table, migration: "0999"}); got.exists || got.carries || got.contradicted != "" {
		t.Errorf("dropStateFor(forward reference for an undropped column) = %+v, want no evidence", got)
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

// slotDropMigrationPrefix is the numeric prefix of the migration that drops
// the persisted duplicate slot identifier from the two checkpoint tables.
const slotDropMigrationPrefix = "0180"

// slotDropSpecSections are the spec sections that own what the slot-column
// drop changes: §6.4 and §7.3 own the workspace root the migration rewrites,
// §10.1 owns the manifest scoping key, the supersede rule, and the reassembly
// predicate the re-keyed indexes serve, and §12.5 owns the retention cap the
// rotation index serves. The migration's `-- spec:` line names exactly this
// set, so a reader following a citation lands on a section that describes
// checkpoint persistence.
var slotDropSpecSections = []string{"6.4", "7.3", "10.1", "12.5"}

// specCitationRE captures the section list of a `spec:` citation line in an
// SQL comment, in either the `-- spec: §a, §b.` or `-- spec: a, b` spelling.
var specCitationRE = regexp.MustCompile(`(?m)^--\s*spec:\s*(.+)$`)

// specSectionRE captures one dotted section number from a citation list.
var specSectionRE = regexp.MustCompile(`\d+(?:\.\d+)*`)

// citedSpecSections returns the sorted, de-duplicated section numbers the
// `-- spec:` lines of an SQL body cite.
func citedSpecSections(body string) []string {
	seen := map[string]bool{}
	for _, m := range specCitationRE.FindAllStringSubmatch(body, -1) {
		for _, s := range specSectionRE.FindAllString(m[1], -1) {
			seen[s] = true
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// specSectionHeading returns the heading text of a numbered section under
// spec/, searching every spec file for a markdown heading whose first token is
// the section number.
func specSectionHeading(t *testing.T, root, section string) (string, bool) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "spec", "*.md"))
	if err != nil {
		t.Fatalf("glob spec/*.md: %v", err)
	}
	re := regexp.MustCompile(`(?m)^#{2,6} ` + regexp.QuoteMeta(section) + ` (.+)$`)
	for _, f := range files {
		if m := re.FindStringSubmatch(readMigration(t, f)); m != nil {
			return strings.TrimSpace(m[1]), true
		}
	}
	return "", false
}

// spec: 10.1 (the manifest scoping key, the supersede rule, and the
// reassembly predicate the drop re-keys), 12.5 (the retention cap the rotation
// index serves)
// diagnosis: the migration that drops the persisted duplicate slot identifier
//
//	cites a spec section that does not own checkpoint persistence, so a reader
//	following the citation lands somewhere that says nothing about the change,
//	and the spec-to-test map files the drop under an unrelated section. The
//	most likely cause is a citation copied from a change proposal's own section
//	numbering, which does not correspond to the numbering under spec/.
func TestCheckpointSlotDropCitesTheOwningSpecSections(t *testing.T) {
	root := repoRoot(t)
	for _, suffix := range []string{"up", "down"} {
		matches, err := filepath.Glob(filepath.Join(root, "migrations", slotDropMigrationPrefix+"_*."+suffix+".sql"))
		if err != nil {
			t.Fatalf("glob migrations/%s_*.%s.sql: %v", slotDropMigrationPrefix, suffix, err)
		}
		if len(matches) != 1 {
			t.Fatalf("migrations/%s_*.%s.sql: want exactly one file, got %d", slotDropMigrationPrefix, suffix, len(matches))
		}
		body := readMigration(t, matches[0])
		assertSetEqual(t, filepath.Base(matches[0])+" spec citations", citedSpecSections(body), slotDropSpecSections)
	}
	// The section the drop must not cite is a live section of the spec that
	// owns an unrelated mechanism, so naming it is not a dangling reference a
	// resolver would catch. Pin what it owns, so the reason it is excluded
	// from the set above stays legible.
	heading, ok := specSectionHeading(t, root, "4.9")
	if !ok {
		t.Fatalf("spec section 4.9 has no heading under spec/")
	}
	if !strings.Contains(strings.ToLower(heading), "credential") {
		t.Fatalf("spec section 4.9 heading = %q, want the credential leasing service; update the exclusion rationale above", heading)
	}
}
