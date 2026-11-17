// SPDX-License-Identifier: MIT

package pagegate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
	"github.com/lennylabs/lenny/scripts/specshift/ref"
)

// Page types and statuses the gates read.
const (
	typeWalkthrough   = "walkthrough"
	typeOverview      = "overview"
	typeIndex         = "index"
	typeCatalog       = "catalog"
	statusPlaceholder = "placeholder"
	statusPartial     = "partial"
	// typeNumbered marks a numbered file whose sections are successor
	// stubs. It carries no front matter, so it has no type of its own.
	typeNumbered = "numbered"
)

// Page is one page the gates read.
type Page struct {
	// Path is the repo-relative path.
	Path string
	// Type and Status are the front matter's fields; a stubbed numbered
	// file has the type "numbered" and no status.
	Type   string
	Status string
	// Summary is the front matter's summary, and SummaryLine the file
	// line of its key.
	Summary     string
	SummaryLine int
	// Body is the text after the front matter, and BodyLine the file
	// line of its first line.
	Body     string
	BodyLine int
}

// fromDefs converts a page of the defs tree.
func fromDefs(p *defs.Page) *Page {
	return &Page{
		Path:        p.Path,
		Type:        p.Front.Type,
		Status:      p.Front.Status,
		Summary:     p.Front.Summary,
		SummaryLine: keyLine(p.FrontText, "summary"),
		Body:        p.Body,
		BodyLine:    p.BodyLine(),
	}
}

// keyLine returns the 1-based line of a front matter key, or 1.
func keyLine(front, key string) int {
	for i, l := range strings.Split(front, "\n") {
		if strings.HasPrefix(l, key+":") {
			return i + 1
		}
	}
	return 1
}

// numberedExpr matches a numbered file of the specification.
var numberedExpr = regexp.MustCompile(`^spec/\d{2}_[^/]*\.md$`)

// numberedHeadingExpr matches the heading of a numbered section, such as
// a level-two heading whose title starts with "4.6 ".
var numberedHeadingExpr = regexp.MustCompile(`(?m)^#{1,6} \d+(?:\.\d+)*\.? `)

// StubbedNumberedFile reports whether every numbered section of a numbered
// file has become a full successor stub, which brings the file under the
// gates. A file with any section still holding its own text stays out of
// scope, because that text predates the conventions the gates hold.
func StubbedNumberedFile(path, content string) bool {
	if !numberedExpr.MatchString(path) {
		return false
	}
	headings := len(numberedHeadingExpr.FindAllString(content, -1))
	return headings > 0 && len(ref.FullStubs(content)) >= headings
}

// Pages returns the pages of a loaded tree, and the numbered files under
// root whose sections are successor stubs, sorted by path.
func Pages(root string, t *defs.Tree) ([]*Page, error) {
	out := make([]*Page, 0, len(t.Pages))
	for _, p := range t.Pages {
		out = append(out, fromDefs(p))
	}
	matches, err := filepath.Glob(filepath.Join(root, defs.SpecDir, "[0-9][0-9]_*.md"))
	if err != nil {
		return nil, fmt.Errorf("list the numbered files: %w", err)
	}
	for _, m := range matches {
		rel, err := filepath.Rel(root, m)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		if !numberedExpr.MatchString(rel) {
			continue
		}
		content, err := os.ReadFile(m)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		if StubbedNumberedFile(rel, string(content)) {
			out = append(out, &Page{Path: rel, Type: typeNumbered, Body: string(content), BodyLine: 1})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Line is one line of a page with its file line.
type Line struct {
	Number int
	Text   string
}

var (
	fenceOpen   = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	codeSpan    = regexp.MustCompile("`+[^`\n]*`+")
	inlineLink  = regexp.MustCompile(`!?\[((?:[^\[\]]|\[[^\[\]]*\])*)\]\([^()\s]*(?:\s+"[^"]*")?\)`)
	htmlAnchor  = regexp.MustCompile(`<a\s+id="[^"]*"\s*(?:/>|>\s*</a>)`)
	kramdownAtt = regexp.MustCompile(`\{:?\s*#[^}]*\}`)
	// numberedLink matches a link into a numbered file, whose text quotes
	// the heading of a numbered section rather than the page's own prose.
	numberedLink = regexp.MustCompile(`!?\[(?:[^\[\]]|\[[^\[\]]*\])*\]\((?:[^()\s]*/)?\d{2}_[^()\s/]*\.md(?:#[^()\s]*)?\)`)
)

// bodyLines returns the lines of a page's body outside code fences, each
// with its file line. A fence is example text, so no gate reads it.
func bodyLines(p *Page) []Line {
	lines := strings.Split(p.Body, "\n")
	var out []Line
	for i := 0; i < len(lines); i++ {
		if m := fenceOpen.FindStringSubmatch(lines[i]); m != nil {
			i = fenceEnd(lines, i, m[1])
			continue
		}
		out = append(out, Line{Number: p.BodyLine + i, Text: lines[i]})
	}
	return out
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

// proseLines returns the prose of a page: its summary and the lines of
// its body outside code fences, with HTML comments, code spans, explicit
// anchors, kramdown attributes, and link targets removed. A link keeps
// its text, except a link into a numbered file, whose text quotes a
// numbered heading and is removed. A comment that spans lines is removed whole.
func proseLines(p *Page) []Line {
	var out []Line
	if p.Summary != "" {
		out = append(out, Line{Number: p.SummaryLine, Text: p.Summary})
	}
	inComment := false
	for _, l := range bodyLines(p) {
		// Code spans go first, so a comment marker quoted in one is not
		// read as a comment.
		text, still := stripComments(codeSpan.ReplaceAllString(l.Text, " "), inComment)
		inComment = still
		out = append(out, Line{Number: l.Number, Text: cleanProse(text)})
	}
	return out
}

// cleanProse removes the parts of a line other than code spans and
// comments that are not prose.
func cleanProse(text string) string {
	text = numberedLink.ReplaceAllString(text, " ")
	text = htmlAnchor.ReplaceAllString(text, "")
	text = kramdownAtt.ReplaceAllString(text, "")
	return inlineLink.ReplaceAllString(text, "$1")
}

// stripComments removes the HTML comments of one line. inComment says
// whether the line opens inside a comment that an earlier line began,
// and the second result whether the line ends inside one.
func stripComments(text string, inComment bool) (string, bool) {
	var sb strings.Builder
	for {
		if inComment {
			end := strings.Index(text, "-->")
			if end < 0 {
				return sb.String(), true
			}
			text = text[end+3:]
			inComment = false
			continue
		}
		start := strings.Index(text, "<!--")
		if start < 0 {
			sb.WriteString(text)
			return sb.String(), false
		}
		sb.WriteString(text[:start])
		text = text[start+4:]
		inComment = true
	}
}
