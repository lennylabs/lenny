// SPDX-License-Identifier: MIT

// Tier-11 documentation check reconciling the architecture overview with
// the communication-channels section. The overview's gateway-to-pod
// rendering used to stand for several conversations and one protocol
// under a single arrow, and the overview carried no pointer into the
// section that owns the channel contract, so a reader who started at the
// architecture page had no route to the registers.
//
// These tests hold the corrected state: the overview names each
// transport connection between the gateway and a pod, keeps the mTLS
// requirement on each of them, names no identifier the registers do not
// carry, and links into the section by an anchor that resolves.
//
// These tests are NOT under a build tag because they exercise the
// repository state directly — no external infrastructure required.
//
// spec: §3, §28.3

package tier11_docs_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// architectureSpecFile is the overview page whose diagram and pointer
// these tests read. channelsSpecFile, declared with the index-row check,
// is the section that owns the channel contract.
const architectureSpecFile = "03_high-level-architecture.md"

// retiredCollapsedRendering is the wording the overview carried when one
// arrow and one protocol name stood for the whole gateway-to-pod
// surface. It is held here as a rejected literal so a revert to it is
// reported rather than passed over.
const retiredCollapsedRendering = "gRPC control protocol"

// channelPointerLink matches a markdown link from another specification
// page into the communication-channels section, capturing the fragment
// when the link carries one.
var channelPointerLink = regexp.MustCompile(`\]\(` + regexp.QuoteMeta(channelsSpecFile) + `(?:#([^)]*))?\)`)

// channelIdentifier matches a link or channel identifier of the §28
// identifier space wherever it is written.
var channelIdentifier = regexp.MustCompile(`\b(?:LNK|CH)-[A-Z0-9]+(?:-[A-Z0-9]+)*\b`)

// registerRowIdentifier matches the identifier a §28.3 register row
// opens with.
var registerRowIdentifier = regexp.MustCompile("^\\|\\s*`((?:LNK|CH)-[A-Z0-9]+(?:-[A-Z0-9]+)*)`")

// TestArchitectureOverviewReconcilesWithTheChannelRegisters_spec_3 reads
// the two specification files and requires the overview to carry a
// resolving pointer into the section, to name each gateway-to-pod
// connection by a registered identifier, and to keep mTLS on each of
// them.
//
// diagnosis: a failure means the architecture overview and the
// communication-channels section disagree. Either the overview renders
// the gateway-to-pod surface as one arrow again, names an identifier no
// register row carries, drops the mTLS requirement four other sections
// state, or points into the section by an anchor no heading derives.
// Correct the overview against §28.3 rather than widening this check.
//
// spec: §3, §28.3
func TestArchitectureOverviewReconcilesWithTheChannelRegisters_spec_3(t *testing.T) {
	architecture, channels := readArchitectureAndChannels(t)
	for _, finding := range architectureChannelFindings(architecture, channels) {
		t.Errorf("spec/%s: %s", architectureSpecFile, finding)
	}
}

// TestArchitectureOverviewCheckReadsItsFailureCases_spec_3 plants each
// state the reconciliation exists to report and requires every one of
// them reported.
//
// The cases are the empty page, a revert to the collapsed rendering, an
// identifier no register row carries, a pointer whose fragment resolves
// to no heading, and an arrow that has given up the mTLS requirement. A
// check that passes the corrected tree while reading none of these is
// green for the wrong reason.
//
// diagnosis: a failure means the reconciliation is blind to one of the
// states it exists to catch, so that state can land in the overview with
// this tier green. Repair the finding function rather than the fixture.
//
// spec: §3, §28.3
func TestArchitectureOverviewCheckReadsItsFailureCases_spec_3(t *testing.T) {
	_, channels := readArchitectureAndChannels(t)

	corrected := strings.Join([]string{
		"## 3. High-Level Architecture",
		"",
		"```",
		"    Gateway ──mTLS gRPC──→ Pods    LNK-POD-GRPC, dialled by the gateway",
		"    Gateway ←──mTLS gRPC── Pods    LNK-GWCONTROL, dialled by the adapter",
		"```",
		"",
		"[Section 28](" + channelsSpecFile + "#28-communication-channels) owns the channels.",
		"",
	}, "\n")
	if findings := architectureChannelFindings(corrected, channels); len(findings) != 0 {
		t.Fatalf("the corrected fixture reported %q, so the reject cases below measure nothing", findings)
	}

	for _, tc := range []struct {
		name    string
		page    string
		wantSub string
	}{
		{
			name:    "empty page",
			page:    "",
			wantSub: "carries no link into",
		},
		{
			name:    "collapsed rendering restored",
			page:    corrected + "\n         Gateway ←──mTLS──→ Pods (" + retiredCollapsedRendering + ")\n",
			wantSub: retiredCollapsedRendering,
		},
		{
			name:    "unregistered identifier",
			page:    strings.Replace(corrected, "LNK-GWCONTROL", "LNK-POD-CONTROL", 1),
			wantSub: "LNK-POD-CONTROL",
		},
		{
			name:    "fragment resolving to no heading",
			page:    strings.Replace(corrected, "#28-communication-channels", "#28-channel-contract", 1),
			wantSub: "28-channel-contract",
		},
		{
			name:    "mTLS dropped from an arrow",
			page:    strings.Replace(corrected, "──mTLS gRPC──→ Pods    LNK-POD-GRPC", "──gRPC──→ Pods    LNK-POD-GRPC", 1),
			wantSub: "states no mTLS",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := architectureChannelFindings(tc.page, channels)
			if !containsSubstring(findings, tc.wantSub) {
				t.Errorf("the planted page reported %q, and none of them names %q", findings, tc.wantSub)
			}
		})
	}
}

// readArchitectureAndChannels returns the body of the architecture
// overview and the body of the communication-channels section.
func readArchitectureAndChannels(t *testing.T) (architecture, channels string) {
	t.Helper()
	root := repoRoot(t)
	return readDoc(t, root+"/spec/"+architectureSpecFile), readDoc(t, root+"/spec/"+channelsSpecFile)
}

// architectureChannelFindings returns one message per disagreement
// between the architecture overview and the communication-channels
// section. An empty result is the reconciled state.
func architectureChannelFindings(architecture, channels string) []string {
	var findings []string

	if strings.Contains(architecture, retiredCollapsedRendering) {
		findings = append(findings, fmt.Sprintf("the overview renders the gateway-to-pod surface as %q, which stands for several channels and one protocol under one arrow", retiredCollapsedRendering))
	}

	findings = append(findings, channelPointerFindings(architecture, channels)...)
	findings = append(findings, architectureIdentifierFindings(architecture, channels)...)

	return findings
}

// channelPointerFindings reports a missing pointer into the section and
// any pointer whose fragment no heading of the section derives.
func channelPointerFindings(architecture, channels string) []string {
	matches := channelPointerLink.FindAllStringSubmatch(architecture, -1)
	if len(matches) == 0 {
		return []string{fmt.Sprintf("carries no link into %s, so a reader of the overview has no route to the channel registers", channelsSpecFile)}
	}

	slugs := map[string]bool{}
	for _, heading := range scanMarkdownHeadings(channels) {
		slugs[slugify(heading.text)] = true
	}

	var findings []string
	for _, m := range matches {
		fragment := m[1]
		if fragment == "" || slugs[fragment] {
			continue
		}
		findings = append(findings, fmt.Sprintf("points into the section at fragment %q, which no heading of %s derives", fragment, channelsSpecFile))
	}
	return findings
}

// architectureIdentifierFindings reports an identifier the overview
// names that no §28.3 register row carries, a line naming a link that
// states no mTLS, and an overview that names no link at all.
//
// The mTLS rule is read per line naming a link, because that line is the
// overview's whole statement about the connection and the requirement is
// stated by §10, §13, §15, and §4 as well.
func architectureIdentifierFindings(architecture, channels string) []string {
	registered := registerIdentifiers(channels)

	var findings []string
	links := 0
	for _, line := range strings.Split(architecture, "\n") {
		named := channelIdentifier.FindAllString(line, -1)
		carriesLink := false
		for _, identifier := range named {
			if !registered[identifier] {
				findings = append(findings, fmt.Sprintf("names %s, which no §28.3 register row carries", identifier))
			}
			if strings.HasPrefix(identifier, "LNK-") {
				carriesLink = true
				links++
			}
		}
		if carriesLink && !strings.Contains(line, "mTLS") {
			findings = append(findings, fmt.Sprintf("states no mTLS on the connection line %q", strings.TrimSpace(line)))
		}
	}
	if links == 0 {
		findings = append(findings, "names no transport connection between the gateway and a pod, so the arrows stand for the whole surface again")
	}
	return findings
}

// registerIdentifiers returns the identifiers the §28.3 register rows
// carry, read from the first column of every register row.
func registerIdentifiers(channels string) map[string]bool {
	identifiers := map[string]bool{}
	for _, line := range strings.Split(channels, "\n") {
		if m := registerRowIdentifier.FindStringSubmatch(line); m != nil {
			identifiers[m[1]] = true
		}
	}
	return identifiers
}

// containsSubstring reports whether any of the findings names substr.
func containsSubstring(findings []string, substr string) bool {
	for _, finding := range findings {
		if strings.Contains(finding, substr) {
			return true
		}
	}
	return false
}
