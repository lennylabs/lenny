// SPDX-License-Identifier: MIT

// Package citation holds the one implementation of the retired line-citation
// form: the grammar that reads it, the continuation join that reads a citation
// wrapped across two comment lines as one citation, and the section resolver
// that reports whether every member still points inside the section it names.
//
// The line pass, the resolver gate, and the per-file ratchet all read the form
// from here. Three implementations of one grammar drift, and a spelling one of
// them misses is a citation the pass rewrites but the gate does not count, or a
// citation the gate counts but the pass cannot reach. Either way a file reaches
// a zero count while a stale pointer survives.
//
// The grammar matches the form wherever it appears in a tracked file rather
// than matching a comment dialect. The one place a carrier's comment marker
// enters is the continuation join, which is what lets a citation wrapped across
// two comment lines be read as one citation.
//
// A citation of the retired form has three parts. It names a specification
// section, either by section number with any number of dotted components or by
// file path with the spec/ prefix optional. It may then carry a short qualifier
// naming a sub-element of that section. It then carries the word line or lines,
// or a colon standing in for that keyword and written directly against the
// reference, followed by one or more members. A member is a single line number
// or a range of two line numbers separated by an ASCII hyphen, an en dash, or
// an em dash, and it may carry a short trailing gloss naming what the line
// says. Members are separated by a comma, a slash, a plus sign, or the word
// and, and a continuation member repeats neither the reference nor the
// qualifier and may repeat the keyword.
//
// The matcher consumes every member of a citation rather than its head. A
// matcher that stopped at the first separator would leave the remaining members
// in place, where the resolver does not read them and the ratchet does not
// count them, and the rewritten carrier would read as an anchor followed by
// orphan integers. Nothing a citation consumes runs past the head of the next
// citation, so a member list never absorbs the citation written behind it. A
// citation whose head opened a parenthesis of the carrier's own runs to the
// parenthesis that closes it, which may sit on the continuation line the join
// folded into the same text.
//
// Every occurrence the grammar reads is returned, so every occurrence is
// resolved by the resolver and counted by the ratchet. An occurrence the pass
// cannot convert without leaving a residue is reported for hand correction
// instead, which is what the resolver's failure kinds carry. Two of those kinds
// are properties of the text the grammar read rather than of the section it
// names: a head that opened a parenthesis with none to close it inside the
// citation's bounds, whose span carries an unpaired delimiter that converting to
// a single anchor would strand the carrier's closing parenthesis behind; and a
// member whose digits do not fit a line number, whose endpoints cannot be
// checked against a section range. The remaining kinds are a member outside the
// section it names, a range whose endpoints straddle a section boundary, a
// section number no heading declares, and a path form naming a file that does
// not resolve under spec/.
//
// This is migration tooling rather than a platform behavior, so it carries no
// spec citation of its own.
package citation

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Member is one line number or one range of line numbers, together with the
// short gloss written against it. End equals Start for a single line number.
type Member struct {
	Start int
	End   int
	Gloss string
	// OutOfRange reports that an endpoint's digits do not fit a line number,
	// in which case that endpoint holds outOfRangeLine. The member is kept
	// rather than dropped, so the member list is consumed whole and the
	// citation is still returned, resolved, and counted; the resolver reports
	// it for hand correction.
	OutOfRange bool
	// Text is the member as written, which is the line number or the range
	// alone. The gloss is held beside it, so a failure message names the
	// numbers that did not resolve rather than the prose written against them.
	Text string
}

// Citation is one occurrence of the retired form, with every member consumed.
//
// Exactly one of Section and File is set: Section for the section-number
// spelling, File for the path spelling, normalized to a spec-relative path with
// the spec/ prefix present.
type Citation struct {
	Section   string
	File      string
	Qualifier string
	Members   []Member

	// Text is the citation with any continuation join applied, so a citation
	// wrapped across two comment lines reads as one line of text. It is what a
	// register keyed by citation text carries.
	Text string
	// Raw is the citation exactly as it appears in the source, comment marker
	// and newline included when it was wrapped.
	Raw string
	// Offset is the byte offset of Raw in the source, and Line is the 1-based
	// source line the citation starts on.
	Offset int
	Line   int

	// Unbalanced reports that the head opened a parenthesis of the carrier's
	// own that no parenthesis closes inside the citation's bounds, so Text
	// carries an unpaired opening delimiter. The occurrence is returned, so it
	// is resolved and counted, and the resolver reports it for hand correction
	// rather than letting a pass convert the span to a single anchor and strand
	// the carrier's closing parenthesis behind it.
	Unbalanced bool
}

// Ref returns the reference as written, which is the section number with its
// section sign or the normalized spec-relative path.
func (c Citation) Ref() string {
	if c.Section != "" {
		return "§" + c.Section
	}
	return c.File
}

// String renders the citation for a gate's failure message.
func (c Citation) String() string { return c.Text }

// Find returns every citation of the retired form in content, in source order.
// The continuation join is applied before the form, so a wrapped citation is
// returned once with its members from both lines.
func Find(content string) []Citation {
	joined, offsets := join(content)
	starts := lineStarts(content)
	var out []Citation
	for pos := 0; pos < len(joined); {
		loc := headExpr.FindStringSubmatchIndex(joined[pos:])
		if loc == nil {
			break
		}
		shift(loc, pos)
		if !boundedLeft(joined, loc[0]) {
			pos = loc[0] + 1
			continue
		}
		c, end, ok := parseAt(joined, loc)
		if !ok {
			pos = loc[1]
			continue
		}
		c.Offset = offsets[loc[0]]
		c.Raw = content[c.Offset:offsets[end]]
		c.Line = lineOf(starts, c.Offset)
		out = append(out, c)
		pos = end
	}
	return out
}

// FindIn returns every citation in the named file's content, and is the form
// the passes and the gates call so a failure names its carrier.
func FindIn(path, content string) []Located {
	found := Find(content)
	out := make([]Located, 0, len(found))
	for _, c := range found {
		out = append(out, Located{Path: path, Citation: c})
	}
	return out
}

// Located is a citation together with the file it was read from.
type Located struct {
	Path string
	Citation
}

// String renders the located citation as a carrier reference a gate can print.
func (l Located) String() string {
	return fmt.Sprintf("%s line %d: %s", l.Path, l.Line, l.Text)
}

// parseAt builds the citation whose head the match at loc covers, consuming
// every continuation member that follows it. It returns the end offset of the
// whole citation in the joined text, and reports false when the text at the
// head is not a citation of the form. A head that opened a parenthesis of the
// carrier's own with none to close with yields a citation ending at its last
// member, marked unbalanced so the resolver reports it.
func parseAt(joined string, loc []int) (Citation, int, bool) {
	var c Citation
	c.Section = group(joined, loc, headSection)
	if path := group(joined, loc, headFile); path != "" {
		c.File = normalizePath(path)
	}
	c.Qualifier = strings.Join(strings.Fields(render(group(joined, loc, headQualifier))), " ")

	first, ok := parseMember(render(group(joined, loc, headMember)))
	if !ok {
		return Citation{}, 0, false
	}
	c.Members = []Member{first}
	end, unbalanced := consumeCitation(joined, loc[1], &c.Members, group(joined, loc, headParen) != "")
	c.Unbalanced = unbalanced
	c.Text = render(joined[loc[0]:end])
	return c, end, true
}

// consumeCitation extends the citation from the end of its head over every
// continuation member, closes the parenthesis a head that opened one closes
// with, and keeps consuming past that parenthesis. It reports whether the
// citation's span carries a parenthesis its head opened and nothing closed.
//
// A head that opened a parenthesis with none to close with inside the
// citation's bounds ends at its last member and is reported as unbalanced. The
// occurrence is still returned, so the resolver resolves it and the ratchet
// counts it; what the report withholds is the conversion, because replacing a
// span carrying an unpaired `(` with a single anchor strands the carrier's `)`
// and the prose between them.
//
// A member list may resume after the head's closing parenthesis, which happens
// when a qualified citation parenthesizes its first range and then writes a
// further member behind the closing parenthesis. The fixture
// `testdata/citations/members-after-close-paren.txt` carries that spelling.
// Stopping at the parenthesis would leave those members in place, where the resolver does not
// read them and the ratchet does not count them, and the rewritten carrier
// would read as an anchor followed by orphan integers while its file reached a
// zero count with a stale pointer surviving.
func consumeCitation(joined string, pos int, members *[]Member, opened bool) (int, bool) {
	limit := nextHead(joined, pos)
	end, glossStart := consumeMembers(joined, pos, limit, members)
	if !opened {
		return end, false
	}
	closed, ok := closeHeadParen(joined, glossStart, end, limit, members)
	if !ok {
		return end, true
	}
	// The citation continues past the parenthesis only when a member list
	// resumes behind it. Prose written after the parenthesis is outside the
	// citation, the same way prose written after any last member is.
	beyond := nextHead(joined, closed)
	if _, _, ok := nextMember(joined, closed, beyond); !ok {
		return closed, false
	}
	end, _ = consumeMembers(joined, closed, beyond, members)
	return end, false
}

// closeHeadParen extends the citation from its last member to the parenthesis
// the head opened, and gives the text between the two to that member as its
// gloss. It returns the offset behind the closing parenthesis.
//
// The parenthesis a head opened belongs to the citation, so everything written
// inside it does too. Closing only on a parenthesis standing immediately behind
// the last member leaves every other spelling of the parenthesized form ending
// inside the parenthetical, so the citation text carries an unpaired `(` and
// the carrier keeps the orphaned `)` with the prose between them. Converting
// that span to a single anchor is the residue the whole-citation rule forbids,
// and where the parenthetical crosses a continuation join it also deletes the
// newline and the following line's comment marker.
//
// The search is bounded by the head of the next citation and by a newline the
// join did not consume. A parenthesis reachable only past the next head belongs
// to a later citation, and consuming up to it would swallow the citation
// written in between, which is never returned and so is neither resolved nor
// counted.
//
// A continuation the join did consume is inside the bound, so a parenthetical
// that opens on a member's line and closes on the following comment line is
// read whole. That is the same wrap inside a member list the form already
// carries: the parenthesis is one member's gloss either way, and refusing to
// cross the wrap ends the citation inside the parenthetical, where the members
// written behind the closing parenthesis go unread by the resolver and
// uncounted by the ratchet.
//
// When no parenthesis closes it inside those bounds the caller ends the
// citation at its last member and marks it unbalanced, so the occurrence is
// resolved and counted while the resolver reports it for hand correction.
func closeHeadParen(joined string, glossStart, pos, limit int, members *[]Member) (int, bool) {
	depth := 1
	for i := pos; i < limit; i++ {
		switch joined[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth > 0 {
				continue
			}
			if gloss := strings.TrimSpace(render(joined[glossStart:i])); gloss != "" {
				(*members)[len(*members)-1].Gloss = gloss
			}
			return i + 1, true
		case '\n':
			return 0, false
		}
	}
	return 0, false
}

// nextHead returns the offset of the next citation head at or after from, or
// the length of the text when there is none. It bounds the gloss, so no gloss
// can swallow the citation that follows it. A swallowed citation is never
// returned, so the resolver does not resolve it, the ratchet does not count it,
// and the pass rewrites it away with the span that swallowed it.
func nextHead(joined string, from int) int {
	if from >= len(joined) {
		return len(joined)
	}
	loc := headExpr.FindStringIndex(joined[from:])
	if loc == nil {
		return len(joined)
	}
	return from + loc[0]
}

// consumeMembers extends the member list from pos over every gloss and every
// continuation member that follows, and returns the end of the last one. No
// member and no gloss runs past limit, which is the head of the next citation.
//
// A separator is tried before a gloss at each step, so the word `and` is read
// as the separator it is rather than as the first word of a bare gloss. A
// separator that is not followed by a member is left unconsumed, so a citation
// followed by ordinary prose ends at its last member. A gloss that begins with
// that same separator word is refused for the same reason: absorbing it would
// put a dangling conjunction in the text a register is keyed by and that the
// pass replaces, which deletes the sentence's conjunction.
//
// A member's gloss is a run of segments rather than a single one, because a
// spelling such as `line 408 step (e) (replay workspace checkpoint) + line 409`
// writes a bare word and a parenthesized phrase against the same member. A gloss
// that ended the citation at its first segment would drop every member after
// it, which is the head-matching failure the whole-citation rule exists to
// prevent: the resolver would not read the dropped members, the ratchet would
// not count them, and the rewritten carrier would read as an anchor followed by
// orphan integers.
// It returns the end of the member list together with the offset the last
// member's gloss opens at, which is the offset behind that member's own text.
// A parenthesis the head opened is closed from there, so the whole of the
// parenthetical behind the last member is that member's gloss rather than
// prose the citation left behind.
func consumeMembers(joined string, pos, limit int, members *[]Member) (int, int) {
	end := pos
	glossStart := pos
	glossed := false
	for {
		if next, m, ok := nextMember(joined, end, limit); ok {
			*members = append(*members, m)
			end = next
			glossStart = next
			glossed = false
			continue
		}
		if glossed {
			return end, glossStart
		}
		next, gloss, ok := glossRun(joined, end, limit)
		if !ok {
			return end, glossStart
		}
		last := &(*members)[len(*members)-1]
		last.Gloss = gloss
		end = next
		glossed = true
	}
}

// maxGlossSegments bounds the run of gloss segments one member carries. The
// gloss the form admits is short, so the run is bounded, and a segment past the
// first is kept only when a further member follows the run. Prose written after
// a glossed citation therefore stays outside the citation, while a gloss
// written ahead of a continuation member does not terminate its member.
const maxGlossSegments = 3

// glossRun consumes one member's gloss and returns the end of it together with
// the gloss text. No segment runs past limit.
func glossRun(joined string, pos, limit int) (int, string, bool) {
	text := joined[:limit]
	first := glossSegment(text, pos)
	if first == "" {
		return pos, "", false
	}
	end := pos + len(first)
	at := end
	for i := 1; i < maxGlossSegments; i++ {
		more := glossSegment(text, at)
		if more == "" {
			break
		}
		at += len(more)
		if _, _, ok := nextMember(joined, at, limit); ok {
			end = at
		}
	}
	return end, strings.TrimSpace(render(joined[pos:end])), true
}

// glossSegment returns one gloss segment at pos, empty when there is none. A
// segment opening with the separator word is refused, so a separator with no
// member behind it stays outside the citation.
func glossSegment(text string, pos int) string {
	if pos > len(text) {
		return ""
	}
	segment := trimTrailingSeparatorWord(glossExpr.FindString(text[pos:]))
	if opensWithSeparatorWord(segment) {
		return ""
	}
	return segment
}

// trimTrailingSeparatorWord drops a separator word standing at the end of a
// bare gloss, which is the separator of the citation that follows rather than a
// word of this one. A quoted or parenthesized gloss closes on its own delimiter
// and is left alone.
func trimTrailingSeparatorWord(segment string) string {
	trimmed := strings.TrimRight(segment, spaceBytes)
	rest, ok := strings.CutSuffix(trimmed, separatorWord)
	if !ok || (rest != "" && isWordByte(rest[len(rest)-1])) {
		return segment
	}
	return strings.TrimRight(rest, spaceBytes)
}

// separatorWord is the one member separator spelled as a word.
const separatorWord = "and"

// opensWithSeparatorWord reports whether a gloss segment begins with the
// separator word standing on its own.
func opensWithSeparatorWord(segment string) bool {
	rest, ok := strings.CutPrefix(strings.TrimLeft(segment, spaceBytes), separatorWord)
	return ok && (rest == "" || !isWordByte(rest[0]))
}

// nextMember consumes one separator, an optional repeat of the keyword, and one
// member. It reports false without consuming anything when the text at pos is
// not a continuation member.
//
// No member starts at or past limit, which is the head of the next citation.
// The bare-prefix path spelling opens on two digits, which a member expression
// matches as happily as a line number, so a citation written behind a member
// separator would otherwise be read as this citation's next member: the second
// citation is then never returned, so the resolver does not resolve it, the
// ratchet does not count it, and the pass rewrites its opening digits away with
// the span that swallowed them, leaving the rest of its path in the carrier.
func nextMember(joined string, pos, limit int) (int, Member, bool) {
	if pos >= limit {
		return pos, Member{}, false
	}
	sep := separatorExpr.FindString(joined[pos:])
	if sep == "" {
		return pos, Member{}, false
	}
	at := pos + len(sep)
	at += len(keywordExpr.FindString(joined[at:]))
	if at >= limit {
		return pos, Member{}, false
	}
	text := memberExpr.FindString(joined[at:limit])
	if text == "" {
		return pos, Member{}, false
	}
	m, ok := parseMember(render(text))
	if !ok {
		return pos, Member{}, false
	}
	return at + len(text), m, true
}

// parseMember reads a single line number or a range into its endpoints.
//
// An endpoint whose digits do not fit a line number yields a member marked out
// of range rather than no member at all. Dropping it would end the member list
// at that point when it is a continuation member, and discard the whole
// occurrence when it is the head's own member. Either way the members behind it
// go unread by the resolver and uncounted by the ratchet, and a file whose only
// citation carries such a member reaches a per-file count of zero while the
// pointer stands. The resolver reports the member for hand correction instead.
func parseMember(text string) (Member, bool) {
	m := Member{Text: text}
	lo, hi, ranged := splitRange(text)
	start, startFits, ok := parseLineNumber(lo)
	if !ok {
		return Member{}, false
	}
	m.Start, m.End = start, start
	m.OutOfRange = !startFits
	if ranged {
		endLine, endFits, ok := parseLineNumber(hi)
		if !ok {
			return Member{}, false
		}
		m.End = endLine
		m.OutOfRange = m.OutOfRange || !endFits
	}
	return m, true
}

// outOfRangeLine is the endpoint an out-of-range member carries. It is outside
// every section's range, so a caller that checks membership without reading
// OutOfRange still reports the member rather than resolving it.
const outOfRangeLine = -1

// parseLineNumber reads one endpoint of a member. fits is false when the digits
// do not fit a line number, in which case the endpoint is outOfRangeLine. ok is
// false only for text the member expression cannot produce, which is text that
// is not a run of digits.
func parseLineNumber(text string) (value int, fits, ok bool) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	switch {
	case err == nil:
		return n, true, true
	case errors.Is(err, strconv.ErrRange):
		return outOfRangeLine, false, true
	default:
		return 0, false, false
	}
}

// splitRange splits a member on the range separator, which is an ASCII hyphen,
// an en dash, or an em dash.
func splitRange(text string) (lo, hi string, ranged bool) {
	if i := strings.IndexAny(text, rangeSeparators); i >= 0 {
		_, width := decodeSeparator(text[i:])
		return text[:i], text[i+width:], true
	}
	return text, "", false
}

// decodeSeparator returns the range separator at the head of s and its width in
// bytes, so a multibyte dash is skipped whole.
func decodeSeparator(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

// normalizePath returns the path spelling with the spec/ prefix present, so a
// citation written with the prefix and one written without it resolve against
// the same file.
func normalizePath(path string) string {
	if strings.HasPrefix(path, specPrefix) {
		return path
	}
	return specPrefix + path
}

// group returns the text of the named submatch of the head expression, empty
// when the group did not participate in the match.
func group(s string, loc []int, name string) string {
	n := headExpr.SubexpIndex(name)
	if n < 0 || 2*n+1 >= len(loc) || loc[2*n] < 0 {
		return ""
	}
	return s[loc[2*n]:loc[2*n+1]]
}

// shift moves every offset in a submatch index by base.
func shift(loc []int, base int) {
	for i := range loc {
		if loc[i] >= 0 {
			loc[i] += base
		}
	}
}

// boundedLeft reports whether the byte before the match is a boundary rather
// than a word byte. A file-name spelling whose two leading digits were taken
// out of a longer run of digits is not a citation.
func boundedLeft(s string, start int) bool {
	if start == 0 {
		return true
	}
	return !isWordByte(s[start-1])
}

// isWordByte reports whether a byte is a word byte, which is what a boundary in
// this grammar is defined against.
func isWordByte(b byte) bool {
	switch {
	case b >= '0' && b <= '9', b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b == '_':
		return true
	}
	return false
}

// lineStarts returns the byte offset of the start of every line in content.
func lineStarts(content string) []int {
	starts := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineOf returns the 1-based line the byte offset sits on.
func lineOf(starts []int, offset int) int {
	return sort.Search(len(starts), func(i int) bool { return starts[i] > offset })
}
