// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	chartvalues "github.com/lennylabs/lenny/pkg/chart/values"
)

func ctlRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// TestCmdValuesValidateConformingFile exercises the success path against
// the chart's own default values.yaml. spec: §24.20 line 303.
func TestCmdValuesValidateConformingFile_spec_24_20(t *testing.T) {
	path := filepath.Join(ctlRepoRoot(t), "charts", "lenny", "values.yaml")
	var stdout, stderr bytes.Buffer
	code := cmdValues([]string{"validate", "--config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "conforms") {
		t.Errorf("expected a conformance message, got %q", stdout.String())
	}
}

// TestCmdValuesValidateRejectsBadValue asserts a non-conforming file
// exits 1 and prints a validation report naming the offending field.
// spec: §17.6 line 666.
func TestCmdValuesValidateRejectsBadValue_spec_17_6_666(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("playground:\n  devTenantId: \"bad.tenant\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cmdValues([]string{"validate", "--config", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code: got %d, want 1; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "devTenantId") {
		t.Errorf("expected the report to name devTenantId, got %q", stderr.String())
	}
}

func TestCmdValuesValidateMissingConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdValues([]string{"validate"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code: got %d, want 2", code)
	}
}

func TestCmdValuesValidateMissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdValues([]string{"validate", "--config", "/nonexistent/values.yaml"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code: got %d, want 1", code)
	}
}

func TestCmdValuesUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cmdValues([]string{"frobnicate"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code: got %d, want 2", code)
	}
	if code := cmdValues(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("empty args exit code: got %d, want 2", code)
	}
}

// TestComposedAnswerFilesConformToChartSchema is the §17.9.2 line 1374 CI
// lint, applied to the implementation's actual answer-file shape: each
// shipped wizard answer file is parsed and run through composeValues, and
// the composed chart-values document must validate against the generated
// schema. This closes the F-17.9.14 gap directly — a file that validates
// against the installAnswers struct can still produce an invalid chart
// values document if the composeValues mapping breaks, and this test
// catches that.
func TestComposedAnswerFilesConformToChartSchema_spec_17_9_2_1374(t *testing.T) {
	schema, err := chartvalues.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	root := ctlRepoRoot(t)
	answers, err := filepath.Glob(filepath.Join(root, "charts", "lenny", "answers", "*.yaml"))
	if err != nil || len(answers) == 0 {
		t.Fatalf("no answer files found: %v", err)
	}
	for _, path := range answers {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		a, err := parseAnswerFile(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", filepath.Base(path), err)
		}
		applyAnswerDefaults(&a)
		composed, err := composeValues(a)
		if err != nil {
			t.Fatalf("composeValues for %s: %v", filepath.Base(path), err)
		}
		if err := chartvalues.ValidateYAML(schema, composed); err != nil {
			t.Errorf("composed values for %s do not conform to the chart schema: %v\ncomposed:\n%s",
				filepath.Base(path), err, composed)
		}
	}
}
