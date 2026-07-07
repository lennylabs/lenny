// SPDX-License-Identifier: MIT

// Tier-11 documentation checks. These tests are NOT under a build tag
// because they exercise the repository state directly — no external
// infrastructure required.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks upward from the test's working dir until it finds
// go.mod.
func repoRoot(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repoRoot: walked past filesystem root looking for go.mod")
		}
		dir = parent
	}
}

// Documentation lives under docs/; the repo MUST have a docs/
// directory and an index.md / README.md at the top of it.
func TestDocsDirectoryStructure(t *testing.T) {
	root := repoRoot(t)
	docs := filepath.Join(root, "docs")
	info, err := os.Stat(docs)
	if err != nil || !info.IsDir() {
		t.Fatalf("docs/ missing or not a directory: %v", err)
	}
	// Either index.md or README.md must exist at the top.
	hasIndex := fileExists(filepath.Join(docs, "index.md"))
	hasReadme := fileExists(filepath.Join(docs, "README.md"))
	if !hasIndex && !hasReadme {
		t.Errorf("docs/ must contain index.md or README.md")
	}
}

// TESTING.md must reference every test tier name we ship so the
// docs stay in sync with the harness.
func TestTESTINGmdMentionsEveryTier(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "TESTING.md"))
	if err != nil {
		t.Fatalf("read TESTING.md: %v", err)
	}
	content := string(b)
	for _, name := range []string{
		"static", "unit", "component", "contract", "integration",
		"e2e_kind", "e2e_cloud", "load_local", "load_kind", "chaos",
		"security", "conformance", "docs", "load_cloud",
	} {
		if !strings.Contains(content, name) {
			t.Errorf("TESTING.md missing reference to tier %q", name)
		}
	}
}

// spec/README.md should exist so contributors can find their way
// into the spec.
func TestSpecREADMEExists(t *testing.T) {
	root := repoRoot(t)
	readme := filepath.Join(root, "spec", "README.md")
	if !fileExists(readme) {
		t.Errorf("spec/README.md missing")
	}
}

// The top-level repo README points to TESTING.md and the spec.
func TestRepoREADMEReferencesSpecAndTesting(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "TESTING") && !strings.Contains(content, "testing") {
		t.Errorf("README.md should reference testing")
	}
	if !strings.Contains(content, "spec") {
		t.Errorf("README.md should reference the spec")
	}
}

// TESTING.md §13.10 names the directories that carry the Phase-5
// REST↔OpenAI contract fidelity coverage, and §13.15 names where the
// LLM-proxy request/response wire-shape coverage lives. A reader
// following either reference must land on a real test suite.
//
// spec: 13.10 (Phase 5 — ExternalAdapterRegistry + MCP/Completions/Open
// Responses + REST/MCP contract tests), 13.15 (Phase 5.8 — LLM Proxy +
// lenny-direct-mode-isolation admission webhook)
// diagnosis: TESTING.md names a contract or component test directory that
// does not exist on disk, or has silently regressed to the stale
// tests/tier3_contract/rest_openai_completions/ path this test was added
// to catch. A failure here means a reader following TESTING.md's Phase-5
// or Phase-5.8 "Test infrastructure to land" bullets lands on a missing
// path.
func TestTESTINGmdContractDirectoryReferencesResolve(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "TESTING.md"))
	if err != nil {
		t.Fatalf("read TESTING.md: %v", err)
	}
	content := string(b)

	for _, rel := range []string{
		"tests/tier3_contract/rest_openai_chat",
		"tests/tier3_contract/rest_openai_responses",
		"tests/tier2_component/translators",
	} {
		marker := "`" + rel + "/`"
		if !strings.Contains(content, marker) {
			t.Errorf("TESTING.md no longer references %s; update this test if the doc text changed intentionally", marker)
			continue
		}
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil || !info.IsDir() {
			t.Errorf("TESTING.md references %s/, but it does not exist on disk: %v", rel, err)
		}
	}

	// Guard against reintroducing the stale directory name this test
	// was added to catch.
	if strings.Contains(content, "rest_openai_completions") {
		t.Errorf("TESTING.md references the stale tests/tier3_contract/rest_openai_completions/ path; the actual directory is rest_openai_chat/")
	}
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
