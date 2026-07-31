// SPDX-License-Identifier: MIT

// Package verdictstatus holds the verdict-level and per-tier status
// values of the lenny-test verdict document, whose schema is owned by
// TESTING.md §7. The values live in their own importable package
// because `cmd/lenny-test` is a main package: a documentation-
// reconciliation test cannot import a main package, so a test that
// holds TESTING.md's enum sentences to the values the harness actually
// emits would otherwise have to restate them and drift.
//
// Every tierStat.Status field carries one of the tier statuses, and the
// verdict field carries one of the verdicts.
//
// Nothing in this package carries a spec annotation. The harness
// reduces such an annotation to a bare section number and would credit
// the resulting failure to spec/07_session-lifecycle.md, which defines
// no verdict schema; the verdict document belongs to TESTING.md §7.
package verdictstatus

import "strings"

// Tier statuses. These are the values serialized into
// `tiers.<name>.status` in the verdict document (TESTING.md §7).
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
// `verdict` field of the verdict document (TESTING.md §7).
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

// UnverifiedMarker is the line prefix a tier-0 Go test writes to stdout
// to report that it could not reach a conclusion. The harness has no
// channel other than the test binary's output for a passing test to say
// that it proved nothing, so the test writes this marker followed by the
// reason and exits zero, and the tier-0 composer promotes the tier to
// Unverified. Producer and parser share the constant so the two cannot
// drift apart.
const UnverifiedMarker = "LENNY-TEST-UNVERIFIED:"

// ScanUnverified reports whether out carries the unverified marker, and
// returns the reason text that follows it on every marked line. The
// marker is matched anywhere in a line rather than at its start,
// because `go test` prefixes a test's own output with indentation and a
// file:line stamp. An empty reason list with ok true means the marker
// was written with no reason text.
func ScanUnverified(out string) (reasons []string, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		_, after, found := strings.Cut(line, UnverifiedMarker)
		if !found {
			continue
		}
		ok = true
		if reason := strings.TrimSpace(after); reason != "" {
			reasons = append(reasons, reason)
		}
	}
	return reasons, ok
}

// TierStatuses returns every declared tier-status value exactly once. A
// consumer that must cover the whole set ranges over this rather than
// restating the values. The order of the returned values is not part of
// the contract.
func TierStatuses() []string {
	return []string{Pass, Fail, Skipped, Inconclusive, NotSelected, Unverified}
}

// Verdicts returns every declared verdict value exactly once. The order
// of the returned values is not part of the contract.
func Verdicts() []string {
	return []string{VerdictPass, VerdictFail, VerdictInconclusive, VerdictUnverified}
}
