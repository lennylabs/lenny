// SPDX-License-Identifier: MIT

// Package zap wraps the §12.9.6 OWASP ZAP integration. The fuzz
// runner invokes the ZAP CLI (or docker image) against a target URL
// with the project's policy file and parses the resulting report.
//
// When ZAP is not installed the helper skips with a precise
// diagnosis. When installed it runs `zap.sh -cmd -quickurl <target>
// -quickprogress -quickout report.xml`, walks the resulting XML, and
// counts alerts by ZAP risk code into Result.
package zap

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
// It shells out to zap.sh with the documented flags, then parses
// the XML quickout into Result. The alert counts are read from the
// ZAP riskcode taxonomy: 3 = High, 2 = Medium, 1 = Low, 0 =
// Informational. The test fails when the parse cannot complete; a
// scan that produces no alerts is a valid result (every count is
// zero).
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
	res, err := ParseReport(report)
	if err != nil {
		t.Fatalf("zap parse report: %v", err)
	}
	return res
}

// ParseReport walks the ZAP XML quickout at path and tallies alerts
// by riskcode. The format follows the OWASPZAPReport schema:
// the root element wraps one or more <site> blocks, each containing
// an <alerts> list of <alertitem> entries whose <riskcode> is the
// classification code. Unknown riskcodes are ignored (forward
// compatible with future ZAP extensions); a malformed XML body is
// surfaced as an error so the caller knows the report is unusable.
func ParseReport(path string) (Result, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read report: %w", err)
	}
	var doc zapReport
	if err := xml.Unmarshal(body, &doc); err != nil {
		return Result{}, fmt.Errorf("parse XML: %w", err)
	}
	res := Result{ReportPath: path}
	for _, site := range doc.Sites {
		for _, item := range site.Alerts {
			n, err := strconv.Atoi(item.RiskCode)
			if err != nil {
				continue
			}
			switch n {
			case 3:
				res.HighAlerts++
			case 2:
				res.MediumAlerts++
			case 1:
				res.LowAlerts++
			}
		}
	}
	return res, nil
}

// zapReport models the subset of the OWASPZAPReport XML schema the
// alert tally needs.
type zapReport struct {
	XMLName xml.Name  `xml:"OWASPZAPReport"`
	Sites   []zapSite `xml:"site"`
}

type zapSite struct {
	Alerts []zapAlert `xml:"alerts>alertitem"`
}

type zapAlert struct {
	RiskCode string `xml:"riskcode"`
}
