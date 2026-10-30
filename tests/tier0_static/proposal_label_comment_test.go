// SPDX-License-Identifier: MIT

package tier0_static

import (
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The proposal-label ratchet keeps the scaffolding labels of a change
// proposal out of Go comments. A proposal names its own parts with change
// identifiers (a lane prefix and a letter or number) and with bare build
// steps (an S and one or two digits). Those labels mean nothing once the
// proposal is closed, so a comment carrying one tells a later reader
// nothing without that document open. The durable reference is a
// `// spec:` citation or a description of the behavior.
//
// The tree already carries such labels, so the gate is a per-file ratchet
// in the manner of the line-citation ratchet: each file is held at or below
// the count recorded in the baseline, a file absent from the baseline is
// held at zero, and a count that falls must be written back so the ratchet
// tightens. A comment rewrap that carries a label forward therefore fails
// once the label has been removed from its file, and a new label in any file
// fails at once.
//
// Only comment text is read. A label inside a string literal is data, such
// as the fixtures below, and a label in a testdata/ directory is fixture
// input rather than a comment on shipped code. The storage service name S3
// is a product name rather than a step label and is not counted.

// proposalLabelBaselinePath is the baseline the ratchet reads, relative to
// the repository root.
const proposalLabelBaselinePath = "tests/registers/proposal-label-comments.yaml"

// proposalLabelRoots are the Go trees whose comments the ratchet reads.
var proposalLabelRoots = []string{"cmd", "pkg", "sdks", "tests"}

// proposalLabelPattern matches a proposal change identifier or a bare build
// step. countCommentLabels passes over the S3 service name the step
// alternative also matches.
var proposalLabelPattern = regexp.MustCompile(`\b(?:(?:CODE|SPEC|SCHEMA|DOCS|CONF)-[A-Z0-9]+|S[0-9]{1,2})\b`)

// proposalLabelBaseline is the on-disk baseline document.
type proposalLabelBaseline struct {
	Kind    string         `yaml:"kind"`
	Version int            `yaml:"version"`
	Files   map[string]int `yaml:"files"`
}

// countCommentLabels returns how many proposal labels the comments of one Go
// source carry.
func countCommentLabels(src []byte) int {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var sc scanner.Scanner
	// Syntax errors are irrelevant to the count, so the handler is nil and
	// the scanner continues past them.
	sc.Init(file, src, nil, scanner.ScanComments)
	n := 0
	for {
		_, tok, lit := sc.Scan()
		if tok == token.EOF {
			return n
		}
		if tok != token.COMMENT {
			continue
		}
		for _, m := range proposalLabelPattern.FindAllString(lit, -1) {
			if m != "S3" {
				n++
			}
		}
	}
}

// countProposalLabels walks the named roots under repo and returns the
// per-file label counts, keyed by slash-separated path relative to repo.
// Files with no label are omitted. testdata/ and vendor/ are skipped.
func countProposalLabels(repo string, roots []string) (map[string]int, error) {
	counts := map[string]int{}
	for _, root := range roots {
		dir := filepath.Join(repo, root)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); name == "testdata" || name == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			if n := countCommentLabels(src); n > 0 {
				rel, err := filepath.Rel(repo, path)
				if err != nil {
					return fmt.Errorf("relativize %s: %w", path, err)
				}
				counts[filepath.ToSlash(rel)] = n
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", root, err)
		}
	}
	return counts, nil
}

// ratchetProposalLabels compares measured counts against the baseline and
// returns one violation per file whose count rose above, or fell below, its
// baseline.
func ratchetProposalLabels(measured, baseline map[string]int) []string {
	var out []string
	for path, n := range measured {
		if want := baseline[path]; n > want {
			out = append(out, fmt.Sprintf("%s: %d proposal label(s) in comments, baseline %d; "+
				"replace each with a spec citation or a description of the behavior", path, n, want))
		}
	}
	for path, want := range baseline {
		if n := measured[path]; n < want {
			out = append(out, fmt.Sprintf("%s: %d proposal label(s) in comments, baseline %d; "+
				"lower the baseline in %s so the ratchet holds the new count", path, n, want, proposalLabelBaselinePath))
		}
	}
	sort.Strings(out)
	return out
}

// loadProposalLabelBaseline reads and validates the baseline document.
func loadProposalLabelBaseline(t *testing.T, repo string) map[string]int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, proposalLabelBaselinePath))
	if err != nil {
		t.Fatalf("read the proposal-label baseline: %v", err)
	}
	var doc proposalLabelBaseline
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse the proposal-label baseline: %v", err)
	}
	if doc.Kind != "proposal-label-comment-baseline" || doc.Version != 1 {
		t.Fatalf("proposal-label baseline declares kind %q version %d, want proposal-label-comment-baseline version 1", doc.Kind, doc.Version)
	}
	return doc.Files
}

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
//
// No Go comment gains a proposal scaffolding label, and a file whose labels
// were removed cannot regrow them.
func TestGoCommentsCarryNoNewProposalLabels(t *testing.T) {
	repo := schematest.RepoRoot(t)
	measured, err := countProposalLabels(repo, proposalLabelRoots)
	if err != nil {
		t.Fatalf("count proposal labels: %v", err)
	}
	for _, v := range ratchetProposalLabels(measured, loadProposalLabelBaseline(t, repo)) {
		t.Error(v)
	}
}

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
//
// The counter reads comment text only, counts each lane-prefixed change
// identifier and each bare build step, and passes over the S3 service name,
// a label inside a string literal, and a testdata/ directory.
func TestProposalLabelCounterReadsCommentsOnly(t *testing.T) {
	repo := t.TempDir()
	files := map[string]string{
		"pkg/a/a.go":          "package a\n\n// It emits the report too (CODE-B). It pins the S11 wiring.\nvar x = 1\n",
		"pkg/a/b.go":          "package a\n\n// Objects land in S3.\nvar y = \"CODE-B S11\"\n",
		"pkg/a/c.go":          "package a\n\n/* SPEC-D and S4 */\nvar z = 1 // DOCS-A1\n",
		"pkg/a/testdata/d.go": "package d\n\n// CODE-A\n",
	}
	for rel, body := range files {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := countProposalLabels(repo, proposalLabelRoots)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	want := map[string]int{"pkg/a/a.go": 2, "pkg/a/c.go": 3}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("counts = %v, want %v", got, want)
	}
}

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
//
// The ratchet fails a file that gains a label, a file absent from the
// baseline that carries one, and a file whose count fell without the
// baseline being lowered; a file at its baseline passes.
func TestProposalLabelRatchetFailsARiseAndAnUnrecordedFall(t *testing.T) {
	baseline := map[string]int{"held.go": 2, "grew.go": 1, "fell.go": 3}
	measured := map[string]int{"held.go": 2, "grew.go": 2, "fell.go": 1, "new.go": 1}
	got := ratchetProposalLabels(measured, baseline)
	if len(got) != 3 {
		t.Fatalf("violations = %q, want one each for grew.go, fell.go and new.go", got)
	}
	for i, prefix := range []string{"fell.go:", "grew.go:", "new.go:"} {
		if !strings.HasPrefix(got[i], prefix) {
			t.Errorf("violation %d = %q, want prefix %q", i, got[i], prefix)
		}
	}
}
