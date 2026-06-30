// SPDX-License-Identifier: MIT

// Package rewrite holds the pure, boundary-anchored path-rewrite
// primitives that the pkg/gateway C3 regroup driver
// (scripts/refactor/main.go) applies per move. It exists as a separate
// package from the driver main so the rewrite logic is unit-testable in
// isolation (proposal 0020 §2, §4 C4): each function takes content in
// and returns content out, with no I/O.
//
// The governing constraint is that many moved gateway paths are strict
// prefixes of sibling paths (pkg/gateway/mcp of mcptools/mcpruntimes/
// mcpschemagen, interceptor of interceptorstore, delegation of
// delegationbudget, admin of admintoken). A bare substring replace would
// corrupt the longer sibling, so every rewrite here anchors on a path-token
// boundary and never matches inside a longer sibling.
//
// spec: §4.1 (gateway is one component, internally partitioned; the regroup
// preserves the subsystem seams).
package rewrite

import "strings"

// Move is one (old import path -> new import path) manifest entry. The
// paths are fully-qualified module import paths
// (github.com/lennylabs/lenny/pkg/gateway/<old> ->
// github.com/lennylabs/lenny/pkg/gateway/<group>/<pkg>).
type Move struct {
	Old string
	New string
}

// modulePrefix is the import-path prefix every manifest path carries. The
// runtime-read forms drop it and name the repo-relative path
// (pkg/gateway/<old>/...), so the rewrite derives the repo-relative old and
// new from the manifest paths by trimming this prefix.
const modulePrefix = "github.com/lennylabs/lenny/"

// RepoRel returns the repo-relative form of a fully-qualified import path
// (github.com/lennylabs/lenny/pkg/gateway/x -> pkg/gateway/x). A path that
// does not carry the module prefix is returned unchanged, which keeps the
// function total for the audit's reuse on arbitrary tokens.
func RepoRel(importPath string) string {
	return strings.TrimPrefix(importPath, modulePrefix)
}

// ImportLiterals rewrites every quote-delimited import literal that names the
// moved package OR a package nested under it:
//
//	"<old>"      -> "<new>"      the moved leaf package itself, and
//	"<old>/sub"  -> "<new>/sub"  a package nested in the moved directory
//	                             (for example "<old>/pgstore", which git mv
//	                             relocates with the directory tree but whose
//	                             importers still name the old path).
//
// Both forms are anchored on the opening quote and either the closing quote or
// a trailing slash, so a moved path that is a strict prefix of a sibling import
// (for example pkg/gateway/mcp vs pkg/gateway/mcptools) never matches inside the
// longer sibling literal: the boundary after the short path is a '/' or '"', and
// the longer sibling has a path-segment letter there instead. Package names are
// unchanged, so no identifier rewrite accompanies this.
//
// The nested-package rewrite is necessary because the manifest lists only the
// moved leaf package's import path, while git mv moves the whole subtree; without
// it, an importer of "<old>/pgstore" would dangle after the move (proposal §2:
// the rewrite is "limited to import paths and the path references that name
// them" — a nested-package import is one such reference).
//
// The replacement is applied for every move; ordering does not matter because
// each old literal is boundary-bounded and the new literals never reintroduce an
// old token in a rewritable position.
func ImportLiterals(content string, moves []Move) string {
	for _, m := range moves {
		// Nested-package form first ("<old>/sub"), then the exact leaf form.
		// Doing the slash form before the exact form is harmless because the
		// exact form is quote-bounded and cannot match inside a "<new>/sub"
		// result.
		content = strings.ReplaceAll(content, `"`+m.Old+`/`, `"`+m.New+`/`)
		content = strings.ReplaceAll(content, `"`+m.Old+`"`, `"`+m.New+`"`)
	}
	return content
}

// RuntimePaths rewrites the two runtime repo-relative path forms a Go source
// uses to read a moved package by path rather than by import (proposal §2):
//
//   - the slash-joined bare string literal "pkg/gateway/<old>/..." — anchored
//     on the opening quote and a trailing slash or closing quote so the short
//     path does not match inside a longer sibling literal; and
//   - the split path-segment form, the consecutive quoted segments a
//     filepath.Join / os.ReadFile / readRepoFile call passes
//     ("pkg", "gateway", "<old>"), into which the new group segment is
//     inserted so the read resolves to the moved location.
//
// Only moves under pkg/gateway are eligible (every C3 move is), and only those
// whose new path inserts exactly one group segment between gateway and the
// package basename are handled by the segment form; a deeper nesting
// (breakerstore -> middleware/circuitbreaker/breakerstore) is still handled by
// the slash-joined form, which carries the whole new tail.
func RuntimePaths(content string, moves []Move) string {
	for _, m := range moves {
		oldRel := RepoRel(m.Old)
		newRel := RepoRel(m.New)
		if oldRel == newRel {
			continue
		}
		content = slashJoinedLiteral(content, oldRel, newRel)
		content = splitSegmentForm(content, oldRel, newRel)
	}
	return content
}

// slashJoinedLiteral rewrites "pkg/gateway/<old>/..." -> "pkg/gateway/<new>/..."
// anchored on the opening quote and a trailing '/' or closing '"', so the
// short old path never matches inside a longer sibling literal.
func slashJoinedLiteral(content, oldRel, newRel string) string {
	// Trailing-slash form: "<oldRel>/...".
	content = strings.ReplaceAll(content, `"`+oldRel+`/`, `"`+newRel+`/`)
	// Exact-literal form: "<oldRel>" (the whole path with no tail).
	content = strings.ReplaceAll(content, `"`+oldRel+`"`, `"`+newRel+`"`)
	return content
}

// splitSegmentForm rewrites the consecutive quoted path segments
// "pkg", "gateway", "<old-basename>" into the new group-prefixed segments,
// for example "pkg", "gateway", "playground" -> "pkg", "gateway",
// "mcpfabric", "playground". It tolerates arbitrary whitespace between the
// quoted segments (gofmt emits ", " but a hand-written call may differ), and
// it anchors each segment on its quotes so a sibling basename does not match.
func splitSegmentForm(content, oldRel, newRel string) string {
	oldSegs := strings.Split(oldRel, "/")
	newSegs := strings.Split(newRel, "/")
	// Only the gateway subtree uses this form, and the read names the leaf
	// package by its trailing segments; rewrite on the differing tail. The
	// shared head ("pkg", "gateway") is identical, so anchor the match on the
	// first segment that differs through the old leaf basename.
	div := commonPrefixLen(oldSegs, newSegs)
	if div >= len(oldSegs) {
		return content
	}
	return replaceSegmentRun(content, oldSegs, newSegs, div)
}

// replaceSegmentRun finds runs of the old quoted-segment tail under a shared
// "pkg", "gateway" head and rewrites them to the new tail. It walks the
// content matching the canonical gofmt spacing first, then a no-space and a
// newline-separated variant, covering the spellings a Join/ReadFile call may
// carry. The head segments before the divergence point are left in place;
// only the differing tail is rewritten, which keeps the match anchored on the
// leaf basename's quotes.
func replaceSegmentRun(content string, oldSegs, newSegs []string, div int) string {
	for _, sep := range []string{", ", ",", ",\n", ",\n\t", ",\n\t\t", ", \n"} {
		oldRun := joinQuoted(oldSegs[div:], sep)
		newRun := joinQuoted(newSegs[div:], sep)
		// Anchor on the preceding head segment so a bare leaf token elsewhere
		// is not matched. The head's last segment is oldSegs[div-1]; require it
		// immediately before the run.
		if div == 0 {
			content = strings.ReplaceAll(content, oldRun, newRun)
			continue
		}
		anchor := `"` + oldSegs[div-1] + `"` + sep
		content = strings.ReplaceAll(content, anchor+oldRun, anchor+newRun)
	}
	return content
}

func joinQuoted(segs []string, sep string) string {
	quoted := make([]string, len(segs))
	for i, s := range segs {
		quoted[i] = `"` + s + `"`
	}
	return strings.Join(quoted, sep)
}

func commonPrefixLen(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// JSONTokens rewrites the gateway path references in a JSON map file's content
// (tests/spec-map.json, tests/change-graph.json). Each map entry names a path
// as a JSON string value or key; the rewrite matches on a path-token boundary
// so the short path does not collide with a longer sibling entry or a /...
// glob. The four boundary forms a map entry uses are, for old path P:
//
//	"P"        the exact path (a package key or a bare package reference)
//	"P/...     a glob prefix (the package's recursive test selector)
//	"P/        a deeper reference (a file or method under the package)
//
// All three are anchored on the opening quote and the trailing '"', '/', so
// the replacement never matches inside "P-sibling" or "Plonger". The '/'
// and "'..." forms collapse to a single "P/ -> Q/ rule because both start
// with the path followed by a slash.
func JSONTokens(content string, moves []Move) string {
	for _, m := range moves {
		oldRel := RepoRel(m.Old)
		newRel := RepoRel(m.New)
		if oldRel == newRel {
			continue
		}
		// Deeper reference and glob: "P/ -> "Q/ (covers "P/..." and "P/file").
		content = strings.ReplaceAll(content, `"`+oldRel+`/`, `"`+newRel+`/`)
		// Exact path: "P" -> "Q" (a bare package key or reference).
		content = strings.ReplaceAll(content, `"`+oldRel+`"`, `"`+newRel+`"`)
	}
	return content
}

// SurvivorClass classifies an occurrence of a pre-move path token by whether
// the C3 driver can rewrite it. The post-move audit (§4 C4) aborts on an Abort
// occurrence and warns on a Warn occurrence.
type SurvivorClass int

const (
	// None means the token does not appear in this content.
	None SurvivorClass = iota
	// Abort means a surviving token in a driver-rewritable boundary-anchored
	// form: a quote-delimited literal, a "P/ deeper reference, or the
	// split path-segment run. The driver provably can fix these, so a survivor
	// is a driver bug and the move must abort.
	Abort
	// Warn means a surviving token in a form the driver cannot reach: a path in
	// a Go comment or a larger informational string, bounded by a space or by
	// '(', ')', '.', ';' rather than by a quote or a slash. The audit records
	// these as residual drift for an optional manual sweep and does not abort.
	Warn
)

// ClassifyGo returns the strongest survivor class for the old path of a move in
// a Go source file's content. It is Abort when a driver-rewritable form
// survives (an import literal "P", a slash-joined runtime literal "P/ or "P",
// or the split path-segment run under "pkg", "gateway"), Warn when only a
// comment or informational-string occurrence survives, and None when the token
// is absent. The driver applies its rewrites before the audit, so an Abort
// here means a rewrite the driver was supposed to perform did not land.
func ClassifyGo(content string, m Move) SurvivorClass {
	oldImport := m.Old
	newImport := m.New
	oldRel := RepoRel(m.Old)
	newRel := RepoRel(m.New)

	// Driver-rewritable forms: a surviving one is an abort. The deeper-reference
	// and exact-literal forms exclude an occurrence that is the rewritten new
	// path, which a self-prefix move (policy -> policy/policy) leaves carrying the
	// old token as a prefix; see hasSurvivingQuotedRef. hasSurvivingQuotedRef is
	// applied to both the fully-qualified import path (catching a surviving
	// "<oldImport>" leaf import or "<oldImport>/sub" nested import, the form
	// ImportLiterals rewrites) and the repo-relative runtime path (catching a
	// surviving "<oldRel>" or "<oldRel>/..." runtime read, the form RuntimePaths
	// rewrites).
	if hasSurvivingQuotedRef(content, oldImport, newImport) {
		return Abort
	}
	if hasSurvivingQuotedRef(content, oldRel, newRel) {
		return Abort
	}
	if hasSplitSegmentRun(content, oldRel, newRel) {
		return Abort
	}

	// Non-rewritable forms (comment or informational string): a warning.
	if hasBoundedToken(content, oldRel) {
		return Warn
	}
	return None
}

// ClassifyJSON returns the survivor class for the old path of a move in a JSON
// map file's content. A driver-rewritable boundary form ("P", "P/..., "P/file)
// aborts, because the driver provably could and should have rewritten it. An
// in-manifest old path that survives only inside a larger informational JSON
// string value (a "notes" sentence, where the token is bounded by a space or by
// '(', ')', '.', ';', ',', ':' rather than by a quote or a slash) warns: the
// driver's JSONTokens rewrites only quote/slash-bounded tokens, so it never
// touches such an occurrence, and aborting on it would make a group move
// unsatisfiable. The Warn path mirrors ClassifyGo's treatment of informational
// strings so the §4 C4 audit records the residual stale drift on the JSON
// surface for an optional manual sweep, matching the other two audited surfaces
// (*.go and prose). spec: §4.1 (proposal 0020 §4 C4 — the post-move audit
// surfaces informational-string occurrences across all three audited surfaces).
func ClassifyJSON(content string, m Move) SurvivorClass {
	oldRel := RepoRel(m.Old)
	newRel := RepoRel(m.New)
	if hasSurvivingQuotedRef(content, oldRel, newRel) {
		return Abort
	}
	if hasBoundedToken(content, oldRel) {
		return Warn
	}
	return None
}

// hasSurvivingQuotedRef reports whether a driver-rewritable quoted path
// reference for oldRel ("<oldRel>" exact, or "<oldRel>/ deeper) survives in
// content that has already been rewritten. It excludes any occurrence that is
// the rewritten new path: a self-prefix move (for example
// pkg/gateway/policy -> pkg/gateway/policy/policy) makes the correct new token
// "pkg/gateway/policy/policy/..." contain the substring "pkg/gateway/policy/,
// which a naive strings.Contains would flag as a surviving deeper reference even
// though the driver produced it. The fix scans each "<oldRel>/ and "<oldRel>"
// match and skips the one that coincides with the start of the new path token
// "<newRel> at the same position. spec: §4.1 (proposal 0020 §4 C4 — the abort
// condition flags only a token the driver provably could and should have
// rewritten, so a correctly-rewritten self-prefix move never aborts).
func hasSurvivingQuotedRef(content, oldRel, newRel string) bool {
	for _, suffix := range []string{`/`, `"`} {
		needle := `"` + oldRel + suffix
		// newToken carries the leading quote so the prefix comparison aligns with
		// the quote-delimited needle position.
		if survivingQuotedMatch(content, needle, `"`+newRel) {
			return true
		}
	}
	return false
}

// survivingQuotedMatch reports whether needle occurs in content at a position
// that is NOT the start of the rewritten newToken. A match where newToken also
// begins at that position is the driver's own rewrite output (the self-prefix
// case), so it is not a survivor. Every other match is a genuine surviving
// pre-move token the driver should have rewritten.
func survivingQuotedMatch(content, needle, newToken string) bool {
	idx := 0
	for {
		i := strings.Index(content[idx:], needle)
		if i < 0 {
			return false
		}
		pos := idx + i
		if !strings.HasPrefix(content[pos:], newToken) {
			return true
		}
		idx = pos + len(needle)
	}
}

// HasProseToken reports whether the old path of a move survives as a standalone
// path token in non-Go prose (markdown, YAML) content, bounded by a non-quote,
// non-slash delimiter. It is the prose-file analogue of the Warn branch in
// ClassifyGo: a bare strings.Contains would false-positive on the new path,
// because an old path such as pkg/gateway/mcp is a substring of its new path
// pkg/gateway/mcpfabric/mcp; the boundary check rejects that prefix occurrence
// (the character after it is a path segment letter, not a delimiter), so only a
// genuine surviving pre-move reference matches.
func HasProseToken(content string, m Move) bool {
	return hasBoundedToken(content, RepoRel(m.Old))
}

// hasSplitSegmentRun reports whether the split path-segment runtime form for
// oldRel survives in content, anchored under the "pkg", "gateway" head so a
// bare leaf token elsewhere does not trip the audit. It returns false when the
// driver's splitSegmentForm provably could not rewrite this move: a self-prefix
// move (policy -> policy/policy) appends a tail segment rather than inserting a
// group segment before the leaf, so commonPrefixLen consumes all of oldSegs and
// splitSegmentForm returns the content unchanged. Aborting the move on a form
// the driver cannot reach would violate the §4 C4 invariant that the abort
// condition matches the driver's rewrite granularity (proposal 0020 §4 C4).
func hasSplitSegmentRun(content, oldRel, newRel string) bool {
	oldSegs := strings.Split(oldRel, "/")
	if len(oldSegs) < 3 || oldSegs[0] != "pkg" || oldSegs[1] != "gateway" {
		return false
	}
	// Self-prefix / appended-tail move: splitSegmentForm cannot rewrite it, so
	// the audit must not abort on its split form either.
	newSegs := strings.Split(newRel, "/")
	if commonPrefixLen(oldSegs, newSegs) >= len(oldSegs) {
		return false
	}
	leaf := oldSegs[len(oldSegs)-1]
	for _, sep := range []string{", ", ",", ",\n", ",\n\t", ",\n\t\t", ", \n"} {
		anchor := `"gateway"` + sep + `"` + leaf + `"`
		if strings.Contains(content, anchor) {
			return true
		}
	}
	return false
}

// hasBoundedToken reports whether oldRel appears as a standalone path token the
// driver cannot rewrite — the comment or informational-string form (proposal §4
// C4, Pass 4). A token qualifies when at least one side is a comment boundary (a
// space, start/end of content, or one of ( ) . ; , :) and neither side is a
// path-continuation character that would make this a longer path token. A '"' or
// '/' on a side is accepted as a boundary only when the other side is a comment
// boundary, because the driver-rewritable forms ("<oldRel>/ and "<oldRel>") are
// already classified Abort by ClassifyGo before this runs, so the only surviving
// quote-adjacent occurrences are informational strings such as the
// scaffolds_test.go skip and error messages, where the token sits immediately
// after the opening quote with a trailing space (for example
// "pkg/gateway/mcptools unit suites."). Requiring a comment boundary on at least
// one side rather than both sides catches those named cases while the
// path-continuation rejection (an alphanumeric, '-', or '_' adjoining the token)
// keeps a new path that carries the old path as a prefix (pkg/gateway/mcp inside
// pkg/gateway/mcpfabric/mcp) from matching.
func hasBoundedToken(content, oldRel string) bool {
	idx := 0
	for {
		i := strings.Index(content[idx:], oldRel)
		if i < 0 {
			return false
		}
		pos := idx + i
		before := byte(' ')
		if pos > 0 {
			before = content[pos-1]
		}
		afterPos := pos + len(oldRel)
		after := byte(' ')
		if afterPos < len(content) {
			after = content[afterPos]
		}
		if isStandaloneToken(before, after) {
			return true
		}
		idx = pos + len(oldRel)
	}
}

// isStandaloneToken reports whether a token bounded on its left by before and on
// its right by after is a standalone prose path token rather than a fragment of a
// longer path. It requires a comment boundary on at least one side and that
// neither side continues the path (an alphanumeric, '-', or '_'). The relaxation
// from "both sides a comment boundary" to "at least one side" is what classifies
// the §4 C4-named informational strings — the token right after an opening quote
// with a trailing space — as the Warn case the proposal builds the audit to
// record.
func isStandaloneToken(before, after byte) bool {
	if isPathContinuation(before) || isPathContinuation(after) {
		return false
	}
	return isCommentBoundary(before) || isCommentBoundary(after)
}

// isPathContinuation reports whether b extends a path token, so an occurrence of
// the old path immediately followed (or preceded) by such a byte is part of a
// longer path (for example pkg/gateway/mcp inside pkg/gateway/mcpfabric/mcp) and
// is not a standalone reference to the moved package.
func isPathContinuation(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '-' || b == '_':
		return true
	default:
		return false
	}
}

// isCommentBoundary reports whether b delimits a path token in prose rather
// than in a Go string literal or a slash-joined path. A '"' or '/' is not a
// comment boundary: the driver-rewritable forms anchored on those bytes are
// classified Abort before hasBoundedToken runs, so treating them as a boundary
// here would re-flag a rewritable occurrence as a warning.
func isCommentBoundary(b byte) bool {
	switch b {
	case '"', '/':
		return false
	case ' ', '\t', '\n', '(', ')', '.', ';', ',', ':':
		return true
	default:
		return false
	}
}
