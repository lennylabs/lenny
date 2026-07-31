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

// glossWordSp is the whitespace class the bare-word gloss admits, which is a
// space or a tab alone. A bare word run has no closing delimiter, so nothing
// but the end of its line bounds it. Admitting the join byte there reads across
// the wrap into the following comment line and takes its first word or two as
// the gloss, so the citation text a register is keyed by carries prose the
// citation does not own and the pass converting the citation to a single anchor
// deletes the newline, the following line's comment marker, and its opening
// words. In the # dialect that removal also stops the following text being a
// comment.
//
// glossDelimSp is the whitespace class a delimited gloss admits ahead of its
// opening delimiter, which additionally admits the byte a consumed continuation
// left behind. A delimited gloss is bounded by the delimiter that closes it, so
// a wrap between the member and the gloss, or between the gloss's opening and
// closing delimiters, is the same wrap inside a member list the form already
// carries. Refusing it there ends the member list at the wrapped member and
// drops every member written behind the wrap, which is the head-matching
// failure the whole-citation rule exists to prevent: the resolver does not read
// the dropped members, the ratchet does not count them, and the rewritten
// carrier reads as an anchor followed by orphan integers while its file reaches
// a zero count.
const (
	glossWordSp  = "[ \t]"
	glossDelimSp = sp
)

// render returns text with every consumed continuation shown as the space it
// stands for, so a citation's text, gloss, and members read as one line.
func render(s string) string { return strings.ReplaceAll(s, string(joinByte), " ") }

// The submatch names of the head expression. They are read by name so a group
// added to or removed from the form does not silently renumber the ones behind
// it.
const (
	headSection   = "section"
	headFile      = "file"
	headQualifier = "qualifier"
	headParen     = "paren"
	headMember    = "member"
)

// qualifierBody is the short sub-element name a citation may interpose between
// its reference and the keyword, which is one to three tokens. A token is a
// word that may carry an internal hyphen, and the last token may instead be a
// number, a numeric range, or a quoted fragment, which is how `item 3`,
// `paths 1-7`, and `"Version negotiation"` are written. The words `table` and
// `preamble` are instances of the general rule rather than the only one-word
// spellings.
//
// A qualifier narrower than this leaves every citation carrying a sub-element
// name the matcher does not admit unconverted, unread by the resolver, and
// uncounted by the ratchet, so its file reaches a zero count with the stale
// pointer standing. Widening it does not turn prose into a citation, because
// the branch the qualifier sits on still requires the literal keyword and a
// member behind it; the colon branch, which stands directly against the
// reference and would read an English or a YAML colon as the keyword, carries
// no qualifier at all.
//
// The first token is a word rather than a number, so a member list is never
// read as a sub-element name.
const (
	qualifierWord  = `[A-Za-z][A-Za-z0-9]*(?:-[A-Za-z0-9]+)*`
	qualifierQuote = `"[^"\n` + joinClass + `]*"|'[^'\n` + joinClass + `]*'`
	qualifierTail  = `(?:` + qualifierWord + `|[0-9]+(?:[-\x{2013}\x{2014}][0-9]+)?|` + qualifierQuote + `)`
	qualifierBody  = `(?:` + qualifierQuote + `|` + qualifierWord +
		`(?:` + sp + `+` + qualifierWord + `)?(?:` + sp + `+` + qualifierTail + `)?)`
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
	`(?:§` + sp + `*(?P<` + headSection + `>\d+(?:\.\d+)*)|(?:spec/)?(?P<` + headFile + `>\d{2}_[A-Za-z0-9][A-Za-z0-9._-]*\.md))` +
		// The keyword branch and the colon branch, ordered with the colon
		// first because only the colon branch stands directly against the
		// reference.
		`(?:` +
		// The colon standing in for the keyword. It is written directly
		// against the reference, with no qualifier between the two, and the
		// member is written directly against it. A wrap the join consumed is
		// admitted after the colon, and nothing else is: an authored space
		// after a colon belongs to English or to YAML rather than to the form,
		// and a qualifier ahead of the colon absorbs a prose word and the
		// digits of an unrelated number, so `§17.1 flat` wrapped onto
		// `maxUnavailable:1` and `§25.11 daily 03:30 UTC` both read as
		// citations. Each such phantom enters the per-file count of a file
		// that carries no citation, and its only route to zero is the pass
		// rewriting prose.
		`:` + joinClass + `?` +
		`|` +
		// The optional short qualifier naming a sub-element of the section.
		`(?:` + sp + `+(?P<` + headQualifier + `>` + qualifierBody + `))?` +
		// The keyword itself. It may open a parenthesis of the carrier's own,
		// with or without the word `spec` behind it, so a qualified citation
		// whose keyword and member sit inside a parenthesis is read whole; the
		// fixture `testdata/citations/qualifier-parenthetical.txt` carries that
		// spelling. A matcher requiring the keyword to follow the reference or
		// the qualifier immediately leaves that spelling unconverted, unread by
		// the resolver, and uncounted by the ratchet. The scanner closes that
		// parenthesis with the citation, and refuses the occurrence when it does
		// not close, so the matched span never carries an unpaired delimiter.
		`(?:` + sp + `+|` + sp + `*(?P<` + headParen + `>\()` + sp + `*(?:spec` + sp + `+)?)(?:lines?)` + sp + `+` +
		`)` +
		// The first member.
		`(?P<` + headMember + `>` + memberBody + `)`,
)

// memberBody is one member: a single line number, or a range of two line
// numbers written directly against the separator.
//
// The endpoints stand against the separator with no whitespace between them,
// which is how the form is written and how every spelling in the tree is
// written. Admitting whitespace there reads an ordinary prose aside written
// after a single-line member as the range's second endpoint, as in
// `line 277 — 129 runes`, which yields a descending range the resolver reports
// as a straddle with nothing to correct and a citation text that runs past the
// member into the sentence, so converting it to an anchor would delete the
// sentence's own number.
const memberBody = `[0-9]+(?:[-\x{2013}\x{2014}][0-9]+)?`

// separatorExpr matches one member separator: a comma, a slash, a plus sign, or
// the word `and`.
var separatorExpr = regexp.MustCompile(`^(?:` + sp + `*[,/+]|` + sp + `+and\b)` + sp + `*`)

// keywordExpr matches a continuation member's repeat of the keyword, which the
// comma and slash spellings both carry.
var keywordExpr = regexp.MustCompile(`^lines?` + sp + `+`)

// memberExpr matches one member.
var memberExpr = regexp.MustCompile(`^` + memberBody)

// glossExpr matches the short trailing gloss naming what the cited line says,
// written as a parenthesized phrase, a quoted fragment, or a bare word or two
// with an optional parenthesized tail. The gloss is consumed with its member
// rather than terminating the match.
//
// A delimited alternative is bounded by its closing delimiter and by the line
// it opened on, and the scanner bounds every alternative at the head of the
// next citation. A gloss alternative that admitted a newline and closed on
// nothing would run from an unpaired quote to the next quote anywhere in the
// file, and because the scan resumes at the end of the consumed span every
// citation inside that run would go unreturned: the resolver would not resolve
// it, the ratchet would not count it, and the pass would rewrite the whole
// span, code included, to one anchor. That is the failure the whole-citation
// rule exists to prevent.
//
// No alternative is bounded by a byte count. A count long enough to be
// invisible in review still rejects the gloss that exceeds it, and a rejected
// gloss ends the member list, so every member written after a long gloss is
// left unconsumed. That is the same head-matching failure in the other
// direction: the resolver does not read the dropped members, the ratchet does
// not count them, and the rewritten carrier reads as an anchor followed by
// orphan integers.
//
// A delimited alternative admits the join byte ahead of its opening delimiter
// and inside its body, so a gloss that opens on its member's line closes on the
// continuation line the join folded into it and the separator and member
// written behind the closing delimiter are consumed as members. The bare-word
// alternative admits glossWordSp alone, because a word run closes on nothing
// and would otherwise take the opening words of the following comment line.
//
// A quoted alternative also requires whitespace ahead of its opening quote,
// because a quote written directly against a member's last digit is the closing
// quote of the carrier's own string literal or an English apostrophe, and
// neither opens a gloss.
var glossExpr = regexp.MustCompile(
	"^(?:" +
		glossDelimSp + `*\(` + glossBody + `*\)` +
		`|` + glossDelimSp + `+"[^"\n]*"` +
		`|` + glossDelimSp + `+'[^'\n]*'` +
		"|" + glossDelimSp + "+`[^`\n]*`" +
		`|` + glossWordSp + `+` + glossWord + `(?:` + glossWordSp + `+` + glossWord + `)?(?:` + glossDelimSp + `*\(` + glossBody + `*\))?` +
		")",
)

// glossBody is the byte class a parenthesized gloss admits, and glossWord is
// one bare word of a gloss.
//
// A word carries a dot only when a word byte follows it, so a word that ends a
// sentence ends the gloss with it. A word run that absorbed the terminating dot
// would take the first word of the next sentence as its second word, and the
// citation text a register is keyed by and the pass replaces would carry prose
// the citation does not own, so converting the citation to an anchor would
// delete the opening word of the following sentence.
const (
	glossBody = `[^()\n]`
	glossWord = `[A-Za-z][A-Za-z0-9_/-]*(?:\.[A-Za-z0-9_/-]+)*`
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
