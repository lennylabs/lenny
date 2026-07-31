// SPDX-License-Identifier: MIT

package line

import (
	"fmt"
	"go/scanner"
	"go/token"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
)

// servedKind names where a served client artifact carries the text a
// client reads.
type servedKind int

const (
	// servedValues is a data artifact whose whole content is served, so
	// every citation it carries sits in text a client reads. It has no
	// comment channel, so it carries no authoring site a tie could
	// survive in and the surviving-tie rule does not range over it.
	servedValues servedKind = iota
	// servedLiterals is a Go source whose string literals become the
	// served artifact. A citation in one of those literals is served; a
	// citation in a comment of the same file is an ordinary authoring
	// site and converts like any other comment.
	servedLiterals
)

// servedArtifacts names the served client artifacts and where each one
// carries served text.
//
// The OpenAPI document is served verbatim to clients and is the source
// the MCP tool inventory is generated from. The MCP tool definitions are
// served as tool schemas. The chart values struct tags are copied
// verbatim into the generated chart JSON schema, which is what an
// operator reads. A specification anchor in any of them is a pointer
// into a document the client does not have, so the citation is removed
// rather than converted.
var servedArtifacts = map[string]servedKind{
	"pkg/gateway/externalapi/openapi/openapi.json": servedValues,
	"pkg/gateway/mcpfabric/mcptools/mcptools.go":   servedLiterals,
	"pkg/chart/values/values.go":                   servedLiterals,
}

// ServedArtifacts returns the served client artifacts in a stable order,
// so a caller can report the set the pass strips from.
func ServedArtifacts() []string {
	out := make([]string, 0, len(servedArtifacts))
	for path := range servedArtifacts {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// servedSpans is where one file carries served text. A nil value means
// the file is an ordinary carrier, so every citation in it converts.
type servedSpans struct {
	kind  servedKind
	spans [][2]int
}

// servedSites returns the served spans of a file. A Go served artifact
// the scanner cannot read fails rather than being classified, because a
// pass that could not tell a served literal from a comment would convert
// a citation into text a client reads.
func servedSites(path, text string) (*servedSpans, error) {
	kind, ok := servedArtifacts[path]
	if !ok {
		return nil, nil
	}
	if kind == servedValues {
		return &servedSpans{kind: kind, spans: [][2]int{{0, len(text)}}}, nil
	}
	spans, err := stringLiteralSpans(path, text)
	if err != nil {
		return nil, err
	}
	return &servedSpans{kind: kind, spans: spans}, nil
}

// covers reports whether the citation sits in served text.
func (s *servedSpans) covers(c citation.Citation) bool {
	if s == nil {
		return false
	}
	for _, span := range s.spans {
		if c.Offset >= span[0] && c.Offset < span[1] {
			return true
		}
	}
	return false
}

// requiresTie reports whether a strip from this artifact has to leave a
// tie standing in the file's authoring source. A Go source carries the
// tie in the doc comment above the site and is held to the standing rule
// that spec-derived code cites its section, so stripping the only
// carrier would delete the tie rather than relocate it. A data artifact
// has no comment channel and so has no authoring site to hold one.
func (s *servedSpans) requiresTie() bool { return s != nil && s.kind == servedLiterals }

// stringLiteralSpans returns the byte span of every string literal in a
// Go source.
func stringLiteralSpans(path, src string) ([][2]int, error) {
	fset := token.NewFileSet()
	file := fset.AddFile(path, fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, []byte(src), nil, 0)
	var spans [][2]int
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.STRING {
			start := file.Offset(pos)
			spans = append(spans, [2]int{start, start + len(lit)})
		}
	}
	if s.ErrorCount > 0 {
		return nil, fmt.Errorf("the served Go artifact does not scan, so a served literal cannot be told from a comment")
	}
	return spans, nil
}

// stripSite returns the edit that removes a served citation, and fails
// when the strip would leave the site with no tie to the specification.
func stripSite(sections *citation.Resolver, text string, c citation.Citation, served *servedSpans) (edit, error) {
	if served.requiresTie() {
		number, err := anchorNumber(sections, c)
		if err != nil {
			return edit{}, err
		}
		if !tieSurvives(text, c.Line, number) {
			return edit{}, fmt.Errorf("stripping the served citation would delete the site's only tie, because the doc comment above it names no §%s", number)
		}
	}
	start, end := stripSpan(text, c.Offset, c.Offset+len(c.Raw))
	return edit{start: start, end: end}, nil
}

// tieSurvives reports whether the doc comment immediately above the
// served site still names the section. The comment run is read from the
// pre-run text, and a citation the run carries converts to the anchor
// for the same section in the same pass, so a tie found here is a tie
// that stands after the run.
func tieSurvives(text string, line int, number string) bool {
	run := docCommentAbove(text, line)
	if run == "" {
		return false
	}
	return anchorOf(number).MatchString(run)
}

// docCommentAbove returns the contiguous run of line comments standing
// directly above the 1-based line, empty when the line carries none.
func docCommentAbove(text string, line int) string {
	lines := strings.Split(text, "\n")
	var run []string
	for i := line - 2; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "//") {
			break
		}
		run = append(run, trimmed)
	}
	return strings.Join(run, "\n")
}

// anchorOf returns the expression matching a reference to one section,
// bounded so §17.9.1 does not match §17.9.10.
func anchorOf(number string) *regexp.Regexp {
	return regexp.MustCompile(`§` + regexp.QuoteMeta(number) + `(?:[^0-9.]|\.[^0-9]|$)`)
}

// stripSpan widens the span a strip removes over the punctuation that
// existed only to introduce the citation, which is the `spec:` label
// standing in front of it, a bracket pair wrapping it, and a dash or
// colon separating it from the text it introduced when the citation
// opened the value. Leaving that punctuation behind is the residue that
// makes a served string read as a fragment.
func stripSpan(text string, start, end int) (int, int) {
	left := trimLeft(text, start)
	if cut, ok := cutLabel(text, left); ok {
		left = trimLeft(text, cut)
	}
	right := trimRight(text, end)
	if l, r, ok := cutBrackets(text, left, right); ok {
		return collapse(text, l, r)
	}
	if opensValue(text, left) {
		right = trimRight(text, cutSeparator(text, right))
	}
	return collapse(text, left, right)
}

// spaceBytes are the horizontal blanks the strip crosses. A newline is
// not one: a strip never joins two lines.
const spaceBytes = " \t"

// trimLeft moves the offset back over horizontal blanks.
func trimLeft(text string, at int) int {
	for at > 0 && strings.IndexByte(spaceBytes, text[at-1]) >= 0 {
		at--
	}
	return at
}

// trimRight moves the offset forward over horizontal blanks.
func trimRight(text string, at int) int {
	for at < len(text) && strings.IndexByte(spaceBytes, text[at]) >= 0 {
		at++
	}
	return at
}

// citationLabel is the label a carrier writes in front of a citation.
const citationLabel = "spec:"

// cutLabel reports the offset of a `spec:` label standing in front of
// the citation.
func cutLabel(text string, at int) (int, bool) {
	if at < len(citationLabel) {
		return at, false
	}
	if !strings.EqualFold(text[at-len(citationLabel):at], citationLabel) {
		return at, false
	}
	return at - len(citationLabel), true
}

// brackets are the delimiter pairs a carrier wraps a citation in.
var brackets = map[byte]byte{'(': ')', '[': ']'}

// cutBrackets reports the span widened over a bracket pair that wraps
// the citation and nothing else.
func cutBrackets(text string, left, right int) (int, int, bool) {
	if left == 0 || right >= len(text) {
		return left, right, false
	}
	closer, ok := brackets[text[left-1]]
	if !ok || text[right] != closer {
		return left, right, false
	}
	return left - 1, right + 1, true
}

// separators are the marks that introduce the text a citation opened,
// each written against a blank so a hyphenated word is not read as one.
var separators = []string{"—", "–", "-", ":", "/", ","}

// cutSeparator reports the offset behind a separator standing between
// the citation and the text it introduced.
func cutSeparator(text string, at int) int {
	for _, sep := range separators {
		if !strings.HasPrefix(text[at:], sep) {
			continue
		}
		behind := at + len(sep)
		if behind < len(text) && strings.IndexByte(spaceBytes, text[behind]) >= 0 {
			return behind
		}
	}
	return at
}

// closers are the marks a blank must not stand in front of once the
// citation between them is gone.
const closers = ".,;:)]}\"`\n"

// opensValue reports whether the offset stands at the opening of the
// text the citation sat in, which is the start of a string literal or of
// a comment body.
func opensValue(text string, at int) bool {
	if at == 0 {
		return true
	}
	if text[at-1] == '"' || text[at-1] == '`' {
		return true
	}
	return commentOpening.MatchString(text[lineStart(text, at):at])
}

// commentOpening matches the run from the start of a line to the body of
// a comment in the dialects the carriers use.
var commentOpening = regexp.MustCompile(`^[ \t]*(?:(?://|#|--|\*)[ \t]*)?$`)

// lineStart returns the offset of the start of the line the offset sits
// on.
func lineStart(text string, at int) int {
	if i := strings.LastIndexByte(text[:at], '\n'); i >= 0 {
		return i + 1
	}
	return 0
}

// duplicated are the marks that read as a repetition once the citation
// between them is gone. A citation written as its own sentence behind
// the sentence it annotates carries the second of the two periods.
const duplicated = ".,;"

// collapse closes the blanks the removal would double up, the mark it
// would repeat, and the blank it would leave standing in front of a
// closing mark.
func collapse(text string, left, right int) (int, int) {
	if left > 0 && right < len(text) && text[right] == text[left-1] && strings.IndexByte(duplicated, text[right]) >= 0 {
		return left, right + 1
	}
	if left > 0 && text[left-1] == ' ' && right < len(text) {
		if text[right] == ' ' {
			return left, right + 1
		}
		if strings.IndexByte(closers, text[right]) >= 0 {
			return left - 1, right
		}
	}
	if opensValue(text, left) {
		return left, trimRight(text, right)
	}
	return left, right
}
