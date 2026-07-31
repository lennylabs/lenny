// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/gate"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The line-citation ratchet counts the citations of the retired
// `§X(.Y)* line(s) L` form in every file inside the shared read domain
// and holds each file at or below the count measured when it landed. The
// resolver beside it reports a citation that stopped pointing inside the
// section it names; nothing there stops a new citation being written,
// and without the ratchet the retirement happens once and the population
// regrows.
//
// It runs here as a tier-0 Go test rather than as a linter rule, because
// the repository's lint invocation is downgraded to a warning and a
// check whose script is absent passes silently.
//
// Every fixture below holds its citations in testdata/, which sits
// outside the read domain of the resolver, the ratchet, and the residual
// scan. A fixture citation is input to a gate rather than a pointer into
// the specification, and its route out of the population is the deletion
// of the case, so a per-file count seeded for it would never fall.

// lineCitationFixtures is the fixture root for the cases below.
const lineCitationFixtures = "testdata/line-citations"

// ratchetTree is a fixture tracked tree the gate runs over. It holds
// whichever carriers a case writes and the baseline at the path the gate
// reads.
type ratchetTree struct {
	t    *testing.T
	root string
}

// newRatchetTree returns an empty fixture tree.
func newRatchetTree(t *testing.T) *ratchetTree {
	t.Helper()
	return &ratchetTree{t: t, root: t.TempDir()}
}

// carrier writes a carrier fixture into the tree at the given path.
func (tr *ratchetTree) carrier(target, fixture string) {
	tr.t.Helper()
	tr.copy(target, filepath.Join(lineCitationFixtures, "carriers", fixture))
}

// spelling writes a fixture carrying one citation in one spelling of the
// retired form.
func (tr *ratchetTree) spelling(target, group, fixture string) {
	tr.t.Helper()
	tr.copy(target, filepath.Join(lineCitationFixtures, group, fixture))
}

// baseline writes a baseline fixture at the path the gate reads.
func (tr *ratchetTree) baseline(fixture string) {
	tr.t.Helper()
	tr.copy(gate.RatchetBaselinePath, filepath.Join(lineCitationFixtures, "baselines", fixture))
}

// removeBaseline deletes the baseline from the tree.
func (tr *ratchetTree) removeBaseline() {
	tr.t.Helper()
	if err := os.Remove(filepath.Join(tr.root, filepath.FromSlash(gate.RatchetBaselinePath))); err != nil {
		tr.t.Fatalf("remove the fixture baseline: %v", err)
	}
}

// copy writes a fixture file into the tree.
func (tr *ratchetTree) copy(target, fixture string) {
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
func (tr *ratchetTree) baselineText() string {
	tr.t.Helper()
	body, err := os.ReadFile(filepath.Join(tr.root, filepath.FromSlash(gate.RatchetBaselinePath)))
	if err != nil {
		tr.t.Fatalf("read the fixture baseline: %v", err)
	}
	return string(body)
}

// run drives the gate over the fixture tree.
func (tr *ratchetTree) run() (gate.RatchetReport, error) {
	tr.t.Helper()
	g := gate.NewRatchetOver(
		scope.DirLister(tr.root), scope.DirReader(tr.root), scope.DirWriter(tr.root),
	)
	return g.Run(context.Background())
}

// runOK drives the gate and fails the test when the run could not
// inspect the tree.
func (tr *ratchetTree) runOK() gate.RatchetReport {
	tr.t.Helper()
	rep, err := tr.run()
	if err != nil {
		tr.t.Fatalf("run the line-citation ratchet over the fixture tree: %v", err)
	}
	return rep
}

// ratchetFailureTexts renders a report's failures for an assertion
// message.
func ratchetFailureTexts(rep gate.RatchetReport) []string {
	out := make([]string, 0, len(rep.Failures))
	for _, f := range rep.Failures {
		out = append(out, f.String())
	}
	return out
}

// spec: §28.1 (N8, the citation rule: a specification citation names a
// heading rather than a line, so the population of line citations does
// not grow)
func TestLineCitationRatchetCertifiesTheTree(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	rep, err := gate.NewRatchet(root).Run(context.Background())
	if err != nil {
		t.Fatalf("run the line-citation ratchet over the tracked tree: %v", err)
	}
	if len(rep.Failures) > 0 {
		t.Errorf("%d file(s) carry more line citations than %s allows:\n  %s",
			len(rep.Failures), gate.RatchetBaselinePath,
			strings.Join(ratchetFailureTexts(rep), "\n  "))
	}
	// A walk that read a fraction of the tree reports zero failures, so
	// the file count is asserted rather than taken as a clean tree. The
	// citation count and the baseline count are not asserted to be
	// non-zero: both reach zero when the last citation is retired, which
	// is the end state the migration drives toward.
	if rep.Files == 0 {
		t.Errorf("the ratchet inspected no file; a report of no failure over an unread tree certifies nothing")
	}
	// Every count the baseline carried is accounted for by the run,
	// either held, raised, or lowered. The identity holds at zero, so it
	// also holds once the population empties.
	if rep.Held+rep.Raised+len(rep.Lowered) != rep.Carried {
		t.Errorf("the baseline carried %d count(s), of which %d held, %d rose, and %d fell; "+
			"the run accounted for a different number than it loaded",
			rep.Carried, rep.Held, rep.Raised, len(rep.Lowered))
	}
	t.Logf("%d file(s) in the read domain, %d citation(s) read, %d count(s) carried, %d held, %d raised, %d lowered",
		rep.Files, rep.Citations, rep.Carried, rep.Held, rep.Raised, len(rep.Lowered))
	// A run that lowered a count rewrote the baseline, so the run names
	// the files whose counts it wrote back.
	for _, lowered := range rep.Lowered {
		t.Logf("lowered %s", lowered)
	}
}

// spec: §28.1 (N8, the citation rule: a file whose line-citation count
// rises is reported, so the population cannot regrow)
func TestLineCitationRatchetFailsOnACountThatRises(t *testing.T) {
	t.Parallel()
	tr := newRatchetTree(t)
	tr.carrier("pkg/carrier/carrier.go", "three.txt")
	tr.baseline("carrier-two.yaml")

	rep := tr.runOK()
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s), want the file whose count rose: %s",
			len(rep.Failures), strings.Join(ratchetFailureTexts(rep), "; "))
	}
	got := rep.Failures[0]
	if got.Path != "pkg/carrier/carrier.go" {
		t.Errorf("the failure names %q, want the carrier pkg/carrier/carrier.go", got.Path)
	}
	if got.Absent || got.Registered != 2 || got.Counted != 3 {
		t.Errorf("the failure reads %+v, want the rise from the registered 2 to the counted 3", got)
	}
	if rendered := got.String(); !strings.Contains(rendered, "pkg/carrier/carrier.go") ||
		!strings.Contains(rendered, "3") || !strings.Contains(rendered, "2") {
		t.Errorf("the rendered failure %q does not name the file and both counts", rendered)
	}
	if body := tr.baselineText(); !strings.Contains(body, "count: 2") {
		t.Errorf("the baseline no longer carries the count it was seeded with:\n%s", body)
	}
}

// spec: §28.1 (N8, the citation rule: a retirement is recorded, so a
// count that falls is written back and cannot be undone)
func TestLineCitationRatchetRewritesACountThatFallsAndFailsItsReturn(t *testing.T) {
	t.Parallel()
	tr := newRatchetTree(t)
	tr.carrier("pkg/carrier/carrier.go", "one.txt")
	tr.baseline("carrier-two.yaml")

	fell := tr.runOK()
	if len(fell.Failures) != 0 {
		t.Fatalf("a count that fell was reported as a failure: %s", strings.Join(ratchetFailureTexts(fell), "; "))
	}
	if len(fell.Lowered) != 1 || fell.Lowered[0].Path != "pkg/carrier/carrier.go" || fell.Lowered[0].Count != 1 {
		t.Fatalf("the run lowered %v, want pkg/carrier/carrier.go to 1", fell.Lowered)
	}
	if body := tr.baselineText(); !strings.Contains(body, "count: 1") || strings.Contains(body, "count: 2") {
		t.Fatalf("the baseline was not rewritten down to the count the run read:\n%s", body)
	}
	held := tr.runOK()
	if len(held.Failures) != 0 || len(held.Lowered) != 0 || held.Held != 1 {
		t.Fatalf("the second run reported %d failure(s), lowered %d count(s), and held %d, want a single held count",
			len(held.Failures), len(held.Lowered), held.Held)
	}

	// The file returns to the count it carried before the retirement.
	// Without the downward rewrite the gate would stay green here.
	tr.carrier("pkg/carrier/carrier.go", "two.txt")
	returned := tr.runOK()
	if len(returned.Failures) != 1 {
		t.Fatalf("reported %d failure(s) on the return to the old count, want 1: %s",
			len(returned.Failures), strings.Join(ratchetFailureTexts(returned), "; "))
	}
	if returned.Failures[0].Registered != 1 || returned.Failures[0].Counted != 2 {
		t.Errorf("the failure reads %+v, want the rise from the rewritten 1 to the counted 2", returned.Failures[0])
	}
}

// spec: §28.1 (N8, the citation rule: a file the baseline carries no
// count for fails on its first line citation)
func TestLineCitationRatchetFailsOnAFirstCitationInAnUnregisteredFile(t *testing.T) {
	t.Parallel()
	tr := newRatchetTree(t)
	tr.carrier("pkg/carrier/carrier.go", "one.txt")
	tr.baseline("other-file.yaml")

	rep := tr.runOK()
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s), want the file the baseline carries no count for: %s",
			len(rep.Failures), strings.Join(ratchetFailureTexts(rep), "; "))
	}
	got := rep.Failures[0]
	if got.Path != "pkg/carrier/carrier.go" || !got.Absent || got.Counted != 1 {
		t.Errorf("the failure reads %+v, want the first citation of an unregistered carrier", got)
	}
	if !strings.Contains(got.String(), gate.RatchetBaselinePath) {
		t.Errorf("the rendered failure %q does not name the baseline", got.String())
	}
}

// spec: §28.1 (N8, the citation rule: the prohibition covers every
// spelling of the retired form, so no spelling regrows after retirement)
func TestLineCitationRatchetFailsAtCountZeroOnEverySpelling(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"singular.txt",
		"range.txt",
		"section-level.txt",
		"comma-list.txt",
		"slash-continuation.txt",
		"plus-continuation-with-glosses.txt",
		"qualified.txt",
		"endash-range.txt",
		"path-form.txt",
		"colon-section.txt",
		"colon-path.txt",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr := newRatchetTree(t)
			tr.spelling("pkg/carrier/carrier.go", "spellings", name)
			tr.baseline("empty.yaml")

			rep := tr.runOK()
			if len(rep.Failures) != 1 {
				t.Fatalf("reported %d failure(s) for a file at count zero, want 1: %s",
					len(rep.Failures), strings.Join(ratchetFailureTexts(rep), "; "))
			}
			// The members of one citation count as one citation. A
			// matcher that stopped at the first separator would leave
			// the rest as orphan integers a retirement never reaches.
			if rep.Failures[0].Counted != 1 || rep.Citations != 1 {
				t.Errorf("counted %d citation(s) in the file and %d in the run, want the spelling read as one citation",
					rep.Failures[0].Counted, rep.Citations)
			}
		})
	}
}

// spec: §28.1 (N8, the citation rule: a citation wrapped across two
// comment lines is one citation, in every wrap position and every
// carrier dialect)
func TestLineCitationRatchetFailsAtCountZeroOnAWrappedCitation(t *testing.T) {
	t.Parallel()
	for _, position := range []string{"reference-keyword", "keyword-member", "member-list"} {
		for _, dialect := range []string{"slash", "hash", "block", "dash"} {
			name := fmt.Sprintf("%s-%s.txt", position, dialect)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				tr := newRatchetTree(t)
				tr.spelling("pkg/carrier/carrier.go", "wrapped", name)
				tr.baseline("empty.yaml")

				rep := tr.runOK()
				if len(rep.Failures) != 1 {
					t.Fatalf("reported %d failure(s) for a file at count zero, want 1: %s",
						len(rep.Failures), strings.Join(ratchetFailureTexts(rep), "; "))
				}
				if rep.Failures[0].Counted != 1 || rep.Citations != 1 {
					t.Errorf("counted %d citation(s) in the file and %d in the run, want the wrapped citation read as one",
						rep.Failures[0].Counted, rep.Citations)
				}
			})
		}
	}
}

// spec: §28.1 (N8, the citation rule: a per-file count belongs to the
// file it was measured over, so a rename moves the key with the file)
func TestLineCitationRatchetFollowsARenamedFile(t *testing.T) {
	t.Parallel()
	moved := newRatchetTree(t)
	moved.carrier("pkg/carrier/renamed.go", "two.txt")
	moved.baseline("renamed-two.yaml")
	rep := moved.runOK()
	if len(rep.Failures) != 0 {
		t.Errorf("the rekeyed count did not carry the renamed file: %s", strings.Join(ratchetFailureTexts(rep), "; "))
	}
	if rep.Held != 1 || len(rep.Lowered) != 0 {
		t.Errorf("the run held %d count(s) and lowered %d, want the unchanged count held under the new path",
			rep.Held, len(rep.Lowered))
	}

	// The same baseline against the pre-rename path: the key did not
	// move, so the file is read as a new one at its first citation.
	stayed := newRatchetTree(t)
	stayed.carrier("pkg/carrier/carrier.go", "two.txt")
	stayed.baseline("renamed-two.yaml")
	unmoved := stayed.runOK()
	if len(unmoved.Failures) != 1 || !unmoved.Failures[0].Absent {
		t.Fatalf("reported %v under the pre-rename path, want the file read as absent from the baseline",
			ratchetFailureTexts(unmoved))
	}
}

// spec: §28.1 (N8, the citation rule: the ratchet's own baseline is
// outside the domain it reads, so the text it holds is not tree content)
func TestLineCitationRatchetDoesNotReadItsOwnBaseline(t *testing.T) {
	t.Parallel()
	tr := newRatchetTree(t)
	tr.carrier("pkg/carrier/carrier.go", "two.txt")
	tr.baseline("self-citing.yaml")

	rep := tr.runOK()
	if len(rep.Failures) != 0 {
		t.Fatalf("the run reported %d failure(s), want none: %s",
			len(rep.Failures), strings.Join(ratchetFailureTexts(rep), "; "))
	}
	if rep.Citations != 2 {
		t.Errorf("read %d citation(s), want the carrier's alone; the text inside %s was counted as tree content",
			rep.Citations, gate.RatchetBaselinePath)
	}
	if body := tr.baselineText(); strings.Contains(body, "path: "+gate.RatchetBaselinePath) {
		t.Errorf("the baseline carries a count for itself:\n%s", body)
	}
}

// spec: §28.1 (N8, the citation rule: a gate that cannot load its
// baseline fails rather than certifying the tree)
func TestLineCitationRatchetFailsOnAnUnloadableBaseline(t *testing.T) {
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
		{"the baseline carries a count of zero", "zero-count.yaml", false},
		{"the baseline is missing", "empty.yaml", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := newRatchetTree(t)
			tr.carrier("pkg/carrier/carrier.go", "two.txt")
			tr.baseline(tc.fixture)
			if tc.remove {
				tr.removeBaseline()
			}
			_, err := tr.run()
			if err == nil {
				t.Fatalf("the run certified the tree with an unloadable baseline")
			}
			if !strings.Contains(err.Error(), gate.RatchetBaselinePath) {
				t.Errorf("the error %q does not name the baseline", err)
			}
		})
	}
}

// spec: §28.1 (N8, the citation rule: a run that inspected nothing does
// not certify the tree)
func TestLineCitationRatchetFailsWhenTheWalkSelectsZeroFiles(t *testing.T) {
	t.Parallel()
	tr := newRatchetTree(t)
	// Every path in the tree is outside the read domain, so the walk
	// selects no file to count.
	tr.copy("proposals/0001_example.md", filepath.Join(lineCitationFixtures, "carriers", "two.txt"))
	tr.baseline("empty.yaml")

	_, err := tr.run()
	if err == nil {
		t.Fatalf("the run certified a tree over which it counted no file")
	}
	if !strings.Contains(err.Error(), "line-citation ratchet") {
		t.Errorf("the error %q does not name the ratchet", err)
	}
}

// spec: §28.1 (N8, the citation rule: once every citation of the retired
// form is gone the population is empty, which is the state the migration
// reaches rather than a failure)
func TestLineCitationRatchetAcceptsAnEmptiedPopulation(t *testing.T) {
	t.Parallel()
	tr := newRatchetTree(t)
	tr.carrier("pkg/carrier/carrier.go", "none.txt")
	tr.baseline("empty.yaml")

	rep := tr.runOK()
	if rep.Files == 0 {
		t.Fatalf("the walk selected no file, so the case does not exercise an emptied population")
	}
	if rep.Citations != 0 || rep.Carried != 0 {
		t.Fatalf("read %d citation(s) against %d carried count(s), want an emptied population",
			rep.Citations, rep.Carried)
	}
	if len(rep.Failures) != 0 {
		t.Errorf("an emptied population was reported as a failure: %s", strings.Join(ratchetFailureTexts(rep), "; "))
	}
}
