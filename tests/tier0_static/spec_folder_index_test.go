// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// Every folder of the specification tree has an index page generated from the
// code registry in spec/conventions.md by scripts/specindex/specindex.py. A
// reader descends from the root map through those pages, so an index that
// lists a page under an old summary or omits a page sends the reader the wrong
// way. These gates run the generator's tests and its check mode. The generator
// is contributor tooling for the layout spec/conventions.md states rather than
// a specified behavior of Lenny, so no requirement applies.

// specIndexToolTimeout bounds one run of the generator. It reads a few hundred
// small markdown files, so a run that reaches this bound is hung.
const specIndexToolTimeout = 120 * time.Second

func runSpecIndexTool(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := schematest.RepoRoot(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is not on PATH, so the specification index pages cannot be checked: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), specIndexToolTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// diagnosis: a rule of scripts/specindex/specindex.py no longer behaves as its
// tests state, so the index pages it writes would drop a page, a summary, or
// a hand-kept section.
func TestSpecIndexToolPassesItsTests(t *testing.T) {
	t.Parallel()
	out, err := runSpecIndexTool(t, "-m", "unittest", "discover", "-s", filepath.Join("scripts", "specindex"), "-p", "test_*.py")
	if err != nil {
		t.Fatalf("scripts/specindex tests failed: %v\n%s", err, out)
	}
}

// diagnosis: an index.md under spec/ no longer matches the code registry in
// spec/conventions.md or the titles of the pages it lists, or a folder has no
// index row in the registry. Run `python3 scripts/specindex/specindex.py` and
// commit the result.
func TestSpecFolderIndexesAreCurrent(t *testing.T) {
	t.Parallel()
	out, err := runSpecIndexTool(t, filepath.Join("scripts", "specindex", "specindex.py"), "--check")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
