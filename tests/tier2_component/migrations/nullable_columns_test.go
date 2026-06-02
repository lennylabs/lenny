// SPDX-License-Identifier: MIT

// This file carries no build tag (unlike the Postgres-backed component
// suite in this directory) so it runs in the default tier-1 build: it is a
// pure static scan of the production migration SQL and needs no container.
package migrations_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRootFromCaller walks up from this test file to the repository root
// (the directory holding go.mod) without depending on the container-tagged
// schematest helper, so the scan runs in the default build.
func repoRootFromCaller(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test file")
		}
		dir = parent
	}
}

// lineComment strips a `-- ...` SQL line comment from a single line.
var lineComment = regexp.MustCompile(`--.*`)

// addColumnSplit splits a normalized statement on each `add column`
// keyword (case-insensitive) so each column definition can be inspected
// independently.
var addColumnSplit = regexp.MustCompile(`(?i)add column`)

// violatesNullableRule reports whether the up-migration SQL in src adds a
// column with a bare NOT NULL constraint and no DEFAULT. It mirrors
// scripts/lint-migrations.sh Pass 4: comments are stripped, the file is
// collapsed to one buffer, statements are split on `;`, and each
// `ADD COLUMN` clause is isolated up to the next comma. A DEFAULT keyword
// precedes its literal within the clause, so a default value that itself
// contains a comma does not produce a false positive.
//
// spec: §10.5 line 415 — every Phase 1 column must be NULLable or carry a
// server-side DEFAULT until Phase 3 drops the old column; a NOT NULL
// column with no default breaks the rolling deploy because an old-version
// replica issues INSERTs that omit it.
func violatesNullableRule(src string) bool {
	var buf strings.Builder
	for _, line := range strings.Split(src, "\n") {
		buf.WriteString(lineComment.ReplaceAllString(line, ""))
		buf.WriteByte(' ')
	}
	for _, stmt := range strings.Split(buf.String(), ";") {
		parts := addColumnSplit.Split(stmt, -1)
		for _, seg := range parts[1:] { // parts[0] precedes the first ADD COLUMN
			if i := strings.IndexByte(seg, ','); i >= 0 {
				seg = seg[:i]
			}
			low := strings.ToLower(seg)
			if strings.Contains(low, "not null") && !strings.Contains(low, "default") {
				return true
			}
		}
	}
	return false
}

// TestPhase1ColumnsAreNullableOrDefaulted_spec_10_5_415 asserts that no
// production up-migration adds a NOT NULL column without a DEFAULT. This
// is the CI defense for the §10.5 expand-contract rolling-deploy
// invariant: an old-version replica that does not know a Phase 1 column
// issues INSERTs that omit it, and a NOT NULL constraint with no default
// makes those INSERTs fail mid rolling-deploy. The NOT NULL constraint may
// only be added in a Phase 3 migration via ALTER COLUMN ... SET NOT NULL,
// after every replica runs the new code.
//
// spec: §10.5 line 415.
func TestPhase1ColumnsAreNullableOrDefaulted_spec_10_5_415(t *testing.T) {
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
		if violatesNullableRule(string(b)) {
			t.Errorf("%s adds a NOT NULL column with no DEFAULT, violating the §10.5 "+
				"expand-contract rolling-deploy invariant (old replicas omitting the "+
				"column on INSERT will fail). Make it NULLable or give it a server-side "+
				"DEFAULT; add NOT NULL in a Phase 3 ALTER COLUMN ... SET NOT NULL migration.",
				filepath.Base(up))
		}
	}
}

// TestViolatesNullableRule_spec_10_5_415 exercises the scanner against the
// boundary cases the §10.5 rule turns on: a bare NOT NULL column (flagged),
// NOT NULL with a DEFAULT (allowed), a NULLable column (allowed), a DEFAULT
// literal that itself contains a comma (allowed — the comma must not split
// the clause before the DEFAULT keyword is seen), a multi-column ALTER
// where only one column violates (flagged), and a CREATE TABLE with NOT
// NULL columns (allowed — a new table has no old-version writers).
func TestViolatesNullableRule_spec_10_5_415(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{"bare not null", `ALTER TABLE sessions ADD COLUMN legacy_token TEXT NOT NULL;`, true},
		{"not null with default", `ALTER TABLE sessions ADD COLUMN flag BOOLEAN NOT NULL DEFAULT false;`, false},
		{"nullable", `ALTER TABLE sessions ADD COLUMN note TEXT;`, false},
		{"default with comma in literal", `ALTER TABLE runtimes ADD COLUMN labels JSONB NOT NULL DEFAULT '{"a":1,"b":2}'::jsonb;`, false},
		{"multi-column one bad", `ALTER TABLE t ADD COLUMN ok BOOLEAN NOT NULL DEFAULT false, ADD COLUMN bad TEXT NOT NULL;`, true},
		{"create table not null", `CREATE TABLE t (id UUID PRIMARY KEY, name TEXT NOT NULL);`, false},
		{"comment hides violation text", "-- ADD COLUMN x TEXT NOT NULL\nALTER TABLE t ADD COLUMN y TEXT;", false},
		{"multi-line bare not null", "ALTER TABLE sessions\n    ADD COLUMN legacy_token TEXT\n    NOT NULL;", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := violatesNullableRule(tc.sql); got != tc.want {
				t.Errorf("violatesNullableRule(%q) = %v, want %v", tc.sql, got, tc.want)
			}
		})
	}
}
