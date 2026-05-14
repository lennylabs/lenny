// SPDX-License-Identifier: MIT

// Package kubebench wraps the §12.9 CIS Kubernetes benchmark via
// the kube-bench tool. The benchmark runs against a live cluster
// and reports findings as JSON; this package shells out to the
// binary, parses the report, and surfaces FAIL/WARN counts to
// tier-9 tests.
package kubebench

import (
	"encoding/json"
	"errors"
	"os/exec"
	"testing"
)

// ErrUnavailable signals that the kube-bench binary is not on PATH.
var ErrUnavailable = errors.New("kubebench: kube-bench not on PATH")

// Available reports whether kube-bench is installed.
func Available() bool {
	_, err := exec.LookPath("kube-bench")
	return err == nil
}

// SkipUnlessAvailable short-circuits the test when kube-bench is
// absent.
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if !Available() {
		t.Skip("kubebench.SkipUnlessAvailable: kube-bench not on PATH. Install per https://github.com/aquasecurity/kube-bench (or use the container image `aquasec/kube-bench`).")
	}
}

// Result is the parsed benchmark verdict.
type Result struct {
	TotalPass int
	TotalFail int
	TotalWarn int
	TotalInfo int
	Reports   []ReportSection
}

// ReportSection is one CIS section's results.
type ReportSection struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Pass  int    `json:"total_pass"`
	Fail  int    `json:"total_fail"`
	Warn  int    `json:"total_warn"`
	Info  int    `json:"total_info"`
	Tests []struct {
		ID     string `json:"section"`
		Result string `json:"status"`
		Desc   string `json:"desc"`
	} `json:"tests"`
}

// Options configures a kube-bench run.
type Options struct {
	// Targets selects which suites to run (master / node / etcd).
	// When empty, kube-bench's default (all relevant) is used.
	Targets []string
	// Kubeconfig is passed to kube-bench so it can reach the
	// cluster. When empty, the helper relies on the in-cluster
	// configuration or KUBECONFIG.
	Kubeconfig string
}

// Run executes kube-bench and returns the parsed verdict.
func Run(t testing.TB, opts Options) Result {
	t.Helper()
	SkipUnlessAvailable(t)
	args := []string{"--json"}
	for _, target := range opts.Targets {
		args = append(args, "run", "--targets", target)
	}
	cmd := exec.Command("kube-bench", args...)
	if opts.Kubeconfig != "" {
		cmd.Env = append(cmd.Env, "KUBECONFIG="+opts.Kubeconfig)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("kube-bench run: %v", err)
	}
	var raw struct {
		Totals struct {
			Pass int `json:"total_pass"`
			Fail int `json:"total_fail"`
			Warn int `json:"total_warn"`
			Info int `json:"total_info"`
		} `json:"totals"`
		Reports []ReportSection `json:"Controls"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("kube-bench parse: %v\nbody: %s", err, out)
	}
	return Result{
		TotalPass: raw.Totals.Pass,
		TotalFail: raw.Totals.Fail,
		TotalWarn: raw.Totals.Warn,
		TotalInfo: raw.Totals.Info,
		Reports:   raw.Reports,
	}
}

// AssertNoFailures fails the test when kube-bench reports any FAIL.
// Useful for the §12.9 critical security gate.
func AssertNoFailures(t testing.TB, r Result) {
	t.Helper()
	if r.TotalFail > 0 {
		t.Errorf("kube-bench reports %d FAIL findings (pass=%d, warn=%d). Inspect Reports for the failing controls.", r.TotalFail, r.TotalPass, r.TotalWarn)
	}
}
