// SPDX-License-Identifier: MIT

package annotation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// ratchetTree is a fixture tree the ratchet runs over.
type ratchetTree struct {
	t    *testing.T
	root string
}

func newRatchetTree(t *testing.T) *ratchetTree {
	t.Helper()
	return &ratchetTree{t: t, root: t.TempDir()}
}

// carrier writes a carrier fixture at the given path.
func (tr *ratchetTree) carrier(target, fixture string) {
	tr.t.Helper()
	tr.write(target, readFixture(tr.t, "ratchet", fixture))
}

// register writes the register with the given counts.
func (tr *ratchetTree) register(counts ...PackageCount) {
	tr.t.Helper()
	if err := os.MkdirAll(filepath.Join(tr.root, "tests", "registers"), 0o755); err != nil {
		tr.t.Fatalf("create the register directory: %v", err)
	}
	if err := WriteRatchetBaseline(scope.DirWriter(tr.root), RatchetBaselinePath, counts); err != nil {
		tr.t.Fatalf("seed the register: %v", err)
	}
}

// write writes raw content at the given path.
func (tr *ratchetTree) write(target string, body []byte) {
	tr.t.Helper()
	full := filepath.Join(tr.root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		tr.t.Fatalf("create the directory for %s: %v", target, err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		tr.t.Fatalf("write %s: %v", target, err)
	}
}

// registerText returns the register as it stands.
func (tr *ratchetTree) registerText() string {
	tr.t.Helper()
	body, err := os.ReadFile(filepath.Join(tr.root, filepath.FromSlash(RatchetBaselinePath)))
	if err != nil {
		tr.t.Fatalf("read the register: %v", err)
	}
	return string(body)
}

func (tr *ratchetTree) run() (RatchetReport, error) {
	return NewRatchetOver(scope.DirLister(tr.root), scope.DirReader(tr.root), scope.DirWriter(tr.root)).Run(context.Background())
}

func (tr *ratchetTree) runOK() RatchetReport {
	tr.t.Helper()
	rep, err := tr.run()
	if err != nil {
		tr.t.Fatalf("run the ratchet: %v", err)
	}
	return rep
}

func TestRatchetCountsEachPackageAcrossItsFiles(t *testing.T) {
	t.Parallel()
	tr := newRatchetTree(t)
	tr.carrier("pkg/carrier/a.go", "two.go.txt")
	tr.carrier("pkg/carrier/a_test.go", "three.go.txt")
	tr.carrier("pkg/other/b.go", "none.go.txt")
	tr.carrier("pkg/carrier/testdata/fixture.go", "three.go.txt")
	counted, files, err := CountSectionCitations(context.Background(), scope.DirLister(tr.root), scope.DirReader(tr.root))
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if files != 3 {
		t.Errorf("read %d Go file(s), want 3 with the testdata fixture left out", files)
	}
	if len(counted) != 1 || counted["pkg/carrier"] != 5 {
		t.Errorf("counted %v, want pkg/carrier at 5 and no entry for a package with identifiers alone", counted)
	}
}

func TestRatchetHoldsAnUnchangedCount(t *testing.T) {
	t.Parallel()
	tr := newRatchetTree(t)
	tr.carrier("pkg/carrier/a.go", "two.go.txt")
	tr.register(PackageCount{"pkg/carrier", 2})
	rep := tr.runOK()
	if len(rep.Failures) != 0 || len(rep.Lowered) != 0 || rep.Held != 1 || rep.Citations != 2 {
		t.Errorf("report %+v, want one held count of 2", rep)
	}
}

func TestRatchetFailsOnACountThatRises(t *testing.T) {
	t.Parallel()
	tr := newRatchetTree(t)
	tr.carrier("pkg/carrier/a.go", "three.go.txt")
	tr.register(PackageCount{"pkg/carrier", 2})
	rep := tr.runOK()
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s), want the rise", len(rep.Failures))
	}
	f := rep.Failures[0]
	if f.Package != "pkg/carrier" || f.Absent || f.Registered != 2 || f.Counted != 3 {
		t.Errorf("failure %+v, want the rise from 2 to 3", f)
	}
	if !strings.Contains(f.String(), RatchetBaselinePath) {
		t.Errorf("the failure %q does not name the register", f)
	}
	if !strings.Contains(tr.registerText(), "count: 2") {
		t.Errorf("a rise was absorbed into the register:\n%s", tr.registerText())
	}
}

func TestRatchetFailsOnAPackageTheRegisterDoesNotCarry(t *testing.T) {
	t.Parallel()
	tr := newRatchetTree(t)
	tr.carrier("pkg/carrier/a.go", "two.go.txt")
	tr.register()
	rep := tr.runOK()
	if len(rep.Failures) != 1 || !rep.Failures[0].Absent || rep.Failures[0].Counted != 2 {
		t.Errorf("failures %v, want the unregistered package at 2", rep.Failures)
	}
}

func TestRatchetWritesAFallingCountBackAndRefusesItsReturn(t *testing.T) {
	t.Parallel()
	tr := newRatchetTree(t)
	tr.carrier("pkg/carrier/a.go", "two.go.txt")
	tr.carrier("pkg/gone/a.go", "two.go.txt")
	tr.register(PackageCount{"pkg/carrier", 3}, PackageCount{"pkg/gone", 2})
	if err := os.Remove(filepath.Join(tr.root, "pkg", "gone", "a.go")); err != nil {
		t.Fatal(err)
	}

	fell := tr.runOK()
	if len(fell.Failures) != 0 {
		t.Fatalf("a fall was reported as a failure: %v", fell.Failures)
	}
	want := []PackageCount{{"pkg/carrier", 2}, {"pkg/gone", 0}}
	if len(fell.Lowered) != 2 || fell.Lowered[0] != want[0] || fell.Lowered[1] != want[1] {
		t.Fatalf("lowered %v, want %v", fell.Lowered, want)
	}
	text := tr.registerText()
	if !strings.Contains(text, "count: 2") || strings.Contains(text, "count: 3") || strings.Contains(text, "pkg/gone") {
		t.Fatalf("the register was not rewritten down:\n%s", text)
	}

	tr.carrier("pkg/carrier/a.go", "three.go.txt")
	back := tr.runOK()
	if len(back.Failures) != 1 || back.Failures[0].Registered != 2 || back.Failures[0].Counted != 3 {
		t.Errorf("the return to the old count reported %v, want the rise from the lowered 2", back.Failures)
	}
}

func TestRatchetFailsOnAnUnloadableRegister(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"does not parse":         "kind: [",
		"declares another kind":  "kind: line-citation-count-baseline\nversion: 1\npackages: []\n",
		"declares other version": "kind: spec-section-citation-count-baseline\nversion: 2\npackages: []\n",
		"has no packages block":  "kind: spec-section-citation-count-baseline\nversion: 1\n",
		"declares one twice":     "kind: spec-section-citation-count-baseline\nversion: 1\npackages:\n  - {package: pkg/carrier, count: 2}\n  - {package: pkg/carrier, count: 2}\n",
		"carries a zero count":   "kind: spec-section-citation-count-baseline\nversion: 1\npackages:\n  - {package: pkg/carrier, count: 0}\n",
		"carries an empty name":  "kind: spec-section-citation-count-baseline\nversion: 1\npackages:\n  - {package: '', count: 1}\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr := newRatchetTree(t)
			tr.carrier("pkg/carrier/a.go", "two.go.txt")
			tr.write(RatchetBaselinePath, []byte(body))
			if _, err := tr.run(); err == nil || !strings.Contains(err.Error(), RatchetBaselinePath) {
				t.Errorf("run returned %v, want an error naming the register", err)
			}
		})
	}
	t.Run("is missing", func(t *testing.T) {
		t.Parallel()
		tr := newRatchetTree(t)
		tr.carrier("pkg/carrier/a.go", "two.go.txt")
		if _, err := tr.run(); err == nil {
			t.Error("the run certified the tree with no register")
		}
	})
}

func TestRatchetFailsWhenItReadsNoGoFile(t *testing.T) {
	t.Parallel()
	tr := newRatchetTree(t)
	tr.write("docs/readme.md", []byte("text\n"))
	tr.register()
	if _, err := tr.run(); err == nil {
		t.Error("the run certified a tree in which it read no Go file")
	}
}
