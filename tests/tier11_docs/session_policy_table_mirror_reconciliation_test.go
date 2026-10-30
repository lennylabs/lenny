// SPDX-License-Identifier: MIT

// Tier-11 doc-consistency check for the reader-facing mirrors of the
// `sessionPolicy` configuration table.
//
// §6.4 states one pod filesystem layout on every pod: every session is bound to
// a slot whatever the pool's `sessionPolicy.maxConcurrentSessions`, and its
// workspace tree sits under `/workspace/slots/{sessionId}/`. A per-slot
// workspace is therefore a property of every session-mode pod rather than a
// property of the concurrent configuration.
//
// Three pages carry the same `sessionPolicy` configuration table and a fourth
// carries the same claim in its execution-mode summary. A page that still
// attributes the per-slot workspace to `maxConcurrentSessions > 1` tells an
// operator the layout is something the concurrent configuration buys, and two
// copies of one table disagree about what the row states.
//
// spec: 5.2 (pool configuration and execution modes), 6.4 (one pod filesystem
// layout on every pod)

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// sessionPolicyTablePages are the pages that mirror the `sessionPolicy`
// configuration table. The reference page owns it and the other two restate it
// verbatim for their own reader.
var sessionPolicyTablePages = []string{
	filepath.Join("docs", "reference", "execution-modes.md"),
	filepath.Join("docs", "operator-guide", "configuration.md"),
	filepath.Join("docs", "runtime-author-guide", "runtime-configuration.md"),
}

// concurrentRowBehavior returns the behavior cell of the `Concurrent` row of a
// page's `sessionPolicy` configuration table.
func concurrentRowBehavior(t *testing.T, page, body string) string {
	t.Helper()
	row := lineContaining(body, "| Concurrent |")
	if row == "" {
		t.Fatalf("%s: no `Concurrent` row in the sessionPolicy configuration table (renamed or removed?)", page)
	}
	cells := strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
	return strings.TrimSpace(cells[len(cells)-1])
}

// spec: 5.2, 6.4
// diagnosis: a mirror of the `sessionPolicy` configuration table still states
// that the concurrent configuration serves its sessions "in per-slot
// workspaces", or the mirrors state different behavior for the same row. The
// per-slot workspace tree is the layout on every pod under §6.4, so a reader of
// the un-swept page concludes an exclusive pod has no slot tree, and a reader
// comparing two pages finds one table with two texts.
func TestSessionPolicyConfigurationTableMirrorsStateOneConcurrentBehavior(t *testing.T) {
	root := repoRoot(t)

	behaviors := make(map[string]string, len(sessionPolicyTablePages))
	for _, rel := range sessionPolicyTablePages {
		body := readDoc(t, filepath.Join(root, rel))
		behavior := concurrentRowBehavior(t, rel, body)
		if strings.Contains(strings.ToLower(behavior), "per-slot workspace") {
			t.Errorf("%s: the `Concurrent` row attributes per-slot workspaces to the concurrent configuration: %s", rel, behavior)
		}
		behaviors[rel] = behavior
	}

	owner := sessionPolicyTablePages[0]
	for _, rel := range sessionPolicyTablePages[1:] {
		if behaviors[rel] != behaviors[owner] {
			t.Errorf("%s mirrors the `Concurrent` row as %q; %s states %q", rel, behaviors[rel], owner, behaviors[owner])
		}
	}
}

// spec: 5.2, 6.4
// diagnosis: the execution-mode summary on docs/about/why-lenny.md names the
// per-slot workspace as what a reader gets from `maxConcurrentSessions > 1`.
// The distinguishing property of that configuration is the co-tenancy of
// several sessions on one pod; the per-slot workspace holds on every
// session-mode pod under §6.4.
func TestWhyLennyConcurrentRowNamesCoTenancyRatherThanPerSlotWorkspaces(t *testing.T) {
	root := repoRoot(t)
	rel := filepath.Join("docs", "about", "why-lenny.md")
	body := readDoc(t, filepath.Join(root, rel))

	row := lineContaining(body, "`session`, `maxConcurrentSessions > 1`")
	if row == "" {
		t.Fatalf("%s: no concurrent-session row in the execution-mode table (renamed or removed?)", rel)
	}
	if strings.Contains(strings.ToLower(row), "per-slot workspace") {
		t.Errorf("%s: the concurrent-session row names the per-slot workspace as its distinguishing property: %s", rel, strings.TrimSpace(row))
	}
	if !strings.Contains(row, "share one pod") {
		t.Errorf("%s: the concurrent-session row does not state the co-tenancy that distinguishes it: %s", rel, strings.TrimSpace(row))
	}
}
