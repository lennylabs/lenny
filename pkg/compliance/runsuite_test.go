// SPDX-License-Identifier: MIT

package compliance_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/compliance"
)

// spec: §24.8 line 113 / §15 line 1414 — RunSuite is the non-testing
// core the gateway validate handler drives. A passing run returns a
// report with no error.
func TestRunSuiteHappyPath(t *testing.T) {
	dir := t.TempDir()
	stub := buildStubHarness(t, dir)
	adapter := compliance.NewAdapter("/tmp/fake-adapter", compliance.LevelStandard)

	report, err := compliance.RunSuite(context.Background(), adapter, compliance.Options{HarnessPath: stub})
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if report.Summary.Total != 1 || report.Summary.Failed != 0 {
		t.Fatalf("summary = %+v, want 1 total / 0 failed", report.Summary)
	}
	if report.Binary != "/tmp/fake-adapter" || report.Level != "standard" {
		t.Fatalf("report binary/level = %q/%q", report.Binary, report.Level)
	}
}

// A failing conformance run is NOT a RunSuite error: the report carries
// the failure and err is nil so the handler can transition the adapter
// to validation_failed.
func TestRunSuiteFailingConformanceIsNotError(t *testing.T) {
	dir := t.TempDir()
	stub := buildFailingStub(t, dir)
	adapter := compliance.NewAdapter("/tmp/fake-adapter", compliance.LevelBasic)

	report, err := compliance.RunSuite(context.Background(), adapter, compliance.Options{HarnessPath: stub})
	if err != nil {
		t.Fatalf("RunSuite returned error for failing conformance: %v", err)
	}
	if report.Summary.Failed != 1 {
		t.Fatalf("summary.Failed = %d, want 1", report.Summary.Failed)
	}
}

// spec: §15 line 1414 — when the harness cannot be located RunSuite
// returns ErrHarnessNotFound so the gate reports "cannot validate"
// rather than failing the adapter.
func TestRunSuiteHarnessNotFound(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	adapter := compliance.NewAdapter("/tmp/fake-adapter", compliance.LevelBasic)
	_, err := compliance.RunSuite(context.Background(), adapter, compliance.Options{})
	if !errors.Is(err, compliance.ErrHarnessNotFound) {
		t.Fatalf("err = %v, want ErrHarnessNotFound", err)
	}
}

func TestRunSuiteRejectsBadAdapter(t *testing.T) {
	if _, err := compliance.RunSuite(context.Background(), nil, compliance.Options{}); err == nil {
		t.Error("nil adapter should error")
	}
	if _, err := compliance.RunSuite(context.Background(), compliance.NewAdapter("", compliance.LevelBasic), compliance.Options{HarnessPath: "/bin/true"}); err == nil {
		t.Error("empty BinaryPath should error")
	}
	if _, err := compliance.RunSuite(context.Background(), compliance.NewAdapter("/x", compliance.Level("bogus")), compliance.Options{HarnessPath: "/bin/true"}); err == nil {
		t.Error("invalid level should error")
	}
}

// buildFailingStub compiles a stub harness that reports one failing
// check so the failing-conformance path can be exercised.
func buildFailingStub(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}
	src := filepath.Join(dir, "fail.go")
	body := `package main
import (
  "encoding/json"
  "os"
)
type Check struct { Name, Spec, Detail string; Pass bool }
type Summary struct { Total, Passed, Failed int }
type Report struct {
  Harness string ` + "`json:\"harness\"`" + `
  Binary  string ` + "`json:\"binary\"`" + `
  Level   string ` + "`json:\"level\"`" + `
  Checks  []Check ` + "`json:\"checks\"`" + `
  Summary Summary ` + "`json:\"summary\"`" + `
}
func main() {
  r := Report{Harness: "stub/fail", Binary: "/tmp/fake-adapter", Level: "basic"}
  r.Checks = []Check{{Name: "heartbeat_emits_ack", Spec: "15.4", Pass: false, Detail: "no ack"}}
  r.Summary = Summary{Total: 1, Passed: 0, Failed: 1}
  json.NewEncoder(os.Stdout).Encode(r)
  os.Exit(1)
}
`
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("write failing stub: %v", err)
	}
	bin := filepath.Join(dir, "lenny-compliance-fail")
	if out, err := exec.Command("go", "build", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("build failing stub: %v\n%s", err, out)
	}
	return bin
}
