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
//	updating docs/adr/index.md, or a catalog entry lost its
//	file or spec reference. The catalog in index.md must stay
//	gap-free and in sync with the on-disk ADR files. "Planned"
//	entries reserve a number without a file; every other status
//	requires a matching NNNN-*.md file.
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

	// Collect ADR files: NNNN-<slug>.md.
	filePattern := regexp.MustCompile(`^([0-9]{4})-[a-z0-9-]+\.md$`)
	fileNums := map[int]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		base := filepath.Base(path)
		m := filePattern.FindStringSubmatch(base)
		if m == nil {
			return nil
		}
		n, _ := strconv.Atoi(m[1])
		if prev, dup := fileNums[n]; dup {
			t.Errorf("duplicate ADR file number %04d: %s and %s", n, prev, base)
		}
		fileNums[n] = base
		return nil
	})
	if err != nil {
		t.Fatalf("walk ADR dir: %v", err)
	}

	indexBody, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	index := string(indexBody)

	// Every ADR file is referenced from the index (allowing either
	// the .md or the Jekyll-rendered .html target).
	for _, f := range fileNums {
		stem := strings.TrimSuffix(f, ".md")
		if !strings.Contains(index, stem+".md") && !strings.Contains(index, stem+".html") {
			t.Errorf("ADR %s not referenced from %s", f, indexPath)
		}
	}

	// Every index link target resolves to an extant file.
	linkRe := regexp.MustCompile(`\(([0-9]{4}-[a-z0-9-]+)\.(?:md|html)\)`)
	for _, m := range linkRe.FindAllStringSubmatch(index, -1) {
		if _, statErr := os.Stat(filepath.Join(dir, m[1]+".md")); statErr != nil {
			t.Errorf("index.md references missing file %s.md", m[1])
		}
	}

	// Parse the catalog: table rows in index.md that name an ADR
	// number and a status. "Planned" entries reserve a number
	// without a file; written entries (Accepted, Proposed,
	// Deprecated, Superseded) must have a file. This is the
	// reserved-numbering model documented in index.md and in
	// TESTING.md §12.11 #4.
	adrRe := regexp.MustCompile(`ADR-([0-9]{4})`)
	statusRe := regexp.MustCompile(`\b(Planned|Proposed|Accepted|Deprecated|Superseded)\b`)
	catalog := map[int]bool{}
	catNums := []int{}
	for _, line := range strings.Split(index, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		am := adrRe.FindStringSubmatch(line)
		sm := statusRe.FindStringSubmatch(line)
		if am == nil || sm == nil {
			continue
		}
		n, _ := strconv.Atoi(am[1])
		if catalog[n] {
			t.Errorf("duplicate ADR number %04d in catalog", n)
			continue
		}
		catalog[n] = true
		catNums = append(catNums, n)
		if sm[1] == "Planned" {
			if !strings.Contains(line, "[§") {
				t.Errorf("ADR-%04d is Planned but has no spec reference in the catalog row", n)
			}
			continue
		}
		if _, ok := fileNums[n]; !ok {
			t.Errorf("ADR-%04d is %s in the catalog but has no docs/adr/%04d-*.md file", n, sm[1], n)
		}
	}

	if len(catNums) == 0 {
		t.Skip("no ADR catalog entries present yet")
	}

	// Every ADR file appears in the catalog.
	for n, f := range fileNums {
		if !catalog[n] {
			t.Errorf("ADR file %s has no catalog entry in index.md", f)
		}
	}

	// Catalog numbering is gap-free.
	sort.Ints(catNums)
	for i, n := range catNums {
		if i > 0 && n != catNums[i-1]+1 {
			t.Errorf("ADR catalog gap: after %04d expected %04d but found %04d",
				catNums[i-1], catNums[i-1]+1, n)
		}
	}
}
