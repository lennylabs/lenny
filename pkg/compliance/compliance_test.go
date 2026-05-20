// SPDX-License-Identifier: MIT

package compliance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/compliance"
)

// buildStubHarness compiles a tiny fake `lenny-compliance` binary
// that emits a fixed JSON report. The fake verifies the helper
// passed the documented --binary / --level / --json flags and
// echoes them back in the report so the assertion path can confirm
// the wire contract.
func buildStubHarness(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}
	src := filepath.Join(dir, "main.go")
	body := `package main
import (
  "encoding/json"
  "fmt"
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
  args := os.Args[1:]
  binary, level := "", ""
  saw := map[string]bool{}
  for i := 0; i < len(args); i++ {
    switch args[i] {
    case "--binary": if i+1 < len(args) { binary = args[i+1]; i++ }; saw["--binary"] = true
    case "--level":  if i+1 < len(args) { level  = args[i+1]; i++ }; saw["--level"]  = true
    case "--json":   saw["--json"] = true
    }
  }
  if !saw["--binary"] || !saw["--level"] || !saw["--json"] {
    fmt.Fprintln(os.Stderr, "stub: missing required flag"); os.Exit(2)
  }
  r := Report{Harness: "stub/0.0.0", Binary: binary, Level: level}
  pass := Check{Name: "stub_pass", Spec: "15.4", Pass: true}
  r.Checks = []Check{pass}
  r.Summary = Summary{Total: 1, Passed: 1}
  json.NewEncoder(os.Stdout).Encode(r)
}
`
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	bin := filepath.Join(dir, "lenny-compliance-stub")
	cmd := exec.Command("go", "build", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build stub harness: %v\n%s", err, out)
	}
	return bin
}

// spec: §12.10 (RegisterAdapterUnderTest entry point)
// diagnosis: a third-party runtime project must drive the harness
// against its own binary through a single Go-test entry point.
// RegisterAdapterUnderTest invokes the harness with the documented
// --binary / --level / --json flags, decodes the JSON report, and
// passes the test when every check passes.
func TestRegisterAdapterUnderTestHappyPath(t *testing.T) {
	dir := t.TempDir()
	stub := buildStubHarness(t, dir)
	adapter := compliance.NewAdapter("/tmp/fake-adapter", compliance.LevelBasic)

	report := compliance.RegisterAdapterUnderTest(t, adapter, compliance.Options{HarnessPath: stub})
	if report.Summary.Passed != 1 || report.Summary.Failed != 0 {
		t.Errorf("report summary = %+v, want 1 pass / 0 fail", report.Summary)
	}
	if report.Binary != "/tmp/fake-adapter" {
		t.Errorf("report.Binary = %q, want /tmp/fake-adapter", report.Binary)
	}
	if report.Level != "basic" {
		t.Errorf("report.Level = %q, want basic", report.Level)
	}
	if report.Harness != "stub/0.0.0" {
		t.Errorf("report.Harness = %q, want stub/0.0.0", report.Harness)
	}
}

// spec: §12.10 (NewAdapter convenience constructor)
// diagnosis: NewAdapter returns an Adapter whose BinaryPath and
// DeclaredLevel reflect the supplied values. This is the entry
// point a runtime project uses when its only requirement is a
// path + level pair.
func TestNewAdapterReportsConstructorValues(t *testing.T) {
	a := compliance.NewAdapter("/usr/local/bin/myruntime", compliance.LevelFull)
	if a.BinaryPath() != "/usr/local/bin/myruntime" {
		t.Errorf("BinaryPath = %q, want /usr/local/bin/myruntime", a.BinaryPath())
	}
	if a.DeclaredLevel() != compliance.LevelFull {
		t.Errorf("DeclaredLevel = %q, want full", a.DeclaredLevel())
	}
}
