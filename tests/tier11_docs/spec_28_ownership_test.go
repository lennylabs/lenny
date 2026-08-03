// SPDX-License-Identifier: MIT

// Tier-11 documentation check holding the record of who creates the
// communication-channels section to one document. The section, its
// §28.1 through §28.4 headings, and the register they carry are landed
// by the proposal that authors them, and the proposal that renames the
// channels appends the later subsections to the file it finds. A clause
// in the renaming proposal that still credits one of its own sub-steps
// with creating the file, the four headings, or the register puts two
// documents in charge of creating one file, which is the state the
// ownership transfer exists to end.
//
// The status block and the scope paragraph of the renaming proposal, and
// its convergence record, are outside the sweep. The first two state
// what the proposal was approved as, and the record is a history of the
// review rather than an instruction to an implementor.
//
// These tests are NOT under a build tag because they exercise the
// repository state directly — no external infrastructure required.
//
// spec: §28, §28.3

package tier11_docs_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// renamingProposalFile is the proposal that renames the channels and
// appends the later subsections to the section.
const renamingProposalFile = "proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md"

// The headings that bracket the swept region of that document. The sweep
// opens at the first numbered section, which leaves the status block and
// the scope paragraph above it outside, and runs to the end of the
// document with the convergence record excised. The record states
// decisions as they were made, so it is a history of the review rather
// than an instruction to an implementor; the sections below it, which
// hold the open decisions and the files-touched list, carry attributions
// an implementor reads and are swept.
const (
	sweptRegionOpening       = "## 0."
	convergenceRecordOpening = "## 9. Resolved in adversarial review"
	convergenceRecordClosing = "## 10."
)

// changeSectionHeading is the heading the proposal publishes its
// sub-steps under, and subStepHeading matches one sub-step heading below
// it. The sub-step names are read out of the document rather than
// restated here: they are labels of that document, so a copy in this
// source would name something a later reader cannot resolve.
const changeSectionHeading = "## 6. Proposed changes"

var subStepHeading = regexp.MustCompile(`^### ([A-Za-z]+-\d+)\.`)

// The three parts of an attribution clause. A creation verb attributes
// the making, or the already-made presence, of something to a subject.
// An object names one of the three things the ownership transfer moved:
// the file as a new specification file, the four headings the section
// opens with, and the register they carry. A negated verb states the
// opposite of an attribution, so a clause carrying one is not a site.
var (
	creationVerbExpr = regexp.MustCompile(`\b(?:creates?|created|authors?|authored|writes|wrote|lands|landed|exists|existed)\b`)
	creationObjExpr  = regexp.MustCompile(`new specification files?|§28\.1 through §28\.4|the §28\.3 register`)
	negatedVerbExpr  = regexp.MustCompile(`\b(?:creates?|writes|authors?|lands|names)\b (?:none|no|neither)\b`)
)

// A files-touched bullet names a file and states whether it is new,
// without naming a subject: the document is the subject of its own
// files-touched list, so a bullet calling the section file new is an
// attribution on its own terms and is read as one.
var (
	sectionFileExpr  = regexp.MustCompile("`?spec/28_communication-channels\\.md`?")
	newFileClaimExpr = regexp.MustCompile(`\b(?:both new|new specification files?|is new|are new)\b|, new\b`)
)

// TestSection28OwnershipSitsInOneProposal_spec_28 sweeps the renaming
// proposal for a clause crediting one of its own sub-steps with creating
// the communication-channels section, its opening headings, or its
// register.
//
// diagnosis: a failure means two proposal documents state that they
// create one specification file, so an implementor reading the renaming
// proposal is told to author a section that is already in the tree.
// Rewrite the clause to credit the proposal that landed the section and
// to state what this sub-step does with it.
//
// spec: §28, §28.3
func TestSection28OwnershipSitsInOneProposal_spec_28(t *testing.T) {
	document := readRenamingProposal(t)
	for _, sentence := range ownershipSites(t, document) {
		t.Errorf("%s credits one of its own sub-steps with creating the section: %q", renamingProposalFile, sentence)
	}
}

// TestSection28OwnershipSweepReadsTheFilesTouchedList_spec_28 plants the
// attribution the sweep exists to report into the files-touched list,
// which sits below the convergence record, and requires it reported.
//
// A sweep that stops at the convergence record reads none of the
// sections below it, and the files-touched list is where the document
// states which specification files it lands as new. The planted bullet
// is the form that list carried before ownership of the section moved,
// so a sweep that reports it reads the whole document rather than its
// first nine sections.
//
// diagnosis: a failure means the ownership sweep reads a truncated
// document, so an attribution reinstated below the convergence record
// goes unreported. Widen the swept region to the end of the document
// with the convergence record excised.
//
// spec: §28, §28.3
func TestSection28OwnershipSweepReadsTheFilesTouchedList_spec_28(t *testing.T) {
	document := readRenamingProposal(t)
	const planted = "\n- `spec/28_communication-channels.md` and `spec/29_communication-scenarios.md`, both new.\n"

	index := strings.Index(document, filesTouchedHeading)
	if index < 0 {
		t.Fatalf("%s carries no heading %q, so the reject case plants nothing below the convergence record", renamingProposalFile, filesTouchedHeading)
	}
	cut := index + len(filesTouchedHeading)
	sites := ownershipSites(t, document[:cut]+planted+document[cut:])

	if len(sites) != 1 {
		t.Fatalf("a files-touched bullet calling the section file new reported %d site(s), want one: %q", len(sites), sites)
	}
	if !strings.Contains(sites[0], "both new") {
		t.Errorf("the reported site is %q, which is not the planted bullet", sites[0])
	}
}

// TestSection28OwnershipSweepReadsACreditedSentence_spec_28 plants a
// sentence crediting the proposal that landed the section in one clause
// and a sub-step of the renaming proposal in another, and requires it
// reported.
//
// Every sentence the ownership transfer rewrote names the landing
// proposal, so a sweep that excuses a whole sentence for naming it is
// blind on exactly the text the transfer produced, and a partial revert
// that restores a sub-step as the creating subject beside the credit
// goes unreported. The credit covers the clause carrying it and no more.
//
// diagnosis: a failure means the ownership sweep reads the credit over
// the whole sentence rather than over the clause carrying it, so a
// sub-step restored as the creating subject alongside the credit is not
// reported. Read the subject, the creation verb, and the moved object
// within one clause.
//
// spec: §28, §28.3
func TestSection28OwnershipSweepReadsACreditedSentence_spec_28(t *testing.T) {
	document := readRenamingProposal(t)
	subSteps := subStepNames(document)
	if len(subSteps) == 0 {
		t.Fatalf("%s publishes no sub-step under %q, so the case plants no subject", renamingProposalFile, changeSectionHeading)
	}

	reverted := subSteps[0] + " creates §28.1 through §28.4, which proposal 0067 confirms."
	sites := ownershipSites(t, plantBelowFilesTouched(t, document, reverted))
	if len(sites) != 1 {
		t.Fatalf("a sentence crediting %s beside the landing proposal reported %d site(s), want one: %q", subSteps[0], len(sites), sites)
	}
	if !strings.Contains(sites[0], "creates §28.1 through §28.4") {
		t.Errorf("the reported site is %q, which is not the planted sentence", sites[0])
	}

	credited := "Proposal 0067 created §28.1 through §28.4, and " + subSteps[0] + " renames the channels they name."
	if sites := ownershipSites(t, plantBelowFilesTouched(t, document, credited)); len(sites) != 0 {
		t.Errorf("a sentence crediting the proposal that landed the section reported %q", sites)
	}
}

// plantBelowFilesTouched returns the document with one paragraph planted
// below the files-touched heading, which sits inside the swept region.
func plantBelowFilesTouched(t *testing.T, document, paragraph string) string {
	t.Helper()
	index := strings.Index(document, filesTouchedHeading)
	if index < 0 {
		t.Fatalf("%s carries no heading %q, so the case plants nothing inside the swept region", renamingProposalFile, filesTouchedHeading)
	}
	cut := index + len(filesTouchedHeading)
	return document[:cut] + "\n\n" + paragraph + "\n" + document[cut:]
}

// TestSection28OwnershipRecordMatchesTheProposal_spec_28 sweeps the
// durable record of what an apply run left open for a clause crediting
// the renaming proposal with creating the section, its opening headings,
// or its register, or calling the section file one that proposal lands
// as new.
//
// The record and the proposal describe one transfer. A record stating
// that the renaming proposal still creates the file outlives the clause
// it describes once that clause is rewritten, and it is the artifact a
// reader consults to find out what is still open, so it states the same
// attribution the proposal does or it states nothing about it.
//
// diagnosis: a failure means the queue record and the proposal disagree
// on which document creates the communication-channels section, so a
// reader is told an ownership edit is outstanding that the tree already
// carries. Close or rewrite the record to what remains open.
//
// spec: §28, §28.3
func TestSection28OwnershipRecordMatchesTheProposal_spec_28(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ownershipRecordFile))
	if err != nil {
		t.Fatalf("read %s: %v", ownershipRecordFile, err)
	}
	subjects, err := recordSubjectMatcher(subStepNames(readRenamingProposal(t)))
	if err != nil {
		t.Fatalf("build the subject matcher: %v", err)
	}

	for _, sentence := range proseSentences(string(body)) {
		if attributesCreation(sentence, subjects) || claimsTheSectionIsNew(sentence) {
			t.Errorf("%s credits the renaming proposal with creating the section: %q", ownershipRecordFile, sentence)
		}
	}
}

// ownershipRecordFile is the durable record of what each apply run left
// open, which is where the ownership transfer was recorded as residue.
const ownershipRecordFile = "PROPOSAL-QUEUE.md"

// recordSubjectMatcher returns the matcher for a subject the record
// attributes work to. The record names the renaming proposal by number
// as well as by sub-step, because it is written from outside that
// document.
func recordSubjectMatcher(subSteps []string) (*regexp.Regexp, error) {
	matcher, err := subjectMatcher(subSteps)
	if err != nil {
		return nil, err
	}
	return regexp.Compile(matcher.String() + `|proposal 0064|\b0064's\b`)
}

// filesTouchedHeading opens the section listing the files the renaming
// proposal touches, which sits below the convergence record.
const filesTouchedHeading = "## 11. Files touched on application"

// readRenamingProposal returns the document the sweep reads.
func readRenamingProposal(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), renamingProposalFile))
	if err != nil {
		t.Fatalf("read %s: %v", renamingProposalFile, err)
	}
	return string(body)
}

// ownershipSites returns every sentence of the swept region that credits
// a subject of the renaming proposal with creating the section, its
// opening headings, or its register, or that states the section file is
// one the proposal lands as new.
func ownershipSites(t *testing.T, document string) []string {
	t.Helper()
	subSteps := subStepNames(document)
	if len(subSteps) == 0 {
		t.Fatalf("%s publishes no sub-step under %q, so the sweep reads no subject", renamingProposalFile, changeSectionHeading)
	}
	subjects, err := subjectMatcher(subSteps)
	if err != nil {
		t.Fatalf("build the subject matcher: %v", err)
	}
	region, err := sweptRegion(document)
	if err != nil {
		t.Fatalf("read the swept region of %s: %v", renamingProposalFile, err)
	}

	var sites []string
	for _, sentence := range proseSentences(region) {
		if attributesCreation(sentence, subjects) || claimsTheSectionIsNew(sentence) {
			sites = append(sites, sentence)
		}
	}
	return sites
}

// ownerCreditPhrase is how a document credits the proposal that landed
// the section, read case-insensitively because it is written both
// mid-sentence and at the head of one. It is a competing subject rather
// than a licence over the whole sentence: a clause carrying it credits
// the landing proposal, and a neighbouring clause crediting a sub-step
// of the renaming proposal instead is still a site.
const ownerCreditPhrase = "proposal 0067"

// A clause boundary is a semicolon or one of the conjunctions the
// proposals join clauses with. Attribution is read inside one clause,
// because every sentence the ownership transfer rewrote states two
// attributions at once: the landing proposal created the section, and a
// sub-step of the renaming proposal does something else with it.
// Reading the three parts across the whole sentence would either report
// every corrected sentence or, with the sentence excused for naming the
// landing proposal, report none of them.
var clauseBoundaryExpr = regexp.MustCompile(`;|,? \b(?:and|but|so|which|because|while|then|although|though|whereas)\b `)

// claimsTheSectionIsNew reports whether one sentence states that the
// renaming proposal lands the section file as a new file.
func claimsTheSectionIsNew(sentence string) bool {
	return sectionFileExpr.MatchString(sentence) && newFileClaimExpr.MatchString(sentence)
}

// subStepNames returns the sub-step labels the proposal publishes under
// its change section, which are the subjects a clause attributes work to.
func subStepNames(document string) []string {
	var names []string
	inSection := false
	for _, line := range strings.Split(document, "\n") {
		if strings.HasPrefix(line, "## ") {
			inSection = strings.HasPrefix(line, changeSectionHeading)
			continue
		}
		if !inSection {
			continue
		}
		if m := subStepHeading.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	return names
}

// attributesCreation reports whether one clause of a sentence credits a
// subject of the renaming proposal with creating the file, the four
// headings, or the register. The three parts are read anywhere in the
// clause rather than in one order, because the document states the
// attribution in several forms: a sub-step that creates a section, a
// section that already exists from a sub-step, and a proposal that lands
// a file. Two clauses are read out. A clause naming the proposal that
// landed the section attributes the creation to it, and a clause with a
// negated verb states the opposite of an attribution.
func attributesCreation(sentence string, subjects *regexp.Regexp) bool {
	for _, clause := range clauseBoundaryExpr.Split(sentence, -1) {
		if strings.Contains(strings.ToLower(clause), ownerCreditPhrase) {
			continue
		}
		if negatedVerbExpr.MatchString(clause) {
			continue
		}
		if subjects.MatchString(clause) &&
			creationVerbExpr.MatchString(clause) &&
			creationObjExpr.MatchString(clause) {
			return true
		}
	}
	return false
}

// subjectMatcher returns the matcher for a subject the proposal
// attributes work to, which is one of its own sub-steps or the proposal
// itself.
func subjectMatcher(subSteps []string) (*regexp.Regexp, error) {
	subjects := make([]string, 0, len(subSteps)+2)
	for _, name := range subSteps {
		subjects = append(subjects, `\b`+regexp.QuoteMeta(name)+`\b`)
	}
	subjects = append(subjects, "[Tt]his sub-step", "[Tt]his proposal")
	matcher, err := regexp.Compile(strings.Join(subjects, "|"))
	if err != nil {
		return nil, fmt.Errorf("compile the subject matcher: %w", err)
	}
	return matcher, nil
}

// sweptRegion returns the part of the document the sweep reads: from the
// first numbered section to the end, with the convergence record cut
// out. Truncating the document at the record instead would leave every
// section below it unread, and the files-touched list down there states
// which specification files the proposal lands as new.
func sweptRegion(document string) (string, error) {
	var kept []string
	opened, inRecord, closed := false, false, false
	for _, line := range strings.Split(document, "\n") {
		switch {
		case strings.HasPrefix(line, convergenceRecordOpening):
			inRecord = true
			continue
		case inRecord && strings.HasPrefix(line, convergenceRecordClosing):
			inRecord, closed = false, true
		case !opened && strings.HasPrefix(line, sweptRegionOpening):
			opened = true
		}
		if opened && !inRecord {
			kept = append(kept, line)
		}
	}
	if !opened {
		return "", fmt.Errorf("the document carries no heading opening %q", sweptRegionOpening)
	}
	if !closed {
		return "", fmt.Errorf("the convergence record opening %q is closed by no heading %q", convergenceRecordOpening, convergenceRecordClosing)
	}
	return strings.Join(kept, "\n"), nil
}

// proseSentences returns the sentences of a markdown region, with the
// source wrapping collapsed and the table rows left out. A table row
// states a mapping rather than a clause, and its cells run together into
// one line that no sentence boundary divides.
func proseSentences(region string) []string {
	var sentences []string
	for _, paragraph := range strings.Split(region, "\n\n") {
		if strings.HasPrefix(strings.TrimSpace(paragraph), "|") {
			continue
		}
		var kept []string
		for _, line := range strings.Split(paragraph, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "|") {
				continue
			}
			kept = append(kept, line)
		}
		text := collapseWrapping(strings.Join(kept, " "))
		sentences = append(sentences, splitSentences(text)...)
	}
	return sentences
}

// sentenceOpening are the characters a sentence of a proposal opens
// with: a capital letter, a section sign, a code delimiter, or the
// emphasis marker a lead-in bold phrase opens with.
const sentenceOpening = "§`*"

// splitSentences splits a paragraph at the end of each sentence, which
// the proposals write as a period followed by a space and one of those
// openings.
func splitSentences(text string) []string {
	var sentences []string
	start := 0
	for i := 0; i+2 < len(text); i++ {
		if text[i] != '.' || text[i+1] != ' ' {
			continue
		}
		next := rune(text[i+2])
		if !unicode.IsUpper(next) && !strings.ContainsRune(sentenceOpening, next) {
			continue
		}
		sentences = append(sentences, text[start:i+1])
		start = i + 2
	}
	return append(sentences, text[start:])
}
