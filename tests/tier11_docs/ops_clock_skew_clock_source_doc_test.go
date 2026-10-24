// SPDX-License-Identifier: MIT

// Tier-11 consistency check between the §25.4 Clock Source list and the
// opening paragraph of the ops-clock-skew runbook.
//
// The Tier 2 remediation-lock store reads the Redis TIME command before each
// lock-writing script, and on a Redis Cluster that command may reach any
// primary, so Tier 2 has no single server clock. A runbook that tells an
// operator lock expiries come from a single per-tier clock sends a skew
// investigation to one Redis node when every primary's clock matters.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 25.4 (Clock Source)

package tier11_docs_test

import (
	"strings"
	"testing"
)

const (
	clockSkewRunbookPath   = "docs/runbooks/ops-clock-skew.md"
	clockSkewRunbookTitle  = "# ops-clock-skew"
	clockSourceSpecHeading = "#### Clock Source"

	// clockSourceSpecTier2 is the §25.4 Clock Source statement that any
	// Redis Cluster primary may answer TIME.
	clockSourceSpecTier2 = "Redis `TIME` command, which any primary of a Redis Cluster may answer"

	// clockSkewRunbookSource is the runbook's statement of the same fact.
	clockSkewRunbookSource = "`lenny-ops` authors every remediation lock's `expiresAt` from each tier's server-side clock, and on a Redis Cluster any primary may answer the Redis `TIME` command"

	// clockSkewRunbookStale is the claim the runbook made before the Tier 2
	// store ran on a Redis Cluster.
	clockSkewRunbookStale = "single per-tier server clock"
)

// firstParagraphAfter returns the first non-empty paragraph that follows the
// line equal to heading.
func firstParagraphAfter(body, heading string) (string, bool) {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != heading {
			continue
		}
		var para []string
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				if len(para) > 0 {
					break
				}
				continue
			}
			para = append(para, next)
		}
		return strings.Join(para, " "), len(para) > 0
	}
	return "", false
}

// sectionAfter returns the text from heading to the next heading of any level.
func sectionAfter(body, heading string) (string, bool) {
	_, rest, ok := strings.Cut(body, heading+"\n")
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, line := range strings.Split(rest, "\n") {
		if strings.HasPrefix(line, "#") {
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String(), true
}

// TestOpsClockSkewRunbookNamesEachTierServerClock_spec_25_4 pins the
// runbook's clock-source sentence to the §25.4 Clock Source list: Tier 2 time
// comes from whichever Redis Cluster primary answers TIME, so the runbook
// names each tier's server-side clock rather than a single clock per tier.
//
// spec: 25.4 (Clock Source)
//
// diagnosis: the ops-clock-skew runbook's opening paragraph disagrees with the §25.4 Clock Source list about where Tier 2 lock time comes from; restate the sentence from the current list.
func TestOpsClockSkewRunbookNamesEachTierServerClock_spec_25_4(t *testing.T) {
	root := repoRoot(t)

	spec := readRepoFile(t, root, "spec", "25_agent-operability.md")
	clockSource, ok := sectionAfter(spec, clockSourceSpecHeading)
	if !ok {
		t.Fatalf("spec/25_agent-operability.md: heading %q not found", clockSourceSpecHeading)
	}
	if !strings.Contains(clockSource, clockSourceSpecTier2) {
		t.Fatalf("§25.4 Clock Source no longer states %q; re-derive the runbook sentence from the current list", clockSourceSpecTier2)
	}

	runbook := readRepoFile(t, root, clockSkewRunbookPath)
	opening, ok := firstParagraphAfter(runbook, clockSkewRunbookTitle)
	if !ok {
		t.Fatalf("%s: no opening paragraph under %q", clockSkewRunbookPath, clockSkewRunbookTitle)
	}
	if strings.Contains(opening, clockSkewRunbookStale) {
		t.Errorf("%s opening paragraph still says %q; §25.4 Clock Source lets any Redis Cluster primary answer TIME", clockSkewRunbookPath, clockSkewRunbookStale)
	}
	if !strings.Contains(opening, clockSkewRunbookSource) {
		t.Errorf("%s opening paragraph does not state %q", clockSkewRunbookPath, clockSkewRunbookSource)
	}
}
