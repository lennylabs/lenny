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

// The single-owner check reads the other half of a reduction.
//
// A section that gave up its channel-contract prose to the communication
// channels section keeps a successor pointer, which the check beside this one
// reads. It must also stop claiming the prose it gave up. The channels section
// opens by stating that it is the normative home for the communication
// channels, so a reduced section that still declares itself and its
// subsections the normative prose description of the same contract leaves two
// sections claiming one body of prose, and a reader who resolves the conflict
// the wrong way reads a description the reduction emptied.
//
// The predicate reads the ownership claim rather than the whole preamble,
// because the preamble keeps material the reduction deliberately left standing:
// the wire-artifact pointer, the compatibility contract for those artifacts,
// and the adapter's demotion obligation. Each of those states something about
// the artifacts or about the runtime author's adapter rather than about a
// channel, and none of them claims prose ownership.
//
// spec: §28, §15.4

// normativeProseClaim matches a sentence declaring a section the normative
// prose description or reference of a contract. Both spellings appeared in the
// tree before the reduction, so a matcher that reads one of them reports a
// restored claim as absent.
var normativeProseClaim = regexp.MustCompile(`(?i)normative\s+prose\s+(?:description|reference)`)

// channelsNormativeHome is the sentence in the channels section that makes it
// the owner of the prose the reduced sections gave up.
const channelsNormativeHome = "normative home for the communication channels"

// ownershipFixture reads a fixture body held under testdata, which is outside
// the read domain of every gate and the write domain of every pass, so a
// specimen sentence there is read by this test alone.
func ownershipFixture(t *testing.T, base string) []string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", base))
	if err != nil {
		t.Fatalf("read ownership fixture %s: %v", base, err)
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		t.Fatalf("ownership fixture %s carries no body", base)
	}
	return lines
}

// claimedProseOwnership returns the lines of a section body that claim
// normative prose ownership.
func claimedProseOwnership(lines []string) []string {
	var claims []string
	for _, l := range lines {
		if normativeProseClaim.MatchString(l) {
			claims = append(claims, strings.TrimSpace(l))
		}
	}
	return claims
}

// spec: 28 (normative home), 15.4 (runtime adapter specification)
// diagnosis: a section that gave up its channel-contract prose to the channels
// section still declares itself the normative prose description of that
// contract, so two sections claim one body of prose and a reader cannot tell
// which one to believe.
func TestReducedSectionsClaimNoNormativeProseOwnership(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	if len(reducedSections) == 0 {
		t.Fatalf("no reduced section is named; the check would pass vacuously")
	}
	for _, rs := range reducedSections {
		t.Run(rs.File+"§"+rs.Section, func(t *testing.T) {
			lines := sectionBody(t, filepath.Join(root, rs.File), rs.Section)
			if len(lines) < 2 {
				t.Fatalf("§%s of %s has an empty body; the check would pass vacuously",
					rs.Section, rs.File)
			}
			for _, claim := range claimedProseOwnership(lines) {
				t.Errorf("%s §%s gave up its channel-contract prose to §%s and still claims "+
					"normative prose ownership of it: %.160s",
					rs.File, rs.Section, rs.Owner, claim)
			}
		})
	}
}

// spec: 28 (normative home)
// diagnosis: the channels section no longer states that it is the normative
// home for the channels, so the reduction removed one owner and left none.
func TestChannelsSectionStatesItIsTheNormativeHome(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "spec/28_communication-channels.md"))
	if err != nil {
		t.Fatalf("read the channels specification: %v", err)
	}
	if !strings.Contains(string(body), channelsNormativeHome) {
		t.Errorf("the channels specification does not state that it is the %s; "+
			"the reduced sections point at a section that claims no ownership",
			channelsNormativeHome)
	}
}

// spec: 28 (normative home), 15.4 (runtime adapter specification)
// diagnosis: the ownership matcher reports a restored claim as absent, or
// reports the carved-out compatibility contract as a claim, so the check above
// is either inert or red on prose the reduction left standing by design.
func TestNormativeProseClaimMatcherReadsTheClaimAndNotTheCarveOut(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		fixture string
		want    int
	}{
		{"a restored ownership sentence is reported", "normative-ownership-claim.md", 1},
		{"the carved-out compatibility contract is not", "normative-ownership-carveout.md", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := claimedProseOwnership(ownershipFixture(t, tc.fixture))
			if len(got) != tc.want {
				t.Errorf("fixture %s: %d claim(s) reported, want %d", tc.fixture, len(got), tc.want)
			}
		})
	}
	if len(claimedProseOwnership(nil)) != 0 {
		t.Errorf("an empty body reported a claim")
	}
}
