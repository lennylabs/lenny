// SPDX-License-Identifier: MIT

package docgate

import (
	"regexp"
	"strings"
)

var (
	fenceOpen   = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	codeSpan    = regexp.MustCompile("(`+)([^`\n]+?)(`+)")
	separator   = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)
	htmlComment = regexp.MustCompile(`<!--.*?-->`)
)

// Spans returns the code spans of a page's body outside fenced code blocks
// and HTML comments. A span in a table row carries its column's header and
// the row's cells by header.
func Spans(p *Page) []Span {
	lines := strings.Split(p.Body, "\n")
	var out []Span
	var header []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if m := fenceOpen.FindStringSubmatch(line); m != nil {
			i = fenceEnd(lines, i, m[1])
			header = nil
			continue
		}
		line = htmlComment.ReplaceAllString(line, "")
		fileLine := p.BodyLine + i
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			header = nil
			out = append(out, lineSpans(line, fileLine, "", -1, nil)...)
			continue
		}
		cells := splitCells(line)
		if header == nil {
			// A row that a separator row follows is the header; a table
			// with no separator is read as prose cells.
			if i+1 < len(lines) && separator.MatchString(lines[i+1]) {
				header = cells
				i++
				continue
			}
		}
		if separator.MatchString(line) {
			continue
		}
		row := map[string]string{}
		for j, c := range cells {
			if j < len(header) {
				row[headerKey(header[j])] = c
			}
		}
		for j, c := range cells {
			h := ""
			if j < len(header) {
				h = header[j]
			}
			out = append(out, lineSpans(c, fileLine, h, j, row)...)
		}
	}
	return out
}

// headerKey normalizes a column header: lower case, with code-span
// backticks and surrounding space removed.
func headerKey(h string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(h), "`"))
}

// lineSpans returns the code spans of one line or one table cell.
func lineSpans(text string, line int, header string, column int, row map[string]string) []Span {
	var out []Span
	for _, m := range codeSpan.FindAllStringSubmatchIndex(text, -1) {
		// A span's opening and closing backtick runs are the same length;
		// a mismatch is not a code span the renderer shows.
		if m[3]-m[2] != m[7]-m[6] {
			continue
		}
		content := strings.TrimSpace(text[m[4]:m[5]])
		if content == "" {
			continue
		}
		out = append(out, Span{
			Text: content, Line: line,
			Before: text[:m[0]], After: text[m[1]:],
			Header: header, Column: column, Row: row,
		})
	}
	return out
}

// splitCells splits a table row into its cells, honoring escaped pipes and
// pipes inside code spans.
func splitCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) {
		line = line[:len(line)-1]
	}
	var cells []string
	var sb strings.Builder
	inCode := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '\\' && i+1 < len(line) && line[i+1] == '|':
			sb.WriteByte('|')
			i++
		case ch == '`':
			inCode = !inCode
			sb.WriteByte(ch)
		case ch == '|' && !inCode:
			cells = append(cells, strings.TrimSpace(sb.String()))
			sb.Reset()
		default:
			sb.WriteByte(ch)
		}
	}
	return append(cells, strings.TrimSpace(sb.String()))
}

// fenceEnd returns the index of the line that closes the fence opened at
// line i, or the last index when the fence never closes.
func fenceEnd(lines []string, i int, marker string) int {
	for j := i + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if strings.HasPrefix(t, marker) && strings.Trim(t, marker[:1]) == "" {
			return j
		}
	}
	return len(lines) - 1
}
