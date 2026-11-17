// SPDX-License-Identifier: MIT

package static

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The records of the specification-structure change (0115) state where its
// tools live, which tier runs its gates, when a numbered file comes under
// the gates, how a folder index is generated, and which commit the frozen
// tag marks. Review found each of these stated against the tree, so these
// gates pin the corrected statements. They are contributor records rather
// than a behavior the specification states, so no specification section
// applies.

// readRecord reads a repo-relative file, or the first of several that
// exists, and fails when none does.
func readRecord(t *testing.T, root string, candidates ...string) string {
	t.Helper()
	for _, rel := range candidates {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}
	t.Fatalf("none of %v exists", candidates)
	return ""
}

// diagnosis: a record of the specification structure states the frozen
// tag, a tool's location, the documentation gates' tier, the numbered-file
// scope of the gates, or the folder-index generation differently from the
// tree. Correct the record named in the failure.
func TestSpecStructureRecordsMatchTheTree(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	// The change folder moves to changes/archive/<date>-0115-spec-structure
	// when the change is archived, so both places are tried.
	archived, _ := filepath.Glob(filepath.Join(root, "changes", "archive", "*-0115-spec-structure"))
	folder := func(name string) []string {
		out := []string{"changes/0115-spec-structure/" + name}
		for _, a := range archived {
			rel, err := filepath.Rel(root, a)
			if err == nil {
				out = append(out, filepath.ToSlash(rel)+"/"+name)
			}
		}
		return out
	}
	cases := []struct {
		file      []string
		want, not []string
	}{
		{
			file: []string{"spec/conventions.md"},
			want: []string{
				"The git tag `spec-frozen` marks the last commit of `main` before the change that introduced this structure merged",
				"The `index.md` keeps its hand-written sections, and only the text between its `index` markers is generated.",
			},
			not: []string{"the first area change edits the specification, and that change creates the tag", "is generated whole"},
		},
		{
			file: []string{"changes/README.md"},
			want: []string{"`spec-frozen` is created on the last commit of `main` before change 0115"},
			not:  []string{"The first area change of the specification migration creates the git tag"},
		},
		{
			file: folder("change.md"),
			want: []string{
				"in `scripts/specshift/survey`",
				"the `status-page` command of `scripts/changes/changes.py`",
				"`tests/tier0_static/doc_spec_gates_test.go`",
				"once every numbered section in it is a full stub",
			},
			not: []string{"`scripts/changes/changes.py` gains `digest` and `since`", "Tier 11: the documentation gates"},
		},
		{
			file: folder("steps.md"),
			want: []string{"to a numbered file once every numbered section in it is a full stub"},
			not:  []string{"and to stubbed numbered sections"},
		},
	}
	for _, c := range cases {
		text := readRecord(t, root, c.file...)
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s does not state %q", c.file[0], w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(text, n) {
				t.Errorf("%s still states %q", c.file[0], n)
			}
		}
	}
}
