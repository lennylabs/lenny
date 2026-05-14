// SPDX-License-Identifier: MIT

// Package zap wraps the §12.9.6 OWASP ZAP integration. The fuzz
// runner invokes the ZAP CLI (or docker image) against a target URL
// with the project's policy file and parses the resulting report.
//
// Today this is a thin scaffold: when ZAP is not installed it skips
// with a precise diagnosis; when installed it runs `zap.sh -cmd
// -quickurl <target> -quickprogress -quickout report.xml` and
// surfaces the report path.
package zap

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

// ErrZAPUnavailable signals that the ZAP CLI is not on PATH.
var ErrZAPUnavailable = errors.New("zap: zap.sh not on PATH")

// Available reports whether zap.sh is reachable.
func Available() bool {
	_, err := exec.LookPath("zap.sh")
	return err == nil
}

// SkipUnlessAvailable short-circuits the test when ZAP is absent.
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if !Available() {
		t.Skip("zap.SkipUnlessAvailable: zap.sh not on PATH. Install ZAP per https://www.zaproxy.org/download/ or use the docker image owasp/zap2docker-stable.")
	}
}

// Options configures a ZAP run.
type Options struct {
	// Target URL to scan.
	Target string

	// PolicyFile is the path to the ZAP scan policy. When empty the
	// runner uses the project default at
	// tests/testinfra/security/zap/lenny-policy.xml.
	PolicyFile string

	// OutputDir is where the ZAP report is written. When empty the
	// runner uses t.TempDir().
	OutputDir string

	// Seed pins the ZAP fuzzer seed for reproducibility. Today ZAP
	// itself does not expose a seed; this field is for future
	// reproducibility hooks.
	Seed int64
}

// Result is the parsed ZAP report.
type Result struct {
	ReportPath   string
	HighAlerts   int
	MediumAlerts int
	LowAlerts    int
}

// Run executes a ZAP scan and returns the parsed result.
//
// Today the implementation is a placeholder: it shells out to
// zap.sh with the documented flags and reports the report path.
// Parsing the XML output is deferred to a real Phase 9 task; the
// scaffold here documents the API the tier-9 suite will call.
func Run(t testing.TB, opts Options) Result {
	t.Helper()
	SkipUnlessAvailable(t)
	if opts.Target == "" {
		t.Fatal("zap.Run: Options.Target is required")
	}
	dir := opts.OutputDir
	if dir == "" {
		dir = t.TempDir()
	}
	report := filepath.Join(dir, "zap-report.xml")
	args := []string{
		"-cmd",
		"-quickurl", opts.Target,
		"-quickprogress",
		"-quickout", report,
	}
	if opts.PolicyFile != "" {
		args = append(args, "-configfile", opts.PolicyFile)
	}
	cmd := exec.Command("zap.sh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zap run: %v\n%s", err, out)
	}
	// Parsing is deferred. The placeholder Result reports the path
	// so callers can inspect the file in CI artifacts.
	return Result{ReportPath: report}
}
