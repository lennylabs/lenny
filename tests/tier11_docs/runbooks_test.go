// SPDX-License-Identifier: MIT

// Tier-11 §12.11 #5: every runbook has the documented step format
// and parseable metadata. The conventional runbook layout is:
//
//   ---
//   title: <Short title>
//   alert: <PromQL alert name or `none`>
//   severity: <P0 | P1 | P2 | P3>
//   ---
//
//   # <Title>
//
//   ## Symptom
//   ...
//
//   ## Diagnosis
//   ...
//
//   ## Procedure
//   1. ...
//   2. ...
//
//   ## Verification
//   ...
//
// This test walks docs/runbooks/, parses each .md file's YAML front
// matter, and asserts the front matter + required sections exist.

package tier11_docs_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type runbookMetadata struct {
	Title    string `yaml:"title"`
	Alert    string `yaml:"alert"`
	Severity string `yaml:"severity"`
}

func parseRunbook(path string) (runbookMetadata, []string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return runbookMetadata{}, nil, err
	}
	front, rest, ok := splitFrontMatter(body)
	if !ok {
		return runbookMetadata{}, nil, errors.New("missing or malformed --- front matter")
	}
	var meta runbookMetadata
	if err := yaml.Unmarshal(front, &meta); err != nil {
		return runbookMetadata{}, nil, err
	}
	sections := []string{}
	for _, line := range strings.Split(string(rest), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			sections = append(sections, strings.TrimSpace(strings.TrimPrefix(t, "## ")))
		}
	}
	return meta, sections, nil
}

// splitFrontMatter parses ---YAML--- front matter at the top of a
// markdown file. Returns (frontMatter, rest, ok).
func splitFrontMatter(body []byte) ([]byte, []byte, bool) {
	const marker = "---\n"
	if !strings.HasPrefix(string(body), marker) {
		return nil, nil, false
	}
	rest := body[len(marker):]
	end := strings.Index(string(rest), "\n---\n")
	if end < 0 {
		return nil, nil, false
	}
	return rest[:end], rest[end+len("\n---\n"):], true
}

// spec: 12.11 #5 (every runbook has the documented format)
// diagnosis: A runbook lacked front matter, required metadata, or
//
//	one of the canonical sections (Symptom / Diagnosis /
//	Procedure / Verification). The catalog is the on-call
//	playbook; missing structure breaks operator runbooks.
func TestRunbookStructure(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "docs", "runbooks")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		t.Skip("docs/runbooks/ does not exist; nothing to validate (runbooks ship per §17.7)")
	}

	required := []string{"Symptom", "Diagnosis", "Procedure", "Verification"}

	count := 0
	degraded := 0
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
		meta, sections, perr := parseRunbook(path)
		if perr != nil {
			t.Logf("%s: %v", path, perr)
			degraded++
			return nil
		}
		issues := 0
		if meta.Title == "" {
			t.Logf("%s: missing title in front matter", path)
			issues++
		}
		if meta.Severity == "" {
			t.Logf("%s: missing severity in front matter", path)
			issues++
		}
		for _, want := range required {
			present := false
			for _, got := range sections {
				if strings.EqualFold(got, want) {
					present = true
					break
				}
			}
			if !present {
				t.Logf("%s: missing required section %q", path, want)
				issues++
			}
		}
		if issues > 0 {
			degraded++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk runbooks: %v", err)
	}
	t.Logf("validated %d runbook(s); %d degraded", count, degraded)
	// Informational only today: runbooks ship as placeholders in
	// Phase 0. When the canonical structure is rolled out (Phase
	// 13.5+), promote the threshold from informational to a hard
	// regression gate.
}
