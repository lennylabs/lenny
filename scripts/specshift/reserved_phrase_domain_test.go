// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// TestReservedPhraseClassDomainExcludesEveryRecordTheNamingLawNames pins
// the excluded half of the reserved-phrase domain. The naming law states
// the domain in two parts: the trees, the Go files, and the root-level
// markdown documents that carry the prohibition, and the records that sit
// outside it, which are the historical audit records, the two root
// planning documents, the build and queue records, the staged proposals,
// and every fixture directory. Each of those records a finding, a plan,
// or a fixture as it was written rather than the current contract.
//
// The carrier predicate answers for the first part alone: it admits every
// tracked root-level markdown document, so the audit records and the
// queue record are inside it. The exclusion is composed on top, through
// the class read domain, and this case reads it there. Without it, moving
// a record out of the excluded list or dropping the class from the
// planning exclusion leaves the naming law and the enforced domain in
// disagreement with nothing reporting it.
//
// This is migration tooling rather than a platform behavior, so the case
// carries no spec-section annotation.
func TestReservedPhraseClassDomainExcludesEveryRecordTheNamingLawNames(t *testing.T) {
	t.Parallel()

	excluded := []string{
		"BUILD-GAPS.md",
		"TEST-GAPS.md",
		"BUILD-PLAN.md",
		"BUILD-PROGRESS.md",
		"PROPOSAL-QUEUE.md",
		"gateway-runtime-comms.md",
		"gateway-runtime-comms-remediation.md",
		"proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md",
		"spec/testdata/specimen.md",
		"tests/tier11_docs/testdata/reserved-phrase-hyphenated.md",
	}
	// The records are named here as well as read from PlanningRecords, so
	// a record dropped from that list fails this case instead of shrinking
	// what it asserts alongside it.
	excluded = append(excluded, scope.PlanningRecords()...)
	for _, record := range excluded {
		readable, err := scope.ReadableForClass(scope.ClassReservedPhrase, record)
		if err != nil {
			t.Fatalf("ReadableForClass(%s, %s): %v", scope.ClassReservedPhrase, record, err)
		}
		if readable {
			t.Errorf("the %s class reads %s, which the naming law places outside the prohibition's domain",
				scope.ClassReservedPhrase, record)
		}
	}

	// The exclusion is bounded: an ordinary carrier of each form the
	// naming law places inside the domain stays readable, so a widened
	// exclusion fails here rather than emptying the domain silently.
	for _, carrier := range []string{
		"spec/28_communication-channels.md",
		"docs/guides/authoring.md",
		"schemas/manifest.json",
		"pkg/gateway/session.go",
		"README.md",
	} {
		readable, err := scope.ReadableForClass(scope.ClassReservedPhrase, carrier)
		if err != nil {
			t.Fatalf("ReadableForClass(%s, %s): %v", scope.ClassReservedPhrase, carrier, err)
		}
		if !readable {
			t.Errorf("the %s class does not read %s, which the naming law places inside the prohibition's domain",
				scope.ClassReservedPhrase, carrier)
		}
	}
}

// TestReservedPhraseCarrierBoundsMarkdownToTheNamedTreesAndTheRoot pins
// the trees half of the domain, which the naming law states as the three
// trees it names, a tracked Go file, and a tracked root-level markdown
// document.
//
// A markdown document below the root and outside those trees is the one
// class the root-level clause bounds, and it is the class no
// extension-based case reaches: a chart values file, a Python source,
// and a register are all rejected by their extension, so the predicate
// could widen to every tracked markdown document anywhere in the tree
// with each of them still answered false. The class read domain is
// composed on top of this predicate and admits such a document, so the
// trees half is stated here and nowhere else, and widening it would put
// the pass and the naming lint over carriers the law does not govern
// while the composed domain reported nothing.
//
// This is migration tooling rather than a platform behavior, so the case
// carries no spec-section annotation.
func TestReservedPhraseCarrierBoundsMarkdownToTheNamedTreesAndTheRoot(t *testing.T) {
	t.Parallel()

	for _, carrier := range []string{
		"spec/28_communication-channels.md",
		"docs/guides/authoring.md",
		"schemas/README.md",
		"README.md",
		"TESTING.md",
		"pkg/gateway/session.go",
	} {
		if !scope.ReservedPhraseCarrier(carrier) {
			t.Errorf("the carrier predicate rejects %s, which the naming law places inside the prohibition's domain", carrier)
		}
	}

	for _, outside := range []string{
		"charts/lenny/README.md",
		"tests/tier11_docs/notes.md",
		"sdks/python/README.md",
		"charts/lenny/values.yaml",
	} {
		if scope.ReservedPhraseCarrier(outside) {
			t.Errorf("the carrier predicate admits %s, which the naming law leaves outside the prohibition's domain", outside)
		}
	}
}
