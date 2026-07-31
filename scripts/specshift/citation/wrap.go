// SPDX-License-Identifier: MIT

package citation

// JoinByte is the byte Join leaves in place of each continuation it consumed.
// A caller matching a form over joined text admits it wherever the form admits
// authored whitespace, so a phrase wrapped across two comment lines matches the
// same way an unwrapped one does.
const JoinByte = joinByte

// Dialect names the comment syntax of a carrier, which is what decides whether
// a line break the carrier holds can be a continuation at all.
type Dialect int

const (
	// LineComment is a carrier whose wrapped prose continues behind a comment
	// marker on the following line, which is Go, protobuf, and the dialects
	// commented with a number sign.
	LineComment Dialect = iota
	// NoComment is a carrier that has no comment syntax, which is markdown and
	// the schema documents. A number sign, an asterisk, or a pair of hyphens
	// opening a line of a markdown document is a heading, a list item, or an
	// emphasis run, and each of those may interrupt a paragraph legally. Read
	// as a continuation marker it would fuse the paragraph line before it with
	// the line the marker opens, so a matcher would take one site across the
	// two and a pass rewriting that span would delete the heading or the list
	// marker between them.
	NoComment
)

// Join applies the continuation join a carrier's dialect states and returns the
// joined text together with the source offset of each of its bytes. The offset
// slice carries one further entry holding the length of the source, so the end
// of a match that runs to the end of the text maps back.
//
// It is exported because the name pass applies the same join before its own
// matcher, per the naming law: a reserved phrase wraps across two consecutive
// comment lines the same way a citation does, and a line-oriented matcher reads
// neither line as a site, so the wrapped occurrence is written by no pass and
// read by no gate. One implementation keeps the two matchers reading one
// population.
func Join(dialect Dialect, content string) (string, []int) {
	if dialect == NoComment {
		return joinWith(nil, content)
	}
	return join(content)
}

// LineOf returns the 1-based source line the byte offset sits on. It is
// exported for the same reason Join is: a pass reporting a site for hand
// correction names the line the same way a citation gate does.
func LineOf(content string, offset int) int {
	return lineOf(lineStarts(content), offset)
}
