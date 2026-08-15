// SPDX-License-Identifier: MIT

// Tier-11 documentation check holding the agent-facing naming rules to
// the naming law the communication-channels section states, and to the
// specimen that section points to.
//
// §28.1 describes the two reserved words rather than writing the banned
// noun phrases out, because the section sits inside the prohibition's
// own domain, and it states that the literal spellings are held outside
// that domain in the naming lint's matcher and in the agent-facing
// naming rules. The matcher half is a regular-expression literal in
// scripts/specshift/name. This check holds the other half: the rules
// document exists, it states every rule the section states, and it
// carries every spelling the prohibition covers, so the sentence §28.1
// writes about it stays true as the tree changes.
//
// Nothing here restates a banned spelling. Every tracked Go file is a
// carrier of the prohibition through its doc comments, so a specimen
// written in this source would be a site of the class the name pass
// removes. The spellings are derived from the two testdata fixtures the
// reserved-phrase cases in this package already read, and a testdata
// directory is outside the read domain of every gate and the write
// domain of every pass.
//
// These tests are NOT under a build tag because they exercise the
// repository state directly — no external infrastructure required.

package tier11_docs_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// channelNamingRulesFile is the agent-facing statement of the naming
// law, given as a repo-relative path.
const channelNamingRulesFile = ".claude/rules/channel-naming.md"

// namingLawHeading opens the section of the specification that states
// the naming law, and namingLawAftermath opens the section after it. The
// law runs between the two.
//
// spec: §28.1
const (
	namingLawHeading   = "### 28.1 Naming law"
	namingLawAftermath = "### 28.2"
)

// minimumRuleStatement is the shortest body a rule's paragraph may carry
// and still count as a statement of the rule. A label with nothing after
// it is a table of contents for the naming law rather than a statement
// of it, and the set comparison alone accepts one.
const minimumRuleStatement = 40

// ruleLabelExpr matches the label one rule of the naming law opens with,
// which is the bold ordinal both the specification and the rules
// document write at the head of the rule's own paragraph.
var ruleLabelExpr = regexp.MustCompile(`(?m)^\*\*(N\d+)\.\*\*`)

// namingLawSection returns the body of §28.1 out of the
// communication-channels section.
//
// spec: §28.1
func namingLawSection(section string) (string, error) {
	start := strings.Index(section, namingLawHeading)
	if start < 0 {
		return "", fmt.Errorf("the channels section carries no heading %q", namingLawHeading)
	}
	rest := section[start:]
	end := strings.Index(rest, namingLawAftermath)
	if end < 0 {
		return "", fmt.Errorf("the naming law is not followed by a section opening %q", namingLawAftermath)
	}
	return rest[:end], nil
}

// ruleStatements returns the rules a text states, as the labels in
// source order and the statement each label carries. A statement runs
// from the end of its label to the start of the next one.
//
// A text carrying no label is an error rather than an empty answer. An
// extraction that stopped matching, a document read from the wrong path,
// and a document whose rules were deleted all produce the same empty
// map, and an assertion over an empty map passes over every rule it was
// written to check.
//
// spec: §28.1
func ruleStatements(text string) ([]string, map[string]string, error) {
	spans := ruleLabelExpr.FindAllStringSubmatchIndex(text, -1)
	if len(spans) == 0 {
		return nil, nil, fmt.Errorf("the text states no rule of the naming law")
	}
	labels := make([]string, 0, len(spans))
	statements := make(map[string]string, len(spans))
	for i, span := range spans {
		label := text[span[2]:span[3]]
		end := len(text)
		if i+1 < len(spans) {
			end = spans[i+1][0]
		}
		if _, repeated := statements[label]; repeated {
			return nil, nil, fmt.Errorf("the text states %s twice", label)
		}
		labels = append(labels, label)
		statements[label] = strings.TrimSpace(text[span[1]:end])
	}
	return labels, statements, nil
}

// reservedSpellings returns every spelling the prohibition covers, one
// per reserved word per spelling, derived from the two fixtures rather
// than written here.
//
// Each fixture carries one word in one spelling. The head noun and the
// separator of the other spelling are read off the pair, so the four
// members are the fixtures' own words in the fixtures' own separators.
//
// spec: §28.1
func reservedSpellings(t *testing.T) []string {
	t.Helper()
	spaced := reservedPhraseSpecimen(t, spaceSeparatedSpecimenFile)
	compound := reservedPhraseSpecimen(t, hyphenatedSpecimenFile)

	spacedWord, spacedHead, ok := strings.Cut(spaced, " ")
	if !ok {
		t.Fatalf("the %s fixture carries no space-separated spelling", spaceSeparatedSpecimenFile)
	}
	compoundWord, compoundHead, ok := strings.Cut(compound, "-")
	if !ok {
		t.Fatalf("the %s fixture carries no hyphenated compound spelling", hyphenatedSpecimenFile)
	}
	if spacedHead != compoundHead {
		t.Fatalf("the two fixtures carry different head nouns, so the pair does not name one prohibition")
	}
	if spacedWord == compoundWord {
		t.Fatalf("the two fixtures carry the same reserved word, so one of the two words has no specimen")
	}

	var spellings []string
	for _, word := range []string{spacedWord, compoundWord} {
		spellings = append(spellings, word+" "+spacedHead, word+"-"+spacedHead)
	}
	sort.Strings(spellings)
	return spellings
}

// missingSpellings returns the spellings a body does not carry, in
// sorted order.
func missingSpellings(body string, spellings []string) []string {
	var missing []string
	for _, spelling := range spellings {
		if !strings.Contains(body, spelling) {
			missing = append(missing, spelling)
		}
	}
	return missing
}

// readChannelNamingRules reads the agent-facing naming rules from a
// repository root, returning the error the read fails with so a case can
// assert on an absent document.
func readChannelNamingRules(root string) (string, error) {
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(channelNamingRulesFile)))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", channelNamingRulesFile, err)
	}
	return string(content), nil
}

// diagnosis: a failure means the agent-facing naming rules no longer
// hold what §28.1 says they hold. A failure of the existence case means
// the document §28.1 points to is absent, so the section's statement
// that the literal spellings are held outside the prohibition's domain
// is false and an agent has no statement of the naming law outside the
// specification. A failure of the stated-rules case means the document
// and §28.1 state different rules, so an agent following the document
// writes a name the naming law rejects. A failure of the specimen case
// means the document no longer carries a spelling the prohibition
// covers, so the specimen the matcher and a reviewing author are checked
// against is incomplete and the corresponding spelling can regress
// unnoticed. The route to green is an edit to the document, because
// §28.1 is the normative text and the document follows it.
//
// spec: §28.1
func TestChannelNamingRulesCarryTheNamingLawAndItsSpecimen_spec_28_1(t *testing.T) {
	t.Run("landed document states every rule", func(t *testing.T) {
		assertRulesDocumentStatesEveryRule(t)
	})
	t.Run("landed document carries every spelling", func(t *testing.T) {
		assertRulesDocumentCarriesEverySpelling(t)
	})
	t.Run("an absent document is reported", func(t *testing.T) {
		assertAbsentRulesDocumentIsReported(t)
	})
	t.Run("a document missing a rule is reported", func(t *testing.T) {
		assertDocumentMissingARuleIsReported(t)
	})
	t.Run("a label with no statement is reported", func(t *testing.T) {
		assertLabelWithNoStatementIsReported(t)
	})
	t.Run("a document missing a spelling is reported", func(t *testing.T) {
		assertDocumentMissingASpellingIsReported(t)
	})
	t.Run("a text stating no rule is an error", func(t *testing.T) {
		assertTextStatingNoRuleIsAnError(t)
	})
}

// statedRules returns the rules §28.1 states, read out of the landed
// section on every run so the assertion below is against the
// specification rather than against a list restated here.
//
// spec: §28.1
func statedRules(t *testing.T) ([]string, map[string]string) {
	t.Helper()
	law, err := namingLawSection(readChannelsSection(t))
	if err != nil {
		t.Fatalf("read the naming law: %v", err)
	}
	labels, statements, err := ruleStatements(law)
	if err != nil {
		t.Fatalf("read the rules the naming law states: %v", err)
	}
	if len(labels) < 2 {
		t.Fatalf("the naming law states %d rule(s) (%v), so the extraction read almost nothing", len(labels), labels)
	}
	return labels, statements
}

// assertRulesDocumentStatesEveryRule holds the document to the rules
// §28.1 states, in both directions. A document short of a rule leaves an
// agent without it, and a document carrying a rule the section does not
// state is a second normative text.
//
// spec: §28.1
func assertRulesDocumentStatesEveryRule(t *testing.T) {
	t.Helper()
	stated, _ := statedRules(t)

	body, err := readChannelNamingRules(repoRoot(t))
	if err != nil {
		t.Fatalf("%v", err)
	}
	carried, statements, err := ruleStatements(body)
	if err != nil {
		t.Fatalf("read the rules %s states: %v", channelNamingRulesFile, err)
	}

	want := append([]string(nil), stated...)
	got := append([]string(nil), carried...)
	sort.Strings(want)
	sort.Strings(got)
	if !equalStrings(got, want) {
		t.Fatalf("%s states rules %v; §28.1 states %v", channelNamingRulesFile, got, want)
	}
	for _, label := range carried {
		if len(statements[label]) < minimumRuleStatement {
			t.Errorf("%s carries %s as a label with %d character(s) of statement, so the rule is named rather than stated",
				channelNamingRulesFile, label, len(statements[label]))
		}
	}
}

// assertRulesDocumentCarriesEverySpelling holds the document to the
// specimen §28.1 points to. The section describes the two reserved words
// rather than writing them, so this document and the naming lint's
// matcher are the only places the literal spellings stand, and an author
// checking a candidate name reads them here.
//
// spec: §28.1
func assertRulesDocumentCarriesEverySpelling(t *testing.T) {
	t.Helper()
	body, err := readChannelNamingRules(repoRoot(t))
	if err != nil {
		t.Fatalf("%v", err)
	}
	spellings := reservedSpellings(t)
	if len(spellings) != 4 {
		t.Fatalf("the fixtures yielded %d spelling(s) (%v), and the prohibition covers two words in two spellings", len(spellings), spellings)
	}
	for _, missing := range missingSpellings(body, spellings) {
		t.Errorf("%s does not carry %q, one of the spellings the prohibition covers", channelNamingRulesFile, missing)
	}
}

// assertAbsentRulesDocumentIsReported pins the state this check exists
// to catch. The document was absent while §28.1 asserted it held the
// literal spellings, so a reader of the section was pointed at nothing.
//
// spec: §28.1
func assertAbsentRulesDocumentIsReported(t *testing.T) {
	t.Helper()
	if _, err := readChannelNamingRules(t.TempDir()); err == nil {
		t.Fatalf("reading %s from a tree that does not carry it returned no error", channelNamingRulesFile)
	}
}

// assertDocumentMissingARuleIsReported pins that dropping one rule from
// the document is reported. Without this case the comparison could match
// on a subset and a document short of a rule would read as conforming.
//
// spec: §28.1
func assertDocumentMissingARuleIsReported(t *testing.T) {
	t.Helper()
	stated, statements := statedRules(t)

	body := rulesDocumentFrom(stated[1:], statements)
	carried, _, err := ruleStatements(body)
	if err != nil {
		t.Fatalf("read the rules a document short of one states: %v", err)
	}
	if len(carried) != len(stated)-1 {
		t.Fatalf("a document short of one rule states %d rule(s) and the law states %d", len(carried), len(stated))
	}
	if equalStrings(carried, stated) {
		t.Fatalf("a document short of %s compares equal to the rules the law states", stated[0])
	}
}

// assertLabelWithNoStatementIsReported pins the degenerate document: one
// that carries every label and states none of them. The set comparison
// alone accepts it, so the length floor is what separates a document
// that states the law from a table of contents for it.
//
// spec: §28.1
func assertLabelWithNoStatementIsReported(t *testing.T) {
	t.Helper()
	stated, _ := statedRules(t)

	var b strings.Builder
	for _, label := range stated {
		fmt.Fprintf(&b, "**%s.**\n\n", label)
	}
	_, statements, err := ruleStatements(b.String())
	if err != nil {
		t.Fatalf("read the rules a document of bare labels states: %v", err)
	}
	for _, label := range stated {
		if len(statements[label]) >= minimumRuleStatement {
			t.Fatalf("a bare label for %s measured %d character(s) of statement", label, len(statements[label]))
		}
	}
}

// assertDocumentMissingASpellingIsReported pins that dropping one
// spelling from the document is reported. A specimen check that read
// nothing, or that matched on any one member, would report the document
// complete while the spelling it lost regressed unnoticed.
//
// spec: §28.1
func assertDocumentMissingASpellingIsReported(t *testing.T) {
	t.Helper()
	body, err := readChannelNamingRules(repoRoot(t))
	if err != nil {
		t.Fatalf("%v", err)
	}
	spellings := reservedSpellings(t)
	for _, dropped := range spellings {
		stripped := strings.ReplaceAll(body, dropped, "")
		missing := missingSpellings(stripped, spellings)
		if len(missing) == 0 {
			t.Errorf("a document with %q removed reported no missing spelling", dropped)
			continue
		}
		if !slices.Contains(missing, dropped) {
			t.Errorf("a document with %q removed reported %v as missing", dropped, missing)
		}
	}
}

// assertTextStatingNoRuleIsAnError pins the inert read. An extraction
// that stopped matching returns no rule, and an assertion over no rule
// passes over the whole law, which is the failure this check is written
// to be incapable of.
//
// spec: §28.1
func assertTextStatingNoRuleIsAnError(t *testing.T) {
	t.Helper()
	for _, c := range []struct {
		name string
		text string
	}{
		{"an empty text", ""},
		{"prose with no rule label", "The gateway mediates every session.\n"},
		{"a label that does not open a line", "see **N1.** above\n"},
	} {
		if _, _, err := ruleStatements(c.text); err == nil {
			t.Errorf("%s was read as a statement of the naming law", c.name)
		}
	}
}

// rulesDocumentFrom renders a document stating the given rules, so a
// case can drive the comparison over a document short of one.
func rulesDocumentFrom(labels []string, statements map[string]string) string {
	var b strings.Builder
	b.WriteString("# Channel naming\n\n")
	for _, label := range labels {
		fmt.Fprintf(&b, "**%s.** %s\n\n", label, statements[label])
	}
	return b.String()
}
