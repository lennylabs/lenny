// SPDX-License-Identifier: MIT

// Tier-11 §12.11 #4: ADR catalog continuity. Mirrors
// scripts/check-adr-catalog.sh but exposes the check via Go test so
// it shows up in the tier verdict.

package tier11_docs_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// spec: 12.11 #4 (ADR catalog continuity)
// diagnosis: An ADR was renamed, deleted, or renumbered without
//
//	updating docs/adr/index.md. The catalog must stay in
//	sync with the on-disk files at every commit.
func TestADRCatalogContinuity(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "docs", "adr")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		t.Skip("docs/adr/ does not exist; nothing to validate")
	}
	indexPath := filepath.Join(dir, "index.md")
	if _, err := os.Stat(indexPath); errors.Is(err, fs.ErrNotExist) {
		t.Skip("docs/adr/index.md missing; ADR catalog has not been bootstrapped")
	}

	// Collect ADR files.
	pattern := regexp.MustCompile(`^([0-9]{4})-[a-z0-9-]+\.md$`)
	files := []string{}
	numbers := []int{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		base := filepath.Base(path)
		m := pattern.FindStringSubmatch(base)
		if m == nil {
			return nil
		}
		files = append(files, base)
		n, _ := strconv.Atoi(m[1])
		numbers = append(numbers, n)
		return nil
	})
	if err != nil {
		t.Fatalf("walk ADR dir: %v", err)
	}

	if len(files) == 0 {
		t.Skip("no ADRs present yet")
	}

	indexBody, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	index := string(indexBody)

	// Every ADR file is referenced from the index (allowing either
	// .md or Jekyll-rendered .html targets).
	for _, f := range files {
		stem := strings.TrimSuffix(f, ".md")
		if !strings.Contains(index, stem+".md") && !strings.Contains(index, stem+".html") {
			t.Errorf("ADR %s not referenced from %s", f, indexPath)
		}
	}

	// No duplicate numbers, no gaps.
	sort.Ints(numbers)
	for i, n := range numbers {
		if i > 0 && n == numbers[i-1] {
			t.Errorf("duplicate ADR number %04d", n)
		}
		if i > 0 && n != numbers[i-1]+1 {
			t.Errorf("ADR sequence gap: after %04d expected %04d but found %04d",
				numbers[i-1], numbers[i-1]+1, n)
		}
	}
}
