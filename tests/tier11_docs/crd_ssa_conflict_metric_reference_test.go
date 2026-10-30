// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the deployer-facing metrics reference
// under docs/reference/metrics.md. The §16.1 catalog defines
// lenny_crd_ssa_conflict_total as a stuck-episode counter labeled by crd
// and controller, and the §16.5 CRDSSAConflictStuck alert evaluates
// against it. A deployer building a dashboard or an alert route reads the
// metrics reference to learn what the counter means and how it is labeled,
// so the reference must carry the counter with the reconciled semantics.
//
// This test is NOT under a build tag: it reads the repository state
// directly and needs no external infrastructure.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spec: §16.1 (metrics reference mirrors the §16.1 catalog row for
//
//	lenny_crd_ssa_conflict_total: a stuck-episode counter labeled by crd
//	and controller with per-resource attribution on the
//	crd_ssa_conflict_stuck structured log)
//
// diagnosis: docs/reference/metrics.md omits the lenny_crd_ssa_conflict_total
//
//	row, or states semantics that contradict the reconciled §16.1
//	definition. The counter increments once per five-consecutive-409 SSA
//	stuck episode on a CRD field a controller does not own, carries only the
//	bounded crd and controller labels (no per-resource label), and the
//	CRDSSAConflictStuck alert fires on it. A deployer reading the reference
//	relies on those facts to interpret the series. This asserts the row is
//	present and names each fact; an absent row, a dropped label, a missing
//	per-resource-log pointer, or a lost alert reference is caught here.
func TestMetricsReferenceCarriesCRDSSAConflictCounter(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "reference", "metrics.md"))
	if err != nil {
		t.Fatalf("read docs/reference/metrics.md: %v", err)
	}

	// Locate the single table row for the counter. Matching on the
	// backticked metric name inside a table cell keeps the assertion on the
	// reference row rather than an incidental prose mention.
	var row string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, "`lenny_crd_ssa_conflict_total`") && strings.HasPrefix(strings.TrimSpace(line), "|") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatal("docs/reference/metrics.md has no table row for `lenny_crd_ssa_conflict_total`")
	}

	// Each token the reconciled §16.1 semantics require the deployer to see:
	// the Counter type, the bounded crd/controller labels, the stuck-episode
	// increment condition, the per-resource structured-log pointer, and the
	// alert that evaluates against the series.
	for _, want := range []string{
		"Counter",
		"`crd`",
		"`controller`",
		"five-consecutive-409",
		"stuck episode",
		"`crd_ssa_conflict_stuck`",
		"`CRDSSAConflictStuck`",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("lenny_crd_ssa_conflict_total reference row does not state %q; row: %s", want, row)
		}
	}
}
