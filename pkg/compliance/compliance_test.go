// SPDX-License-Identifier: MIT

package compliance_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// spec: §26.1 lines 14-22 (catalog table) + §26.3-26.11 (per-runtime
// Image field), §26.7 line 312, §26.9 line 399, §26.11 line 458, etc.
// diagnosis: every §26 reference runtime is published at
// `ghcr.io/lennylabs/runtime-<name>:<release>`. The embedded-stack
// catalog (`pkg/embedded/stack/catalog.go`) and Helm chart
// (`charts/lenny/values.yaml`) carry the absolute form; the compliance
// manifest carries the path-relative form `lennylabs/runtime-<name>:1.0.0`
// so that an operator pulls from any configured registry. A regression
// that drops the `runtime-` prefix or rolls the tag back to `:v1`
// would silently skew nightly conformance pulls from the Helm/embedded
// runtime image. This assertion catches both.
func TestReferenceCatalogImageRefsCanonical(t *testing.T) {
	catalog, err := compliance.ReferenceCatalog()
	if err != nil {
		t.Fatalf("read reference catalog: %v", err)
	}
	if len(catalog) == 0 {
		t.Fatal("reference catalog is empty")
	}
	for _, r := range catalog {
		want := fmt.Sprintf("lennylabs/runtime-%s:1.0.0", r.Name)
		if r.Image != want {
			t.Errorf("§26 runtime %q image = %q, want %q (spec §26.1: ghcr.io/lennylabs/runtime-<name>:<release>; manifest carries the path-relative form)", r.Name, r.Image, want)
		}
		if strings.HasPrefix(r.Image, "ghcr.io/") {
			t.Errorf("§26 runtime %q image %q is absolute; the manifest expresses the path-relative form so LENNY_REFERENCE_IMAGE_REGISTRY can target any registry", r.Name, r.Image)
		}
		if !strings.Contains(r.Image, "/runtime-") {
			t.Errorf("§26 runtime %q image %q drops the canonical `runtime-` repository prefix per spec §26.x Image lines (e.g. §26.7 line 312)", r.Name, r.Image)
		}
		if strings.HasSuffix(r.Image, ":v1") {
			t.Errorf("§26 runtime %q image %q uses the legacy `:v1` tag; canonical form is `:1.0.0` per pkg/embedded/stack/catalog.go and charts/lenny/values.yaml", r.Name, r.Image)
		}
	}
}
