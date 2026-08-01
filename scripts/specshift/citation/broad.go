// SPDX-License-Identifier: MIT

package citation

import (
	"regexp"
	"strconv"
	"strings"
)

// Broad is one occurrence of the deliberately broad citation predicate: a
// section sigil or a section path standing adjacent to a line-number token,
// together with the run of numbers written behind that token.
//
// The predicate is what the residual scan of the two line-citation classes
// ranges over. It is wider than the form Find reads on purpose. It commits to
// no member separator, to no range spelling, and to no comment dialect beyond
// the continuation join, so a spelling the form misses is still selected and
// reported for triage rather than passing unread. A precise predicate is
// another enumeration wearing a regular expression, and it misses the same tail
// the enumeration does.
//
// An occurrence the form reads is selected by this predicate as well. The
// residual is the difference: a caller subtracts the occurrences Find returns
// by source span and triages what is left.
type Broad struct {
	// Text is the occurrence with any continuation join applied, so a
	// citation wrapped across two comment lines reads as one line of text. It
	// is what a residual register keyed by member text carries.
	Text string
	// Lines is the occurrence split at each continuation the join consumed,
	// in source order, so an occurrence written on one line is one element.
	// A caller that records an occurrence in a tracked file writes the
	// elements on separate lines and joins them back with a space to recover
	// Text. Recording a wrapped occurrence on one line instead would present
	// the citation form with a spelling the wrap kept it from reading, under
	// the path of the file the record was written into.
	Lines []string
	// Raw is the occurrence exactly as it appears in the source.
	Raw string
	// Offset is the byte offset of Raw in the source, End is the offset just
	// past it, and Line is the 1-based source line the occurrence starts on.
	Offset int
	End    int
	Line   int
}

// String renders the occurrence for a gate's failure message.
func (b Broad) String() string { return b.Text }

// broadReference is either half of the reference the predicate accepts: the
// section sign with a dotted section number behind it, or a specification file
// name with the spec/ prefix optional. It is the reference half of the form's
// own head expression with the whitespace class widened, because the predicate
// commits to no spelling of the space between the sigil and the number.
const broadReference = `(?:§` + sp + `*\d+(?:\.\d+)*|(?:spec/)?\d{2}_[A-Za-z0-9][A-Za-z0-9._-]*\.md)`

// broadAdjacency is the run the predicate tolerates between the reference and
// the line-number token. It admits the byte a consumed continuation left
// behind, so a citation wrapped across two comment lines counts as adjacent,
// and it stops at a newline the join did not consume, so an occurrence never
// runs past the comment that carries it.
//
// The bound is short enough that a reference and an unrelated number several
// clauses apart are not read as one occurrence, and long enough to carry the
// sub-element qualifier the form admits between the reference and the keyword.
const broadAdjacency = `[^\n]{0,32}?`

// broadKeyword is the line-number token: the keyword in either number, upper
// or lower case, or the colon that stands in for it. Punctuation between the
// keyword and the first number is admitted rather than enumerated, which is
// what "no separator is committed to" means at the head of the member run.
const broadKeyword = `(?i:lines?)` + sp + `*[:#=]?` + sp + `*`

// broadMembers is the run of numbers behind the token. A member is a number or
// a range written with any dash, and members are separated by anything that is
// not a number, up to a short bound, so a separator this predicate has never
// seen still keeps the run together.
const (
	broadMember     = `\d+(?:` + sp + `*[-\x{2013}\x{2014}]` + sp + `*\d+)?`
	broadSeparator  = `(?:` + sp + `*(?:[,;/+&]|and\b|(?i:lines?))` + sp + `*)`
	broadMemberList = broadMember + `(?:` + broadSeparator + `+` + broadMember + `)*`
)

// broadExpr matches one occurrence of the predicate over joined text. The
// colon branch stands directly against the reference, as the form's own colon
// spelling does, and the keyword branch sits behind the adjacency run.
var broadExpr = regexp.MustCompile(
	broadReference + `(?:` + `:` + joinClass + `?` + `|` + broadAdjacency + broadKeyword + `)` + broadMemberList,
)

// FindBroad returns every occurrence of the broad predicate in content, in
// source order and without overlap.
//
// The scan runs over the joined text, so a wrapped occurrence is read whole and
// its text reads as one line, and it maps every span back to the source the way
// Find does. A match whose reference was taken out of a longer word is
// discarded, because a file-name spelling cut out of a longer digit run names no
// section.
func FindBroad(content string) []Broad {
	joined, offsets := join(content)
	starts := lineStarts(content)
	var out []Broad
	for pos := 0; pos < len(joined); {
		loc := broadExpr.FindStringIndex(joined[pos:])
		if loc == nil {
			break
		}
		start, end := loc[0]+pos, loc[1]+pos
		if !boundedLeft(joined, start) {
			pos = start + 1
			continue
		}
		offset := offsets[start]
		b := Broad{
			Text:   render(joined[start:end]),
			Lines:  strings.Split(joined[start:end], string(JoinByte)),
			Raw:    content[offset:offsets[end]],
			Offset: offset,
			End:    offsets[end],
			Line:   lineOf(starts, offset),
		}
		out = append(out, b)
		pos = end
	}
	return out
}

// LocatedBroad is a broad occurrence together with the file it was read from.
type LocatedBroad struct {
	Path string
	Broad
}

// String renders the occurrence as a carrier reference a gate can print.
func (l LocatedBroad) String() string {
	return l.Path + ":" + strconv.Itoa(l.Line) + ": " + l.Text
}

// FindBroadIn returns every occurrence of the broad predicate in one file.
func FindBroadIn(path, content string) []LocatedBroad {
	found := FindBroad(content)
	out := make([]LocatedBroad, 0, len(found))
	for _, b := range found {
		out = append(out, LocatedBroad{Path: path, Broad: b})
	}
	return out
}
