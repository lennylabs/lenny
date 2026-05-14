// SPDX-License-Identifier: MIT

package chaos_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// spec: 12.8 (every runbook has a chaos test)
// diagnosis: A runbook under docs/runbooks/ has no entry in
//
//	tests/tier8_chaos/runbook-map.yaml. Add the entry so the
//	chaos suite documents which test exercises the runbook.
//
// The test is informational (Logf) today: §17.7 hasn't shipped the
// runbook catalog yet, and most placeholder runbooks would flag.
// When the runbook structure check (tier-11) becomes hard, this
// check should follow.
func TestRunbookMapCoverage(t *testing.T) {
	root := repoRootCwd(t)
	runbookDir := filepath.Join(root, "docs", "runbooks")
	mapPath := filepath.Join(root, "tests", "tier8_chaos", "runbook-map.yaml")

	if _, err := os.Stat(runbookDir); errors.Is(err, fs.ErrNotExist) {
		t.Skip("docs/runbooks/ does not exist; nothing to validate")
	}
	if _, err := os.Stat(mapPath); errors.Is(err, fs.ErrNotExist) {
		t.Skip("tests/tier8_chaos/runbook-map.yaml not present")
	}

	body, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatalf("read map: %v", err)
	}
	var doc struct {
		Runbooks map[string]struct {
			Test     string `yaml:"test"`
			Severity string `yaml:"severity"`
			Coverage string `yaml:"coverage"`
		} `yaml:"runbooks"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse map: %v", err)
	}

	// Walk docs/runbooks/ for slugs. The slug is the filename
	// minus the .md extension.
	slugs := map[string]bool{}
	err = filepath.WalkDir(runbookDir, func(path string, d fs.DirEntry, err error) error {
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
		slug := strings.TrimSuffix(base, ".md")
		slugs[slug] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk runbooks: %v", err)
	}

	missing := []string{}
	for slug := range slugs {
		if _, ok := doc.Runbooks[slug]; !ok {
			missing = append(missing, slug)
		}
	}
	for _, m := range missing {
		t.Logf("runbook %s has no entry in runbook-map.yaml", m)
	}
	t.Logf("runbooks=%d  mapped=%d  unmapped=%d", len(slugs), len(slugs)-len(missing), len(missing))
}

// repoRootCwd is a non-test-tied resolver used here to avoid an
// import cycle with schematest. Duplicating the walk is cheap.
func repoRootCwd(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("no go.mod from %s", wd)
	return ""
}
