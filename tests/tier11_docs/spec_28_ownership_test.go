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
// the scope paragraph above it outside, and closes at the convergence
// record.
const (
	sweptRegionOpening = "## 0."
	sweptRegionClosing = "## 9. Resolved in adversarial review"
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
	body, err := os.ReadFile(filepath.Join(repoRoot(t), renamingProposalFile))
	if err != nil {
		t.Fatalf("read %s: %v", renamingProposalFile, err)
	}
	document := string(body)

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

	for _, sentence := range proseSentences(region) {
		if strings.Contains(sentence, ownerProposalNumber) {
			continue
		}
		if attributesCreation(sentence, subjects) {
			t.Errorf("%s credits one of its own sub-steps with creating the section: %q", renamingProposalFile, sentence)
		}
	}
}

// ownerProposalNumber is the number of the proposal that landed the
// section. A clause naming it credits that proposal rather than one of
// the renaming proposal's own sub-steps.
const ownerProposalNumber = "0067"

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

// attributesCreation reports whether one sentence credits a subject of
// the renaming proposal with creating the file, the four headings, or
// the register. The three parts are read anywhere in the sentence rather
// than in one order, because the document states the attribution in
// several forms: a sub-step that creates a section, a section that
// already exists from a sub-step, and a proposal that lands a file. A
// negated verb is the one form that states the opposite, and it is read
// out.
func attributesCreation(sentence string, subjects *regexp.Regexp) bool {
	if negatedVerbExpr.MatchString(sentence) {
		return false
	}
	return subjects.MatchString(sentence) &&
		creationVerbExpr.MatchString(sentence) &&
		creationObjExpr.MatchString(sentence)
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

// sweptRegion returns the part of the document the sweep reads.
func sweptRegion(document string) (string, error) {
	start := strings.Index(document, sweptRegionOpening)
	if start < 0 {
		return "", fmt.Errorf("the document carries no heading opening %q", sweptRegionOpening)
	}
	rest := document[start:]
	end := strings.Index(rest, sweptRegionClosing)
	if end < 0 {
		return "", fmt.Errorf("the document carries no heading %q", sweptRegionClosing)
	}
	return rest[:end], nil
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
