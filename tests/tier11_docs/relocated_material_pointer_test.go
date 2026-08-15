// SPDX-License-Identifier: MIT

package tier11_docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The relocated-material pointer check reads the inbound half of a reduction.
//
// The check beside this one reads the pointer a reduced section keeps. This one
// reads the sentences elsewhere in the specification that cite a mechanism the
// reduction moved. Such a sentence carries no line citation and no retired
// anchor, so no pass repairs it: the section it names keeps its heading and its
// anchor, and the citation still resolves, while the material it cites is no
// longer there. A reader following it lands on a section that discusses
// adjacent material and takes the remaining prose for the whole answer.
//
// Each statement below is a sentence whose subject matter the reduction moved
// into a §28.5 contract card. The statements are named rather than derived, for
// the reason the successor-pointer check states: which material moved is a fact
// about a change that has already happened, and a sentence citing material that
// moved and one citing material that stayed look identical afterwards.
//
// Two things are checked per statement. The first citation the statement makes
// after its anchoring phrase resolves into §28.5, which is what stops it
// pointing at the section that gave the material up, and the statement names
// the channel the mechanism belongs to, so a reader landing on a section of
// many cards can tell which one applies.
//
// spec: §28.5.3, §4.4.3, §5.1, §9.1, §11.2, §11.3, §12.3

// relocatedStatement names one sentence that cites material a reduction moved.
// Anchor is the phrase the sentence's citation follows, and Channels are the
// channel identifiers the statement must name.
type relocatedStatement struct {
	File     string
	Anchor   string
	Channels []string
}

// relocatedStatements are the specification sentences that cite intra-pod
// material the §28.5 contract cards now own.
var relocatedStatements = []relocatedStatement{
	{
		File:     "spec/04_system-components.md",
		Anchor:   "`checkpoint_request`/`checkpoint_ready` handshake via the CH-RUNTIMEOPS (see",
		Channels: []string{"CH-RUNTIMEOPS"},
	},
	{
		File:     "spec/05_runtime-registry-and-pool-model.md",
		Anchor:   "first `lifecycle_capabilities` / `lifecycle_support` exchange (",
		Channels: []string{"CH-RUNTIMEOPS"},
	},
	{
		File:     "spec/05_runtime-registry-and-pool-model.md",
		Anchor:   "The `lifecycle_support` handshake stated by",
		Channels: []string{"CH-RUNTIMEOPS"},
	},
	{
		File:     "spec/09_mcp-integration.md",
		Anchor:   "Platform tools, per-connector tool servers. See",
		Channels: []string{"CH-MCP-PLATFORM", "CH-MCP-CONNECTOR"},
	},
	{
		File:     "spec/11_policy-and-controls.md",
		Anchor:   "`llm_request_completed` lifecycle frames the runtime sends (",
		Channels: []string{"CH-RUNTIMEOPS"},
	},
	{
		File:     "spec/11_policy-and-controls.md",
		Anchor:   "re-reported on reconnection to a new gateway replica (",
		Channels: []string{"CH-RUNTIMEOPS"},
	},
	{
		File:     "spec/11_policy-and-controls.md",
		Anchor:   "reconstructed from pod-reported token usage during session recovery (",
		Channels: []string{"CH-RUNTIMEOPS"},
	},
	{
		File:     "spec/11_policy-and-controls.md",
		Anchor:   "after crash recovery (",
		Channels: []string{"CH-RUNTIMEOPS"},
	},
	{
		File:     "spec/12_storage-architecture.md",
		Anchor:   "In-memory buffer events reconstructed from pod-reported token usage (",
		Channels: []string{"CH-RUNTIMEOPS"},
	},
}

// specCitation matches a markdown link into a numbered specification section in
// either label spelling the tree writes.
var specCitation = regexp.MustCompile(`\[(?:§|Section\s+)?(\d+(?:\.\d+)*)\]\(([^)]*)\)`)

// citationWindow bounds how far past its anchoring phrase a statement's
// citation may sit. A statement cites the owner of the material it has just
// named; a citation further along the line belongs to a later clause.
const citationWindow = 200

// statementFaults returns what is wrong with the named statement in the given
// body, and returns nothing when the statement cites a §28.5 card and names its
// channel. A body that does not carry the anchoring phrase is itself a fault:
// the sentence the reduction falsified has been reworded or deleted, and the
// check would otherwise pass by reading nothing.
func statementFaults(lines []string, st relocatedStatement) []string {
	var faults []string
	var found int
	for _, l := range lines {
		idx := strings.Index(l, st.Anchor)
		if idx < 0 {
			continue
		}
		found++
		tail := l[idx+len(st.Anchor):]
		if len(tail) > citationWindow {
			tail = tail[:citationWindow]
		}
		m := specCitation.FindStringSubmatch(tail)
		if m == nil {
			faults = append(faults, "cites no specification section after \""+st.Anchor+"\"")
			continue
		}
		if !strings.HasPrefix(m[1], "28.5") {
			faults = append(faults, "cites §"+m[1]+" for material the §28.5 contract cards own")
			continue
		}
		if !strings.Contains(m[2], "28_communication-channels.md") {
			faults = append(faults, "labels its citation §"+m[1]+" but targets "+m[2])
			continue
		}
		statement := st.Anchor + tail
		for _, ch := range st.Channels {
			if !strings.Contains(statement, ch) {
				faults = append(faults, "points at §"+m[1]+" without naming "+ch+
					", so a reader cannot tell which card applies")
			}
		}
	}
	if found == 0 {
		faults = append(faults, "carries no statement anchored at \""+st.Anchor+"\"")
	}
	return faults
}

// spec: 4.4.3 (checkpoint quiescence), 5.1 (integration level admission), 9.1 (where mcp is used), 11.2 (budgets and quotas), 11.3 (billing), 12.3 (postgres ha), 28.5.3 (intra-pod cards)
// diagnosis: a specification sentence still names the section that gave up the
// intra-pod mechanism it cites, so a reader following it lands on a section
// that no longer states the mechanism and reads the surrounding prose as the
// whole answer. The recovery-normative members are the checkpoint-consistency
// handshake and the four crash-recovery pointers.
func TestRelocatedMaterialIsCitedAtTheCardThatOwnsIt(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	if len(relocatedStatements) == 0 {
		t.Fatalf("no relocated statement is named; the check would pass vacuously")
	}
	for _, st := range relocatedStatements {
		t.Run(st.File+" "+st.Anchor, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, st.File))
			if err != nil {
				t.Fatalf("read %s: %v", st.File, err)
			}
			for _, fault := range statementFaults(strings.Split(string(body), "\n"), st) {
				t.Errorf("%s: %s", st.File, fault)
			}
		})
	}
}

// declaredComparison is the statement of the declared-versus-observed
// integration-level comparison in each of the two sections that make it. The
// two must name one owner, because a reader who finds them disagreeing has no
// way to tell which section states the handshake they compare against.
var declaredComparison = []relocatedStatement{
	{
		File:     "spec/05_runtime-registry-and-pool-model.md",
		Anchor:   "The `lifecycle_support` handshake stated by",
		Channels: []string{"CH-RUNTIMEOPS"},
	},
	{
		File:     "spec/15_external-api-surface.md",
		Anchor:   "both compare declared against the `lifecycle_support` handshake stated by",
		Channels: []string{"CH-RUNTIMEOPS"},
	},
}

// spec: 5.1 (integration level admission), 15.4.6 (conformance test suite), 28.5.3 (intra-pod cards)
// diagnosis: the registration-time admission check and the local observed-level
// probe name different owners for the handshake they both compare against, so
// the specification states one comparison against two sources of truth.
func TestBothStatementsOfTheLevelComparisonNameOneOwner(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	owners := map[string][]string{}
	for _, st := range declaredComparison {
		body, err := os.ReadFile(filepath.Join(root, st.File))
		if err != nil {
			t.Fatalf("read %s: %v", st.File, err)
		}
		if faults := statementFaults(strings.Split(string(body), "\n"), st); len(faults) > 0 {
			for _, fault := range faults {
				t.Errorf("%s: %s", st.File, fault)
			}
			continue
		}
		for _, l := range strings.Split(string(body), "\n") {
			idx := strings.Index(l, st.Anchor)
			if idx < 0 {
				continue
			}
			tail := l[idx+len(st.Anchor):]
			if len(tail) > citationWindow {
				tail = tail[:citationWindow]
			}
			m := specCitation.FindStringSubmatch(tail)
			owners[m[1]] = append(owners[m[1]], st.File)
		}
	}
	if len(owners) != 1 {
		t.Errorf("the declared-versus-observed comparison names %d owners: %v", len(owners), owners)
	}
}

// spec: 28.5.3 (intra-pod cards)
// diagnosis: the statement matcher reports a corrected pointer as a fault, or
// passes a pointer that still names the section the material left or that names
// no channel, so the check above is either red on correct prose or inert.
func TestStatementMatcherReadsTheOwnerAndTheChannel(t *testing.T) {
	t.Parallel()
	handshake := relocatedStatement{
		Anchor:   "first `lifecycle_capabilities` / `lifecycle_support` exchange (",
		Channels: []string{"CH-RUNTIMEOPS"},
	}
	for _, tc := range []struct {
		name    string
		fixture string
		want    int
	}{
		{"a pointer at the owning card is accepted", "relocated-pointer-owning-card.md", 0},
		{"a pointer at the section the material left is reported", "relocated-pointer-retired-owner.md", 1},
		{"a pointer that names no channel is reported", "relocated-pointer-unnamed-card.md", 1},
		{"a body that lost the statement is reported", "relocated-pointer-absent-statement.md", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := statementFaults(ownershipFixture(t, tc.fixture), handshake)
			if len(got) != tc.want {
				t.Errorf("fixture %s: %d fault(s) reported, want %d: %v", tc.fixture, len(got), tc.want, got)
			}
		})
	}
	if len(statementFaults(nil, handshake)) != 1 {
		t.Errorf("an empty body reported no fault; the check would pass vacuously on a deleted statement")
	}
}
