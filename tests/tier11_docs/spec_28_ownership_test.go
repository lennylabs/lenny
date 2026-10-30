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
// the section file, the four headings the section opens with, and the
// register they carry. A negated verb states the opposite of an
// attribution, so a clause carrying one is not a site.
//
// The section file is an object under the two paraphrases the proposals
// write it as. Under its path it is read by the file rule below, which
// requires the path and the creation verb to stand together: a target
// list enumerates the path beside the files a sub-step touches, and
// reading a verb anywhere in that enumeration as an attribution would
// report a list the transfer left true. The new-specification-file
// object is a claim over more than one file: the renaming proposal still
// lands one specification file of its own, the scenarios section, so a
// singular claim is that file and reading it as an attribution would
// report a sentence the transfer left true.
var (
	creationVerbExpr = regexp.MustCompile(`\b(?:creates?|created|authors?|authored|writes|wrote|lands|landed|exists|existed)\b`)
	creationObjExpr  = regexp.MustCompile(`the communication-channels file|those five headings|(?:both|two) new specification files?|new specification files\b|§28\.1 through §28\.4|the §28\.3 register`)
	negatedVerbExpr  = regexp.MustCompile(`\b(?:creates?|writes|authors?|lands|names)\b (?:none|no|neither)\b`)
)

// A files-touched bullet names a file and states whether it is new,
// without naming a subject: the document is the subject of its own
// files-touched list, so a bullet calling the section file new is an
// attribution on its own terms and is read as one. The same holds for a
// bullet or an argument that states the file is created without naming
// who creates it, which the verb rule below reads.
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

	// The credit standing in the same clause as the sub-step is the form a
	// partial revert takes, because every sentence the transfer rewrote
	// names the landing proposal already. A credit read as an exemption
	// over its clause reports neither of these.
	for _, reverted := range []string{
		subSteps[0] + " creates §28.1 through §28.4 as proposal 0067 states.",
		"This sub-step creates the §28.3 register under proposal 0067.",
	} {
		sites := ownershipSites(t, plantBelowFilesTouched(t, document, reverted))
		if len(sites) != 1 {
			t.Errorf("the sentence %q reported %d site(s), want one: %q", reverted, len(sites), sites)
			continue
		}
		if !strings.Contains(sites[0], "creates") {
			t.Errorf("the reported site is %q, which is not the planted sentence", sites[0])
		}
	}

	credited := "Proposal 0067 created §28.1 through §28.4, and " + subSteps[0] + " renames the channels they name."
	if sites := ownershipSites(t, plantBelowFilesTouched(t, document, credited)); len(sites) != 0 {
		t.Errorf("a sentence crediting the proposal that landed the section reported %q", sites)
	}

	// A sub-step standing before a verb whose nearer subject is the credit
	// is no attribution, so the sweep reads the subject rather than the
	// presence of a sub-step name.
	appends := subSteps[0] + " appends its subsections to the §28.1 through §28.4 headings proposal 0067 creates."
	if sites := ownershipSites(t, plantBelowFilesTouched(t, document, appends)); len(sites) != 0 {
		t.Errorf("a sentence whose creation verb takes the landing proposal as its subject reported %q", sites)
	}
}

// TestSection28OwnershipSweepReadsTheTransferredSentences_spec_28 plants
// each sentence the ownership transfer removed from the renaming
// proposal, in the form that document carried it, and requires every one
// reported.
//
// The transfer moved three things: the section file, its §28.1 through
// §28.4 headings, and its §28.3 register. A sweep that reads the file
// only as a claim over more than one new specification file, or only
// alongside a separate newness claim, reports the two sentences naming
// the headings or the register and passes over the ones naming the file
// itself. Reverting one of those sentences then restores the
// double-ownership state with the sweep green, which is the failure this
// case pins: each removed phrasing is a site on its own.
//
// diagnosis: a failure means the ownership sweep reads a subset of the
// sentences the transfer rewrote, so an attribution restored in one of
// the other forms goes unreported. Read the section file as a moved
// object in its own right, under its path and under the paraphrases the
// document writes it as.
//
// spec: §28, §28.3
func TestSection28OwnershipSweepReadsTheTransferredSentences_spec_28(t *testing.T) {
	document := readRenamingProposal(t)
	subSteps := subStepNames(document)
	if len(subSteps) == 0 {
		t.Fatalf("%s publishes no sub-step under %q, so the case plants no subject", renamingProposalFile, changeSectionHeading)
	}

	// The first three are the index-row argument, the build-order bullet,
	// and the files-touched bullet as the document carried them before
	// ownership of the section moved. The last two are the same
	// attribution written with a sub-step as the subject, under the path
	// and under the paraphrase the document uses for it.
	for _, reverted := range []string{
		"The rows land in the same change that creates `spec/28_communication-channels.md` with those five headings, so every row this sub-step writes resolves at its exit and no row in this proposal precedes its target file.",
		"**Naming law, registers, and prose.** `spec/28_communication-channels.md` is created here carrying §28.1 through §28.4, which are the law and the three registers, together with the reserved-word removal and the `spec/03` correction.",
		"- `spec/28_communication-channels.md` and `spec/29_communication-scenarios.md`, both new.",
		subSteps[0] + " creates `spec/28_communication-channels.md` carrying the naming law and the three registers.",
		subSteps[0] + " creates the communication-channels file with those five headings.",
	} {
		sites := ownershipSites(t, plantBelowFilesTouched(t, document, reverted))
		if len(sites) == 0 {
			t.Errorf("the sentence %q reported no site, so reverting it leaves the sweep green", reverted)
		}
	}

	// The corrected forms of the first two sentences credit the landing
	// proposal in the clause naming the file, so widening the file to a
	// moved object must leave them unreported.
	for _, corrected := range []string{
		"Proposal 0067 created `spec/28_communication-channels.md` with those five headings and wrote those rows before " + subSteps[0] + " runs, so every row resolves.",
		"`spec/29_communication-scenarios.md`, new. `spec/28_communication-channels.md` exists, created by proposal 0067, and " + subSteps[0] + " appends to it.",
	} {
		if sites := ownershipSites(t, plantBelowFilesTouched(t, document, corrected)); len(sites) != 0 {
			t.Errorf("the corrected sentence %q reported %q", corrected, sites)
		}
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
		if creditsTheRenamingProposal(sentence, subjects) {
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
		if creditsTheRenamingProposal(sentence, subjects) {
			sites = append(sites, sentence)
		}
	}
	return sites
}

// ownerCreditPhrase is how a document credits the proposal that landed
// the section, read case-insensitively because it is written both
// mid-sentence and at the head of one. It is a competing subject rather
// than a licence over the sentence or over the clause carrying it: it
// stands for the landing proposal where it is the subject of the
// creation verb, and a sub-step of the renaming proposal standing nearer
// before that verb is the subject instead, whichever clause the credit
// falls in.
//
// Every sentence the ownership transfer rewrote names the landing
// proposal, so a credit read as a licence over its whole clause excuses
// exactly the sentences the transfer produced: a partial revert
// restoring a sub-step as the creating subject keeps the credit standing
// beside it and goes unreported.
const ownerCreditPhrase = "proposal 0067"

// ownerCreditExpr matches that credit wherever it stands in a clause.
var ownerCreditExpr = regexp.MustCompile(`(?i)` + regexp.QuoteMeta(ownerCreditPhrase))

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

// claimsTheSectionIsCreatedHere reports whether one clause of a sentence
// states that the section file is created, without crediting the
// proposal that landed it.
//
// The document writes its build-order bullets, its index-row argument,
// and its files-touched list with no named subject, so a clause naming
// the section file beside a creation verb attributes the creation to the
// document carrying it. Requiring a separate newness claim on top of the
// path, as the files-touched rule does, leaves every such clause
// unreported: a build-order bullet stating that the file is created here
// and an index-row argument resting on the change that creates it both
// name the file and claim no newness. The credit standing anywhere in
// the clause is what separates the corrected text, which states the file
// was landed elsewhere, from the attribution.
func claimsTheSectionIsCreatedHere(sentence string) bool {
	for _, clause := range clauseBoundaryExpr.Split(sentence, -1) {
		if negatedVerbExpr.MatchString(clause) || ownerCreditExpr.MatchString(clause) {
			continue
		}
		for _, listed := range strings.Split(clause, ",") {
			if sectionFileExpr.MatchString(listed) && creationVerbExpr.MatchString(listed) {
				return true
			}
		}
	}
	return false
}

// creditsTheRenamingProposal reports whether one sentence credits the
// renaming proposal, or one of its sub-steps, with creating the section
// file, its opening headings, or its register, or states the section
// file is one that proposal lands as new.
func creditsTheRenamingProposal(sentence string, subjects *regexp.Regexp) bool {
	return attributesCreation(sentence, subjects) ||
		claimsTheSectionIsNew(sentence) ||
		claimsTheSectionIsCreatedHere(sentence)
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
// headings, or the register. The moved object is read anywhere in the
// clause rather than in one position, because the document names it in
// several: a sub-step that creates a section, a section that already
// exists from a sub-step, and a proposal that lands a file. A clause
// with a negated verb is read out, because a clause stating that a
// sub-step writes none of that content states the opposite of an
// attribution.
func attributesCreation(sentence string, subjects *regexp.Regexp) bool {
	for _, clause := range clauseBoundaryExpr.Split(sentence, -1) {
		if negatedVerbExpr.MatchString(clause) || !creationObjExpr.MatchString(clause) {
			continue
		}
		if subjectOfCreation(clause, subjects) {
			return true
		}
	}
	return false
}

// subjectOfCreation reports whether a subject of the renaming proposal
// is the subject of a creation verb of the clause.
//
// The subject of a verb is the nearer of the two candidates standing
// before it: a sub-step of the renaming proposal, or the credit to the
// proposal that landed the section. Reading the credit as an exemption
// over the whole clause instead would excuse a clause whose creating
// subject is a sub-step merely because the credit stands somewhere after
// it, which is the double-ownership state the transfer ended.
func subjectOfCreation(clause string, subjects *regexp.Regexp) bool {
	subjectsAt := matchOffsets(subjects, clause)
	creditsAt := matchOffsets(ownerCreditExpr, clause)
	for _, verb := range matchOffsets(creationVerbExpr, clause) {
		subject := lastOffsetBefore(subjectsAt, verb)
		if subject < 0 {
			continue
		}
		if lastOffsetBefore(creditsAt, verb) > subject {
			continue
		}
		return true
	}
	return false
}

// matchOffsets returns the start offset of every match of an expression
// in a clause, in source order.
func matchOffsets(expr *regexp.Regexp, clause string) []int {
	var offsets []int
	for _, m := range expr.FindAllStringIndex(clause, -1) {
		offsets = append(offsets, m[0])
	}
	return offsets
}

// lastOffsetBefore returns the greatest offset standing before another,
// or -1 when none does.
func lastOffsetBefore(offsets []int, at int) int {
	last := -1
	for _, offset := range offsets {
		if offset < at {
			last = offset
		}
	}
	return last
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
