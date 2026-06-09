// SPDX-License-Identifier: MIT

// Package workspacepack builds the tar.gz archive the §26.2 line 114 CLI
// uploads when a `lenny session new --workspace <dir>` invocation stages a
// local repository into the session's workspace. It walks the directory
// honoring a `.lennyignore` file (falling back to `.gitignore`) and emits
// a gzip-compressed tar the gateway extracts under an `uploadArchive`
// workspace-plan source.
//
// spec: §26.2 lines 95-114; §14 uploadArchive; §7.4 upload safety.
package workspacepack

import (
	"regexp"
	"strings"
)

// ignoreMatcher evaluates a workspace path against an ordered set of
// .gitignore-style patterns. It implements the commonly used subset of
// the gitignore format: comments (`#`), blank lines, negation (`!`),
// anchored patterns (leading `/`), directory-only patterns (trailing
// `/`), the `*`, `?`, and `[...]` wildcards, and `**` for matching across
// directory boundaries. Nested ignore files below the root are not read;
// the matcher uses the single ignore file at the workspace root.
//
// spec: §26.2 line 114.
type ignoreMatcher struct {
	rules []ignoreRule
}

// ignoreRule is one compiled ignore pattern.
type ignoreRule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// newIgnoreMatcher compiles the content of an ignore file into a matcher.
// Lines are processed top-to-bottom so the last matching rule wins, which
// is how gitignore resolves a pattern and a later negation of it.
func newIgnoreMatcher(content string) *ignoreMatcher {
	m := &ignoreMatcher{}
	for _, raw := range strings.Split(content, "\n") {
		rule, ok := compileIgnoreLine(raw)
		if !ok {
			continue
		}
		m.rules = append(m.rules, rule)
	}
	return m
}

// Match reports whether relPath (slash-separated, workspace-relative) is
// ignored. isDir selects directory-only patterns. The result is the value
// of the last rule that matches; an unmatched path is not ignored.
func (m *ignoreMatcher) Match(relPath string, isDir bool) bool {
	ignored := false
	for _, r := range m.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.re.MatchString(relPath) {
			ignored = !r.negate
		}
	}
	return ignored
}

// compileIgnoreLine turns one ignore-file line into a rule. It returns
// ok=false for blank lines and comments.
func compileIgnoreLine(raw string) (ignoreRule, bool) {
	line := strings.TrimRight(raw, "\r")
	// Trailing unescaped whitespace is not significant in gitignore.
	line = strings.TrimRight(line, " \t")
	if line == "" {
		return ignoreRule{}, false
	}
	if strings.HasPrefix(line, "#") {
		return ignoreRule{}, false
	}
	// An escaped leading '#' or '!' is a literal first character.
	if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
		line = line[1:]
	}

	var rule ignoreRule
	if strings.HasPrefix(line, "!") {
		rule.negate = true
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		rule.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	if line == "" {
		return ignoreRule{}, false
	}

	// A separator anywhere except a trailing one anchors the pattern to
	// the workspace root; a name with no internal separator matches at any
	// depth.
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return ignoreRule{}, false
	}

	rule.re = regexp.MustCompile(patternToRegex(line, anchored))
	return rule, true
}

// patternToRegex translates a gitignore glob into an anchored regular
// expression matching a slash-separated relative path. A non-anchored
// pattern is allowed to match at any directory depth.
func patternToRegex(p string, anchored bool) string {
	var b strings.Builder
	b.WriteString("^")
	if !anchored {
		b.WriteString("(?:.*/)?")
	}
	n := len(p)
	for i := 0; i < n; i++ {
		c := p[i]
		switch c {
		case '*':
			if i+1 < n && p[i+1] == '*' {
				i++ // consume the second '*'
				atStart := i == 1
				prevSlash := i >= 2 && p[i-2] == '/'
				nextSlash := i+1 < n && p[i+1] == '/'
				atEnd := i+1 >= n
				if (atStart || prevSlash) && nextSlash {
					// "**/" — match zero or more leading directories.
					b.WriteString("(?:[^/]+/)*")
					i++ // consume the trailing '/'
				} else if (atStart || prevSlash) && atEnd {
					// trailing "**" — match everything beneath.
					b.WriteString(".*")
				} else {
					// "**" not isolated as a segment degrades to "*".
					b.WriteString("[^/]*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			class, consumed := translateClass(p[i:])
			b.WriteString(class)
			i += consumed - 1
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	return b.String()
}

// translateClass copies a bracket character class beginning at s[0]=='['
// into a regexp class, mapping a leading gitignore negation '!' to '^'. It
// returns the regex fragment and the number of input bytes consumed. A
// class with no closing ']' is treated as a literal '['.
func translateClass(s string) (string, int) {
	// s[0] == '['
	end := -1
	for i := 1; i < len(s); i++ {
		if s[i] == ']' && i > 1 {
			end = i
			break
		}
	}
	if end == -1 {
		return `\[`, 1
	}
	body := s[1:end]
	if strings.HasPrefix(body, "!") {
		body = "^" + body[1:]
	}
	return "[" + body + "]", end + 1
}
