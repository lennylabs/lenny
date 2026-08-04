// SPDX-License-Identifier: MIT

// Tier-11 documentation check holding each script-driven sub-step of the
// renaming proposal to one account of what its confined run writes and to
// the declaration position the name pass reads.
//
// A sub-step that hands one of its edits to a migration pass names the one
// `spec/` file the invocation is issued from and states that the confined
// run rewrites every `spec/` carrier of its class rather than that file
// alone. A sentence in the same sub-step claiming that every other `spec/`
// path the sub-step touches is edited by hand states the contrary of that
// file-list sentence, and the agent that composes the command reads both.
// The claim covers the edits the sub-step authors rather than the paths its
// confined run writes.
//
// The name pass indexes the declared identifier space out of the register
// tables of the communication-channels section (scripts/specshift/name:
// "The registers are the declaration position"). Naming a different table of
// that section as what the pass indexes points the precondition an agent
// confirms before the run at a table that declares nothing.
//
// These tests are NOT under a build tag because they exercise the
// repository state directly — no external infrastructure required.
//
// spec: §28, §28.3

package tier11_docs_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// assignmentSentenceExpr matches the sentence a script-driven sub-step
// names its one `spec/` target file in, and fileListClaimExpr matches the
// sentence stating what the confined run writes beyond that file.
var (
	assignmentSentenceExpr = regexp.MustCompile("assigned `spec/` target\\s+file")
	fileListClaimExpr      = regexp.MustCompile("rewrites every `spec/`\\s+carrier")
)

// touchedPathClaimExpr matches the widened form of the authored-edit
// sentence, which claims every other `spec/` path the sub-step touches
// carries an edit made by hand. The confined run touches `spec/` paths
// beyond the assigned file by design, so the claim holds over the edits the
// sub-step stages rather than over the paths it touches.
var touchedPathClaimExpr = regexp.MustCompile("[Ee]very other `spec/` path[^.]*\\btouch(?:es|ed|ing)?\\b")

// TestMechanicalSubStepsScopeTheAuthoredEditClaim_spec_28 reports every
// script-driven sub-step of the renaming proposal that states both that its
// confined run rewrites `spec/` carriers beyond the assigned file and that
// every other `spec/` path it touches is edited by hand.
//
// diagnosis: a failure means the text an apply agent composes its command
// from gives two accounts of which `spec/` paths the run writes, so the
// agent confirming that the dry run touched only targeted files reads the
// contrary of the expected file list. Narrow the authored-edit sentence to
// the edits the sub-step stages.
//
// spec: §28, §28.3
func TestMechanicalSubStepsScopeTheAuthoredEditClaim_spec_28(t *testing.T) {
	document := readRenamingProposal(t)
	for name, section := range mechanicalSubStepSections(t, document) {
		if !statesClaim(fileListClaimExpr, section) {
			continue
		}
		if site := touchedPathClaimExpr.FindString(collapseWrapping(section)); site != "" {
			t.Errorf("%s sub-step %s states that the confined run rewrites carriers beyond the assigned file and also that %q", renamingProposalFile, name, site)
		}
	}
}

// TestAuthoredEditClaimSweepReadsTheWidenedForm_spec_28 restores the
// widened claim in every sub-step that carries the narrowed one and
// requires each reported.
//
// The narrowed and the widened sentence differ by one clause, so a sweep
// keyed on anything coarser than that clause passes both and the
// contradiction returns unreported.
//
// diagnosis: a failure means the sweep does not read the widened
// authored-edit claim, so restoring it leaves the check green. Key the
// sweep on the claim's own clause.
//
// spec: §28, §28.3
func TestAuthoredEditClaimSweepReadsTheWidenedForm_spec_28(t *testing.T) {
	document := readRenamingProposal(t)

	reverted := 0
	for _, section := range mechanicalSubStepSections(t, document) {
		if !statesClaim(fileListClaimExpr, section) {
			continue
		}
		reverted += len(narrowedClaimExpr.FindAllString(section, -1))
	}
	if reverted == 0 {
		t.Fatalf("%s carries no narrowed authored-edit sentence, so the case reverts nothing", renamingProposalFile)
	}

	widened := narrowedClaimExpr.ReplaceAllString(document, widenedClaim)
	sites := 0
	for name, section := range mechanicalSubStepSections(t, widened) {
		if !statesClaim(fileListClaimExpr, section) {
			continue
		}
		if !strings.Contains(collapseWrapping(section), widenedClaim) {
			continue
		}
		if !statesClaim(touchedPathClaimExpr, section) {
			t.Errorf("sub-step %s carries the widened claim unreported", name)
			continue
		}
		sites++
	}
	if sites != reverted {
		t.Errorf("the widened claim was reported in %d sub-step(s), want %d", sites, reverted)
	}
}

// The narrowed authored-edit claim as the document carries it, wrapped at
// any column, and the widened form the sweep exists to report.
var (
	narrowedClaimExpr = regexp.MustCompile("`spec/` path this\\s+sub-step stages an edit in takes an authored\\s+edit")
	widenedClaim      = "`spec/` path this sub-step touches takes an edit made by hand"
)

// namePassDeclarationExpr matches the clause naming what the name pass
// indexes in its assigned file, and namePassTargetExpr matches the
// assignment of that file.
var (
	namePassDeclarationExpr = regexp.MustCompile(`§28\.3 register tables the name pass indexes`)
	namePassTargetExpr      = regexp.MustCompile("assigned `spec/` target file is\\s+`spec/28_communication-channels\\.md`")
)

// TestNamePassTargetNamesTheDeclarationPosition_spec_28_3 requires the
// sub-step assigning its mechanical edit to the communication-channels
// section to name the register tables the name pass indexes and the
// subsections the sub-step confirms are present before the run.
//
// diagnosis: a failure means the precondition an agent confirms before the
// confined name-pass run points at a table that declares no identifier, so
// the run's abort on an undeclared identifier space reads as unexplained.
// Name the register tables as the position the pass indexes.
//
// spec: §28, §28.3
func TestNamePassTargetNamesTheDeclarationPosition_spec_28_3(t *testing.T) {
	section := namePassSubStep(t, readRenamingProposal(t))
	sentence := assignmentSentence(t, section)

	if !namePassDeclarationExpr.MatchString(sentence) {
		t.Errorf("the assignment sentence %q does not name the register tables the name pass indexes", sentence)
	}
	if strings.Contains(sentence, "naming table") {
		t.Errorf("the assignment sentence %q names the naming table, which declares no identifier", sentence)
	}
	if !strings.Contains(sentence, "§28.1 through §28.4") {
		t.Errorf("the assignment sentence %q does not state the subsections the sub-step confirms are present", sentence)
	}
}

// TestNamePassDeclarationSweepReadsTheNamingTableForm_spec_28_3 restores
// the naming-table wording and requires it reported.
//
// diagnosis: a failure means the sweep passes a sentence pointing the
// pass's declaration position at the naming table, so the wording returns
// unreported. Key the sweep on the register tables.
//
// spec: §28, §28.3
func TestNamePassDeclarationSweepReadsTheNamingTableForm_spec_28_3(t *testing.T) {
	declared := regexp.MustCompile(`committed §28\.3 register tables the name\s+pass indexes`)
	const reverted = "§28.3 naming table the pass indexes"

	document := readRenamingProposal(t)
	if !declared.MatchString(document) {
		t.Fatalf("%s carries no clause naming the register tables the name pass indexes, so the case reverts nothing", renamingProposalFile)
	}

	sentence := assignmentSentence(t, namePassSubStep(t, declared.ReplaceAllString(document, reverted)))
	if namePassDeclarationExpr.MatchString(sentence) {
		t.Errorf("the reverted sentence %q still reads as naming the register tables", sentence)
	}
	if !strings.Contains(sentence, "naming table") {
		t.Errorf("the reverted sentence %q does not carry the restored wording, so the case reverts nothing", sentence)
	}
}

// assignmentSentence returns the sentence of a sub-step section that names
// its one `spec/` target file.
func assignmentSentence(t *testing.T, section string) string {
	t.Helper()
	for _, sentence := range proseSentences(section) {
		if statesClaim(assignmentSentenceExpr, sentence) {
			return sentence
		}
	}
	t.Fatalf("the sub-step names no assigned `spec/` target file")
	return ""
}

// namePassSubStep returns the section of the sub-step that assigns its
// mechanical edit to the communication-channels section.
func namePassSubStep(t *testing.T, document string) string {
	t.Helper()
	for _, section := range mechanicalSubStepSections(t, document) {
		if statesClaim(namePassTargetExpr, section) {
			return section
		}
	}
	t.Fatalf("%s assigns no mechanical edit to `spec/28_communication-channels.md`", renamingProposalFile)
	return ""
}

// mechanicalSubStepSections returns the body of every sub-step that assigns
// its mechanical edit to one `spec/` target file, keyed by the sub-step
// label the document publishes it under.
func mechanicalSubStepSections(t *testing.T, document string) map[string]string {
	t.Helper()
	sections, err := subStepSections(document)
	if err != nil {
		t.Fatalf("read the sub-step sections of %s: %v", renamingProposalFile, err)
	}
	mechanical := make(map[string]string, len(sections))
	for name, section := range sections {
		if statesClaim(assignmentSentenceExpr, section) {
			mechanical[name] = section
		}
	}
	if len(mechanical) == 0 {
		t.Fatalf("%s publishes no sub-step naming an assigned `spec/` target file", renamingProposalFile)
	}
	return mechanical
}

// statesClaim reports whether a region of the document carries a claim,
// with the source wrapping collapsed so a sentence broken across two lines
// reads the same as one written on a single line.
func statesClaim(expr *regexp.Regexp, region string) bool {
	return expr.MatchString(collapseWrapping(region))
}

// subStepSections splits the change section of the proposal into the body
// of each sub-step it publishes, keyed by the sub-step label.
func subStepSections(document string) (map[string]string, error) {
	sections := make(map[string]string)
	var current string
	var body []string
	inSection := false
	flush := func() {
		if current != "" {
			sections[current] = strings.Join(body, "\n")
		}
		current, body = "", nil
	}
	for _, line := range strings.Split(document, "\n") {
		if strings.HasPrefix(line, "## ") {
			flush()
			inSection = strings.HasPrefix(line, changeSectionHeading)
			continue
		}
		if !inSection {
			continue
		}
		if m := subStepHeading.FindStringSubmatch(line); m != nil {
			flush()
			current = m[1]
			continue
		}
		if current != "" {
			body = append(body, line)
		}
	}
	flush()
	if len(sections) == 0 {
		return nil, fmt.Errorf("the document publishes no sub-step under %q", changeSectionHeading)
	}
	return sections, nil
}
