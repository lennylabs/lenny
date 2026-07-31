// SPDX-License-Identifier: MIT

package verdictstatus_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// The verdict document's `verdict` field and per-tier `status` field
// value sets are owned by TESTING.md §7. These cases carry no spec
// annotation: the harness attributes an annotated failure to a numbered
// section under spec/, and this package implements test infrastructure
// rather than a spec behavior.

// TestTierStatusSetCoversEveryConstant pins TierStatuses to the
// declared constants. A documentation-reconciliation test ranges over
// the slice, so a value that exists as a constant but is missing from
// its slice would let a documented enum sentence drop it silently.
func TestTierStatusSetCoversEveryConstant(t *testing.T) {
	want := map[string]bool{
		verdictstatus.Pass:         false,
		verdictstatus.Fail:         false,
		verdictstatus.Skipped:      false,
		verdictstatus.Inconclusive: false,
		verdictstatus.NotSelected:  false,
		verdictstatus.Unverified:   false,
	}
	got := verdictstatus.TierStatuses()
	if len(got) != len(want) {
		t.Fatalf("TierStatuses() returned %d values, want %d: %v", len(got), len(want), got)
	}
	for _, s := range got {
		seen, ok := want[s]
		if !ok {
			t.Errorf("TierStatuses() contains %q, which is not a declared tier-status constant", s)
			continue
		}
		if seen {
			t.Errorf("TierStatuses() repeats %q", s)
		}
		want[s] = true
	}
	for s, seen := range want {
		if !seen {
			t.Errorf("TierStatuses() omits the declared tier status %q", s)
		}
	}
}

// TestVerdictSetCoversEveryConstant pins Verdicts to the declared
// verdict constants.
func TestVerdictSetCoversEveryConstant(t *testing.T) {
	want := map[string]bool{
		verdictstatus.VerdictPass:         false,
		verdictstatus.VerdictFail:         false,
		verdictstatus.VerdictInconclusive: false,
		verdictstatus.VerdictUnverified:   false,
	}
	got := verdictstatus.Verdicts()
	if len(got) != len(want) {
		t.Fatalf("Verdicts() returned %d values, want %d: %v", len(got), len(want), got)
	}
	for _, v := range got {
		seen, ok := want[v]
		if !ok {
			t.Errorf("Verdicts() contains %q, which is not a declared verdict constant", v)
			continue
		}
		if seen {
			t.Errorf("Verdicts() repeats %q", v)
		}
		want[v] = true
	}
	for v, seen := range want {
		if !seen {
			t.Errorf("Verdicts() omits the declared verdict %q", v)
		}
	}
}

// TestSerializedValues pins the wire spelling of each constant. A CI
// consumer parses these out of tests/results/latest.json, so the
// spelling is part of the contract and cannot change with a rename.
func TestSerializedValues(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"pass", verdictstatus.Pass, "pass"},
		{"fail", verdictstatus.Fail, "fail"},
		{"skipped", verdictstatus.Skipped, "skipped"},
		{"inconclusive", verdictstatus.Inconclusive, "inconclusive"},
		{"not-selected", verdictstatus.NotSelected, "not-selected"},
		{"unverified", verdictstatus.Unverified, "unverified"},
		{"PASS", verdictstatus.VerdictPass, "PASS"},
		{"FAIL", verdictstatus.VerdictFail, "FAIL"},
		{"INCONCLUSIVE", verdictstatus.VerdictInconclusive, "INCONCLUSIVE"},
		{"UNVERIFIED", verdictstatus.VerdictUnverified, "UNVERIFIED"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: constant serializes as %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestVerdictAndTierStatusSetsAreDisjoint keeps the two enums distinct.
// The verdict values are upper-case and the tier statuses lower-case,
// and a consumer that switches on one must not match a value from the
// other.
func TestVerdictAndTierStatusSetsAreDisjoint(t *testing.T) {
	tiers := map[string]bool{}
	for _, s := range verdictstatus.TierStatuses() {
		tiers[s] = true
	}
	for _, v := range verdictstatus.Verdicts() {
		if tiers[v] {
			t.Errorf("%q is both a verdict and a tier status", v)
		}
	}
}

// TestNoSpecAnnotationInPackage keeps this package free of the harness
// spec annotation. The harness scans the failing test's own package
// directory for that marker and reduces whatever follows it to a bare
// section number, so an annotation naming TESTING.md §7 here would be
// recorded as a result for spec section 7, which is the session
// lifecycle and defines no verdict schema. TESTING.md owns this schema,
// and the harness has no annotation form that points at it.
func TestNoSpecAnnotationInPackage(t *testing.T) {
	// Built by concatenation so this file does not trip its own check.
	marker := "spec" + ":"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
			continue
		}
		body, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		scanned++
		for i, line := range strings.Split(string(body), "\n") {
			rest, ok := strings.CutPrefix(strings.TrimSpace(line), "//")
			if !ok {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(rest), marker) {
				t.Errorf("%s:%d carries a harness spec annotation; the verdict schema is owned by TESTING.md and an annotation here is attributed to a spec section instead", e.Name(), i+1)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no Go files; the check would pass vacuously")
	}
}

// TestScanUnverifiedReadsIndentedMarker pins the marker parse against
// the output a Go test binary actually produces, which indents a test's
// own writes. A parser that only matched at the start of a line would
// read every such report as silence and report the tier as passing.
func TestScanUnverifiedReadsIndentedMarker(t *testing.T) {
	out := "=== RUN   TestProtoNoDrift\n    proto_test.go:31: " +
		verdictstatus.UnverifiedMarker + " protoc-gen-go not on PATH\n--- PASS: TestProtoNoDrift\nPASS\n"
	reasons, ok := verdictstatus.ScanUnverified(out)
	if !ok {
		t.Fatal("indented marker line was not recognized")
	}
	if len(reasons) != 1 || reasons[0] != "protoc-gen-go not on PATH" {
		t.Fatalf("reasons = %q; want one entry naming the missing binary", reasons)
	}
}

// TestScanUnverifiedSilentOutput holds the negative case: output with
// no marker reports no conclusion of its own, so the tier stays at the
// status its checks otherwise produced.
func TestScanUnverifiedSilentOutput(t *testing.T) {
	reasons, ok := verdictstatus.ScanUnverified("ok  \tpkg/example\t0.02s\n")
	if ok || len(reasons) != 0 {
		t.Fatalf("unmarked output reported ok=%v reasons=%q", ok, reasons)
	}
}

// TestScanUnverifiedCollectsEveryReason keeps every marked line, so a
// run in which several tests reached no conclusion reports all of them
// rather than the first.
func TestScanUnverifiedCollectsEveryReason(t *testing.T) {
	out := verdictstatus.UnverifiedMarker + " first reason\nnoise\n\t" +
		verdictstatus.UnverifiedMarker + "\t second reason \n"
	reasons, ok := verdictstatus.ScanUnverified(out)
	if !ok {
		t.Fatal("marker lines were not recognized")
	}
	if len(reasons) != 2 || reasons[0] != "first reason" || reasons[1] != "second reason" {
		t.Fatalf("reasons = %q; want both reasons trimmed", reasons)
	}
}
