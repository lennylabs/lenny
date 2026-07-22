// SPDX-License-Identifier: MIT

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflowJob is the subset of a GitHub Actions job's shape this
// check reads: the "run:" script text of every step, concatenated.
type workflowJob struct {
	Steps []struct {
		Run string `yaml:"run"`
	} `yaml:"steps"`
}

type workflowDoc struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

// jobRunScripts returns every "run:" script string across every job
// and step in the given workflow file.
func jobRunScripts(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc workflowDoc
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var scripts []string
	for _, job := range doc.Jobs {
		for _, step := range job.Steps {
			if step.Run != "" {
				scripts = append(scripts, step.Run)
			}
		}
	}
	return scripts
}

// invokesReferenceCatalogConformance reports whether any run script
// invokes the conformance tier's reference-catalog subset, either
// directly (--tier conformance --subset reference-catalog) or via a
// group whose plan resolves to it (tiersForGroup in
// cmd/lenny-test/tiers.go maps the "nightly", "weekly", and
// "pre-release" groups to conformance/reference-catalog).
func invokesReferenceCatalogConformance(scripts []string) bool {
	for _, s := range scripts {
		hasConformanceTier := strings.Contains(s, "--tier conformance")
		hasReferenceCatalogSubset := strings.Contains(s, "reference-catalog")
		if hasConformanceTier && hasReferenceCatalogSubset {
			return true
		}
		if strings.Contains(s, "--group nightly") ||
			strings.Contains(s, "--group weekly") ||
			strings.Contains(s, "--group pre-release") {
			return true
		}
	}
	return false
}

// spec: TESTING.md §12.10 ("Cadence. Bundled runtimes: PR. Reference
// catalog: nightly. Third-party: invoked by `lenny-test conformance`
// per request.") and ("The nine reference runtimes ... run
// conformance on every nightly.").
//
// diagnosis: cmd/lenny-test/tiers.go's tiersForGroup maps the
// "nightly" (and "weekly", "pre-release") group's conformance tier to
// the "reference-catalog" subset, but that mapping is only reachable
// when a workflow actually invokes `--group nightly` or
// `--tier conformance --subset reference-catalog`. A failure here
// means no scheduled GitHub Actions workflow calls that subset, so
// the §26 reference-runtime conformance battery TESTING.md promises
// "every nightly" never runs in CI at any cadence. Fix by adding the
// invocation to .github/workflows/nightly.yml (the workflow that
// declares the nightly schedule trigger).
func TestNightlyWorkflowInvokesReferenceCatalogConformance(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, ".github", "workflows", "nightly.yml")
	scripts := jobRunScripts(t, path)
	if !invokesReferenceCatalogConformance(scripts) {
		t.Fatalf(
			"%s never invokes the conformance tier's reference-catalog subset "+
				"(no step runs `--tier conformance --subset reference-catalog` or "+
				"`--group nightly`); TESTING.md §12.10 requires the nine reference "+
				"runtimes to run conformance on every nightly, and tiers.go's "+
				"tiersForGroup(\"nightly\") plan is unreachable unless some workflow "+
				"step actually invokes it",
			path,
		)
	}
}
