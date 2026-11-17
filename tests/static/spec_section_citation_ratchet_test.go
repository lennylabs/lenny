// SPDX-License-Identifier: MIT

package static

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The section-citation ratchet holds each Go package's count of
// section-number citations in // spec: annotations at or below the count
// in tests/registers/spec-section-citations.yaml. Packages stand in for
// areas until the areas map to packages. The ratchet's own behavior is
// tested over fixture trees in scripts/specshift/annotation.
//
// The rule is a project rule rather than a behavior the specification
// states, so this gate carries no specification annotation.

// diagnosis: a package carries more section-number citations than the
// register records, or a package's count fell and the run wrote the
// lower count back. For a rise, cite the requirement identifier when the
// page the package implements exists; otherwise raise the package's
// count in the register in the same commit. For a fall, commit the
// rewritten register with the change.
func TestSpecSectionCitationRatchetHoldsEachPackage(t *testing.T) {
	t.Parallel()
	rep, err := annotation.NewRatchet(schematest.RepoRoot(t)).Run(context.Background())
	if err != nil {
		t.Fatalf("run the section-citation ratchet: %v", err)
	}
	if len(rep.Failures) > 0 {
		lines := make([]string, 0, len(rep.Failures))
		for _, f := range rep.Failures {
			lines = append(lines, f.String())
		}
		t.Errorf("%d package(s) carry more section-number citations than %s allows. "+
			"Cite the identifier where the page exists; in an area that has not migrated, "+
			"raise the package's count in the register in the same commit:\n  %s",
			len(rep.Failures), annotation.RatchetBaselinePath, strings.Join(lines, "\n  "))
	}
	if len(rep.Lowered) > 0 {
		lines := make([]string, 0, len(rep.Lowered))
		for _, c := range rep.Lowered {
			lines = append(lines, c.String())
		}
		t.Errorf("%d package count(s) fell, and the run lowered them in %s. Commit the lowered register with the change:\n  %s",
			len(rep.Lowered), annotation.RatchetBaselinePath, strings.Join(lines, "\n  "))
	}
	if rep.Held+len(rep.Lowered)+risen(rep) != rep.Carried {
		t.Errorf("the register carried %d count(s), and the run accounted for %d held, %d lowered, and %d risen",
			rep.Carried, rep.Held, len(rep.Lowered), risen(rep))
	}
	t.Logf("%d Go file(s), %d section-number citation(s), %d package count(s) carried, %d held",
		rep.Files, rep.Citations, rep.Carried, rep.Held)
}

// risen counts the failures that are rises of a registered count rather
// than packages the register does not carry.
func risen(rep annotation.RatchetReport) int {
	n := 0
	for _, f := range rep.Failures {
		if !f.Absent {
			n++
		}
	}
	return n
}
