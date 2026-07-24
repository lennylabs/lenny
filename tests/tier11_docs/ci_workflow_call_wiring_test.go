// SPDX-License-Identifier: MIT

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflowCallerJob is the subset of a job's shape this check reads:
// a job-level `uses:` reference to another workflow file.
type workflowCallerJob struct {
	Uses string `yaml:"uses"`
}

// workflowCallerDoc is the subset of a workflow file's shape this
// check reads: its jobs, keyed by job id.
type workflowCallerDoc struct {
	Jobs map[string]workflowCallerJob `yaml:"jobs"`
}

// localWorkflowCallees returns the repo-relative `uses:` references
// (e.g. "./.github/workflows/pr.yml") that the workflow file at path
// makes to another workflow file in this repository, as opposed to a
// versioned external action reference.
func localWorkflowCallees(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc workflowCallerDoc
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	const prefix = "./.github/workflows/"
	var callees []string
	for _, job := range doc.Jobs {
		if strings.HasPrefix(job.Uses, prefix) {
			callees = append(callees, job.Uses)
		}
	}
	sort.Strings(callees)
	return callees
}

// declaresWorkflowCall reports whether the workflow file at path
// declares `workflow_call` in its top-level `on:` trigger block. The
// `on:` block may be a mapping (`on:\n  workflow_call:\n  push:`), a
// sequence (`on: [push, workflow_call]`), or (for a single-trigger
// file elsewhere in this repo) a bare scalar; this handles all three.
func declaresWorkflowCall(t *testing.T, path string) bool {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		On yaml.Node `yaml:"on"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	switch doc.On.Kind {
	case yaml.MappingNode:
		for i := 0; i < len(doc.On.Content); i += 2 {
			if doc.On.Content[i].Value == "workflow_call" {
				return true
			}
		}
	case yaml.SequenceNode:
		for _, item := range doc.On.Content {
			if item.Value == "workflow_call" {
				return true
			}
		}
	case yaml.ScalarNode:
		return doc.On.Value == "workflow_call"
	}
	return false
}

// spec: TESTING.md §20.9 ("Nightly pipeline ... 1. Full PR pipeline.")
// and §20.10 ("Weekly / pre-release pipeline ... 1. Full nightly
// pipeline."). The nightly, weekly, and pre-release workflows
// implement "full <lower pipeline>" by delegating to that pipeline's
// workflow file through a job-level `uses: ./.github/workflows/X.yml`
// reference. GitHub Actions requires the callee to declare
// `on: workflow_call:` for a local `uses:` job reference to validate.
//
// diagnosis: a failure here means some workflow under
// .github/workflows/ calls a sibling workflow via a local `uses:`
// reference whose target does not declare `workflow_call` in its
// `on:` block. GitHub Actions rejects that reference at
// workflow-validation time, so the calling job — and every job gated
// behind it — never runs in CI. Fix by adding `workflow_call:` to the
// callee's `on:` block.
func TestLocalWorkflowUsesReferencesDeclareWorkflowCall(t *testing.T) {
	root := repoRoot(t)
	workflowsDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(workflowsDir)
	if err != nil {
		t.Fatalf("read dir %s: %v", workflowsDir, err)
	}

	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		callerPath := filepath.Join(workflowsDir, entry.Name())
		for _, ref := range localWorkflowCallees(t, callerPath) {
			checked++
			calleePath := filepath.Join(root, strings.TrimPrefix(ref, "./"))
			if _, err := os.Stat(calleePath); err != nil {
				t.Fatalf("%s references %s via `uses:`, but %s does not exist",
					entry.Name(), ref, calleePath)
			}
			if !declaresWorkflowCall(t, calleePath) {
				t.Errorf(
					"%s calls %s via a local `uses:` reference, but %s's `on:` block "+
						"does not declare `workflow_call:`; GitHub Actions requires the "+
						"callee to declare workflow_call for a local `uses:` job reference "+
						"to validate, so this delegation job (and anything gated behind it) "+
						"cannot run in CI",
					entry.Name(), ref, filepath.Base(calleePath),
				)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no workflow under %s references another local workflow via `uses:`; "+
			"expected at least nightly.yml -> pr.yml, weekly.yml -> nightly.yml, and "+
			"pre-release.yml -> weekly.yml", workflowsDir)
	}
}
