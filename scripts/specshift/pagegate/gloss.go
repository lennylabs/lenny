// SPDX-License-Identifier: MIT

package pagegate

import (
	"fmt"
	"regexp"
	"strings"
)

// Gloss is one borrowed term of a page: a row of the page's Terms table
// whose term cell carries no anchor. The conventions page's "Gloss rule"
// defines it this way, so the gate needs no judgment to find one.
type Gloss struct {
	// Line is the file line of the row.
	Line int
	// Term is the term cell and Text the gloss cell, as written.
	Term string
	Text string
	// Row is the whole row as written, which holds the gloss's link.
	Row string
}

var (
	headingLine   = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$`)
	tableRow      = regexp.MustCompile(`^\s*\|`)
	separatorRow  = regexp.MustCompile(`^\s*\|?(\s*:?-+:?\s*\|)+\s*(:?-+:?\s*)?$`)
	termAnchor    = regexp.MustCompile(`<a\s+id="[^"]*"`)
	standaloneNum = regexp.MustCompile(`(?:^|[^\p{L}\p{N}_./-])(\d+(?:[.,]\d+)*)`)
)

// Glosses returns the borrowed terms of a page's Terms tables: every
// table row under a heading titled "Terms", up to the next heading, other
// than a header row and a separator row, whose first cell carries no
// anchor.
func Glosses(p *Page) []Gloss {
	lines := bodyLines(p)
	var out []Gloss
	inTerms := false
	for i, l := range lines {
		if m := headingLine.FindStringSubmatch(l.Text); m != nil {
			title := strings.TrimSpace(kramdownAtt.ReplaceAllString(m[2], ""))
			inTerms = strings.EqualFold(title, "Terms")
			continue
		}
		if !inTerms || !tableRow.MatchString(l.Text) || separatorRow.MatchString(l.Text) {
			continue
		}
		if i+1 < len(lines) && separatorRow.MatchString(lines[i+1].Text) {
			continue
		}
		cells := splitRow(l.Text)
		if len(cells) == 0 || termAnchor.MatchString(cells[0]) {
			continue
		}
		g := Gloss{Line: l.Number, Term: cells[0], Row: l.Text}
		if len(cells) > 1 {
			g.Text = strings.Join(cells[1:], " ")
		}
		out = append(out, g)
	}
	return out
}

// splitRow splits a table row into its trimmed cells. A pipe inside a
// code span or escaped with a backslash does not split a cell.
func splitRow(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	var cells []string
	var sb strings.Builder
	inCode := false
	for i := 0; i < len(row); i++ {
		c := row[i]
		switch {
		case c == '`':
			inCode = !inCode
		case c == '\\' && i+1 < len(row) && row[i+1] == '|':
			sb.WriteString(`\|`)
			i++
			continue
		case c == '|' && !inCode:
			cells = append(cells, strings.TrimSpace(sb.String()))
			sb.Reset()
			continue
		}
		sb.WriteByte(c)
	}
	if rest := strings.TrimSpace(sb.String()); rest != "" {
		cells = append(cells, rest)
	}
	return cells
}

// checkGlosses holds every gloss of a page to the gloss rule: no number
// and no normative keyword in the gloss, and exactly one link in the row.
func checkGlosses(p *Page) []Problem {
	var out []Problem
	at := func(g Gloss, format string, args ...any) {
		out = append(out, Problem{Path: p.Path, Line: g.Line, Message: fmt.Sprintf("the gloss of the borrowed term %s ", strings.TrimSpace(g.Term)) + fmt.Sprintf(format, args...)})
	}
	for _, g := range Glosses(p) {
		links := len(inlineLink.FindAllString(codeSpan.ReplaceAllString(g.Row, " "), -1))
		if links != 1 {
			at(g, "holds %d links; a gloss holds exactly one, to the term's owner", links)
		}
		// A number in a code span is still a value, so the number check
		// reads the gloss with its code spans.
		withCode, _ := stripComments(g.Text, false)
		if m := standaloneNum.FindStringSubmatch(cleanProse(withCode)); m != nil {
			at(g, "holds the number %s; a gloss says what the thing is and leaves values to the owner", m[1])
		}
		prose, _ := stripComments(codeSpan.ReplaceAllString(g.Text, " "), false)
		if k := normativeKeyword(cleanProse(prose)); k != "" {
			at(g, "uses the normative keyword %q; a gloss states no rule", k)
		}
	}
	return out
}
