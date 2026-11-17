// SPDX-License-Identifier: MIT

package pagegate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
)

// Problem is one finding, located by file and line.
type Problem = defs.Problem

// Check runs the text gates over the pages and returns their problems in
// path and line order.
func Check(pages []*Page) []Problem {
	var out []Problem
	for _, p := range pages {
		if p.Type != typeNumbered {
			out = append(out, checkGlosses(p)...)
		}
		if p.Type == typeWalkthrough {
			out = append(out, checkWalkthrough(p)...)
		}
		out = append(out, checkStyle(p)...)
		out = append(out, checkReviewComments(p)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// upperKeyword matches a BCP 14 keyword in upper case. MUST NOT, SHALL
// NOT, SHOULD NOT, and NOT RECOMMENDED each contain one of these.
var upperKeyword = regexp.MustCompile(`\b(MUST|REQUIRED|SHALL|SHOULD|RECOMMENDED|MAY|OPTIONAL)\b`)

// lowerKeyword matches the lower-case words the conventions count as
// normative keywords on a page or in a gloss that carries none, with a
// capital at the start of a sentence.
var lowerKeyword = regexp.MustCompile(`\b([Mm]ust|[Ss]hall|[Ss]hould)\b`)

// normativeKeyword returns the first normative keyword of a text, or "".
func normativeKeyword(text string) string {
	if m := upperKeyword.FindString(text); m != "" {
		return m
	}
	return lowerKeyword.FindString(text)
}

// checkWalkthrough reports every normative keyword of a walkthrough page.
func checkWalkthrough(p *Page) []Problem {
	var out []Problem
	for _, l := range proseLines(p) {
		if k := normativeKeyword(l.Text); k != "" {
			out = append(out, Problem{Path: p.Path, Line: l.Number, Message: fmt.Sprintf(
				"a walkthrough holds no normative keyword, and this line uses %q; state the rule on its owner page and link it", k)})
		}
	}
	return out
}

// reviewComment matches the opening of a review comment.
var reviewComment = regexp.MustCompile(`<!--\s*(src|judgment|unresolved)\s*:`)

// checkReviewComments reports a review comment on a page whose status is
// neither placeholder nor partial. A comment quoted in a code span is
// text about the comment rather than the comment.
func checkReviewComments(p *Page) []Problem {
	if p.Status == statusPlaceholder || p.Status == statusPartial {
		return nil
	}
	var out []Problem
	for _, l := range bodyLines(p) {
		for _, m := range reviewComment.FindAllStringSubmatch(codeSpan.ReplaceAllString(l.Text, " "), -1) {
			out = append(out, Problem{Path: p.Path, Line: l.Number, Message: fmt.Sprintf(
				"a %q review comment stands on a page whose status is neither placeholder nor partial; move it into the migration record or an issue", m[1])})
		}
	}
	return out
}

// SizeWarning is one page over its word budget.
type SizeWarning struct {
	Path  string
	Words int
	Limit int
}

func (w SizeWarning) String() string {
	return fmt.Sprintf("%s: %d words, over the budget of about %d", w.Path, w.Words, w.Limit)
}

// Word budgets of the conventions page's "Page size".
const (
	contentWordBudget = 6000
	indexWordBudget   = 1500
)

// Sizes returns the pages over their word budget, in path order. A page's
// words are those of its body, code fences included and comments left
// out. A catalog has no budget.
func Sizes(pages []*Page) []SizeWarning {
	var out []SizeWarning
	for _, p := range pages {
		if p.Type == typeCatalog {
			continue
		}
		limit := contentWordBudget
		if p.Type == typeIndex {
			limit = indexWordBudget
		}
		if n := wordCount(p.Body); n > limit {
			out = append(out, SizeWarning{Path: p.Path, Words: n, Limit: limit})
		}
	}
	return out
}

// wordCount counts the words of a text outside its HTML comments.
func wordCount(body string) int {
	text, _ := stripComments(body, false)
	return len(strings.Fields(text))
}
