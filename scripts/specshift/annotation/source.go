// SPDX-License-Identifier: MIT

package annotation

import (
	"go/scanner"
	"go/token"
	"regexp"
	"strings"
)

// CommentLine is one line of comment text in a Go file, with its comment
// marker removed.
type CommentLine struct {
	// Line is the 1-based line number.
	Line int
	// Text is the comment text without its `//` or `/*` marker.
	Text string
}

// Annotation is one `// spec:` annotation, joined across the comment
// lines that continue it.
type Annotation struct {
	// Line is the line that carries the `spec:` label.
	Line int
	// EndLine is the last line the annotation continues onto.
	EndLine int
	// Payload is the text after the label, with continuation lines
	// joined by a space.
	Payload string
}

// Comments returns every comment line of a Go source file, in order. It
// uses the Go scanner rather than a line match, so text inside a string
// literal is never read as a comment. A block comment contributes one
// line per source line it spans.
func Comments(filename string, src []byte) []CommentLine {
	fset := token.NewFileSet()
	file := fset.AddFile(filename, -1, len(src))
	var s scanner.Scanner
	// A file that does not scan cleanly still yields the comments before
	// and after the fault; the compiler, rather than this reader, reports
	// the fault.
	s.Init(file, src, func(token.Position, string) {}, scanner.ScanComments)
	var out []CommentLine
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.COMMENT {
			continue
		}
		line := fset.Position(pos).Line
		if strings.HasPrefix(lit, "//") {
			out = append(out, CommentLine{Line: line, Text: strings.TrimPrefix(lit, "//")})
			continue
		}
		body := strings.TrimSuffix(strings.TrimPrefix(lit, "/*"), "*/")
		for i, part := range strings.Split(body, "\n") {
			out = append(out, CommentLine{Line: line + i, Text: strings.TrimPrefix(strings.TrimSpace(part), "*")})
		}
	}
	return out
}

// labelLine matches a comment line that opens a labelled comment of its
// own, such as `diagnosis:`, so an annotation does not continue onto it.
var labelLine = regexp.MustCompile(`^[a-z][a-z-]*:(\s|$)`)

// Annotations groups comment lines into annotations. A line continues
// the annotation above it when it sits on the next line and the text so
// far leaves a parenthesis open or ends with a comma, which is how a
// wrapped list of citations reads.
func Annotations(lines []CommentLine) []Annotation {
	var out []Annotation
	for i := 0; i < len(lines); i++ {
		payload, ok := Payload(lines[i].Text)
		if !ok {
			continue
		}
		a := Annotation{Line: lines[i].Line, EndLine: lines[i].Line, Payload: payload}
		for i+1 < len(lines) && continues(a, lines[i+1]) {
			i++
			a.Payload = strings.TrimSpace(a.Payload + " " + strings.TrimSpace(lines[i].Text))
			a.EndLine = lines[i].Line
		}
		out = append(out, a)
	}
	return out
}

// continues reports whether the next comment line continues the
// annotation.
func continues(a Annotation, next CommentLine) bool {
	if next.Line != a.EndLine+1 {
		return false
	}
	text := strings.TrimSpace(next.Text)
	if text == "" || labelLine.MatchString(text) {
		return false
	}
	return strings.Count(a.Payload, "(") > strings.Count(a.Payload, ")") ||
		strings.HasSuffix(a.Payload, ",")
}
