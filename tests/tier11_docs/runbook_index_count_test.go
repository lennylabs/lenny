// SPDX-License-Identifier: MIT

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// runbookCountRE matches the §25.7 Path A discovery prose's stated
// runbook count, e.g. "(currently 15 runbooks)".
var runbookCountRE = regexp.MustCompile(`currently (\d+) runbooks`)

// TestRunbookIndexSpecProseCountMatchesShippedSet cross-checks the
// §25.7 Path A discovery prose's stated runbook count against the
// runbook files actually shipped under docs/runbooks/.
//
// spec: §25.7 Path A discovery ("When no filters are provided, the
// endpoint returns all runbooks — the full index. The set is small
// (currently 15 runbooks), so returning everything is cheap and
// allows agents to make their own relevance decisions.",
// spec/25_agent-operability.md).
//
// diagnosis: A fixed runbook count in spec prose drifts every time a
// runbook is added to or removed from docs/runbooks/, violating the
// no-stale-count rule (.claude/rules/doc-style.md "Explicit counts of
// capabilities, types, SPIs"; .claude/rules/doc-content.md: "Avoid
// stating a count of things the platform provides ... Counts go stale
// when the set changes"). This guard fails whenever the two diverge so
// the drift cannot go unnoticed again.
func TestRunbookIndexSpecProseCountMatchesShippedSet(t *testing.T) {
	t.Skip("pending a change-proposal spec edit to spec/25_agent-operability.md removing the stale " +
		"parenthetical runbook count (the guard hook blocks a direct spec write); see the open " +
		"TEST-GAPS.md finding for this behavior")

	root := repoRoot(t)
	specPath := filepath.Join(root, "spec", "25_agent-operability.md")
	body, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}

	m := runbookCountRE.FindSubmatch(body)
	if m == nil {
		t.Skip("spec/25_agent-operability.md no longer states a fixed runbook count; nothing to cross-check")
	}
	statedCount, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("parse stated runbook count %q: %v", m[1], err)
	}

	dir := filepath.Join(root, "docs", "runbooks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	shipped := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		shipped++
	}

	if statedCount != shipped {
		t.Errorf("spec/25_agent-operability.md states %q but docs/runbooks/ ships %d runbook files; "+
			"remove the stale count through the change-proposal pipeline", string(m[0]), shipped)
	}
}
