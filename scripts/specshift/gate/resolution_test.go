// SPDX-License-Identifier: MIT

package gate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Every verbatim copy of the retired citation form these cases need sits
// in testdata/, which is outside the read domain of the resolver, the
// ratchet, and the residual scan. A copy in this Go source would name no
// section a retirement could correct, so no gate would ever see its
// population fall to zero.
const citationFixtures = "testdata/citations"

// specFixture is the one specification file the run-level cases resolve
// against. Its §4.1 runs from its heading to the next one, so a citation
// of a line past the end of the file cannot resolve inside it.
const specFixture = `## 4. Example Component

Prose in the section preamble.

### 4.1 First Subsection

Body of the first subsection.

### 4.2 Second Subsection

Body of the second subsection.
`

// fixture reads a citation fixture.
func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(citationFixtures, name))
	if err != nil {
		t.Fatalf("read citation fixture %s: %v", name, err)
	}
	return string(data)
}

// fixtureCitations returns the citation text of every citation a fixture
// carries, read through the same matcher the gate runs, so no case holds
// a copy of the form itself.
func fixtureCitations(t *testing.T, name string) []string {
	t.Helper()
	var out []string
	for _, c := range citation.FindIn(name, fixture(t, name)) {
		out = append(out, c.Text)
	}
	if len(out) == 0 {
		t.Fatalf("citation fixture %s carries no citation", name)
	}
	return out
}

// writeFile writes a repo-relative path into the tree at root.
func writeFile(t *testing.T, root, target, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("create the directory for %s: %v", target, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
}

// newTree returns a tree holding the specification fixture and the
// directory the baseline lives in, together with the gate over it.
func newTree(t *testing.T) (root string, g *Resolution) {
	t.Helper()
	root = t.TempDir()
	writeFile(t, root, "spec/04_example.md", specFixture)
	writeFile(t, root, ResolutionBaselinePath, "")
	return root, NewResolutionOver(scope.DirLister(root), scope.DirReader(root), scope.DirWriter(root))
}

// seed writes the baseline holding the given entries.
func seed(t *testing.T, root string, entries []ResolutionEntry) {
	t.Helper()
	if err := WriteResolutionBaseline(scope.DirWriter(root), ResolutionBaselinePath, entries); err != nil {
		t.Fatalf("seed the baseline: %v", err)
	}
}

// spec: §28.1 (N8, the citation rule: the baseline is keyed by the file
// a citation was written in and the citation as written, so one key
// carries one entry however many occurrences the resolver reported)
func TestWriteResolutionBaselineCollapsesRepeatsOfOneKey(t *testing.T) {
	t.Parallel()
	texts := fixtureCitations(t, "two-citations.txt")
	root, g := newTree(t)
	// A file that writes the same citation twice yields two occurrences
	// under one key, which is what a seeding call passes.
	seed(t, root, []ResolutionEntry{
		{Path: "pkg/carrier/carrier.go", Text: texts[0]},
		{Path: "pkg/carrier/carrier.go", Text: texts[1]},
		{Path: "pkg/carrier/carrier.go", Text: texts[0]},
		{Path: "docs/guide.md", Text: texts[0]},
	})

	carried, err := g.loadBaseline()
	if err != nil {
		t.Fatalf("the written baseline does not load: %v", err)
	}
	if len(carried) != 3 {
		t.Errorf("the written baseline carries %d entry(ies), want the three distinct keys", len(carried))
	}
	for _, want := range []ResolutionEntry{
		{Path: "pkg/carrier/carrier.go", Text: texts[0]},
		{Path: "pkg/carrier/carrier.go", Text: texts[1]},
		{Path: "docs/guide.md", Text: texts[0]},
	} {
		if !carried[want] {
			t.Errorf("the written baseline does not carry %s", want)
		}
	}
}

// spec: §28.1 (N8, the citation rule: an empty baseline is the state the
// retirement of the last citation reaches, and it loads rather than
// failing)
func TestWriteResolutionBaselineWritesALoadableEmptyFile(t *testing.T) {
	t.Parallel()
	root, g := newTree(t)
	seed(t, root, nil)

	carried, err := g.loadBaseline()
	if err != nil {
		t.Fatalf("an emptied baseline does not load: %v", err)
	}
	if len(carried) != 0 {
		t.Errorf("an emptied baseline loaded %d entry(ies)", len(carried))
	}
}

// spec: §28.1 (N8, the citation rule: a run accounts for every entry the
// baseline carried, either by a citation still in the tree or by
// retiring the entry)
func TestResolutionRunAccountsForEveryCarriedEntry(t *testing.T) {
	t.Parallel()
	root, g := newTree(t)
	// The carrier's citation does not resolve and the baseline carries it.
	// The second entry names a file the tree does not hold, so its
	// citation is gone and the run retires it.
	writeFile(t, root, "pkg/carrier/carrier.go", fixture(t, "carrier.txt"))
	carried := []ResolutionEntry{
		{Path: "pkg/carrier/carrier.go", Text: fixtureCitations(t, "carrier.txt")[0]},
		{Path: "pkg/gone/gone.go", Text: fixtureCitations(t, "gone.txt")[0]},
	}
	seed(t, root, carried)

	rep, err := g.Run(context.Background())
	if err != nil {
		t.Fatalf("run the resolver over the fixture tree: %v", err)
	}
	if len(rep.Failures) != 0 {
		t.Fatalf("the run reported %d failure(s), want none", len(rep.Failures))
	}
	if rep.Carried != len(carried) {
		t.Errorf("the report names %d carried entry(ies), want %d", rep.Carried, len(carried))
	}
	if rep.Matched+len(rep.Retired) != rep.Carried {
		t.Errorf("the run matched %d entry(ies) and retired %d, which does not account for the %d it loaded",
			rep.Matched, len(rep.Retired), rep.Carried)
	}
	if rep.Matched != 1 || len(rep.Retired) != 1 {
		t.Errorf("the run matched %d entry(ies) and retired %d, want one of each", rep.Matched, len(rep.Retired))
	}
}

// spec: §28.1 (N8, the citation rule: one baseline entry covers every
// occurrence of its citation text in the file it was written for)
func TestResolutionRunCountsOneEntryForRepeatedOccurrences(t *testing.T) {
	t.Parallel()
	root, g := newTree(t)
	writeFile(t, root, "pkg/carrier/carrier.go", fixture(t, "carrier-repeated.txt"))
	seed(t, root, []ResolutionEntry{
		{Path: "pkg/carrier/carrier.go", Text: fixtureCitations(t, "carrier-repeated.txt")[0]},
	})

	rep, err := g.Run(context.Background())
	if err != nil {
		t.Fatalf("run the resolver over the fixture tree: %v", err)
	}
	if len(rep.Failures) != 0 {
		t.Fatalf("the run reported %d failure(s), want none", len(rep.Failures))
	}
	if rep.Baselined != 2 {
		t.Errorf("%d occurrence(s) were covered by the baseline, want both", rep.Baselined)
	}
	if rep.Matched != 1 || rep.Carried != 1 {
		t.Errorf("the run matched %d of %d carried entry(ies), want the one entry that covers both occurrences",
			rep.Matched, rep.Carried)
	}
	if len(rep.Retired) != 0 {
		t.Errorf("retired %d entry(ies) whose citation the tree still carries", len(rep.Retired))
	}
}

// spec: §28.1 (N8, the citation rule: a tree that carries no citation of
// the retired form and an emptied baseline is the end state the
// migration reaches, and the resolver certifies it)
func TestResolutionRunAcceptsAnEmptiedPopulation(t *testing.T) {
	t.Parallel()
	root, g := newTree(t)
	writeFile(t, root, "pkg/carrier/carrier.go", "// The carrier names its section by heading.\n")
	seed(t, root, nil)

	rep, err := g.Run(context.Background())
	if err != nil {
		t.Fatalf("the resolver refused an emptied population: %v", err)
	}
	if rep.Files == 0 {
		t.Fatalf("the walk selected no file, so the case does not exercise an emptied population")
	}
	if rep.Citations != 0 || rep.Carried != 0 || len(rep.Failures) != 0 {
		t.Errorf("the run read %d citation(s) against %d carried entry(ies) and reported %d failure(s), "+
			"want an emptied population certified", rep.Citations, rep.Carried, len(rep.Failures))
	}
}
