// SPDX-License-Identifier: MIT

package citation

import (
	"regexp"
	"strings"
)

// specPrefix is the directory every path-form citation resolves under.
const specPrefix = "spec/"

// rangeSeparators are the three characters that separate the endpoints of a
// range member: an ASCII hyphen, an en dash (U+2013), and an em dash (U+2014).
const rangeSeparators = "-–—"

// joinByte is the byte join writes in place of a continuation it consumed, and
// joinClass is that byte as a regular-expression escape. It is a distinguishable
// byte rather than a space so the grammar can tell a wrap the join collapsed
// from whitespace the author wrote. The colon spelling turns on that
// distinction: the form is written with the first member directly against the
// colon, and the only spaced spelling in the tree is a colon citation wrapped
// across two comment lines. Admitting authored whitespace there instead would
// read an English or YAML colon as a citation keyword, which turns prose into
// phantom citations the ratchet counts and the pass can only clear by rewriting
// prose.
const (
	joinByte   = '\x1f'
	joinClass  = `\x{001f}`
	spaceBytes = " \t\x1f"
)

// sp is the horizontal-whitespace class the form admits, which is a space, a
// tab, or the byte a consumed continuation left behind.
const sp = "[ \t\x1f]"

// render returns text with every consumed continuation shown as the space it
// stands for, so a citation's text, gloss, and members read as one line.
func render(s string) string { return strings.ReplaceAll(s, string(joinByte), " ") }

// The submatch indices of headExpr.
const (
	headSection   = 1
	headFile      = 2
	headQualifier = 3
	headParen     = 4
	headMember    = 6
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
	`(?:§` + sp + `*(\d+(?:\.\d+)*)|(?:spec/)?(\d{2}_[A-Za-z0-9][A-Za-z0-9._-]*\.md))` +
		// The optional short qualifier naming a sub-element of the section.
		// A qualifier of three words is spelled in words alone, because a
		// numbered third token belongs to a member list rather than to a
		// sub-element name: `line 315 and line 321` is two members.
		`(?:` + sp + `+([A-Za-z]+` + sp + `+[0-9A-Za-z]+|[A-Za-z]+(?:` + sp + `+[A-Za-z]+){2}|table|preamble|[A-Z][A-Z0-9]*-[0-9]+))?` +
		// The keyword, or the colon standing in for it with the member written
		// directly against it. A wrap the join consumed is admitted after the
		// colon, and nothing else is: an authored space after a colon belongs
		// to English or to YAML rather than to the form.
		//
		// The keyword may open a parenthesis of the carrier's own, with or
		// without the word `spec` behind it, so a citation written as
		// `§10.3 NET-063 (spec line 327)` is read whole. A matcher requiring
		// the keyword to follow the reference or the qualifier immediately
		// leaves that spelling unconverted, unread by the resolver, and
		// uncounted by the ratchet.
		`(?::` + joinClass + `?|(?:` + sp + `+|` + sp + `*(\()` + sp + `*(?:spec` + sp + `+)?)(lines?)` + sp + `+)` +
		// The first member.
		`([0-9]+(?:` + sp + `*[-\x{2013}\x{2014}]` + sp + `*[0-9]+)?)`,
)

// separatorExpr matches one member separator: a comma, a slash, a plus sign, or
// the word `and`.
var separatorExpr = regexp.MustCompile(`^(?:` + sp + `*[,/+]|` + sp + `+and\b)` + sp + `*`)

// keywordExpr matches a continuation member's repeat of the keyword, which the
// comma and slash spellings both carry.
var keywordExpr = regexp.MustCompile(`^lines?` + sp + `+`)

// memberExpr matches one member.
var memberExpr = regexp.MustCompile(`^[0-9]+(?:` + sp + `*[-\x{2013}\x{2014}]` + sp + `*[0-9]+)?`)

// glossExpr matches the short trailing gloss naming what the cited line says,
// written as a parenthesized phrase, a quoted fragment, or a bare word or two
// with an optional parenthesized tail. The gloss is consumed with its member
// rather than terminating the match.
//
// Every alternative is bounded to one line and to a short run of bytes. A gloss
// alternative that admitted a newline and had no length bound would run from an
// unpaired quote to the next quote anywhere in the file, and because the scan
// resumes at the end of the consumed span every citation inside that run would
// go unreturned: the resolver would not resolve it, the ratchet would not count
// it, and the pass would rewrite the whole span, code included, to one anchor.
// That is the failure the whole-citation rule exists to prevent.
//
// A quoted alternative also requires whitespace ahead of its opening quote,
// because a quote written directly against a member's last digit is the closing
// quote of the carrier's own string literal or an English apostrophe, and
// neither opens a gloss.
var glossExpr = regexp.MustCompile(
	"^(?:" +
		sp + `*\(` + glossBody + `{0,` + maxGlossBody + `}\)` +
		`|` + sp + `+"[^"\n]{0,` + maxGlossBody + `}"` +
		`|` + sp + `+'[^'\n]{0,` + maxGlossBody + `}'` +
		"|" + sp + "+`[^`\n]{0," + maxGlossBody + "}`" +
		`|` + sp + `+` + glossWord + `(?:` + sp + `+` + glossWord + `)?(?:` + sp + `*\(` + glossBody + `{0,` + maxGlossBody + `}\))?` +
		")",
)

// glossBody is the byte class a parenthesized gloss admits, and glossWord is
// one bare word of a gloss. maxGlossBody bounds every one of them, so a gloss
// stays the short phrase the form admits.
const (
	glossBody    = `[^()\n]`
	glossWord    = `[A-Za-z][A-Za-z0-9_./-]*`
	maxGlossBody = "80"
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

// join collapses every continuation into a single joinByte and returns the
// joined text together with the source offset of each of its bytes. The offset
// slice carries one further entry holding the length of the source, so the end
// of a match that runs to the end of the text maps back.
func join(content string) (string, []int) {
	joined := make([]byte, 0, len(content))
	offsets := make([]int, 0, len(content)+1)
	copyRun := func(from, to int) {
		for i := from; i < to; i++ {
			b := content[i]
			// A joinByte the source itself carries is rewritten to a space, so
			// only a continuation the join consumed carries the meaning the
			// colon spelling reads from it.
			if b == joinByte {
				b = ' '
			}
			joined = append(joined, b)
			offsets = append(offsets, i)
		}
	}
	last := 0
	for _, m := range continuationExpr.FindAllStringIndex(content, -1) {
		copyRun(last, m[0])
		joined = append(joined, joinByte)
		offsets = append(offsets, m[0])
		last = m[1]
	}
	copyRun(last, len(content))
	offsets = append(offsets, len(content))
	return string(joined), offsets
}
