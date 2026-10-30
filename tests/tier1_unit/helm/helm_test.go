// SPDX-License-Identifier: MIT

// Tier-1 unit helm-template rendering tests per TESTING.md §12.1.
// helm-unittest reads YAML test cases under charts/<chart>/tests/
// and asserts the rendered manifest matches expectations. The Go
// harness here shells out to `helm unittest <chart>`; it skips
// gracefully when the helm CLI or the helm-unittest plugin are not
// installed.

package helm_test

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func repoRoot(t *testing.T) string {
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

// spec: 12.1 (helm-template rendering asserts the documented manifests)
// diagnosis: A helm-unittest case failed. Either a template emitted
//
//	unexpected output or the assertion (asserts:) does not
//	match the chart's current shape. Inspect the
//	charts/<chart>/tests/<case>.yaml file and the failing
//	template.
func TestChartTemplatesViaHelmUnittest(t *testing.T) {
	root := repoRoot(t)
	chart := filepath.Join(root, "charts", "lenny")

	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skipf("helm not on PATH: %v", err)
	}
	// helm-unittest is a helm plugin. Check via `helm plugin list`.
	listCmd := exec.Command(helm, "plugin", "list")
	listOut, err := listCmd.CombinedOutput()
	if err != nil {
		t.Skipf("helm plugin list failed: %v", err)
	}
	if !contains(string(listOut), "unittest") {
		t.Skipf("helm-unittest plugin not installed. Install via `helm plugin install https://github.com/helm-unittest/helm-unittest`")
	}

	cmd := exec.Command(helm, "unittest", chart)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm unittest %s: %v\n%s", chart, err, out)
	}
	t.Logf("helm-unittest output:\n%s", out)
}

// spec: 12.1 (every chart that ships has at least one unittest case)
// diagnosis: A chart at charts/<name>/ exists but charts/<name>/tests/
//
//	contains no test cases. Author at least one case per
//	chart so coverage is non-zero.
func TestEveryChartHasUnittestCases(t *testing.T) {
	root := repoRoot(t)
	charts := filepath.Join(root, "charts")
	entries, err := os.ReadDir(charts)
	if err != nil {
		t.Fatalf("read charts: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		testsDir := filepath.Join(charts, e.Name(), "tests")
		if _, err := os.Stat(testsDir); errors.Is(err, fs.ErrNotExist) {
			t.Errorf("charts/%s/tests/ is missing — every chart needs at least one helm-unittest case", e.Name())
			continue
		}
		cases, err := os.ReadDir(testsDir)
		if err != nil {
			t.Errorf("charts/%s/tests/: %v", e.Name(), err)
			continue
		}
		caseCount := 0
		for _, c := range cases {
			if !c.IsDir() && (filepath.Ext(c.Name()) == ".yaml" || filepath.Ext(c.Name()) == ".yml") {
				caseCount++
			}
		}
		if caseCount == 0 {
			t.Errorf("charts/%s/tests/: no .yaml test cases present", e.Name())
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
