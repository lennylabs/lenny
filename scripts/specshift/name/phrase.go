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
// held to one comment by the paragraph rule below. A match inside a
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
		if !oneParagraph(joined[match.lo:match.hi], content, source.lo) {
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

// oneParagraph reports whether a match stands inside a single run of
// prose. A match carrying no join byte spans one line and always does. A
// match the join folded together spans two lines, and it is one site
// only when the line it opens on is itself written behind a comment
// marker, which is what makes the marker on the following line the
// continuation of the same comment.
//
// The rule is answered here, over the joined text, rather than by
// reading some carriers under a join of their own, because the join is
// one join and the naming lint reads the population this matcher
// enumerates. It bounds the hazard a markdown document carries: a number
// sign, an asterisk, or a pair of hyphens opening a line of a markdown
// paragraph is a heading, a list item, or an emphasis run, each of which
// may interrupt a paragraph legally. The paragraph line above it carries
// no marker, so the two lines are two paragraphs and the fold across
// them is no site. Without the rule the pass would either abort at a
// position that is no bare noun phrase or splice a substitution over the
// newline, deleting the heading or the list marker between them and the
// anchor derived from the heading with it.
func oneParagraph(match, content string, at int) bool {
	if !strings.ContainsRune(match, rune(citation.JoinByte)) {
		return true
	}
	return commentedLine(content, at)
}

// commentedLine reports whether the source line holding an offset opens
// behind one of the comment markers the continuation join consumes.
func commentedLine(content string, at int) bool {
	start := strings.LastIndexByte(content[:at], '\n') + 1
	line := strings.TrimLeft(content[start:], " \t")
	for _, marker := range commentMarkers {
		if strings.HasPrefix(line, marker) {
			return true
		}
	}
	return false
}

// commentMarkers are the markers the continuation join consumes, which
// are the line comment of the slash dialects, the number-sign dialects,
// the double-hyphen dialects, and the leading star of a block comment
// together with the opener that introduces it.
var commentMarkers = []string{"//", "/*", "#", "--", "*"}

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
// pass holds two positions. The first is the prose comment the scope
// package's position rule states, so a site in a function-body comment
// or in a trailing inline comment is outside the pass while a site in a
// header block, in the documentation of a declaration, or between the
// elements of a package-level literal is inside it. The second is the
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
// reads a site in, which are the prose comments the scope package's
// position rule governs together with the string literals the
// pinned-literal register resolves for this carrier.
func goAdmittedSpans(target, content string, pinned map[int]bool) ([]span, error) {
	fset, file, err := parseGo(target, content)
	if err != nil {
		return nil, err
	}
	prose, err := scope.GoProseCommentSpans(target, content)
	if err != nil {
		return nil, err
	}
	var out []span
	for _, p := range prose {
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
// pinned-literal register numbers are enumerated from. The prose
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
