// SPDX-License-Identifier: MIT

package name

import (
	"regexp"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
)

// BroadPhrase is one occurrence of the deliberately broad reserved-phrase
// predicate: one of the two reserved words standing next to the head noun
// the naming law bans it in front of, under any separator, in any carrier,
// at any position of that carrier.
//
// The predicate is what the residual scan of the reserved-phrase class
// ranges over. It is wider than the population Sites reads on purpose. It
// commits to no separator beyond a short run of bytes that are not word
// bytes, to no carrier, and to no in-file position, so an occurrence the
// enumerated matcher's own domain and position rules leave outside the pass
// is still selected and triaged rather than passing unread. A precise
// predicate is another enumeration wearing a regular expression, and it
// misses the same tail the enumeration does.
//
// An occurrence Sites reads is selected by this predicate as well. The
// residual is the difference: a caller subtracts the sites Sites returns and
// triages what is left.
type BroadPhrase struct {
	// Text is the occurrence with any continuation join rendered as a
	// space, so an occurrence wrapped across two comment lines reads as one
	// line of text and carries the spelling Site.Text carries for the same
	// occurrence.
	Text string
	// Offset is the byte offset of the occurrence in the source, End is the
	// offset just past it, and Line is the 1-based source line it opens on.
	Offset int
	End    int
	Line   int
}

// String renders the occurrence for a gate's failure message.
func (b BroadPhrase) String() string { return b.Text }

// broadSeparator is the run the predicate tolerates between the reserved
// word and the head noun. It is a run of authored whitespace and consumed
// continuations of any length, or any short run of bytes that are not word
// bytes.
//
// The first branch is what makes the predicate contain the enumerated
// matcher rather than merely differ from it: that matcher admits a
// whitespace run of any length, so a bound on every branch would leave an
// occurrence it reads unselected here. The second is the over-breadth,
// which is every punctuation run neither matcher anticipated.
//
// A word byte is outside both branches, so the two words written as a
// single token are outside this predicate. That spelling is a retired
// channel identifier rather than a bare noun phrase, and the identifier
// class owns it.
var broadSeparator = `(?:[ \t` + joinClass + `]+|[^A-Za-z0-9_]{1,3})`

// broadExpr matches one occurrence of the predicate over joined text. The
// two reserved words and the head noun stand here as a regular-expression
// literal for the reason reservedExpr states.
var broadExpr = regexp.MustCompile(`(?i)\b(?:lifecycle|control)` + broadSeparator + `channels?\b`)

// FindBroadPhrases returns every occurrence of the predicate in content, in
// source order and without overlap.
//
// The scan runs over the joined text, so an occurrence wrapped across two
// comment lines is read whole, and it maps every span back to the source the
// way findSites does. It applies no carrier rule, no in-file position rule,
// and no markdown-anchor-identifier exclusion. Each of those is a statement
// the naming law makes about where its prohibition reaches rather than a
// property of the words, so an occurrence outside them is a member of this
// predicate whose register entry records why the pass leaves it standing.
func FindBroadPhrases(content string) []BroadPhrase {
	joined, offsets := citation.Join(content)
	var out []BroadPhrase
	for _, m := range broadExpr.FindAllStringIndex(joined, -1) {
		offset := offsets[m[0]]
		out = append(out, BroadPhrase{
			Text:   render(joined[m[0]:m[1]]),
			Offset: offset,
			End:    offsets[m[1]],
			Line:   citation.LineOf(content, offset),
		})
	}
	return out
}
