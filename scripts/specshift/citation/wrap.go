// SPDX-License-Identifier: MIT

package citation

// JoinByte is the byte Join leaves in place of each continuation it consumed.
// A caller matching a form over joined text admits it wherever the form admits
// authored whitespace, so a phrase wrapped across two comment lines matches the
// same way an unwrapped one does.
const JoinByte = joinByte

// Join applies the comment-marker continuation join and returns the joined text
// together with the source offset of each of its bytes. The offset slice
// carries one further entry holding the length of the source, so the end of a
// match that runs to the end of the text maps back.
//
// There is one join and every carrier is read under it. It is exported because
// the name pass applies the same join before its own matcher, per the naming
// law: a reserved phrase wraps across two consecutive comment lines the same
// way a citation does, and a line-oriented matcher reads neither line as a
// site, so the wrapped occurrence is written by no pass and read by no gate.
// One implementation keeps the two matchers reading one population. A matcher
// that also has to decide whether the two joined lines belong to one paragraph
// answers that after the join, over the joined text, rather than by reading a
// carrier under a join of its own.
func Join(content string) (string, []int) { return join(content) }

// LineOf returns the 1-based source line the byte offset sits on. It is
// exported for the same reason Join is: a pass reporting a site for hand
// correction names the line the same way a citation gate does.
func LineOf(content string, offset int) int {
	return lineOf(lineStarts(content), offset)
}
