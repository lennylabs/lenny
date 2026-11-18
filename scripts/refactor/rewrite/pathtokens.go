// SPDX-License-Identifier: MIT

package rewrite

import "strings"

// PathTokens rewrites the repository-relative package paths that a register
// or a list names as bare tokens, quoted or not, such as the YAML registers
// under tests/registers and the pending lists beside the test maps. A token
// is a maximal run of the characters a path is spelled with (letters,
// digits, '_', '-', '.', and '/'), so the rewrite is anchored on whatever
// bounds the run: a space, a quote, a colon, a comma, a bracket, or the
// start or end of a line. A run is rewritten when it equals a moved path or
// starts with the moved path and a slash, which covers a package key, a
// directory with a trailing slash, a recursive selector such as
// pkg/gateway/x/..., and a file under the package. A run that merely starts
// with the moved path's characters, such as a longer sibling, is left
// alone.
//
// Like ImportLiterals, the rewrite is one pass over the original content
// that matches the longest moved path, so a rewritten token is never
// examined again.
func PathTokens(content string, moves []Move) string {
	pairs := jsonOldNew(moves)
	if len(pairs) == 0 {
		return content
	}
	var b strings.Builder
	b.Grow(len(content))
	i := 0
	for i < len(content) {
		if !isPathTokenByte(content[i]) {
			b.WriteByte(content[i])
			i++
			continue
		}
		j := i
		for j < len(content) && isPathTokenByte(content[j]) {
			j++
		}
		b.WriteString(rewriteToken(content[i:j], pairs))
		i = j
	}
	return b.String()
}

// isPathTokenByte reports whether b can appear in a repository-relative
// path token.
func isPathTokenByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '_', b == '-', b == '.', b == '/':
		return true
	}
	return false
}
