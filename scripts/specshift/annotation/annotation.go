// SPDX-License-Identifier: MIT

// Package annotation reads the `// spec:` annotations in Go source and
// holds the two static-tier checks of the annotation rule in
// .claude/rules/spec-annotations.md: the lint over the lines a branch
// adds or changes, and the ratchet over each package's count of
// section-number citations.
//
// The rule moves code from section-number citations to identifier
// citations area by area. During that transition an annotation carries
// either form, so the readers here classify each cited item rather than
// refusing the older form.
package annotation

import (
	"regexp"
	"strings"
)

// annotationLabel is the label that opens an annotation, after the
// comment marker and any space.
const annotationLabel = "spec:"

// Payload returns the text after the annotation label when the comment
// text opens with it, and reports whether it does. The text is a comment
// with its `//` or `/*` marker already removed, or the body of one line
// of a block comment.
func Payload(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, annotationLabel) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, annotationLabel)), true
}

// CommentText strips the line-comment marker from one raw comment line,
// returning the text a reader sees. A block comment's markers are
// stripped as well, so a one-line block comment reads like a line
// comment.
func CommentText(raw string) string {
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, "//"):
		return strings.TrimPrefix(s, "//")
	case strings.HasPrefix(s, "/*"):
		s = strings.TrimPrefix(s, "/*")
		return strings.TrimSuffix(s, "*/")
	}
	return s
}

// Items splits an annotation payload at its top-level commas and
// semicolons. A comma inside parentheses belongs to a heading or a
// qualifier, such as `§4.7 (Pod claims, release)`, and does not split
// the item.
func Items(payload string) []string {
	var items []string
	depth := 0
	start := 0
	for i, r := range payload {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',', ';':
			if depth == 0 {
				items = appendItem(items, payload[start:i])
				start = i + 1
			}
		}
	}
	return appendItem(items, payload[start:])
}

// appendItem appends a trimmed, non-empty item.
func appendItem(items []string, item string) []string {
	if s := strings.TrimSpace(item); s != "" {
		items = append(items, s)
	}
	return items
}

// sectionItem matches an item that cites a numbered specification
// section, in the `§N.M` form or in the bare `N.M` form the earlier test
// convention used.
var sectionItem = regexp.MustCompile(`^(?:§\s*\d|\d+(?:\.\d+)*(?:\s|\(|$))`)

// IsSectionCitation reports whether an annotation item cites a numbered
// specification section rather than an identifier.
func IsSectionCitation(item string) bool {
	return sectionItem.MatchString(strings.TrimSpace(item))
}

// SectionCitations counts the section-number citations in an annotation
// payload: one per top-level item that cites a section, plus each further
// `§` that opens a citation at the top level of the same item, so
// `§4.7 / §5.1` counts two. A `§` inside a parenthesised heading is part
// of that heading and is not counted.
func SectionCitations(payload string) int {
	n := 0
	for _, item := range Items(payload) {
		if !IsSectionCitation(item) {
			continue
		}
		extra := topLevelSectionSigns(item)
		if strings.HasPrefix(item, "§") {
			extra--
		}
		n += 1 + max(extra, 0)
	}
	return n
}

// topLevelSectionSigns counts the `§` signs followed by a digit outside
// any parentheses.
func topLevelSectionSigns(item string) int {
	n := 0
	depth := 0
	rs := []rune(item)
	for i, r := range rs {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '§':
			if depth == 0 && nextIsDigit(rs, i+1) {
				n++
			}
		}
	}
	return n
}

// nextIsDigit reports whether the first non-space rune at or after i is
// a digit.
func nextIsDigit(rs []rune, i int) bool {
	for ; i < len(rs); i++ {
		if rs[i] == ' ' {
			continue
		}
		return rs[i] >= '0' && rs[i] <= '9'
	}
	return false
}

// IdentifierKind classifies an identifier-form citation.
type IdentifierKind string

const (
	// Requirement is `CODE-Rn`.
	Requirement IdentifierKind = "requirement"
	// Issue is `CODE-In`.
	Issue IdentifierKind = "issue"
	// Operation is `CODE-<operation name>`.
	Operation IdentifierKind = "operation"
	// DefinedValue is a definition block's anchor, `<kind>-<slug>`.
	DefinedValue IdentifierKind = "defined value"
	// PageCode is a bare page code, as a package's doc.go names it.
	PageCode IdentifierKind = "page code"
)

// The identifier grammar of the specification structure: a page code is
// upper-case letters and digits opening with a letter, with at least two
// letters (`K8S` is a code, `N8` is a naming-law rule), a requirement and
// an issue are the code with `R` or `I` and a number, and any other
// suffix is an operation in the interface's own spelling.
var (
	pageCode       = regexp.MustCompile(`^[A-Z][A-Z0-9]*[A-Z][A-Z0-9]*$`)
	requirementID  = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,11}-R[0-9]+$`)
	issueID        = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,11}-I[0-9]+$`)
	operationID    = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,11}-[A-Za-z][A-Za-z0-9]*$`)
	definedValueID = regexp.MustCompile(`^(?:setting|limit|metric|alert|error|admission-refusal|label|annotation|event|register|state|grant)-[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// channelClassPrefixes are the class prefixes of the communication
// channel identifiers (`LNK-`, `CH-`, and `REG-`). They are not page
// codes, so an identifier carrying one is a channel identifier rather
// than an operation, and the registry of page identifiers does not
// resolve it.
var channelClassPrefixes = []string{"LNK-", "CH-", "REG-"}

// Classify returns the kind of an identifier-form token and whether the
// token has one of the identifier forms at all.
func Classify(token string) (IdentifierKind, bool) {
	for _, p := range channelClassPrefixes {
		if strings.HasPrefix(token, p) {
			return "", false
		}
	}
	switch {
	case requirementID.MatchString(token):
		return Requirement, true
	case issueID.MatchString(token):
		return Issue, true
	case operationID.MatchString(token):
		return Operation, true
	case definedValueID.MatchString(token):
		return DefinedValue, true
	case len(token) <= 12 && pageCode.MatchString(token):
		return PageCode, true
	}
	return "", false
}

// Identifiers returns the identifier-form tokens an annotation payload
// cites, in order. A section-number item contributes none, because its
// parenthesised heading is prose. Each other item contributes its
// leading token when that token has an identifier form, the issue a
// `(partial: CODE-In)` qualifier names, and the issue a
// `removal pending CODE-In` item names.
func Identifiers(payload string) []string {
	var ids []string
	for _, item := range Items(payload) {
		if IsSectionCitation(item) {
			continue
		}
		if rest, ok := strings.CutPrefix(item, "removal pending "); ok {
			item = strings.TrimSpace(rest)
		}
		head, qualifier := splitQualifier(item)
		if _, ok := Classify(head); ok {
			ids = append(ids, head)
		}
		if rest, ok := strings.CutPrefix(qualifier, "partial:"); ok {
			tok := strings.TrimSpace(rest)
			if _, ok := Classify(tok); ok {
				ids = append(ids, tok)
			}
		}
	}
	return ids
}

// splitQualifier splits `HEAD (qualifier)` into its head token and the
// text inside the first pair of parentheses.
func splitQualifier(item string) (string, string) {
	open := strings.IndexByte(item, '(')
	if open < 0 {
		return strings.TrimSpace(item), ""
	}
	head := strings.TrimSpace(item[:open])
	rest := item[open+1:]
	if end := strings.IndexByte(rest, ')'); end >= 0 {
		rest = rest[:end]
	}
	return head, strings.TrimSpace(rest)
}
