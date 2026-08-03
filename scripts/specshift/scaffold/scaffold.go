// SPDX-License-Identifier: MIT

// Package scaffold decides whether a text carries a proposal's own
// scaffolding label. A proposal numbers its change sections, its
// decisions, and its review passes so that its reviewers can refer to
// them while it is being written. Those labels name parts of the
// proposal document, so they resolve to nothing for a later reader of
// the code or of the git history, and the repository rule in
// .claude/skills/implement-proposal/SKILL.md keeps them out of what
// ships: shipped code, comments, test names, and commit messages cite a
// specification section or describe the behaviour.
//
// The matcher is written as regular-expression source rather than as
// specimens of the labels, because this package sits inside the domain
// the sweep over the tracked tree reads, and a specimen standing here
// would be a site of the class the sweep reports. The fixtures that do
// carry specimens live under testdata/, which is outside that domain.
package scaffold

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// classLabelExpr matches one class-prefixed scaffolding label: a
// proposal's prefix for a specification change, an amendment, a code
// change, or a resolution, followed by a hyphen and either a number or a
// letter with an optional number.
var classLabelExpr = regexp.MustCompile(`\b(?:SPEC|AMEND|CODE|RES)-(?:\d+|[A-Z]\d*)\b`)

// stepExpr matches a bare reference to a numbered step, item, or review
// pass. The word alone is ordinary prose, so the number is what makes
// the reference one a reader cannot resolve.
var stepExpr = regexp.MustCompile(`(?i)\b(?:pass|step|item)[ \t]+\d+\b`)

// bareLabelExpr matches a scaffolding label written without a class
// prefix: a single letter, an optional hyphen and letter, and a number.
//
// That form is indistinguishable from ordinary identifiers, register
// keys, and cipher names, so it is decided only on a line that also
// names a proposal document. A leak of the bare form carries that
// context in practice, because the label means nothing without it.
var bareLabelExpr = regexp.MustCompile(`\b[SCD]-?[A-Z]?\d+\b`)

// proposalRefExpr matches a line that names a proposal document, either
// by the word and its number or by the directory the proposals sit in.
var proposalRefExpr = regexp.MustCompile(`(?i)proposals?[ /]`)

// specRefExpr matches a specification section citation. A numbered step
// the specification itself defines is a durable reference, so a step
// reference standing on a line that cites a section is not a site.
var specRefExpr = regexp.MustCompile(`§\s*\d`)

// Site is one occurrence of a scaffolding label, given as the 1-based
// line it stands on and the text it covers.
type Site struct {
	// Line is the 1-based source line of the occurrence.
	Line int
	// Text is the label as it stands in the source.
	Text string
}

// Find returns every scaffolding label a text carries, in source order.
//
// The text is read line by line, because both exemptions this predicate
// applies (a specification citation beside a step reference, and a
// proposal named beside a bare label) are stated over the line the
// occurrence stands on.
func Find(content string) []Site {
	return find(content, false)
}

// FindInProposalText returns the same sites over a text whose subject is
// already known to be a proposal, such as the message of a commit that
// names the proposal it implements. The bare-label form is decided on
// every line of such a text rather than on the lines that name a
// proposal themselves, because the context that makes the label
// ambiguous is carried by the text as a whole.
func FindInProposalText(content string) []Site {
	return find(content, true)
}

func find(content string, proposalContext bool) []Site {
	var sites []Site
	for i, line := range strings.Split(content, "\n") {
		for _, m := range lineSites(line, proposalContext) {
			sites = append(sites, Site{Line: i + 1, Text: m})
		}
	}
	return sites
}

// CommitScan is the outcome of reading the messages of the commits that
// landed a body of work.
type CommitScan struct {
	// Read is the number of messages the scan read.
	Read int
	// Sites holds the labels of every message that carries one, keyed by
	// the commit the message belongs to.
	Sites map[string][]Site
}

// ScanCommits reads a set of commit messages, keyed by commit, and
// returns the scaffolding label each one carries.
//
// It fails on an empty set rather than reporting a clean scan. A scan
// that read no message asserts nothing: the revision selection it was
// taken over no longer reaches the commits that landed the work, so a
// message added afterwards carries any label past the check unreported.
func ScanCommits(messages map[string]string) (CommitScan, error) {
	if len(messages) == 0 {
		return CommitScan{}, fmt.Errorf("scan the commit messages: the selection reached no commit, so the scan asserts nothing")
	}
	scan := CommitScan{Read: len(messages), Sites: map[string][]Site{}}
	for commit, message := range messages {
		if sites := FindInProposalText(message); len(sites) > 0 {
			scan.Sites[commit] = sites
		}
	}
	return scan, nil
}

// lineSites returns the labels of one line, in column order.
func lineSites(line string, proposalContext bool) []string {
	spans := classLabelExpr.FindAllStringIndex(line, -1)
	if !specRefExpr.MatchString(line) {
		spans = append(spans, stepExpr.FindAllStringIndex(line, -1)...)
	}
	if proposalContext || proposalRefExpr.MatchString(line) {
		spans = append(spans, bareLabelExpr.FindAllStringIndex(line, -1)...)
	}
	sort.Slice(spans, func(a, b int) bool { return spans[a][0] < spans[b][0] })
	texts := make([]string, 0, len(spans))
	for _, span := range spans {
		texts = append(texts, line[span[0]:span[1]])
	}
	return texts
}
