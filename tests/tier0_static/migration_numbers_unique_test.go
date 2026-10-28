// SPDX-License-Identifier: MIT

package tier0_static

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// migrationFileExpr matches one golang-migrate file name: a numeric
// version, a name, and the direction.
var migrationFileExpr = regexp.MustCompile(`^([0-9]+)_(.+)\.(up|down)\.sql$`)

// migrationNumberDefects reads the names in a migrations directory and
// returns one defect per version that more than one migration claims and
// per migration that lacks its up or its down file. Names that are not
// migration files (Go tests, the embed file) are ignored.
//
// golang-migrate records only the version of an applied migration, so two
// migrations sharing a number are one migration to the database: a
// database another branch migrated to its own file at that number reads
// as already carrying this branch's file, and `lenny-migrate up` skips it
// (T-17.4.31). The number is the identity, so it has to be unique.
func migrationNumberDefects(names []string) []string {
	type pair struct{ up, down bool }
	byVersion := map[int]map[string]*pair{}
	for _, n := range names {
		m := migrationFileExpr.FindStringSubmatch(n)
		if m == nil {
			continue
		}
		var v int
		if _, err := fmt.Sscanf(m[1], "%d", &v); err != nil {
			continue
		}
		base := m[1] + "_" + m[2]
		if byVersion[v] == nil {
			byVersion[v] = map[string]*pair{}
		}
		p := byVersion[v][base]
		if p == nil {
			p = &pair{}
			byVersion[v][base] = p
		}
		if m[3] == "up" {
			p.up = true
		} else {
			p.down = true
		}
	}
	versions := make([]int, 0, len(byVersion))
	for v := range byVersion {
		versions = append(versions, v)
	}
	sort.Ints(versions)
	var defects []string
	for _, v := range versions {
		bases := make([]string, 0, len(byVersion[v]))
		for b := range byVersion[v] {
			bases = append(bases, b)
		}
		sort.Strings(bases)
		if len(bases) > 1 {
			defects = append(defects, fmt.Sprintf("migration %d is claimed by %s", v, strings.Join(bases, ", ")))
			continue
		}
		p := byVersion[v][bases[0]]
		switch {
		case !p.up:
			defects = append(defects, fmt.Sprintf("migration %s has a down file and no up file", bases[0]))
		case !p.down:
			defects = append(defects, fmt.Sprintf("migration %s has an up file and no down file", bases[0]))
		}
	}
	return defects
}

// TestMigrationNumbersAreUnique fails when two migrations in migrations/
// share a version number, or when a migration lacks its up or down file.
//
// spec: 10.5 (Upgrade and Rollback Strategy, "Schema Migration Tooling and
// Runbook": each migration is a pair of up/down SQL files under migrations/)
func TestMigrationNumbersAreUnique(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join(schematest.RepoRoot(t), "migrations"))
	if err != nil {
		t.Fatalf("read migrations/: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatalf("migrations/ holds no files; the check would certify nothing")
	}
	if defects := migrationNumberDefects(names); len(defects) > 0 {
		t.Errorf("migrations/ carries %d numbering defect(s); renumber the newer migration above the highest version:\n  %s",
			len(defects), strings.Join(defects, "\n  "))
	}
}

// TestMigrationNumberDefectsNamesEachCollision pins the predicate the
// tree check runs: a shared number is reported with both names, a
// missing direction is reported, zero padding does not hide a collision,
// and non-migration files are ignored.
//
// spec: 10.5 (Upgrade and Rollback Strategy, "Schema Migration Tooling and
// Runbook": each migration is a pair of up/down SQL files under migrations/)
func TestMigrationNumberDefectsNamesEachCollision(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
		want  []string
	}{
		{"clean pairs", []string{
			"0191_a.up.sql", "0191_a.down.sql", "0192_b.up.sql", "0192_b.down.sql",
			"0192_b_test.go", "embed.go", "README.md",
		}, nil},
		{"two migrations at one number", []string{
			"0192_b.up.sql", "0192_b.down.sql", "0192_c.up.sql", "0192_c.down.sql",
		}, []string{"migration 192 is claimed by 0192_b, 0192_c"}},
		{"zero padding does not separate a number", []string{
			"0192_b.up.sql", "0192_b.down.sql", "192_c.up.sql", "192_c.down.sql",
		}, []string{"migration 192 is claimed by 0192_b, 192_c"}},
		{"up and down named differently", []string{
			"0192_b.up.sql", "0192_c.down.sql",
		}, []string{"migration 192 is claimed by 0192_b, 0192_c"}},
		{"missing down", []string{"0192_b.up.sql"}, []string{"migration 0192_b has an up file and no down file"}},
		{"missing up", []string{"0192_b.down.sql"}, []string{"migration 0192_b has a down file and no up file"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := migrationNumberDefects(tc.files)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("defects = %q, want %q", got, tc.want)
			}
		})
	}
}
