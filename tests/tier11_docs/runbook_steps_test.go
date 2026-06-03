// SPDX-License-Identifier: MIT

// Tier-11 §25.7: every bundled runbook exposes at least one structured
// step through GET /v1/admin/runbooks/{name}/steps. The step indexer
// (pkg/ops/runbooks.ParseSteps) extracts a step from each `### ` heading
// that carries one or more `<!-- access: -->` markers; a runbook without
// markers returns an empty `{"steps": []}` and defeats the §25.7
// "machine consumers parse" promise at line 3054. This test fails when a
// runbook produces no steps so the catalogue stays machine-parseable.
//
// spec: §25.7 lines 3032-3074 (access markers, /steps response). F-25.7.6.

package tier11_docs_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/runbooks"
)

func TestEveryRunbookProducesSteps_spec_25_7_3054(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "docs", "runbooks")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		t.Skip("docs/runbooks/ does not exist; nothing to validate (runbooks ship per §17.7)")
	}

	count := 0
	empty := []string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		base := filepath.Base(path)
		if base == "index.md" || base == "README.md" {
			return nil
		}
		count++
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Errorf("%s: %v", path, rerr)
			return nil
		}
		if len(runbooks.ParseSteps(body)) == 0 {
			empty = append(empty, base)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk runbooks: %v", err)
	}
	for _, name := range empty {
		t.Errorf("%s produces no structured steps: add `### ` step headings with `<!-- access: -->` markers (§25.7 line 3054)", name)
	}
	if count == 0 {
		t.Errorf("found no runbooks under %s; expected docs/runbooks to be populated", dir)
	}
	t.Logf("validated %d runbook(s); %d produced empty /steps", count, len(empty))
}
