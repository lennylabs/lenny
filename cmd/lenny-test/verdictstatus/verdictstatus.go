// SPDX-License-Identifier: MIT

// Package verdictstatus holds the verdict-level and per-tier status
// values of the lenny-test verdict document (§7). The values live in
// their own importable package because `cmd/lenny-test` is a main
// package: a documentation-reconciliation test cannot import a main
// package, so a test that holds TESTING.md's enum sentences to the
// values the harness actually emits would otherwise have to restate
// them and drift.
//
// Every tierStat.Status field carries one of the tier statuses, and the
// verdict field carries one of the verdicts.
package verdictstatus

// Tier statuses. These are the values serialized into
// `tiers.<name>.status` in the verdict document (§7).
const (
	// Pass reports that the tier ran and every selected test passed.
	Pass = "pass"
	// Fail reports that the tier ran and at least one test failed.
	Fail = "fail"
	// Skipped reports that the tier was selected but did not run.
	Skipped = "skipped"
	// Inconclusive reports that the tier failed for an
	// infrastructure-class reason rather than a test failure.
	Inconclusive = "inconclusive"
	// NotSelected reports that the tier fell outside the resolved
	// selector.
	NotSelected = "not-selected"
	// Unverified reports that a check in the tier could not reach a
	// conclusion, so the tier neither passed nor failed.
	Unverified = "unverified"
)

// Verdicts. These are the values serialized into the top-level
// `verdict` field of the verdict document (§7).
const (
	// VerdictPass reports that every tier that ran passed.
	VerdictPass = "PASS"
	// VerdictFail reports that at least one tier failed.
	VerdictFail = "FAIL"
	// VerdictInconclusive reports that the run hit an
	// infrastructure-class failure and no test failure.
	VerdictInconclusive = "INCONCLUSIVE"
	// VerdictUnverified reports that a check could not reach a
	// conclusion, so the run proved neither success nor failure.
	VerdictUnverified = "UNVERIFIED"
)

// TierStatuses returns every tier-status value, in the order the
// TESTING.md §7 enum sentence lists them. A consumer that must cover
// the whole set ranges over this rather than restating the values.
func TierStatuses() []string {
	return []string{Pass, Fail, Skipped, Inconclusive, NotSelected, Unverified}
}

// Verdicts returns every verdict value, in the order the TESTING.md §7
// enum sentence lists them.
func Verdicts() []string {
	return []string{VerdictPass, VerdictFail, VerdictInconclusive, VerdictUnverified}
}
