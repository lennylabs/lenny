// SPDX-License-Identifier: MIT

package verdictstatus_test

import (
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// spec: TESTING.md §7 (verdict document: the `verdict` field and the
// per-tier `status` field value sets).
//
// TierStatuses and Verdicts are what a documentation-reconciliation
// test ranges over, so a value that exists as a constant but is missing
// from its slice would let a documented enum sentence drop it silently.
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

// spec: TESTING.md §7 (verdict document: the `verdict` field value set).
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

// spec: TESTING.md §7 (verdict document: serialized values of the
// `verdict` and per-tier `status` fields).
//
// The constants are wire values a CI consumer parses out of
// tests/results/latest.json, so their spelling is part of the contract
// and cannot change with a rename.
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

// spec: TESTING.md §7 (verdict document: the two value sets are
// distinct enums).
//
// A tier status and a verdict must never collide: the verdict values
// are upper-case and the tier statuses lower-case, and a consumer that
// switches on one must not match a value from the other.
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
