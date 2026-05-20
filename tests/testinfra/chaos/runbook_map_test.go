// SPDX-License-Identifier: MIT

package chaos_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// runbookFrontMatter is the subset of docs/runbooks/<slug>.md front
// matter the map gate consults. A runbook with at least one entry in
// the triggers list is alert-driven and MUST be mapped to a chaos
// test in runbook-map.yaml; a runbook with `triggers: []` is
// operational (scheduled rotation, tier promotion) and is exempt.
type runbookFrontMatter struct {
	Triggers []struct {
		Alert    string `yaml:"alert"`
		Severity string `yaml:"severity"`
	} `yaml:"triggers"`
}

// parseRunbookFrontMatter reads a runbook's YAML front matter and
// returns whether the runbook is alert-driven (non-empty triggers).
// A runbook with malformed or absent front matter is treated as
// alert-driven so the missing-map error is surfaced for review.
func parseRunbookFrontMatter(path string) (alertDriven bool, err error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	const marker = "---\n"
	if !strings.HasPrefix(string(body), marker) {
		return true, nil
	}
	rest := body[len(marker):]
	end := strings.Index(string(rest), "\n---\n")
	if end < 0 {
		return true, nil
	}
	var fm runbookFrontMatter
	if err := yaml.Unmarshal(rest[:end], &fm); err != nil {
		return true, err
	}
	return len(fm.Triggers) > 0, nil
}

// spec: 12.8 (every alert-driven runbook has a chaos test)
// diagnosis: A runbook under docs/runbooks/ has no entry in
//
//	tests/tier8_chaos/runbook-map.yaml. Operational runbooks
//	(triggers: []) are exempt because they document scheduled
//	procedures (rotations, tier promotions) rather than incident
//	response, so there is no chaos scenario to map them to.
//	Every alert-driven runbook must be mapped so the chaos suite
//	exercises the failure mode the runbook addresses.
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

	type slugMeta struct {
		alertDriven bool
		path        string
	}
	slugs := map[string]slugMeta{}
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
		alertDriven, perr := parseRunbookFrontMatter(path)
		if perr != nil {
			t.Errorf("%s: parse front matter: %v", path, perr)
		}
		slugs[slug] = slugMeta{alertDriven: alertDriven, path: path}
		return nil
	})
	if err != nil {
		t.Fatalf("walk runbooks: %v", err)
	}

	missing := []string{}
	exempt := 0
	mapped := 0
	for slug, meta := range slugs {
		if _, ok := doc.Runbooks[slug]; ok {
			mapped++
			continue
		}
		if !meta.alertDriven {
			exempt++
			continue
		}
		missing = append(missing, slug)
	}
	sort.Strings(missing)
	// Alert-driven runbooks MUST be mapped to a chaos test in
	// runbook-map.yaml. Operational runbooks (triggers: []) remain
	// exempt because they document scheduled procedures rather than
	// incident response, so there is no chaos scenario to map them
	// to.
	for _, m := range missing {
		t.Errorf("alert-driven runbook %q has no entry in runbook-map.yaml", m)
	}
	t.Logf("runbooks=%d  mapped=%d  operational-exempt=%d  unmapped-alert-driven=%d",
		len(slugs), mapped, exempt, len(missing))

	// Every mapped slug MUST point at a runbook that exists.
	orphans := []string{}
	for slug := range doc.Runbooks {
		if _, ok := slugs[slug]; !ok {
			orphans = append(orphans, slug)
		}
	}
	sort.Strings(orphans)
	for _, o := range orphans {
		t.Errorf("runbook-map.yaml entry %q has no matching docs/runbooks/%s.md", o, o)
	}
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
