// SPDX-License-Identifier: MIT

package tier0_static

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// embpgConfigLiteral matches an embedded Postgres construction, from the New
// call through the closing brace of its Config literal.
var embpgConfigLiteral = regexp.MustCompile(`(?s)\bemb(?:pg|postgres)\.New\(\w*\.?\w*\{.*?\n\s*\}\)`)

// fixedPortField matches a nonzero literal Port field.
var fixedPortField = regexp.MustCompile(`\bPort:\s+[1-9]`)

// fixedEmbeddedPostgresPorts returns the literals in src that give an embedded
// Postgres a fixed port.
func fixedEmbeddedPostgresPorts(src string) []string {
	var out []string
	for _, lit := range embpgConfigLiteral.FindAllString(src, -1) {
		if fixedPortField.MatchString(lit) {
			out = append(out, lit)
		}
	}
	return out
}

// TestFixedEmbeddedPostgresPortMatcher pins the matcher on a literal it must
// refuse and on the literals it must accept, so a change to the pattern cannot
// leave the repository gate passing vacuously.
//
// spec: TESTING.md §17.10 (Flake budget)
// diagnosis: the matcher behind TestEmbeddedPostgresPortsAreEphemeral_spec_17_10
// no longer recognizes a fixed port, or it refuses an ephemeral one.
func TestFixedEmbeddedPostgresPortMatcher(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"fixed port", "pg := embpostgres.New(embpostgres.Config{\n\tPort:     15523,\n\tDatabase: \"lenny\",\n})\n", 1},
		{"other alias", "pg := embpg.New(embpg.Config{\n\tPort: 5432,\n})\n", 1},
		{"ephemeral port", "pg := embpostgres.New(embpostgres.Config{\n\tPort:     0,\n\tDatabase: \"lenny\",\n})\n", 0},
		{"no port field", "pg := embpg.New(embpg.Config{\n\tDatabase: \"lenny\",\n})\n", 0},
		{"unrelated literal", "c := other.New(other.Config{\n\tPort: 15523,\n})\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(fixedEmbeddedPostgresPorts(tc.src)); got != tc.want {
				t.Fatalf("fixed ports in %q = %d, want %d", tc.src, got, tc.want)
			}
		})
	}
}

// TestEmbeddedPostgresPortsAreEphemeral_spec_17_10 refuses an embedded
// Postgres fixture with a fixed port. go test runs packages in parallel and
// other worktrees run on the same host, so two fixtures on one port fail
// whichever starts second with "process already listening on port N"
// (TEST-GAPS T-17.4.48).
//
// spec: TESTING.md §17.10 (Flake budget)
// diagnosis: a test gives embpg.Config a literal port. Use Port: 0, which
// resolves a free port at Start, and read it back from DSN() or Port().
func TestEmbeddedPostgresPortsAreEphemeral_spec_17_10(t *testing.T) {
	root := schematest.RepoRoot(t)
	var offenders []string
	for _, top := range []string{"pkg", "cmd", "tests", "sdks"} {
		dir := filepath.Join(root, top)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if len(fixedEmbeddedPostgresPorts(string(b))) > 0 {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("embedded Postgres fixtures with a fixed port (use Port: 0): %q", offenders)
	}
}
