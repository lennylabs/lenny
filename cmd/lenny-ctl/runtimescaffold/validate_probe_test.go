// SPDX-License-Identifier: MIT

package runtimescaffold

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spec: §24.18 line 231 / §15.4.6 — `lenny runtime validate` declared-vs-
// observed probe and --report.

// writeRepo creates a minimal runtime repository that passes the static
// checks, declaring the given integration level.
func writeRepo(t *testing.T, level string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := "name: vr\ntype: agent\nimage: ghcr.io/acme/vr:0.1.0\n" +
		"integrationLevel: " + level + "\ncapabilities:\n  interaction: bidirectional\n"
	if err := os.WriteFile(filepath.Join(dir, "runtime.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write runtime.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	return dir
}

func allPass() map[string]bool {
	m := map[string]bool{}
	for name := range checkLevel {
		m[name] = true
	}
	return m
}

// TestValidateProbeMatchExitsZero: a full-passing runtime declaring full
// reconciles to a match and exits 0.
func TestValidateProbeMatchExitsZero(t *testing.T) {
	repo := writeRepo(t, "full")
	stub := buildProbeStub(t, allPass())
	var stdout, stderr bytes.Buffer
	code := Validate(ValidateOptions{Path: repo, BinaryPath: "/tmp/fake", HarnessPath: stub}, &stdout, &stderr)
	if code != ValidateOK {
		t.Fatalf("match: exit %d, want 0\nstdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "Observed integration level: full (declared full) — match") {
		t.Errorf("stdout missing the match line:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Result: pass") {
		t.Errorf("stdout missing pass result:\n%s", stdout.String())
	}
}

// TestValidateProbeUnderperformsExitsNonZero: declaring full but only
// clearing the standard gate exits non-zero with runtime_level_underperforms.
func TestValidateProbeUnderperformsExitsNonZero(t *testing.T) {
	repo := writeRepo(t, "full")
	pass := allPass()
	for name, lvl := range checkLevel {
		if levelRank(lvl) == 3 { // full-level checks fail.
			pass[name] = false
		}
	}
	stub := buildProbeStub(t, pass)
	var stdout, stderr bytes.Buffer
	code := Validate(ValidateOptions{Path: repo, BinaryPath: "/tmp/fake", HarnessPath: stub}, &stdout, &stderr)
	if code != ValidateFailed {
		t.Fatalf("underperforms: exit %d, want 1\nstdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "runtime_level_underperforms") {
		t.Errorf("stdout missing the underperforms error:\n%s", stdout.String())
	}
}

// TestValidateProbeUnderdeclaredWarnsExitsZero: declaring basic but
// clearing the full gate is an under-declaration — WARN, exit 0.
func TestValidateProbeUnderdeclaredWarnsExitsZero(t *testing.T) {
	repo := writeRepo(t, "basic")
	stub := buildProbeStub(t, allPass())
	var stdout, stderr bytes.Buffer
	code := Validate(ValidateOptions{Path: repo, BinaryPath: "/tmp/fake", HarnessPath: stub}, &stdout, &stderr)
	if code != ValidateOK {
		t.Fatalf("underdeclared: exit %d, want 0\nstdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "WARN: runtime is underdeclared") {
		t.Errorf("stdout missing the underdeclared WARN:\n%s", stdout.String())
	}
}

// TestValidateReportWritesJSON: --report writes a machine-readable JSON
// document carrying the integrationLevel reconciliation.
func TestValidateReportWritesJSON(t *testing.T) {
	repo := writeRepo(t, "full")
	stub := buildProbeStub(t, allPass())
	reportPath := filepath.Join(t.TempDir(), "report.json")
	var stdout, stderr bytes.Buffer
	code := Validate(ValidateOptions{Path: repo, BinaryPath: "/tmp/fake", HarnessPath: stub, ReportPath: reportPath}, &stdout, &stderr)
	if code != ValidateOK {
		t.Fatalf("report run: exit %d, want 0", code)
	}
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var rep ValidateReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("report is not valid JSON: %v\n%s", err, raw)
	}
	if rep.Result != "pass" || rep.DeclaredLevel != "full" {
		t.Errorf("report result/level = %q/%q, want pass/full", rep.Result, rep.DeclaredLevel)
	}
	if rep.IntegrationLevel == nil || rep.IntegrationLevel.Status != StatusMatch {
		t.Errorf("report integrationLevel = %+v, want a match", rep.IntegrationLevel)
	}
	if rep.Conformance == nil || rep.Conformance.Summary.Total == 0 {
		t.Errorf("report conformance battery is empty: %+v", rep.Conformance)
	}
}

// TestValidateNoBinaryReportsNotProbed: without --binary the validator
// runs static checks only and reports the observed level as not probed.
func TestValidateNoBinaryReportsNotProbed(t *testing.T) {
	repo := writeRepo(t, "standard")
	var stdout, stderr bytes.Buffer
	code := Validate(ValidateOptions{Path: repo}, &stdout, &stderr)
	if code != ValidateOK {
		t.Fatalf("static-only: exit %d, want 0\nstdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "not probed") {
		t.Errorf("static-only run should report the observed level as not probed:\n%s", stdout.String())
	}
}

// TestValidateHarnessNotFoundDegrades: --binary set but the harness is
// absent from PATH degrades to a static-only report without failing.
func TestValidateHarnessNotFoundDegrades(t *testing.T) {
	repo := writeRepo(t, "full")
	t.Setenv("PATH", t.TempDir()) // no lenny-compliance anywhere.
	var stdout, stderr bytes.Buffer
	// HarnessPath empty forces the PATH lookup, which now finds nothing.
	code := Validate(ValidateOptions{Path: repo, BinaryPath: "/tmp/fake"}, &stdout, &stderr)
	if code != ValidateOK {
		t.Fatalf("harness-not-found: exit %d, want 0 (degrade, not fail)\nstdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "lenny-compliance harness was not found") {
		t.Errorf("stdout should note the missing harness:\n%s", stdout.String())
	}
}
