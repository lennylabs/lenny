// SPDX-License-Identifier: MIT

package structure

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/survey"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The coverage gate holds the test rule of
// .claude/rules/spec-annotations.md on the requirements a branch adds or
// modifies against its merge base with main: each one has a test that
// cites it, or an open or planned issue that records why it has none
// (change 0116, decision 1). It reads the working tree, as the annotation
// lint does, and builds on the annotation index and the specification
// comparison of scripts/specshift/survey. Numbered sections are outside
// it, and it passes with nothing to check until an area registers
// requirements.
//
// The rule is a project rule rather than a behavior the specification
// states, so this gate carries no specification annotation.

// diagnosis: the branch adds a requirement, or changes a requirement's
// text, and no test function's doc comment cites it as `// spec:
// <identifier>`, and no open or planned issue under issues/ lists it in
// its `requirements` front matter. Add the annotation to the test that
// verifies the requirement, or file the issue that records why no test
// does yet. The finding names the requirement and its page.
func TestEveryChangedRequirementHasACitingTest(t *testing.T) {
	t.Parallel()
	rep, err := survey.CheckCoverage(context.Background(), schematest.RepoRoot(t))
	if errors.Is(err, annotation.ErrNoBase) {
		t.Skipf("not-yet-applicable: %v, as in a shallow clone, so there is no branch diff to read", err)
	}
	if err != nil {
		t.Fatalf("read the branch diff: %v", err)
	}
	for _, u := range rep.Unread {
		t.Logf("annotation index: left out %s", u)
	}
	if uncovered := rep.Uncovered(); len(uncovered) > 0 {
		lines := make([]string, 0, len(uncovered))
		for _, c := range uncovered {
			lines = append(lines, c.String())
		}
		t.Errorf("%d requirement(s) added or modified since %s (%s) have no citing test:\n  %s",
			len(uncovered), rep.Base, rep.BaseRef, strings.Join(lines, "\n  "))
	}
	t.Logf("checked %d added or modified requirement(s) against %s (%s)", len(rep.Requirements), rep.Base, rep.BaseRef)
}
