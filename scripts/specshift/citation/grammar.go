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

// glossOpenSp is the whitespace class a gloss admits ahead of its opening
// delimiter, which is a space or a tab alone. The join byte is excluded: a
// gloss whose opening delimiter sat behind a consumed continuation would take
// the whole of the following comment line whenever that line opens with a
// parenthesis, a quote, or a backtick, even though nothing of the citation was
// wrapped. The citation text a register is keyed by would then carry a
// sentence's own code span or parenthetical, so the key would drift with prose
// the citation never pointed at, and the scan would resume past that fragment,
// where a citation written inside it is never returned, never resolved, and
// never counted. A gloss therefore opens on the line its member sits on.
//
// A gloss is bounded by the delimiter that closes it, so its body still admits
// the join byte and a gloss opened on its member's line closes on the
// continuation line the join folded in. Refusing it there would end the member
// list at the wrapped member and drop every member written behind the wrap,
// which is the head-matching failure the whole-citation rule exists to prevent:
// the resolver does not read the dropped members, the ratchet does not count
// them, and the rewritten carrier reads as an anchor followed by orphan
// integers while its file reaches a zero count.
const glossOpenSp = "[ \t]"

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
	headMember    = "member"
)

// qualifierBody is the run a citation interposes between its reference and
// the line-number keyword, which names the sub-element the citation points
// at. It is the run the class's broad predicate tolerates in the same
// position, less the bytes named below, and it carries the same length
// bound, so every occurrence the form reads is an occurrence the predicate
// selects and the form's population stays inside the class's own.
//
// A run narrower than this leaves every citation whose sub-element name the
// matcher does not admit unconverted, unread by the resolver, and uncounted
// by the ratchet, so its file reaches a zero count with the stale pointer
// standing. The spellings such a narrowing missed are ordinary: a separator
// standing between the reference and the keyword, a colon written with
// whitespace behind it, a parenthesis that opens ahead of the sub-element
// name rather than against the keyword, a name of more than three tokens,
// and a name carrying a path, a dotted field, or a markdown link target.
// The fixtures under testdata/citations carry one carrier of each.
// Enumerating those spellings is
// the narrowing this replaces: each enumeration missed the next spelling,
// and the members it missed had no route out, because the pass that would
// rewrite them, the resolver that would read them, and the ratchet that
// would count them are all driven by this one form.
//
// Widening the run does not read prose as a citation, because the branch it
// sits on still requires the literal keyword and a line number behind it.
// Three bytes are held out of the run so the two spellings that are not
// citations stay outside the form: the backslash of a regular-expression
// literal written over this very form, and the percent sign and equals sign
// of an assertion message naming a fixture's heading range. The section
// sign is held out as well, so a run never reaches across the reference of
// the citation written behind it and swallows the citation in between.
//
// The run ends on a byte that is not a word byte, so the keyword stands at
// the head of a word rather than at the tail of one and `deadline 30` is no
// citation. It is written lazily, so a citation ends at the first
// line-number token behind its reference rather than at the last one inside
// the bound.
const (
	qualifierByte = `[^\n%\\\x{00a7}]`
	qualifierEnd  = `[^\n%\\\x{00a7}A-Za-z0-9_]`
)

// qualifierBody is that run: whatever the citation writes there, up to the
// bound below, ending on the byte that separates the run from the keyword.
const qualifierBody = qualifierByte + `{0,95}?` + qualifierEnd

// refExpr matches a reference of either spelling, which is what a head opens
// on. It is read again inside a matched head, so a citation is read from the
// reference standing closest to its line numbers.
var refExpr = regexp.MustCompile(
	`§` + sp + `*\d+(?:\.\d+)*|(?:spec/)?\d{2}_[A-Za-z0-9][A-Za-z0-9._-]*\.md`,
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
		// and a member written behind such a space absorbs the digits of an
		// unrelated number, so `§17.1 flat` wrapped onto `maxUnavailable:1`
		// and `§25.11 daily 03:30 UTC` would both read as citations. Each
		// such phantom enters the per-file count of a file that carries no
		// citation, and its only route to zero is the pass rewriting prose. A
		// colon the keyword itself stands behind is read by the branch below,
		// where the keyword and the line number behind it identify the form.
		`:` + joinClass + `?` +
		`|` +
		// The run naming the sub-element of the section, and the keyword
		// itself. The run carries whatever the citation writes between its
		// reference and the keyword, the parenthesis a carrier opens
		// included, wherever that parenthesis opens. A parenthesis the head
		// leaves open is recognized by the scanner from the run's own
		// delimiters, so the parenthesized spelling is read whole; the
		// fixture `testdata/citations/qualifier-parenthetical.txt` carries
		// one. When nothing closes it inside the citation's bounds the
		// occurrence is still returned, so it is resolved and counted, and it
		// is marked unbalanced so the resolver reports it for hand correction
		// rather than a pass converting a span carrying an unpaired delimiter
		// to a single anchor.
		`(?P<` + headQualifier + `>` + qualifierBody + `)(?:lines?)` + sp + `+` +
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

// glossExpr matches the delimited run written behind a member: a parenthesized
// phrase, or a fragment in double quotes, single quotes, or backticks. The run
// is consumed with its member rather than terminating the match, so the member
// list continues past it and the citation is read, keyed, resolved, and counted
// whole.
//
// Consuming the run and replacing it are separate decisions. What a rewrite
// writes over is the citation's reference-and-members run, which ends at the
// last member's own text, so the delimited run behind that member is kept.
// Citation.MembersEnd is that end, and it is the span the line pass converts
// and the span a served strip removes. Offset+len(Raw) is the span the grammar
// read, and nothing writes over it.
//
// The two spans are separate because a delimited run in this position is not
// reliably a gloss. A gloss is text about the cited line, which the anchor
// beside it makes redundant: a citation carrying the parenthetical
// `(durable checkpoint)` keeps reading as its section reference with that
// parenthetical behind it, and a section reference carrying a parenthesized
// phrase with no line number involved is an idiom the tree already holds by the
// thousand. The same position carries other things. It carries the sentence's
// own object, as in a declaration named for the `"+5s"` margin the spec states,
// where deleting the run leaves the sentence with no noun. It carries a
// requirement identifier, which is the only greppable tie between a rule and
// the code that implements it. It carries a lettered or numbered sub-case,
// which is what says which of a section's obligations the code implements. It
// carries a quoted spec term or a spec value a constant encodes, and a metric
// name or an error code a declaration is named for.
//
// Position does not tell those apart from a gloss. A run standing in pointer
// annotation position, behind `spec:` and nothing else, is a requirement
// identifier or a sub-case or a code literal about a quarter of the time in
// this tree, and a run in double quotes or backticks is the sentence's own
// object far more often than it is a gloss. The delimiter does not tell them
// apart either. A content test would have to enumerate what a gloss looks like,
// which is the enumeration failure the qualifier commentary above rejects: each
// enumeration misses the next spelling, and here every miss is a deletion.
//
// The two ways of being wrong are not equally bad, and the rule takes the
// cheaper one. Keeping a run that was a gloss leaves a phrase standing that the
// anchor beside it partly implies, which costs a reader a few redundant words
// in a comment and produces text the tree already reads as ordinary. Deleting a
// run that was not a gloss takes the carrier's own words out of a comment
// nobody re-reads, and no gate reports it: the citation was counted, resolved,
// and retired exactly as intended, the accounting balances, and nothing holds
// the deleted words to recover them from. So every delimited run is kept, at
// every site, without the rule asking which kind it is.
//
// Do not narrow this back. Folding the run into the span a rewrite replaces,
// whether for one delimiter, for the annotation position alone, or behind any
// test of what the run contains, reinstates the deletion at every site the test
// misreads, and the misreads are silent. The asymmetry above holds at every
// threshold such a test could take, so no signal buys anything the flat rule
// does not already give.
//
// Every alternative commits to a delimiter, so every alternative says where it
// ends. A bare word or two standing behind a member is not a gloss alternative,
// even though authors write one there, because nothing in the text distinguishes
// a gloss the author wrote against the pointer from the sentence's own noun
// phrase running on behind it. A citation standing on its own in a comment and
// a citation embedded in a sentence are the same bytes at that position, which
// are a member, a space, and a word, and the two readings are told apart by
// where the citation sits in its comment and by what stands ahead of it. The
// grammar sees neither. Refusing the bare word keeps it out of the citation's
// text as well as out of the replaced span, so the register key names the
// pointer alone. The fixtures under testdata/citations carry one of each
// reading against the same member spelling.
//
// Refusing the bare word ends the member list wherever a bare word stands
// between two members of one citation, and where a bare word stands ahead of a
// delimited phrase that carries a further member behind it. The members written
// behind such a word are left in the carrier, which is the head-matching failure
// this file names throughout, and the pass does not write that state: the anchor
// it emits stands against the words and the members the citation did not
// consume, and those compose the retired form again, which the pass's own
// post-condition reports for hand correction. The whole tree holds a handful of
// carriers written that way, and they are hand-corrected rather than silently
// truncated. Admitting the bare word to reach them would reintroduce the
// deletion for every embedded citation in the tree, which is the trade the
// paragraph above rejects.
//
// So the spellings this matcher consumes behind a member are the parenthesized
// phrase, the double-quoted fragment, the single-quoted fragment, and the
// backticked fragment, and no others. The written statement of the retired
// citation form also admits a bare word or two there, which makes this matcher
// narrower than that statement. The narrowing is deliberate and stays. A bare
// word is left to the residual gate and to hand correction rather than
// consumed, and that is safe for three reasons. Both citation baselines stand
// drained to zero entries, so admitting the bare word would rewrite nothing in
// the tree as it stands. The residual gate's predicate is broader than this
// grammar and overlaps rather than matches it, so a site whose gloss this
// grammar refuses is still selected there and still reported. A site the pass
// refuses is corrected by hand, which is the sanctioned route for the handful
// of carriers written that way. Widening the alternatives here to close the
// difference would buy those sites nothing and would cost the deletion the
// paragraphs above reject.
//
// A gloss is bounded by its closing delimiter and by the line it opened on, and
// the scanner bounds every alternative at the head of the next citation. A gloss
// alternative that admitted a newline and closed on nothing would run from an
// unpaired quote to the next quote anywhere in the file, and because the scan
// resumes at the end of the consumed span every citation inside that run would
// go unreturned: the resolver would not resolve it, the ratchet would not count
// it, and the pass would rewrite the whole span, code included, to one anchor.
// That is the failure the whole-citation rule exists to prevent.
//
// No alternative is bounded by a byte count. A count long enough to be
// invisible in review still rejects the gloss that exceeds it, and a rejected
// gloss ends the member list, so every member written after a long gloss is
// left unconsumed. That is the same head-matching failure in the other
// direction: the resolver does not read the dropped members, the ratchet does
// not count them, and the rewritten carrier reads as an anchor followed by
// orphan integers.
//
// A delimited alternative admits the join byte inside its body but not ahead of
// its opening delimiter, so a gloss opens on its member's line, closes on the
// continuation line the join folded into it, and the separator and member
// written behind the closing delimiter are consumed as members. Neither
// alternative reads a delimited fragment that opens the following comment line,
// which is a sentence's own code span or parenthetical rather than a gloss.
//
// A quoted alternative also requires whitespace ahead of its opening quote,
// because a quote written directly against a member's last digit is the closing
// quote of the carrier's own string literal or an English apostrophe, and
// neither opens a gloss.
var glossExpr = regexp.MustCompile(
	"^(?:" +
		glossOpenSp + `*\(` + glossBody + `*\)` +
		`|` + glossOpenSp + `+"[^"\n]*"` +
		`|` + glossOpenSp + `+'[^'\n]*'` +
		"|" + glossOpenSp + "+`[^`\n]*`" +
		")",
)

// glossBody is the byte class a parenthesized gloss admits. It stops at the
// parentheses that open and close the gloss and at a newline the join did not
// consume, so the body runs no further than the parenthetical the author wrote.
const glossBody = `[^()\n]`

// The join between two lines of one carrier: the newline together with the
// carrier's comment marker and the whitespace on either side of it. The marker
// is the one place a carrier's dialect enters the grammar.
//
// Without the join a line-oriented scan sees a reference with no line-number
// token on the first line and a line-number token with no reference on the
// second, so neither the form nor the residual predicate reads the citation,
// the resolver does not resolve it, the ratchet does not count it, and the file
// reaches a zero count while a stale pointer survives.
//
// A marker is required. A wrap the join crosses is a wrap inside one comment,
// and the marker is what identifies the second line as the continuation of that
// comment. A join that crossed a bare line break would fold two unrelated lines
// of a carrier together, so a reference ending one line and a number opening the
// next would read as one citation the author did not write, and the wrapped
// population the resolver baseline and the per-file ratchet baseline are seeded
// from is measured under this rule.
const (
	continuationLead    = `[ \t]*\r?\n[ \t]*`
	continuationMarkers = `/{2,}|#+|--|\*`
)

// continuationExpr matches one continuation: the newline, the carrier's comment
// marker, and the whitespace on either side of it.
var continuationExpr = regexp.MustCompile(
	continuationLead + `(?:` + continuationMarkers + `)[ \t]*`,
)

// continuationMarkerExpr matches one continuation anchored at the offset the
// join consumed it from, capturing the marker itself, and lineOpenerExpr
// captures the marker a source line opens with. The opener set carries the
// block-comment opener beside the markers a continuation may lead with, because
// a block comment opens on `/*` and continues on the star the join consumes.
//
// Both are held here beside the marker set, so a caller deciding whether two
// joined lines belong to one comment reads the same statement of a carrier's
// comment syntax the join itself is defined against.
var (
	continuationMarkerExpr = regexp.MustCompile(`^` + continuationLead + `(` + continuationMarkers + `)[ \t]*`)
	lineOpenerExpr         = regexp.MustCompile(`^[ \t]*(/\*|` + continuationMarkers + `)`)
)

// continuationMarker returns the marker the continuation join consumed at a
// source offset, or the empty string when no continuation stands there.
func continuationMarker(content string, at int) string {
	if at < 0 || at > len(content) {
		return ""
	}
	m := continuationMarkerExpr.FindStringSubmatch(content[at:])
	if m == nil {
		return ""
	}
	return m[1]
}

// lineOpenMarker returns the marker the source line holding an offset opens
// with, or the empty string when the line opens on anything else.
func lineOpenMarker(content string, at int) string {
	if at < 0 || at > len(content) {
		return ""
	}
	start := strings.LastIndexByte(content[:at], '\n') + 1
	m := lineOpenerExpr.FindStringSubmatch(content[start:])
	if m == nil {
		return ""
	}
	return m[1]
}

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
		start, end := m[0], m[1]
		copyRun(last, start)
		joined = append(joined, joinByte)
		offsets = append(offsets, start)
		last = end
	}
	copyRun(last, len(content))
	offsets = append(offsets, len(content))
	return string(joined), offsets
}
