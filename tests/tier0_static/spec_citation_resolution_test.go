// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/gate"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The citation resolver reads every citation of the retired
// `§X(.Y)* line(s) L` form inside the shared read domain and reports the
// ones that no longer point inside the section they name. It lands
// against a baseline of the citations that were already stale when it
// was written, so what it enforces from here is that the population does
// not grow and that a citation broken by a content move is reported
// rather than absorbed.
//
// Every fixture below holds its citations in testdata/, which sits
// outside the read domain of the resolver, the ratchet, and the residual
// scan. A fixture citation is input to a gate rather than a pointer into
// the specification: it names no section a retirement could correct, and
// its route out of the population is the deletion of the case, so a
// baseline entry seeded for it would never fall.

// citationResolutionFixtures is the fixture root for the cases below.
const citationResolutionFixtures = "testdata/citation-resolution"

// resolutionTree is a fixture tracked tree the gate runs over. It holds
// one specification file, whichever carriers a case writes, and the
// baseline at the path the gate reads.
type resolutionTree struct {
	t    *testing.T
	root string
}

// newResolutionTree returns a tree carrying the named specification
// fixture as spec/04_example.md.
func newResolutionTree(t *testing.T, specFixture string) *resolutionTree {
	t.Helper()
	tr := &resolutionTree{t: t, root: t.TempDir()}
	tr.copy("spec/04_example.md", filepath.Join(citationResolutionFixtures, "spec", specFixture))
	return tr
}

// carrier writes a carrier fixture into the tree at the given path.
func (tr *resolutionTree) carrier(target, fixture string) {
	tr.t.Helper()
	tr.copy(target, filepath.Join(citationResolutionFixtures, "carriers", fixture))
}

// baseline writes a baseline fixture at the path the gate reads.
func (tr *resolutionTree) baseline(fixture string) {
	tr.t.Helper()
	tr.copy(gate.ResolutionBaselinePath, filepath.Join(citationResolutionFixtures, "baselines", fixture))
}

// removeBaseline deletes the baseline from the tree.
func (tr *resolutionTree) removeBaseline() {
	tr.t.Helper()
	if err := os.Remove(filepath.Join(tr.root, filepath.FromSlash(gate.ResolutionBaselinePath))); err != nil {
		tr.t.Fatalf("remove the fixture baseline: %v", err)
	}
}

// copy writes a fixture file into the tree.
func (tr *resolutionTree) copy(target, fixture string) {
	tr.t.Helper()
	body, err := os.ReadFile(fixture)
	if err != nil {
		tr.t.Fatalf("read fixture %s: %v", fixture, err)
	}
	full := filepath.Join(tr.root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		tr.t.Fatalf("create the directory for %s: %v", target, err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		tr.t.Fatalf("write %s: %v", target, err)
	}
}

// baselineText returns the baseline as it stands in the tree.
func (tr *resolutionTree) baselineText() string {
	tr.t.Helper()
	body, err := os.ReadFile(filepath.Join(tr.root, filepath.FromSlash(gate.ResolutionBaselinePath)))
	if err != nil {
		tr.t.Fatalf("read the fixture baseline: %v", err)
	}
	return string(body)
}

// run drives the gate over the fixture tree.
func (tr *resolutionTree) run() (gate.ResolutionReport, error) {
	tr.t.Helper()
	g := gate.NewResolutionOver(
		scope.DirLister(tr.root), scope.DirReader(tr.root), scope.DirWriter(tr.root),
	)
	return g.Run(context.Background())
}

// runOK drives the gate and fails the test when the run could not
// inspect the tree.
func (tr *resolutionTree) runOK() gate.ResolutionReport {
	tr.t.Helper()
	rep, err := tr.run()
	if err != nil {
		tr.t.Fatalf("run the citation resolver over the fixture tree: %v", err)
	}
	return rep
}

// failureTexts renders a report's failures for an assertion message.
func failureTexts(rep gate.ResolutionReport) []string {
	out := make([]string, 0, len(rep.Failures))
	for _, f := range rep.Failures {
		out = append(out, f.String())
	}
	return out
}

// spec: §28.1 (N8, the citation rule: a specification citation names a
// heading rather than a line, and a citation left in the tree keeps
// resolving inside the section it names)
func TestSpecCitationResolutionCertifiesTheTree(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	rep, err := gate.NewResolution(root).Run(context.Background())
	if err != nil {
		t.Fatalf("run the citation resolver over the tracked tree: %v", err)
	}
	if len(rep.Failures) > 0 {
		t.Errorf("%d citation(s) resolve inside no section and are not in %s:\n  %s",
			len(rep.Failures), gate.ResolutionBaselinePath,
			strings.Join(failureTexts(rep), "\n  "))
	}
	// A walk that read a fraction of the tree reports zero failures, so
	// the file count is asserted rather than taken as a clean tree. The
	// citation count and the baseline count are not asserted to be
	// non-zero: both reach zero when every citation of the retired form
	// has been rewritten and the baseline has emptied, which is the end
	// state the migration drives toward rather than a defect.
	if rep.Files == 0 {
		t.Errorf("the resolver inspected no file; a report of no failure over an unread tree certifies nothing")
	}
	// Every entry the baseline carried is accounted for by the run, either
	// matched by a citation still in the tree or retired. The identity
	// holds at zero, so it also holds once the population empties.
	if rep.Matched+len(rep.Retired) != rep.Carried {
		t.Errorf("the baseline carried %d entry(ies), of which %d matched a citation and %d were retired; "+
			"the run accounted for a different number than it loaded",
			rep.Carried, rep.Matched, len(rep.Retired))
	}
	t.Logf("%d file(s) in the read domain, %d citation(s) read, %d non-resolving, %d occurrence(s) covered by %d carried entry(ies), %d matched, %d entry(ies) retired",
		rep.Files, rep.Citations, rep.NonResolving, rep.Baselined, rep.Carried, rep.Matched, len(rep.Retired))
}

// spec: §28.1 (N8, the citation rule: a citation that points inside the
// section it names is the resolving case the gate accepts)
func TestSpecCitationResolutionAcceptsAResolvingCitation(t *testing.T) {
	t.Parallel()
	tr := newResolutionTree(t, "04_example.md")
	tr.carrier("pkg/carrier/carrier.go", "resolving.txt")
	tr.baseline("empty.yaml")

	rep := tr.runOK()
	if rep.Citations != 1 {
		t.Fatalf("read %d citation(s) from the fixture tree, want 1", rep.Citations)
	}
	if len(rep.Failures) != 0 {
		t.Errorf("a citation inside the section it names was reported: %s", strings.Join(failureTexts(rep), "; "))
	}
}

// spec: §28.1 (N8, the citation rule: once every citation of the retired
// form is gone the population is empty, which is the state the migration
// reaches rather than a failure)
func TestSpecCitationResolutionAcceptsAnEmptiedPopulation(t *testing.T) {
	t.Parallel()
	// The tree holds the specification fixture and a carrier that writes
	// no citation, so the walk selects files while the read finds none.
	tr := newResolutionTree(t, "04_example.md")
	tr.carrier("pkg/carrier/carrier.go", "no-citation.txt")
	tr.baseline("empty.yaml")

	rep := tr.runOK()
	if rep.Files == 0 {
		t.Fatalf("the walk selected no file, so the case does not exercise an emptied population")
	}
	if rep.Citations != 0 || rep.Carried != 0 {
		t.Fatalf("read %d citation(s) against %d baseline entry(ies), want an emptied population",
			rep.Citations, rep.Carried)
	}
	if len(rep.Failures) != 0 {
		t.Errorf("an emptied population was reported as a failure: %s", strings.Join(failureTexts(rep), "; "))
	}
}

// spec: §28.1 (N8, the citation rule: a citation that no longer points
// inside the section it names is reported with its carrier)
func TestSpecCitationResolutionReportsFileLineAndTextOfAnUnbaselinedFailure(t *testing.T) {
	t.Parallel()
	tr := newResolutionTree(t, "04_example.md")
	tr.carrier("pkg/carrier/carrier.go", "nonresolving.txt")
	tr.baseline("empty.yaml")

	rep := tr.runOK()
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s), want 1: %s", len(rep.Failures), strings.Join(failureTexts(rep), "; "))
	}
	got := rep.Failures[0]
	if got.Path != "pkg/carrier/carrier.go" {
		t.Errorf("the failure names %q, want the carrier pkg/carrier/carrier.go", got.Path)
	}
	if got.Line != 2 {
		t.Errorf("the failure names line %d, want the line the citation is written on (2)", got.Line)
	}
	if !strings.Contains(got.Text, "line 15") {
		t.Errorf("the failure carries %q, which does not name the citation as written", got.Text)
	}
	if rendered := got.String(); !strings.Contains(rendered, "pkg/carrier/carrier.go") ||
		!strings.Contains(rendered, "line 2") || !strings.Contains(rendered, got.Text) {
		t.Errorf("the rendered failure %q does not name the file, the line, and the citation text", rendered)
	}
}

// spec: §28.1 (N8, the citation rule: an exemption is written for one
// citation in one file, so it does not travel to another file)
func TestSpecCitationResolutionBaselineEntryDoesNotTravelBetweenFiles(t *testing.T) {
	t.Parallel()
	same := newResolutionTree(t, "04_example.md")
	same.carrier("pkg/carrier/carrier.go", "nonresolving.txt")
	same.baseline("carrier-nonresolving.yaml")
	rep := same.runOK()
	if len(rep.Failures) != 0 {
		t.Errorf("the baselined citation was reported under its own file: %s", strings.Join(failureTexts(rep), "; "))
	}
	if rep.Baselined != 1 {
		t.Errorf("%d citation(s) matched the baseline, want 1", rep.Baselined)
	}

	other := newResolutionTree(t, "04_example.md")
	other.carrier("pkg/carrier/carrier.go", "nonresolving.txt")
	other.baseline("other-file.yaml")
	moved := other.runOK()
	if len(moved.Failures) != 1 {
		t.Fatalf("reported %d failure(s) for the same citation text under a file the baseline does not name, want 1: %s",
			len(moved.Failures), strings.Join(failureTexts(moved), "; "))
	}
	if moved.Failures[0].Path != "pkg/carrier/carrier.go" {
		t.Errorf("the failure names %q, want pkg/carrier/carrier.go", moved.Failures[0].Path)
	}
}

// spec: §28.1 (N8, the citation rule: a baseline entry belongs to the
// file it was written for, so a rename moves the entry with the file)
func TestSpecCitationResolutionBaselineEntryFollowsARenamedFile(t *testing.T) {
	t.Parallel()
	renamed := newResolutionTree(t, "04_example.md")
	renamed.carrier("pkg/carrier/renamed.go", "nonresolving.txt")
	renamed.baseline("renamed.yaml")
	rep := renamed.runOK()
	if len(rep.Failures) != 0 {
		t.Errorf("the rekeyed entry did not carry the citation under the new path: %s",
			strings.Join(failureTexts(rep), "; "))
	}

	old := newResolutionTree(t, "04_example.md")
	old.carrier("pkg/carrier/carrier.go", "nonresolving.txt")
	old.baseline("renamed.yaml")
	stayed := old.runOK()
	if len(stayed.Failures) != 1 {
		t.Fatalf("reported %d failure(s) under the pre-rename path, want 1: %s",
			len(stayed.Failures), strings.Join(failureTexts(stayed), "; "))
	}
}

// spec: §28.1 (N8, the citation rule: every member of a citation points
// inside the section the citation names)
func TestSpecCitationResolutionFailsUnlessEveryMemberResolves(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		fixture string
	}{
		{"one member of a list falls outside the section", "multi-member.txt"},
		{"a range runs past the end of the section", "range.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := newResolutionTree(t, "04_example.md")
			tr.carrier("pkg/carrier/carrier.go", tc.fixture)
			tr.baseline("empty.yaml")

			rep := tr.runOK()
			if rep.Citations != 1 {
				t.Fatalf("read %d citation(s), want the members read as one citation", rep.Citations)
			}
			if len(rep.Failures) != 1 {
				t.Fatalf("reported %d failure(s), want 1: %s",
					len(rep.Failures), strings.Join(failureTexts(rep), "; "))
			}
		})
	}
}

// spec: §28.1 (N8, the citation rule: a citation broken by a heading
// move is reported rather than absorbed by the baseline)
func TestSpecCitationResolutionReportsACitationBrokenByAHeadingMove(t *testing.T) {
	t.Parallel()
	before := newResolutionTree(t, "04_example.md")
	before.carrier("pkg/carrier/carrier.go", "heading-move.txt")
	before.baseline("empty.yaml")
	if rep := before.runOK(); len(rep.Failures) != 0 {
		t.Fatalf("the citation did not resolve before the heading moved: %s", strings.Join(failureTexts(rep), "; "))
	}

	after := newResolutionTree(t, "04_example-heading-moved.md")
	after.carrier("pkg/carrier/carrier.go", "heading-move.txt")
	after.baseline("carrier-nonresolving.yaml")
	rep := after.runOK()
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s) after the heading moved, want 1: %s",
			len(rep.Failures), strings.Join(failureTexts(rep), "; "))
	}
	if rep.Baselined != 0 {
		t.Errorf("%d citation(s) matched the baseline; an entry written for another citation absorbed this one",
			rep.Baselined)
	}
}

// spec: §28.1 (N8, the citation rule: an exemption does not outlive the
// citation it was written for)
func TestSpecCitationResolutionRetiresAnEntryWhoseCitationIsGone(t *testing.T) {
	t.Parallel()
	tr := newResolutionTree(t, "04_example.md")
	tr.carrier("pkg/carrier/carrier.go", "nonresolving.txt")
	tr.baseline("stale.yaml")

	rep := tr.runOK()
	if len(rep.Failures) != 0 {
		t.Fatalf("the run reported a failure: %s", strings.Join(failureTexts(rep), "; "))
	}
	if len(rep.Retired) != 1 {
		t.Fatalf("retired %d entry(ies), want the one whose citation is no longer in the tree", len(rep.Retired))
	}
	if !strings.Contains(rep.Retired[0].Text, "line 19") {
		t.Errorf("retired %q, want the entry whose citation the tree no longer carries", rep.Retired[0])
	}
	if body := tr.baselineText(); strings.Contains(body, "line 19") {
		t.Errorf("the rewritten baseline still carries the retired entry:\n%s", body)
	}
	second := tr.runOK()
	if len(second.Retired) != 0 || len(second.Failures) != 0 {
		t.Errorf("the second run retired %d entry(ies) and reported %d failure(s), want none of either",
			len(second.Retired), len(second.Failures))
	}
	if second.Baselined != 1 {
		t.Errorf("%d citation(s) matched the rewritten baseline, want the surviving entry to still carry its citation",
			second.Baselined)
	}
}

// spec: §28.1 (N8, the citation rule: an exemption does not outlive the
// citation it was written for, including on a run that reports another
// citation as a failure)
func TestSpecCitationResolutionRetiresAnEntryBesideAReportedFailure(t *testing.T) {
	t.Parallel()
	tr := newResolutionTree(t, "04_example.md")
	// The carrier's citation does not resolve and the baseline does not
	// carry it, so the run reports a failure. The entry the baseline does
	// carry names a file the tree does not hold, so its citation is gone.
	tr.carrier("pkg/carrier/other.go", "nonresolving.txt")
	tr.baseline("carrier-nonresolving.yaml")

	rep := tr.runOK()
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s), want the unbaselined citation: %s",
			len(rep.Failures), strings.Join(failureTexts(rep), "; "))
	}
	if len(rep.Retired) != 1 {
		t.Fatalf("retired %d entry(ies) on a run that reported a failure, want the entry whose citation is gone",
			len(rep.Retired))
	}
	if rep.Retired[0].Path != "pkg/carrier/carrier.go" {
		t.Errorf("retired %q, want the entry written for pkg/carrier/carrier.go", rep.Retired[0])
	}
	if body := tr.baselineText(); strings.Contains(body, "pkg/carrier/carrier.go") {
		t.Errorf("the baseline still carries the entry whose citation is gone:\n%s", body)
	}
}

// spec: §28.1 (N8, the citation rule: the resolver's own baseline is
// outside the domain it reads, so the copies of the citations it holds
// are not tree content)
func TestSpecCitationResolutionDoesNotReadItsOwnBaseline(t *testing.T) {
	t.Parallel()
	tr := newResolutionTree(t, "04_example.md")
	tr.carrier("pkg/carrier/carrier.go", "nonresolving.txt")
	tr.baseline("carrier-nonresolving.yaml")

	rep := tr.runOK()
	if len(rep.Failures) != 0 {
		t.Fatalf("the run reported %d failure(s), want none: %s",
			len(rep.Failures), strings.Join(failureTexts(rep), "; "))
	}
	if rep.Citations != 1 {
		t.Errorf("read %d citation(s), want the carrier's alone; the copy inside %s was read as tree content",
			rep.Citations, gate.ResolutionBaselinePath)
	}
}

// spec: §28.1 (N8, the citation rule: a gate that cannot load its
// baseline fails rather than certifying the tree)
func TestSpecCitationResolutionFailsOnAnUnloadableBaseline(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		fixture string
		remove  bool
	}{
		{"the baseline does not parse", "malformed.yaml", false},
		{"the baseline declares no files block", "no-files-block.yaml", false},
		{"the baseline declares another kind", "wrong-kind.yaml", false},
		{"the baseline declares one file twice", "duplicate-file.yaml", false},
		{"the baseline holds a file with no citation", "no-citation.yaml", false},
		{"the baseline is missing", "empty.yaml", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := newResolutionTree(t, "04_example.md")
			tr.carrier("pkg/carrier/carrier.go", "nonresolving.txt")
			tr.baseline(tc.fixture)
			if tc.remove {
				tr.removeBaseline()
			}
			_, err := tr.run()
			if err == nil {
				t.Fatalf("the run certified the tree with an unloadable baseline")
			}
			if !strings.Contains(err.Error(), gate.ResolutionBaselinePath) {
				t.Errorf("the error %q does not name the baseline", err)
			}
		})
	}
}

// spec: §28.1 (N8, the citation rule: a run that inspected nothing does
// not certify the tree)
func TestSpecCitationResolutionFailsWhenTheWalkSelectsZeroFiles(t *testing.T) {
	t.Parallel()
	tr := &resolutionTree{t: t, root: t.TempDir()}
	// Every path in the tree is outside the read domain, so the walk
	// selects no file to inspect.
	tr.copy("proposals/0001_example.md", filepath.Join(citationResolutionFixtures, "carriers", "nonresolving.txt"))
	tr.baseline("empty.yaml")

	_, err := tr.run()
	if err == nil {
		t.Fatalf("the run certified a tree over which it inspected no file")
	}
	if !strings.Contains(err.Error(), "citation resolver") {
		t.Errorf("the error %q does not name the resolver", err)
	}
}
