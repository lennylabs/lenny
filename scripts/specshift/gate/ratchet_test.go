// SPDX-License-Identifier: MIT

package gate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// seedCounts writes the citation-count baseline holding the given
// counts, through the gate's own writer, so no case hand-assembles the
// file the loader reads.
func seedCounts(t *testing.T, root string, counts []RatchetCount) {
	t.Helper()
	if err := WriteRatchetBaseline(scope.DirWriter(root), RatchetBaselinePath, counts); err != nil {
		t.Fatalf("seed the citation-count baseline: %v", err)
	}
}

// ratchetRoot returns a fixture tree carrying the named citation fixture
// at pkg/carrier/carrier.go, together with the directory the baseline
// sits in.
func ratchetRoot(t *testing.T, fixtureName string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "pkg/carrier/carrier.go", fixture(t, fixtureName))
	writeFile(t, root, filepath.Dir(RatchetBaselinePath)+"/.keep", "")
	return root
}

// runRatchet drives the gate over the fixture tree.
func runRatchet(t *testing.T, root string) RatchetReport {
	t.Helper()
	rep, err := NewRatchetOver(scope.DirLister(root), scope.DirReader(root), scope.DirWriter(root)).
		Run(context.Background())
	if err != nil {
		t.Fatalf("run the ratchet over the fixture tree: %v", err)
	}
	return rep
}

// The writer is the one producer of the baseline, so the counts it
// writes are the counts the loader accepts. A file at zero holds no
// entry, and the loader rejects one, so the writer drops it rather than
// emitting a document its own loader fails.
func TestRatchetBaselineWriterDropsAZeroCount(t *testing.T) {
	t.Parallel()
	root := ratchetRoot(t, "two-citations.txt")
	seedCounts(t, root, []RatchetCount{
		{Path: "pkg/carrier/carrier.go", Count: 2},
		{Path: "pkg/carrier/retired.go", Count: 0},
	})
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(RatchetBaselinePath)))
	if err != nil {
		t.Fatalf("read the seeded baseline: %v", err)
	}
	if strings.Contains(string(body), "retired.go") {
		t.Errorf("the writer emitted an entry for a file at count zero:\n%s", body)
	}
	rep := runRatchet(t, root)
	if len(rep.Failures) != 0 || rep.Held != 1 {
		t.Errorf("the run reported %d failure(s) and held %d count(s) over the seeded baseline, want one held count",
			len(rep.Failures), rep.Held)
	}
}

// A caller that passed the same file twice would produce a document the
// loader rejects for declaring one file twice, so the writer reports it
// rather than writing it.
func TestRatchetBaselineWriterRejectsARepeatedFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	err := WriteRatchetBaseline(scope.DirWriter(root), RatchetBaselinePath, []RatchetCount{
		{Path: "pkg/carrier/carrier.go", Count: 2},
		{Path: "pkg/carrier/carrier.go", Count: 3},
	})
	if err == nil {
		t.Fatalf("the writer accepted one file written twice")
	}
	if !strings.Contains(err.Error(), "pkg/carrier/carrier.go") {
		t.Errorf("the error %q does not name the repeated file", err)
	}
}

// A gate with no writer configured cannot record a fall, and a run that
// reported the fall without writing it would let the file regrow to its
// old count while the gate stayed green.
func TestRatchetFailsToRecordAFallWithNoWriter(t *testing.T) {
	t.Parallel()
	root := ratchetRoot(t, "carrier.txt")
	seedCounts(t, root, []RatchetCount{{Path: "pkg/carrier/carrier.go", Count: 3}})
	g := NewRatchetOver(scope.DirLister(root), scope.DirReader(root), nil)
	if _, err := g.Run(context.Background()); err == nil {
		t.Fatalf("the run reported a fall it could not write back")
	}
}

// A run cancelled mid-scan reports the cancellation rather than the
// counts it had reached, which would read as a completed inspection.
func TestRatchetReportsACancelledScan(t *testing.T) {
	t.Parallel()
	root := ratchetRoot(t, "carrier.txt")
	seedCounts(t, root, []RatchetCount{{Path: "pkg/carrier/carrier.go", Count: 1}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := NewRatchetOver(scope.DirLister(root), scope.DirReader(root), scope.DirWriter(root))
	if _, err := g.Run(ctx); err == nil {
		t.Fatalf("the run certified the tree after its context was cancelled")
	}
}

// The gate over a git checkout carries the shared read domain, the
// tracked-tree lister, and the baseline path, so a caller cannot
// assemble one that reads a different population.
func TestNewRatchetCarriesTheSharedDomain(t *testing.T) {
	t.Parallel()
	g := NewRatchet(t.TempDir())
	if g.List == nil || g.Read == nil || g.Write == nil {
		t.Fatalf("the gate is missing a dependency: %+v", g)
	}
	if g.Baseline != RatchetBaselinePath {
		t.Errorf("the gate reads %q, want %q", g.Baseline, RatchetBaselinePath)
	}
}
