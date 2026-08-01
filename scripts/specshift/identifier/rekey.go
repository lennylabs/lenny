// SPDX-License-Identifier: MIT

package identifier

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// rekey moves the keys one path-keyed register carries for the files the
// run moves, and the symbol references it carries for the tokens the run
// renames.
//
// A key is a whole scalar: the path a citation baseline is keyed by, the
// glob key of the change graph, the spec file of a spec-map section, and
// each member of the arrays that section carries, the `schemas` array
// among them, which no check existence-checks and which would therefore
// go stale silently. A scalar that merely mentions the path inside a
// wider value is not a key, and is left for the stale-key check to
// report rather than edited by a substring rule that would rewrite prose.
//
// A symbol reference is written `<path>::<symbol>`, keyed by symbol
// rather than by path, so it is moved by the token rename the
// substitution produced rather than by the file move.
func rekey(target string, content []byte, moves, symbols map[string]string) []byte {
	text := string(content)
	// The symbol references are rewritten first, because each carries
	// the declaring file's old path and the path rule would otherwise
	// move that half out from under them.
	for _, old := range sortedKeys(symbols) {
		text = referenceExpr(old).ReplaceAllString(text, quoteReplacement(symbols[old]))
	}
	for _, old := range sortedKeys(moves) {
		for _, expr := range scalarExprs(target, old) {
			text = expr.ReplaceAllString(text, "${1}"+quoteReplacement(moves[old])+"${2}")
		}
	}
	return []byte(text)
}

// scalarExprs returns the expressions matching one path written as a
// whole scalar in a register of the carrier's format, each with the
// delimiters captured either side so the replacement keeps them.
//
// The JSON form is the quoted string, in a key position and in an array
// member alike, together with the `<path>::` prefix of a symbol
// reference, whose symbol half is rewritten by the symbol rule. The YAML
// form is the value of a mapping key or of a sequence item, quoted or
// bare.
func scalarExprs(target, path string) []*regexp.Regexp {
	quoted := regexp.QuoteMeta(path)
	switch strings.ToLower(filepath.Ext(target)) {
	case ".json":
		return []*regexp.Regexp{
			regexp.MustCompile(`(")` + quoted + `(")`),
			regexp.MustCompile(`(")` + quoted + `(::)`),
		}
	case ".yaml", ".yml":
		return []*regexp.Regexp{
			regexp.MustCompile(`(?m)((?:^|[ \t])(?:-[ \t]+)?(?:[A-Za-z0-9_.-]+:[ \t]+)?['"]?)` + quoted + `(['"]?[ \t]*$)`),
		}
	}
	return nil
}

// referenceExpr matches one `<path>::<symbol>` reference, held to the
// whole symbol so a symbol that is a prefix of another is not rewritten
// in its place.
func referenceExpr(reference string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(reference) + `\b`)
}

// quoteReplacement escapes the expansion syntax of a replacement, so a
// path or a symbol carrying a dollar sign is written literally.
func quoteReplacement(s string) string { return strings.ReplaceAll(s, "$", "$$") }

// staleKeys returns every path and symbol the run retired that a
// register still names after the rekey.
//
// The check is what makes the rekey channel fail closed. A key the
// rewrite did not reach, because the register writes it inside a wider
// value such as a note or a citation text, leaves the register pointing
// at a file the run has moved: the line-citation ratchet then fires on
// the new path as a file it has no count for, and the tier-0 check that
// resolves a symbol reference against its declaring file fails on the
// retired token. The run stops with the tree unchanged and the operator
// corrects the value by hand, rather than completing and leaving the
// gates to report it afterwards.
func staleKeys(content []byte, moves, symbols map[string]string) []string {
	text := string(content)
	var stale []string
	for _, old := range sortedKeys(moves) {
		if strings.Contains(text, old) {
			stale = append(stale, fmt.Sprintf("%s, which moved to %s", old, moves[old]))
		}
	}
	for _, old := range sortedKeys(symbols) {
		if referenceExpr(old).MatchString(text) {
			stale = append(stale, fmt.Sprintf("%s, which was renamed to %s", old, symbols[old]))
		}
	}
	sort.Strings(stale)
	return stale
}
