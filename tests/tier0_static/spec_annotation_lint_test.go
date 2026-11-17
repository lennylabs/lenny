// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The annotation lint holds .claude/rules/spec-annotations.md on the Go
// lines a branch adds or changes against its merge base with main. It
// reads changed lines alone, because the tree still carries the
// annotations written under the earlier rule, and each area re-annotates
// its code when it migrates; the section-citation ratchet beside it
// keeps that earlier population from growing.
//
// The rule is a project rule rather than a behavior the specification
// states, so these gates carry no specification annotation.

// diagnosis: a line this branch adds or changes puts a // spec:
// annotation inside a function body or on a struct field, names a test
// with a section number, or copies specification text into a comment.
// Each finding names the file, the line, and the rule it breaks.
func TestSpecAnnotationLintPassesOnTheBranchDiff(t *testing.T) {
	t.Parallel()
	rep := lintBranch(t)
	var findings []string
	for _, f := range rep.Findings {
		if f.Rule != annotation.UnresolvedIdentifier && f.Rule != annotation.RetiredIdentifier {
			findings = append(findings, f.String())
		}
	}
	if len(findings) > 0 {
		t.Errorf("%d line(s) changed since %s (%s) break the annotation rule in .claude/rules/spec-annotations.md:\n  %s",
			len(findings), rep.Base, rep.BaseRef, strings.Join(findings, "\n  "))
	}
	t.Logf("linted %d changed Go file(s) against %s (%s)", rep.Files, rep.Base, rep.BaseRef)
}

// diagnosis: a // spec: annotation this branch adds or changes cites an
// identifier that spec/registry.json does not carry, so the identifier
// is misspelled, retired, or belongs to a page that is not registered.
func TestSpecAnnotationIdentifiersResolveOnTheBranchDiff(t *testing.T) {
	t.Parallel()
	rep := lintBranch(t)
	for _, reason := range rep.Skipped {
		if strings.HasPrefix(reason, "identifier resolution:") {
			t.Skipf("not-yet-applicable: %s; the reference tool's step adds the registry", reason)
		}
	}
	var findings []string
	for _, f := range rep.Findings {
		if f.Rule == annotation.UnresolvedIdentifier || f.Rule == annotation.RetiredIdentifier {
			findings = append(findings, f.String())
		}
	}
	if len(findings) > 0 {
		t.Errorf("%d annotation(s) changed since %s cite an unregistered identifier:\n  %s",
			len(findings), rep.Base, strings.Join(findings, "\n  "))
	}
}

// lintBranch runs the lint over the working tree, skipping when the
// branch has no merge base with the integration branch.
func lintBranch(t *testing.T) annotation.BranchReport {
	t.Helper()
	rep, err := annotation.LintBranch(context.Background(), schematest.RepoRoot(t))
	if errors.Is(err, annotation.ErrNoBase) {
		t.Skipf("not-yet-applicable: %v, as in a shallow clone, so there is no branch diff to lint", err)
	}
	if err != nil {
		t.Fatalf("lint the branch diff: %v", err)
	}
	return rep
}
