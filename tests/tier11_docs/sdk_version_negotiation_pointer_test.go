// SPDX-License-Identifier: MIT

// Tier-11 documentation check reconciling the SDK versioning statement
// in the external API surface with the section that states the adapter
// protocol version negotiation.
//
// The versioning statement tells a runtime author that a runtime built
// against an older SDK keeps working against a newer gateway, and it
// cites the negotiation as its authority. The negotiation is stated by
// the `INIT` row of the RPC lifecycle state machine, which names
// `adapterProtocolVersion`, `AdapterInitAck`, and
// `PROTOCOL_VERSION_INCOMPATIBLE`. A pointer that resolves to a heading
// whose section states none of that sends the author to a page that
// cannot answer the question the sentence raised, and a resolution check
// reads the fragment rather than what the destination says, so it
// reports nothing.
//
// This check reads the destination. It requires the pointer to resolve,
// to land in a section that states the negotiation, and to carry a label
// naming the section number of the heading it resolves to.
//
// These tests are NOT under a build tag because they exercise the
// repository state directly — no external infrastructure required.
//
// spec: §15.4.2, §15.7

package tier11_docs_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// apiSurfaceSpecFile is the page that carries both the SDK versioning
// statement and the section the pointer resolves to.
const apiSurfaceSpecFile = "15_external-api-surface.md"

// versionNegotiationPointerExpr reads the link the SDK versioning
// statement cites the negotiation by, capturing the label and the target
// as written.
var versionNegotiationPointerExpr = regexp.MustCompile(`protocol version negotiation from \[([^\]]+)\]\(([^)\s]+)\)`)

// sectionNumberExpr reads the section number a specification heading
// opens with, which is what a citation label names.
var sectionNumberExpr = regexp.MustCompile(`^(\d+(?:\.\d+)*)\b`)

// versionNegotiationTerms are the identifiers the negotiation is stated
// by. A destination section that carries all of them states the
// handshake the versioning sentence relies on; one that carries none of
// them states something else.
var versionNegotiationTerms = []string{"adapterProtocolVersion", "AdapterInitAck", "PROTOCOL_VERSION_INCOMPATIBLE"}

// TestSDKVersioningPointerResolvesToTheNegotiation_spec_15_4_2 reads the
// specification pages and requires the SDK versioning statement to cite
// the section that states the protocol version negotiation, by a
// resolving fragment and under a label naming that section.
//
// diagnosis: a failure means the SDK versioning statement in §15.7 cites
// a destination that does not state the adapter protocol version
// negotiation, or cites it under a label naming a different section. A
// runtime author following the pointer lands on a page that cannot
// answer why an older SDK keeps working. Repoint the link at the heading
// that owns the handshake rather than widening this check.
//
// spec: §15.4.2, §15.7
func TestSDKVersioningPointerResolvesToTheNegotiation_spec_15_4_2(t *testing.T) {
	pages := readVersionNegotiationPages(t)
	for _, finding := range versionNegotiationPointerFindings(pages) {
		t.Errorf("spec/%s: %s", apiSurfaceSpecFile, finding)
	}

	label, target, ok := versionNegotiationPointer(pages[apiSurfaceSpecFile])
	if !ok {
		t.Fatalf("spec/%s: the SDK versioning statement carries no version-negotiation pointer", apiSurfaceSpecFile)
	}
	if want := "#1542-rpc-lifecycle-state-machine"; target != want {
		t.Errorf("spec/%s: the version-negotiation pointer targets %q, want %q, the heading that states the handshake", apiSurfaceSpecFile, target, want)
	}
	if want := "§15.4.2"; label != want {
		t.Errorf("spec/%s: the version-negotiation pointer is labelled %q, want %q", apiSurfaceSpecFile, label, want)
	}
}

// TestSDKVersioningPointerCheckReadsItsFailureCases_spec_15_4_2 plants
// each state the reconciliation exists to report and requires every one
// of them reported.
//
// The cases are the empty page, a page that states no pointer at all, a
// fragment that resolves to no heading, a pointer into a section that
// states none of the negotiation, and a label naming a section other
// than the one the fragment resolves to. A check that passes the
// corrected tree while reading none of these is green for the wrong
// reason.
//
// diagnosis: a failure means the reconciliation is blind to one of the
// states it exists to catch, so that state can land in the
// specification with this tier green. Repair the finding function rather
// than the fixture.
//
// spec: §15.4.2, §15.7
func TestSDKVersioningPointerCheckReadsItsFailureCases_spec_15_4_2(t *testing.T) {
	const otherPage = "28_communication-channels.md"

	api := strings.Join([]string{
		"#### 15.4.2 RPC Lifecycle State Machine",
		"",
		"| `INIT` | The adapter sends `AdapterInit` with `adapterProtocolVersion`; the gateway",
		"replies with `AdapterInitAck` or closes with `PROTOCOL_VERSION_INCOMPATIBLE`. |",
		"",
		"### 15.7 Runtime Author SDKs",
		"",
		"**Versioning and stability.** The gateway honors the protocol version negotiation from [§15.4.2](#1542-rpc-lifecycle-state-machine) so that a runtime built against an older SDK continues to work.",
		"",
	}, "\n")
	channels := strings.Join([]string{
		"##### 28.5.3 Intra-pod",
		"",
		"The card states the transport and the framing of the intra-pod links.",
		"",
	}, "\n")
	corrected := map[string]string{apiSurfaceSpecFile: api, otherPage: channels}

	if findings := versionNegotiationPointerFindings(corrected); len(findings) != 0 {
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
			wantSub: "carries no version-negotiation pointer",
		},
		{
			name:    "statement without a pointer",
			page:    strings.Replace(api, "from [§15.4.2](#1542-rpc-lifecycle-state-machine)", "in the adapter handshake", 1),
			wantSub: "carries no version-negotiation pointer",
		},
		{
			name:    "fragment resolving to no heading",
			page:    strings.Replace(api, "#1542-rpc-lifecycle-state-machine", "#1541-adapterbinary-protocol", 1),
			wantSub: "resolves to no heading",
		},
		{
			name:    "pointer into a section stating no negotiation",
			page:    strings.Replace(api, "[§15.4.2](#1542-rpc-lifecycle-state-machine)", "[§28.5.3]("+otherPage+"#2853-intra-pod)", 1),
			wantSub: "states none of the protocol version negotiation",
		},
		{
			name:    "label naming another section",
			page:    strings.Replace(api, "[§15.4.2]", "[§15.4.1]", 1),
			wantSub: "labelled",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages := map[string]string{apiSurfaceSpecFile: tc.page, otherPage: channels}
			findings := versionNegotiationPointerFindings(pages)
			if !containsSubstring(findings, tc.wantSub) {
				t.Errorf("findings %q report no %q", findings, tc.wantSub)
			}
		})
	}
}

// readVersionNegotiationPages returns the bodies of the two pages the
// pointer can address: the citing page and the communication-channels
// section its retired target sat in.
func readVersionNegotiationPages(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	return map[string]string{
		apiSurfaceSpecFile: readDoc(t, root+"/spec/"+apiSurfaceSpecFile),
		channelsSpecFile:   readDoc(t, root+"/spec/"+channelsSpecFile),
	}
}

// versionNegotiationPointer returns the label and target of the pointer
// the SDK versioning statement cites the negotiation by.
func versionNegotiationPointer(api string) (label, target string, ok bool) {
	m := versionNegotiationPointerExpr.FindStringSubmatch(api)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// versionNegotiationPointerFindings returns one message per disagreement
// between the SDK versioning statement and the section that states the
// negotiation. An empty result is the reconciled state. pages is keyed
// by spec file name, so a file-qualified target is read against the page
// it addresses.
func versionNegotiationPointerFindings(pages map[string]string) []string {
	api := pages[apiSurfaceSpecFile]
	label, target, ok := versionNegotiationPointer(api)
	if !ok {
		return []string{"the SDK versioning statement carries no version-negotiation pointer"}
	}

	idx := strings.Index(target, "#")
	if idx < 0 {
		return []string{fmt.Sprintf("the version-negotiation pointer %q addresses no heading", target)}
	}
	file := apiSurfaceSpecFile
	if idx > 0 {
		file = target[:idx]
	}
	fragment := target[idx+1:]

	page, held := pages[file]
	if !held {
		return []string{fmt.Sprintf("the version-negotiation pointer addresses %s, which this check does not read", file)}
	}

	heading, body, found := sectionForSlug(page, fragment)
	if !found {
		return []string{fmt.Sprintf("the version-negotiation pointer targets %q, which resolves to no heading of %s", target, file)}
	}

	var findings []string
	if missing := missingNegotiationTerms(body); len(missing) != 0 {
		findings = append(findings, fmt.Sprintf("the version-negotiation pointer resolves to %q, which states none of the protocol version negotiation it is cited for (missing %s)", heading, strings.Join(missing, ", ")))
	}
	if number := sectionNumberExpr.FindString(strings.TrimSpace(heading)); number != "" {
		if !strings.Contains(label, number) {
			findings = append(findings, fmt.Sprintf("the version-negotiation pointer is labelled %q while resolving to section %s", label, number))
		}
	}
	return findings
}

// missingNegotiationTerms returns the negotiation identifiers a
// destination section does not state.
func missingNegotiationTerms(body string) []string {
	var missing []string
	for _, term := range versionNegotiationTerms {
		if !strings.Contains(body, term) {
			missing = append(missing, term)
		}
	}
	return missing
}

// sectionForSlug returns the heading text and the body of the section a
// fragment resolves to, where the body runs to the next heading at the
// same level or above.
func sectionForSlug(page, fragment string) (heading, body string, found bool) {
	lines := strings.Split(page, "\n")
	level := 0
	start := -1
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, "#")
		depth := len(line) - len(trimmed)
		if depth == 0 || !strings.HasPrefix(trimmed, " ") {
			continue
		}
		text := strings.TrimSpace(trimmed)
		if start < 0 {
			if slugify(text) != fragment {
				continue
			}
			heading, level, start = text, depth, i+1
			continue
		}
		if depth <= level {
			return heading, strings.Join(lines[start:i], "\n"), true
		}
	}
	if start < 0 {
		return "", "", false
	}
	return heading, strings.Join(lines[start:], "\n"), true
}
