// SPDX-License-Identifier: MIT

// Tier-11 sweep for the retired slot-identifier path placeholder.
//
// A session-mode slot's identifier is its session's identifier, so a pod
// holds one namespace rather than two and there is no separate slot
// identifier for a path template to address. Every per-slot path is
// keyed on the session: the workspace tree, the artifacts tree, and the
// per-slot credential file. A path template that still spells its
// variable segment as the slot placeholder names a value the platform
// does not mint and points a reader at the two-namespace reading the
// merge retired.
//
// The sweep reads the reader-facing surfaces the sibling retirement
// sweeps read, widened with the library and test roots: a path template
// is documented in a package comment, a struct-field comment, and a test
// header as often as it is in prose, and a placeholder that addresses
// nothing is invisible to the compiler wherever it stands.
//
// spec: 6.4 (per-session workspace and artifacts layout), 6.1 (the
// per-slot credential file), 5.2 (a session-mode slot's identifier is
// its session's identifier)

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// retiredSlotPlaceholders are the spellings of the retired slot
// identifier a path template addresses its variable segment with: the
// documented placeholder and the reader-facing stand-in. Each is built
// from its parts so that this file, which the widened sweep reads, does
// not report its own subject.
var retiredSlotPlaceholders = []string{
	"{slot" + "Id}",
	"<slot" + "Id>",
}

// sessionPlaceholder is the spelling that replaced them.
const sessionPlaceholder = "{sessionId}"

// permittedSlotPlaceholderStatements are the occurrences of a retired
// placeholder that state its retirement, keyed by the repository-relative
// file they stand in and matched as a substring of the trimmed line. The
// exemption covers the statement's own occurrence, so a further
// occurrence on the same line is still reported.
var permittedSlotPlaceholderStatements = map[string][]string{}

// spec: 6.4, 6.1, 5.2
// diagnosis: a swept surface still spells a path template's variable
//
//	segment as the retired slot identifier. A session-mode slot's
//	identifier is its session's identifier, so the placeholder names a
//	value nothing mints, and a reader who follows it looks for a second
//	namespace the pod does not have. A failure names the file and line to
//	restate on the session placeholder.
func TestNoSurfaceNamesTheRetiredSlotPathPlaceholder(t *testing.T) {
	root := repoRoot(t)
	seen := map[string]map[string]bool{}
	for _, path := range placeholderSweepSurfaces(t, root) {
		rel := mustRel(t, root, path)
		permitted := permittedSlotPlaceholderStatements[rel]
		for i, line := range strings.Split(readSweptFile(t, path), "\n") {
			if !containsAnySlotPlaceholder(line) {
				continue
			}
			trimmed := strings.TrimSpace(line)
			residue, matched := stripPermitted(permitted, trimmed)
			for _, statement := range matched {
				if seen[rel] == nil {
					seen[rel] = map[string]bool{}
				}
				seen[rel][statement] = true
			}
			if !containsAnySlotPlaceholder(residue) {
				continue
			}
			t.Errorf("%s:%d spells a path template's variable segment as the retired slot identifier; "+
				"a session-mode slot's identifier is its session's identifier, so the segment is %s:\n%s",
				rel, i+1, sessionPlaceholder, trimmed)
		}
	}
	for rel, statements := range permittedSlotPlaceholderStatements {
		for _, statement := range statements {
			if !seen[rel][statement] {
				t.Errorf("%s no longer carries the retirement statement %q; drop the exemption or restore the statement", rel, statement)
			}
		}
	}
}

// containsAnySlotPlaceholder reports whether the text carries either
// spelling of the retired placeholder.
func containsAnySlotPlaceholder(text string) bool {
	for _, placeholder := range retiredSlotPlaceholders {
		if strings.Contains(text, placeholder) {
			return true
		}
	}
	return false
}

// spec: 6.4
// diagnosis: the retired-placeholder sweep reads the reader-facing roots
//
//	alone. The token's carriers are path templates written in package and
//	field comments under pkg/ and in test headers under tests/, which the
//	sibling retirement sweeps do not read, so a reintroduced placeholder
//	there would ship green. A failure means the widened root set no longer
//	reaches the libraries and the suites.
func TestThePlaceholderSweepReadsTheLibraryAndTestRoots(t *testing.T) {
	roots := map[string]bool{}
	for _, rel := range placeholderSweepRoots {
		roots[rel] = true
	}
	for _, rel := range append(append([]string{}, retirementSweepRoots...), "pkg", "tests") {
		if !roots[rel] {
			t.Errorf("placeholderSweepRoots omits %s, so a retired placeholder under it is unread", rel)
		}
	}
	root := repoRoot(t)
	reached := map[string]bool{}
	for _, path := range placeholderSweepSurfaces(t, root) {
		reached[strings.SplitN(mustRel(t, root, path), string(filepath.Separator), 2)[0]] = true
	}
	for _, rel := range []string{"pkg", "tests"} {
		if !reached[rel] {
			t.Errorf("the placeholder sweep walked no file under %s", rel)
		}
	}
}
