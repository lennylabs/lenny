// SPDX-License-Identifier: MIT

// Tier-11 documentation check holding each script-driven sub-step of the
// renaming proposal to one account of how its migration pass is invoked.
//
// The pipeline composes a script-driven edit's command from the sub-step's
// own text and spawns one agent per distinct target file of that sub-step,
// each running that command. An invocation carrying no confinement is a
// usage error the tool rejects, and a sub-step naming several `spec/`
// target files for one pass becomes several concurrent runs of the
// identical command, the second of which aborts on the register entries
// the first consumed. So each script-driven sub-step names one `spec/`
// file for its pass, drawn from the paths its own target list carries,
// states the dry-run and apply command lines confined to `spec/` together
// with the complementary run confined away from it, and states what the
// confined run writes beyond the named file.
//
// The migration passes are also split across two runs, one per phase, so
// no sentence in the same sub-step may state that a pass covers its domain
// in a single tree-wide run: the agent composing the command reads both.
//
// These tests are NOT under a build tag because they exercise the
// repository state directly — no external infrastructure required.
//
// spec: §28, §28.1, §28.3

package tier11_docs_test

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// invocationExpr matches one command line of a migration-pass invocation
// as a sub-step states it, capturing the pass, the register it is driven
// by, the confinement, and the apply flag when the line carries one.
var invocationExpr = regexp.MustCompile(`^go run \./scripts/specshift -pass (\S+) -register (\S+) -(only|except) (\S+)( -apply)?$`)

// passRegisters is the register each migration pass is driven by. A pass
// invoked with another register reads a class of sites it does not rewrite.
var passRegisters = map[string]string{
	"name":       "tests/registers/reserved-phrase-senses.yaml",
	"identifier": "tests/registers/identifier-senses.yaml",
	"line":       "tests/registers/line-citations.yaml",
	"anchor":     "tests/spec-anchor-moves.json",
}

// The clauses a script-driven sub-step states its invocation contract in:
// the assignment of its one target file, the complementary run of the
// other phase, the file list the confined run writes, and the two-run form
// that replaced the single-run claim.
var (
	assignmentSentenceExpr = regexp.MustCompile("assigned `spec/` target file")
	codePhaseFormExpr      = regexp.MustCompile("`-except spec/` in place of `-only spec/`")
	fileListClaimExpr      = regexp.MustCompile("rewrites every `spec/` carrier")
	expectedOutputExpr     = regexp.MustCompile(`expected output`)
	twoRunFormExpr         = regexp.MustCompile(`two\s+confined\s+runs\s+stated\s+above`)
	twoRunScopeExpr        = regexp.MustCompile(`\b(?:across|in|over)\s+the\s+two\s+confined\s+runs\s+stated\s+above`)
	emptyDiffClaimExpr     = regexp.MustCompile(`empty by construction`)
	dryRunEvidenceExpr     = regexp.MustCompile(`dry-run report`)
	workingTreeDiffExpr    = regexp.MustCompile("`git diff -- spec/`")
)

// singleRunClaimExpr matches a sentence stating that a named migration
// pass covers its domain in one tree-wide run. Both spellings sit in
// sentences that name the pass, so the sweep reads the pass name with the
// scope claim rather than the scope claim alone: the proposal states
// tree-wide properties of other subjects that are no site.
var singleRunClaimExpr = regexp.MustCompile(`\b(?:name|identifier|line|anchor) pass\b[^.]*\b(?:in one run|tree-wide)\b|\b(?:in one run|tree-wide)\b[^.]*\b(?:name|identifier|line|anchor) pass\b`)

// specTargetPathExpr matches a `spec/` document path as the proposal
// writes it.
var specTargetPathExpr = regexp.MustCompile(`spec/[0-9A-Za-z_.-]+\.md`)

// TestMechanicalSubStepsConfineEachInvocationToOnePhase_spec_28 requires
// every migration-pass command line a script-driven sub-step states to
// carry a confinement, to name the register its pass is driven by, and to
// appear as a dry-run and apply pair, with the complementary run of the
// other phase stated beside it.
//
// diagnosis: a failure means the command an apply agent composes from the
// sub-step is unconfined, is driven by another class's register, or is
// missing one of the two forms the pipeline runs in order, so the run is
// rejected as a usage error, rewrites a class the sub-step does not stage,
// or writes outside its phase's commit scope. Restate the pair.
//
// spec: §28, §28.1
func TestMechanicalSubStepsConfineEachInvocationToOnePhase_spec_28(t *testing.T) {
	for name, section := range mechanicalSubStepSections(t, readRenamingProposal(t)) {
		lines := invocationLines(section)
		if len(lines) == 0 {
			t.Errorf("%s sub-step %s names an assigned `spec/` target file and states no command line", renamingProposalFile, name)
			continue
		}

		dry := map[string]int{}
		apply := map[string]int{}
		for _, line := range lines {
			match := invocationExpr.FindStringSubmatch(line)
			if match == nil {
				t.Errorf("sub-step %s states the invocation %q, which carries no confinement", name, line)
				continue
			}
			pass, register, scope, confinement, applied := match[1], match[2], match[3], match[4], match[5] != ""
			if want, known := passRegisters[pass]; !known || register != want {
				t.Errorf("sub-step %s drives the %s pass by %q, want %q", name, pass, register, want)
			}
			if scope != "only" || confinement != "spec/" {
				t.Errorf("sub-step %s states the specification-phase invocation %q, which is not confined to `spec/`", name, line)
			}
			if applied {
				apply[pass]++
			} else {
				dry[pass]++
			}
		}

		for _, pass := range sortedPasses(dry, apply) {
			if dry[pass] != 1 || apply[pass] != 1 {
				t.Errorf("sub-step %s states %d dry-run and %d apply invocation(s) of the %s pass, want one of each", name, dry[pass], apply[pass], pass)
			}
		}
		if !statesClaim(codePhaseFormExpr, section) {
			t.Errorf("sub-step %s states no complementary run confined away from `spec/`, so the code phase leaves its carriers unwritten", name)
		}
	}
}

// TestMechanicalSubStepsAssignOneTargetedSpecFile_spec_28 requires the
// assignment sentence of every script-driven sub-step to name exactly one
// `spec/` document, and that document to be one its own target list
// already carries.
//
// diagnosis: a failure means the sub-step either fans its confined command
// out to several agents, whose second run aborts on the register entries
// the first consumed, or stages an edit in a file its target list omits.
// Name one target file, drawn from the target list.
//
// spec: §28, §28.3
func TestMechanicalSubStepsAssignOneTargetedSpecFile_spec_28(t *testing.T) {
	document := readRenamingProposal(t)
	for name, section := range mechanicalSubStepSections(t, document) {
		sentence, ok := assignmentSentence(section)
		if !ok {
			t.Errorf("sub-step %s names no assigned `spec/` target file", name)
			continue
		}
		assigned := uniquePaths(specTargetPathExpr.FindAllString(sentence, -1))
		if len(assigned) != 1 {
			t.Errorf("sub-step %s assigns its migration pass to %v, want one `spec/` file", name, assigned)
			continue
		}
		targets := collapseWrapping(targetRegion(section))
		if !strings.Contains(targets, assigned[0]) {
			t.Errorf("sub-step %s assigns its migration pass to %s, which its target list does not name", name, assigned[0])
		}
	}
}

// TestMechanicalSubStepsStateWhatTheConfinedRunWrites_spec_28 requires
// every script-driven sub-step to state that its confined run rewrites the
// `spec/` carriers of its class beyond the assigned file, that the wider
// file list is the expected output, and, where it states the assigned
// file's diff is empty, the evidence that stands in for that diff.
//
// diagnosis: a failure means the apply agent's confirmation that the dry
// run touched only targeted files, and the verifier's rule that an empty
// mechanical diff is a failure, resolve against nothing in the sub-step's
// own text, so a correct run reads as one that exceeded its scope or wrote
// nothing. State the file list and the evidence.
//
// spec: §28, §28.1
func TestMechanicalSubStepsStateWhatTheConfinedRunWrites_spec_28(t *testing.T) {
	for name, section := range mechanicalSubStepSections(t, readRenamingProposal(t)) {
		if !statesClaim(fileListClaimExpr, section) || !statesClaim(expectedOutputExpr, section) {
			t.Errorf("sub-step %s does not state that its confined run rewrites `spec/` carriers beyond the assigned file and that the wider file list is expected", name)
		}
		if !statesClaim(emptyDiffClaimExpr, section) {
			if !invokesOnly(section, "identifier") {
				t.Errorf("sub-step %s states no diff for its assigned file, which its pass writes nothing to", name)
			}
			continue
		}
		if !statesClaim(dryRunEvidenceExpr, section) || !statesClaim(workingTreeDiffExpr, section) {
			t.Errorf("sub-step %s states an empty diff for its assigned file and names no evidence in its place", name)
		}
	}
}

// TestMechanicalSubStepsStateNoSingleRunScope_spec_28 requires no sentence
// of a script-driven sub-step to state that one of its passes covers its
// domain in a single tree-wide run.
//
// diagnosis: a failure means the paragraph the apply agent composes the
// command from states the contrary of the confined command lines beside
// it, so the agent has two accounts of the run's scope. Replace the
// single-run clause with the two-run form.
//
// spec: §28, §28.1
func TestMechanicalSubStepsStateNoSingleRunScope_spec_28(t *testing.T) {
	for name, section := range mechanicalSubStepSections(t, readRenamingProposal(t)) {
		for _, site := range singleRunSites(section) {
			t.Errorf("sub-step %s states %q, which is the contrary of its confined command lines", name, site)
		}
		if invokesOnly(section, "identifier") {
			continue
		}
		if !statesClaim(twoRunFormExpr, section) {
			t.Errorf("sub-step %s states no run of its pass across the two confined runs", name)
		}
	}
}

// TestSingleRunSweepReadsTheRestoredScope_spec_28 restores the single-run
// scope in every sub-step carrying the two-run form and requires each
// restored site reported.
//
// The two forms differ by one clause, so a sweep keyed on anything coarser
// passes both and the contradiction returns unreported.
//
// diagnosis: a failure means the sweep does not read a sentence scoping a
// pass to one tree-wide run, so restoring it leaves the check green. Key
// the sweep on the pass name together with the scope clause.
//
// spec: §28, §28.1
func TestSingleRunSweepReadsTheRestoredScope_spec_28(t *testing.T) {
	document := readRenamingProposal(t)

	restored := 0
	for _, section := range mechanicalSubStepSections(t, document) {
		if statesClaim(twoRunScopeExpr, section) {
			restored++
		}
	}
	if restored == 0 {
		t.Fatalf("%s carries no two-run form in a script-driven sub-step, so the case restores nothing", renamingProposalFile)
	}

	reverted := twoRunScopeExpr.ReplaceAllString(document, "in one run")
	reported := 0
	for name, section := range mechanicalSubStepSections(t, reverted) {
		if !statesClaim(twoRunFormExpr, section) && strings.Contains(collapseWrapping(section), "one run") {
			if len(singleRunSites(section)) == 0 {
				t.Errorf("sub-step %s carries the restored single-run scope unreported", name)
				continue
			}
			reported++
		}
	}
	if reported != restored {
		t.Errorf("the restored single-run scope was reported in %d sub-step(s), want %d", reported, restored)
	}
}

// singleRunSites returns every sentence of a sub-step that scopes one of
// its passes to a single tree-wide run.
func singleRunSites(section string) []string {
	var sites []string
	for _, sentence := range proseSentences(section) {
		if site := singleRunClaimExpr.FindString(sentence); site != "" {
			sites = append(sites, site)
		}
	}
	return sites
}

// invokesOnly reports whether every invocation a sub-step states runs the
// named pass. A sub-step staging that pass alone is exempt from the claims
// the other passes' sub-steps carry, because its assigned file holds sites
// its pass rewrites.
func invokesOnly(section, pass string) bool {
	lines := invocationLines(section)
	if len(lines) == 0 {
		return false
	}
	for _, line := range lines {
		match := invocationExpr.FindStringSubmatch(line)
		if match == nil || match[1] != pass {
			return false
		}
	}
	return true
}

// invocationLines returns every migration-pass command line a sub-step
// states, read out of its fenced blocks.
func invocationLines(section string) []string {
	var lines []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "go run ./scripts/specshift") {
			lines = append(lines, line)
		}
	}
	return lines
}

// assignmentSentence returns the sentence of a sub-step that names its one
// `spec/` target file.
func assignmentSentence(section string) (string, bool) {
	for _, sentence := range proseSentences(section) {
		if assignmentSentenceExpr.MatchString(sentence) {
			return sentence, true
		}
	}
	return "", false
}

// targetRegion returns the target list of a sub-step, which opens the
// sub-step and closes where its rationale opens.
func targetRegion(section string) string {
	const (
		opening = "**Target:**"
		closing = "**Rationale:**"
	)
	start := strings.Index(section, opening)
	if start < 0 {
		return ""
	}
	region := section[start:]
	if end := strings.Index(region, closing); end >= 0 {
		region = region[:end]
	}
	return region
}

// uniquePaths returns the distinct paths of a match list, in the order
// they first appear.
func uniquePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var distinct []string
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		distinct = append(distinct, path)
	}
	return distinct
}

// sortedPasses returns the passes either tally carries, in a stable order
// so a failure names them the same way on every run.
func sortedPasses(tallies ...map[string]int) []string {
	seen := map[string]bool{}
	var passes []string
	for _, tally := range tallies {
		for pass := range tally {
			if !seen[pass] {
				seen[pass] = true
				passes = append(passes, pass)
			}
		}
	}
	sort.Strings(passes)
	return passes
}

// mechanicalSubStepSections returns the body of every sub-step that hands
// an edit to a migration pass, keyed by the sub-step label the document
// publishes it under. A sub-step is script-driven when it names an
// assigned `spec/` target file or states a pass invocation.
func mechanicalSubStepSections(t *testing.T, document string) map[string]string {
	t.Helper()
	sections, err := subStepSections(document)
	if err != nil {
		t.Fatalf("read the sub-step sections of %s: %v", renamingProposalFile, err)
	}
	mechanical := make(map[string]string, len(sections))
	for name, section := range sections {
		if statesClaim(assignmentSentenceExpr, section) || len(invocationLines(section)) > 0 {
			mechanical[name] = section
		}
	}
	if len(mechanical) == 0 {
		t.Fatalf("%s publishes no sub-step handing an edit to a migration pass", renamingProposalFile)
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
