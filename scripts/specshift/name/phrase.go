// SPDX-License-Identifier: MIT

package name

import (
	"fmt"
	"go/scanner"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
)

// reservedExpr matches one bare reserved noun phrase, in the
// space-separated spelling and in the hyphenated compound spelling
// alike, with the plural admitted in both. The two reserved words and
// the head noun are held here as a regular-expression literal rather
// than in prose anywhere in this package, because the naming law's
// domain covers the doc comments of every tracked Go file and a specimen
// written in one would be a site of the class this pass removes.
//
// The separator class admits the byte the continuation join leaves
// behind, so a phrase wrapped across two consecutive comment lines
// matches as one site.
var reservedExpr = regexp.MustCompile(
	`(?i)(?:lifecycle|control)(?:[ \t]+|` + joinClass + `|-)channels?`,
)

// joinClass is the byte the continuation join leaves behind, written as
// a regular-expression escape.
var joinClass = fmt.Sprintf(`\x{%04x}`, citation.JoinByte)

// anchorExprs match the markdown anchor identifiers the naming law
// places outside the matcher, which are the kramdown attribute that
// declares an anchor and the fragment of an intra-repo markdown link. An
// anchor identifier is an addressable link target rather than prose, so
// a site inside one needs no register entry and is left as it stands:
// rewriting one breaks every inbound link, including the untracked links
// this repository cannot see.
var anchorExprs = []*regexp.Regexp{
	regexp.MustCompile(`\{:[^}\n]*#[A-Za-z0-9._-]+[^}\n]*\}`),
	regexp.MustCompile(`\]\([^)\n]*#[A-Za-z0-9._-]+\)`),
}

// site is one occurrence of a reserved noun phrase, given as a byte span
// of the source together with the line it opens on and the text it
// covers with the continuation join rendered as a space.
type site struct {
	start int
	end   int
	line  int
	text  string
}

// span is a byte range of a text, half-open.
type span struct {
	lo int
	hi int
}

// covers reports whether the span contains the whole of another.
func (s span) covers(other span) bool { return other.lo >= s.lo && other.hi <= s.hi }

// findSites returns every reserved-phrase site one file carries, in
// source order, which is the order the register's occurrence numbers are
// assigned in.
//
// The continuation join is applied before the matcher, so a phrase
// wrapped across two comment lines is one site rather than two half
// sites neither the pass nor the naming lint reads. A match inside a
// markdown anchor identifier is not a site and takes no occurrence
// number, because it needs no register entry.
func findSites(target, content string) ([]site, error) {
	joined, offsets := citation.Join(content)
	excluded := anchorSpans(joined)
	admits, err := carrierFilter(target, content)
	if err != nil {
		return nil, err
	}
	var out []site
	for _, m := range reservedExpr.FindAllStringIndex(joined, -1) {
		match := span{lo: m[0], hi: m[1]}
		if !bounded(joined, match) || covered(excluded, match) {
			continue
		}
		source := span{lo: offsets[match.lo], hi: offsets[match.hi]}
		// A wrapped site is admitted on the offset it opens at. Its span
		// runs past the comment marker of the following line, which is a
		// second comment token, so a span test would read a site the join
		// exists to read as no site at all.
		if !admits(source.lo) {
			continue
		}
		out = append(out, site{
			start: source.lo,
			end:   source.hi,
			line:  citation.LineOf(content, source.lo),
			text:  render(joined[match.lo:match.hi]),
		})
	}
	return out, nil
}

// render writes the join byte back as a space, so a wrapped site reads
// as one line of text in a failure message.
func render(s string) string { return strings.ReplaceAll(s, string(citation.JoinByte), " ") }

// bounded reports whether the match stands on word boundaries. A phrase
// taken out of the middle of a longer word is not a bare noun phrase.
func bounded(joined string, match span) bool {
	if match.lo > 0 && isWordByte(joined[match.lo-1]) {
		return false
	}
	return match.hi >= len(joined) || !isWordByte(joined[match.hi])
}

// isWordByte reports whether a byte is a word byte, which is what a
// boundary is defined against.
func isWordByte(b byte) bool {
	switch {
	case b >= '0' && b <= '9', b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b == '_':
		return true
	}
	return false
}

// anchorSpans returns the markdown anchor identifiers of a text.
func anchorSpans(joined string) []span {
	var out []span
	for _, expr := range anchorExprs {
		for _, m := range expr.FindAllStringIndex(joined, -1) {
			out = append(out, span{lo: m[0], hi: m[1]})
		}
	}
	return out
}

// within reports whether any of the spans contains the offset.
func within(spans []span, at int) bool {
	for _, s := range spans {
		if at >= s.lo && at < s.hi {
			return true
		}
	}
	return false
}

// covered reports whether any of the spans contains the match.
func covered(spans []span, match span) bool {
	for _, s := range spans {
		if s.covers(match) {
			return true
		}
	}
	return false
}

// carrierFilter returns the predicate deciding which source offsets of
// one carrier the matcher reads a site at.
//
// Every carrier but Go is read whole, which is what makes the pass's
// domain the domain the naming lint reads: the specification, the
// documentation, the schemas, and the tracked root-level contract
// documents each carry the phrase in prose, and a carrier the pass
// narrowed would leave sites the lint reports with no pass able to write
// them. In a Go file the law's domain is the comment, so a site in a
// string literal is outside it: `lenny runtime validate` prints such a
// literal as operator-facing help text, which this migration leaves
// unchanged.
func carrierFilter(target, content string) (func(int) bool, error) {
	if filepath.Ext(target) != ".go" {
		return func(int) bool { return true }, nil
	}
	comments, err := goCommentSpans(target, content)
	if err != nil {
		return nil, err
	}
	return func(at int) bool { return within(comments, at) }, nil
}

// goCommentSpans returns the byte span of every comment in a Go file.
//
// A file the scanner cannot read fails the run rather than being read
// whole or skipped. Reading it whole would rewrite a string literal the
// law does not govern, and skipping it would leave every site it carries
// with no pass able to write them, which is the writerless site the
// shared domain exists to prevent.
func goCommentSpans(target, content string) ([]span, error) {
	fset := token.NewFileSet()
	file := fset.AddFile(target, fset.Base(), len(content))
	var s scanner.Scanner
	var errs scanner.ErrorList
	s.Init(file, []byte(content), func(pos token.Position, msg string) {
		errs.Add(pos, msg)
	}, scanner.ScanComments)
	var out []span
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT {
			at := file.Offset(pos)
			out = append(out, span{lo: at, hi: at + len(lit)})
		}
	}
	if errs.Len() > 0 {
		return nil, fmt.Errorf("read the comments of %s: %w", target, errs.Err())
	}
	return out, nil
}
