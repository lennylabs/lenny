// SPDX-License-Identifier: MIT

package name

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
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
// matches as one site. The join byte is admitted beside the hyphen of
// the compound spelling as well as alone, because a wrap falling
// immediately after that hyphen leaves both bytes standing between the
// reserved word and the head noun. A class reading only the join byte
// alone matches neither line of such a wrap, so the occurrence is a site
// no pass writes and no lint reports, and every later occurrence number
// of the file shifts by one against the register keyed to it.
var reservedExpr = regexp.MustCompile(
	`(?i)(?:lifecycle|control)(?:[ \t]+|-?` + joinClass + `-?|-)channels?`,
)

// joinClass is the byte the continuation join leaves behind, written as
// a regular-expression escape.
var joinClass = fmt.Sprintf(`\x{%04x}`, citation.JoinByte)

// FindReservedPhrases returns the source line of every bare reserved
// noun phrase a text carries, wherever it stands, in source order.
//
// The pass reads the positions the naming law governs, which in a Go
// file is the doc comment. This answers the wider question a gate
// package asks of its own source: whether the phrase stands anywhere in
// it, a string literal holding a fixture body included. A fixture of
// that kind belongs under testdata, which sits outside the read domain
// of the gates and the write domain of every pass, so a caller can hold
// its own tree to that rule.
//
// It is exported so no caller restates the phrase. The matcher is
// written once, as a regular-expression literal in this file, because a
// specimen written anywhere else would be a site of the class the pass
// removes.
func FindReservedPhrases(content string) []int {
	joined, offsets := citation.Join(content)
	var lines []int
	for _, m := range reservedExpr.FindAllStringIndex(joined, -1) {
		lines = append(lines, citation.LineOf(content, offsets[m[0]]))
	}
	return lines
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
// assigned in. The pinned set names the string literals of a tier-11
// reconciliation carrier the pinned-literal register resolves, and is
// empty for every other carrier.
//
// The continuation join is applied before the matcher, so a phrase
// wrapped across two comment lines is one site rather than two half
// sites neither the pass nor the naming lint reads. It is the one join
// the citation matcher applies, over every carrier, so the two matchers
// enumerate one wrapped population. A match that spans a join is then
// held to one comment by the rule below. A match inside a
// markdown anchor identifier is not a site and takes no occurrence
// number, because it needs no register entry.
func findSites(target, content string, pinned map[int]bool) ([]site, error) {
	joined, offsets := citation.Join(content)
	excluded := anchorSpans(joined)
	admits, err := carrierFilter(target, content, pinned)
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
		if !oneComment(target, joined, match, content, offsets) {
			continue
		}
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

// oneComment reports whether a match stands inside a single comment. A
// match carrying no join byte spans one line and always does. A match
// the join folded together spans a line break, and it is one site only
// when both lines of every fold it spans open behind the same comment
// marker of the carrier's own format, which is what makes the second
// line the continuation of the comment the first line opens.
//
// The rule is answered here, over the joined text, rather than by
// reading some carriers under a join of their own, because the join is
// one join and the naming lint reads the population this matcher
// enumerates. It bounds the hazard a markdown document carries: a number
// sign, an asterisk, or a pair of hyphens opening a markdown line is a
// heading, a list item, or an emphasis run rather than a comment, so a
// fold between two headings or two list items is no site, and neither is
// a fold from a paragraph line onto a heading below it. Without the rule
// the pass would either abort at a position that is no bare noun phrase,
// with no hand correction available, or splice a substitution over the
// newline, deleting the heading or the list marker between the two lines
// and the anchor derived from the heading with it, which the anchor pass
// and the fragment-link gate resolve against.
func oneComment(target, joined string, match span, content string, offsets []int) bool {
	ext := strings.ToLower(filepath.Ext(target))
	for i := match.lo; i < match.hi; i++ {
		if joined[i] != citation.JoinByte {
			continue
		}
		opened := commentDialect(ext, citation.LineOpenMarker(content, offsets[i]))
		if opened == "" || opened != commentDialect(ext, citation.ContinuationMarker(content, offsets[i])) {
			return false
		}
	}
	return true
}

// commentDialect names the comment dialect a line-opening marker writes
// in a carrier of the given format, and returns the empty string when
// the marker is not a comment marker there at all.
//
// The markers the continuation join consumes are shared across formats,
// and the same bytes carry different meanings in different ones. A
// number sign, an asterisk, and a pair of hyphens open a heading, a list
// item, and an emphasis run in a markdown document, so a markdown
// carrier admits only the slash line comment, which a fenced snippet
// holds. A JSON document carries no comment syntax at all. A format this
// migration does not name admits none either, so a fold in one is no
// site rather than a site the pass writes blind.
//
// The block-comment opener and the star a block comment continues on are
// one dialect, so a phrase wrapped inside a block comment is one site.
func commentDialect(ext, marker string) string {
	switch ext {
	case ".go", ".proto":
		switch {
		case strings.HasPrefix(marker, "//"):
			return "//"
		case marker == "/*" || marker == "*":
			return "/*"
		}
	case ".yaml", ".yml":
		if strings.HasPrefix(marker, "#") {
			return "#"
		}
	case ".md", ".json":
		if strings.HasPrefix(marker, "//") {
			return "//"
		}
	}
	return ""
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

// anchorSpans returns the markdown anchor identifiers of a text, which
// the naming law places outside the matcher. The exclusion itself comes
// from the scope package, so the pass writes against the same statement
// of it the naming lint reads. The path half of a link destination is
// outside the exclusion, so a phrase there is a site the register
// resolves like any other.
func anchorSpans(joined string) []span {
	var out []span
	for _, m := range scope.MarkdownAnchorIdentifiers(joined) {
		out = append(out, span{lo: m[0], hi: m[1]})
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
// A carrier outside the prohibition's domain admits no offset at all, so
// the phrase it holds is neither a site the pass rewrites nor a site the
// fail-closed rule aborts on. That domain is the one the scope package
// states, and it is what makes the pass's population the population the
// naming lint reads: chart values, chart templates, scaffold templates,
// and the runtime SDK sources of the languages other than Go all carry
// the phrase in text the law does not govern, and a pass reading them
// would abort at every one while the lint reported none. A tracked Go
// file under sdks/ is inside the domain like any other tracked Go file,
// so the doc comments of the Go runtime SDK are read here.
//
// A carrier inside the domain is read whole except for Go, where the
// pass holds two positions. The first is the doc comment the scope
// package's position rule states, so a site in the documentation of the
// package clause or of a declaration is inside the pass while a site in
// a detached header block, in a comment between the elements of a
// package-level literal, in a trailing inline comment, or in a
// function-body comment is outside it. The second is the
// string literal the pinned-literal register
// names, which is how the specification prose, heading slugs, and
// intra-spec links a tier-11 reconciliation test pins are rewritten by
// the same run that rewrites the specification they pin. Every other
// string literal is outside the pass: `lenny runtime validate` prints
// one as operator-facing help text, which this migration leaves
// unchanged.
func carrierFilter(target, content string, pinned map[int]bool) (func(int) bool, error) {
	if !scope.ReservedPhraseCarrier(target) {
		return func(int) bool { return false }, nil
	}
	if filepath.Ext(target) != ".go" {
		return func(int) bool { return true }, nil
	}
	admitted, err := goAdmittedSpans(target, content, pinned)
	if err != nil {
		return nil, err
	}
	return func(at int) bool { return within(admitted, at) }, nil
}

// goAdmittedSpans returns the byte spans of a Go carrier the matcher
// reads a site in, which are the doc comments the scope package's
// position rule governs together with the string literals the
// pinned-literal register resolves for this carrier.
func goAdmittedSpans(target, content string, pinned map[int]bool) ([]span, error) {
	fset, file, err := parseGo(target, content)
	if err != nil {
		return nil, err
	}
	docs, err := scope.GoDocCommentSpans(target, content)
	if err != nil {
		return nil, err
	}
	var out []span
	for _, p := range docs {
		out = append(out, span{lo: p[0], hi: p[1]})
	}
	for i, literal := range stringLiteralSpans(fset, file) {
		if pinned[i+1] {
			out = append(out, literal)
		}
	}
	return out, nil
}

// goStringLiteralCount returns how many string literals a Go carrier
// holds, which is what the pinned-literal register's positions are
// numbered against.
func goStringLiteralCount(target, content string) (int, error) {
	fset, file, err := parseGo(target, content)
	if err != nil {
		return 0, err
	}
	return len(stringLiteralSpans(fset, file)), nil
}

// parseGo parses a Go carrier, which is what the string literals the
// pinned-literal register numbers are enumerated from. The doc
// comments the same carrier holds come from the scope package, which
// states the position rule the naming lint reads against.
//
// A file the parser cannot read fails the run rather than being read
// whole or skipped. Reading it whole would rewrite an implementation
// comment and an operator-facing literal the law does not govern, and
// skipping it would leave every pinned literal it carries with no pass
// able to write it, which is the writerless site the shared domain
// exists to prevent.
func parseGo(target, content string) (*token.FileSet, *ast.File, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, target, content, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, fmt.Errorf("read the doc comments of %s: %w", target, err)
	}
	return fset, file, nil
}

// stringLiteralSpans returns the byte span of every string literal of a
// Go file, in source order, which is the order the pinned-literal
// register's positions are assigned in.
func stringLiteralSpans(fset *token.FileSet, file *ast.File) []span {
	var out []span
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		lo := fset.Position(lit.Pos()).Offset
		out = append(out, span{lo: lo, hi: lo + len(lit.Value)})
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].lo < out[j].lo })
	return out
}
