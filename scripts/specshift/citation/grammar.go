// SPDX-License-Identifier: MIT

package citation

import "regexp"

// specPrefix is the directory every path-form citation resolves under.
const specPrefix = "spec/"

// rangeSeparators are the three characters that separate the endpoints of a
// range member: an ASCII hyphen, an en dash (U+2013), and an em dash (U+2014).
const rangeSeparators = "-–—"

// The submatch indices of headExpr.
const (
	headSection   = 1
	headFile      = 2
	headQualifier = 3
	headMember    = 5
)

// headExpr matches the head of a citation: the reference, the optional
// qualifier, the keyword or the colon standing in for it, and the first member.
// Every further member is consumed by the scanner in citation.go, which orders
// a separator ahead of a gloss so the word `and` is read as a separator.
//
// Whitespace inside the form is horizontal only. A newline that is not part of
// a continuation join terminates the citation, so a citation never runs past
// the end of the comment that carries it.
var headExpr = regexp.MustCompile(
	// The reference, by section number with any number of dotted components
	// or by file path with the spec/ prefix optional.
	`(?:§[ \t]*(\d+(?:\.\d+)*)|(?:spec/)?(\d{2}_[A-Za-z0-9][A-Za-z0-9._-]*\.md))` +
		// The optional short qualifier naming a sub-element of the section.
		`(?:[ \t]+([A-Za-z]+[ \t]+[0-9A-Za-z]+|table|preamble|[A-Z][A-Z0-9]*-[0-9]+))?` +
		// The keyword, or the colon written directly against the reference.
		`(?::[ \t]*|[ \t]+(lines?)[ \t]+)` +
		// The first member.
		`([0-9]+(?:[ \t]*[-\x{2013}\x{2014}][ \t]*[0-9]+)?)`,
)

// separatorExpr matches one member separator: a comma, a slash, a plus sign, or
// the word `and`.
var separatorExpr = regexp.MustCompile(`^(?:[ \t]*[,/+]|[ \t]+and\b)[ \t]*`)

// keywordExpr matches a continuation member's repeat of the keyword, which the
// comma and slash spellings both carry.
var keywordExpr = regexp.MustCompile(`^lines?[ \t]+`)

// memberExpr matches one member.
var memberExpr = regexp.MustCompile(`^[0-9]+(?:[ \t]*[-\x{2013}\x{2014}][ \t]*[0-9]+)?`)

// glossExpr matches the short trailing gloss naming what the cited line says,
// written as a parenthesized phrase, a quoted fragment, or a bare word or two
// with an optional parenthesized tail. The gloss is consumed with its member
// rather than terminating the match.
var glossExpr = regexp.MustCompile(
	"^(?:" +
		`[ \t]*\([^()]*\)` +
		`|[ \t]*"[^"]*"` +
		`|[ \t]*'[^']*'` +
		"|[ \t]*`[^`]*`" +
		`|[ \t]+[A-Za-z][A-Za-z0-9_./-]*(?:[ \t]+[A-Za-z][A-Za-z0-9_./-]*)?(?:[ \t]*\([^()]*\))?` +
		")",
)

// continuationExpr matches the join between two comment lines: the newline
// together with the carrier's comment marker and the whitespace on either side
// of it. The marker is the one place a carrier's dialect enters the grammar.
//
// Without the join a line-oriented scan sees a reference with no line-number
// token on the first line and a line-number token with no reference on the
// second, so neither the form nor the residual predicate reads the citation,
// the resolver does not resolve it, the ratchet does not count it, and the file
// reaches a zero count while a stale pointer survives.
var continuationExpr = regexp.MustCompile(`[ \t]*\r?\n[ \t]*(?:/{2,}|#+|--|\*)[ \t]*`)

// join collapses every continuation into a single space and returns the joined
// text together with the source offset of each of its bytes. The offset slice
// carries one further entry holding the length of the source, so the end of a
// match that runs to the end of the text maps back.
func join(content string) (string, []int) {
	joined := make([]byte, 0, len(content))
	offsets := make([]int, 0, len(content)+1)
	last := 0
	for _, m := range continuationExpr.FindAllStringIndex(content, -1) {
		for i := last; i < m[0]; i++ {
			joined = append(joined, content[i])
			offsets = append(offsets, i)
		}
		joined = append(joined, ' ')
		offsets = append(offsets, m[0])
		last = m[1]
	}
	for i := last; i < len(content); i++ {
		joined = append(joined, content[i])
		offsets = append(offsets, i)
	}
	offsets = append(offsets, len(content))
	return string(joined), offsets
}
