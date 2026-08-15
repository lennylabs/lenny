// SPDX-License-Identifier: MIT

// Tier-11 documentation check reconciling the two same-page envelope
// citations in the external API surface with the heading on that page
// that defines the unified message envelope.
//
// The integration-level matrix states which envelope fields a
// Basic-level runtime has to populate, and the schema-versioning list
// states that a persisted envelope carries `schemaVersion`. Both cite
// the envelope as the authority for the field set they talk about, and
// both sit on the same page as the heading that defines it. A pointer
// that resolves to the intra-pod channel card instead sends a reader
// looking for `from`, `inReplyTo`, `threadId`, and `delivery` to a card
// that states the framing of the intra-pod links and none of those
// fields, and a fragment-resolution check reads only that the anchor
// exists, so it reports nothing.
//
// This check reads the destination. It requires each of the two
// citations to resolve, to land in a section that states the envelope
// material, to be written in the same-page form, and to carry a label
// naming the section number the destination heading sits under.
//
// These tests are NOT under a build tag because they exercise the
// repository state directly — no external infrastructure required.
//
// spec: §15.4, §15.4.3, §15.5

package tier11_docs_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// apiEnvelopeMatrixCellExpr reads the Basic-level cell of the
// integration-level matrix row that states the envelope field set.
var apiEnvelopeMatrixCellExpr = regexp.MustCompile(`\*\*MessageEnvelope fields\*\*\s*\|([^|]*)\|`)

// apiEnvelopeSchemaVersionExpr reads the envelope entry of the
// schema-versioning list of persisted record types, capturing the label
// and the target as written. The trailing clause distinguishes this
// entry from the other envelope citations the page writes.
var apiEnvelopeSchemaVersionExpr = regexp.MustCompile("`MessageEnvelope` \\(\\[([^\\]]+)\\]\\(([^()\\s]+)\\), persisted in the `session_messages` table\\)")

// apiEnvelopeCellLinkExpr reads the first inline markdown link of a
// table cell, capturing the label and the target as written.
var apiEnvelopeCellLinkExpr = regexp.MustCompile(`\[([^\]]+)\]\(([^()\s]+)\)`)

// apiEnvelopeMaterialTerms are the envelope field identifiers the two
// citing sites are written for. A destination section carrying all of
// them defines the field set the sites cite; the intra-pod card carries
// none of them.
var apiEnvelopeMaterialTerms = []string{"MessageEnvelope", "inReplyTo", "threadId", "delivery"}

// apiEnvelopeCitation is one envelope citation in the external API
// surface, named so a finding names the site rather than the string.
type apiEnvelopeCitation struct {
	// Site names the citing site in reader terms.
	Site string
	// Label is the citation label as written.
	Label string
	// Target is the link target as written.
	Target string
}

// TestAPISurfaceEnvelopeCitationsResolveToTheDefinition_spec_15_4 reads
// the specification pages and requires both same-page envelope
// citations in the external API surface to resolve to the heading that
// defines the unified message envelope, under a label naming that
// section.
//
// diagnosis: a failure means the integration-level matrix or the
// schema-versioning list cites the envelope field set against a
// destination that defines none of it, or in the file-qualified form,
// or under a label naming a different section. A reader following the
// pointer lands somewhere that does not carry the field set the
// sentence promises. Repoint the citation at the heading that owns the
// envelope rather than widening this check.
//
// spec: §15.4, §15.4.3, §15.5
func TestAPISurfaceEnvelopeCitationsResolveToTheDefinition_spec_15_4(t *testing.T) {
	pages := readAPIEnvelopeCitationPages(t)
	for _, finding := range apiEnvelopeCitationFindings(pages) {
		t.Errorf("spec/%s: %s", apiSurfaceSpecFile, finding)
	}

	const wantTarget = "#messageenvelope--unified-message-format"
	for _, citation := range apiEnvelopeCitations(pages[apiSurfaceSpecFile]) {
		if citation.Target != wantTarget {
			t.Errorf("spec/%s: the %s citation targets %q, want %q, the heading that defines the envelope", apiSurfaceSpecFile, citation.Site, citation.Target, wantTarget)
		}
		if number := envelopeLabelNumberExpr.FindString(citation.Label); number != "15.4" {
			t.Errorf("spec/%s: the %s citation is labelled %q, want a label naming section 15.4", apiSurfaceSpecFile, citation.Site, citation.Label)
		}
	}
}

// TestAPISurfaceEnvelopeCitationCheckReadsItsFailureCases_spec_15_4
// plants each state the reconciliation exists to report and requires
// every one of them reported.
//
// The cases are the empty page, each of the two sites left with no
// pointer, a fragment resolving to no heading, a pointer into the
// intra-pod channel card, which states the framing of the intra-pod
// links and none of the envelope fields, a same-page destination
// addressed in the file-qualified form, and a label naming a
// subsection of the section the fragment resolves to. A check that
// passes the corrected tree while reading none of these is green for
// the wrong reason.
//
// diagnosis: a failure means the reconciliation is blind to one of the
// states it exists to catch, so that state can land in the
// specification with this tier green. Repair the finding function
// rather than the fixture.
//
// spec: §15.4, §15.4.3, §15.5
func TestAPISurfaceEnvelopeCitationCheckReadsItsFailureCases_spec_15_4(t *testing.T) {
	channels := strings.Join([]string{
		"##### 28.5.3 Intra-pod",
		"",
		"The card states the transport and the framing of the intra-pod links.",
		"",
	}, "\n")
	api := apiEnvelopeFixturePage()
	corrected := map[string]string{apiSurfaceSpecFile: api, channelsSpecFile: channels}

	if findings := apiEnvelopeCitationFindings(corrected); len(findings) != 0 {
		t.Fatalf("the corrected fixture reported %q, so the reject cases below measure nothing", findings)
	}

	const envelopeTarget = "#messageenvelope--unified-message-format"
	for _, tc := range []struct {
		name    string
		page    string
		wantSub string
	}{
		{
			name:    "empty page",
			page:    "",
			wantSub: "carries no envelope citation",
		},
		{
			name:    "matrix row without a pointer",
			page:    strings.Replace(api, " ([Section 15.4]("+envelopeTarget+"))", "", 1),
			wantSub: "integration-level matrix row carries no envelope citation",
		},
		{
			name:    "schema-versioning entry without a pointer",
			page:    strings.Replace(api, "`MessageEnvelope` ([Section 15.4]("+envelopeTarget+"), persisted", "`MessageEnvelope` (persisted", 1),
			wantSub: "schema-versioning list entry carries no envelope citation",
		},
		{
			name:    "fragment resolving to no heading",
			page:    strings.Replace(api, envelopeTarget, "#1541-adapterbinary-protocol", 1),
			wantSub: "resolves to no heading",
		},
		{
			name:    "pointer into the intra-pod card",
			page:    strings.Replace(api, "[Section 15.4]("+envelopeTarget+")", "[Section 28.5.3]("+channelsSpecFile+"#2853-intra-pod)", 1),
			wantSub: "states none of the MessageEnvelope material",
		},
		{
			name:    "same-page destination addressed in the file-qualified form",
			page:    strings.Replace(api, "("+envelopeTarget+")", "("+apiSurfaceSpecFile+envelopeTarget+")", 1),
			wantSub: "file-qualified form",
		},
		{
			name:    "label naming a subsection of the destination",
			page:    strings.Replace(api, "[Section 15.4](", "[Section 15.4.1](", 1),
			wantSub: "labelled",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages := map[string]string{apiSurfaceSpecFile: tc.page, channelsSpecFile: channels}
			findings := apiEnvelopeCitationFindings(pages)
			if !containsSubstring(findings, tc.wantSub) {
				t.Errorf("findings %q report no %q", findings, tc.wantSub)
			}
		})
	}
}

// apiEnvelopeFixturePage is a citing page carrying the definition
// heading and one citation of each of the two forms the external API
// surface writes: the integration-level matrix row and the
// schema-versioning list entry.
func apiEnvelopeFixturePage() string {
	const target = "#messageenvelope--unified-message-format"
	return strings.Join([]string{
		"### 15.4 Runtime Adapter Specification",
		"",
		"#### `MessageEnvelope` — Unified Message Format",
		"",
		"Every `MessageEnvelope` carries `from`, `inReplyTo`, `threadId`, and `delivery`.",
		"",
		"### 15.4.3 Runtime Integration Levels",
		"",
		"| Capability | Basic | Standard |",
		"|:--|:--|:--|",
		"| **MessageEnvelope fields** | Only `type`, `id`, `input` needed; all other envelope fields safely ignored ([Section 15.4](" + target + ")). | Full envelope. |",
		"",
		"### 15.5 API Versioning and Stability",
		"",
		"7. **Schema versioning.** This applies to `WorkspacePlan` ([Section 14](14_workspace-plan-schema.md)), and `MessageEnvelope` ([Section 15.4](" + target + "), persisted in the `session_messages` table).",
		"",
	}, "\n")
}

// readAPIEnvelopeCitationPages returns the bodies of the citing page
// and of the communication-channels page an envelope citation can
// wrongly address.
func readAPIEnvelopeCitationPages(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	return map[string]string{
		apiSurfaceSpecFile: readDoc(t, root+"/spec/"+apiSurfaceSpecFile),
		channelsSpecFile:   readDoc(t, root+"/spec/"+channelsSpecFile),
	}
}

// apiEnvelopeCitations returns the two envelope citations the external
// API surface writes for the envelope field set.
func apiEnvelopeCitations(api string) []apiEnvelopeCitation {
	var citations []apiEnvelopeCitation
	if cell := apiEnvelopeMatrixCellExpr.FindStringSubmatch(api); cell != nil {
		if link := apiEnvelopeCellLinkExpr.FindStringSubmatch(cell[1]); link != nil {
			citations = append(citations, apiEnvelopeCitation{
				Site:   "integration-level matrix row",
				Label:  link[1],
				Target: link[2],
			})
		}
	}
	if m := apiEnvelopeSchemaVersionExpr.FindStringSubmatch(api); m != nil {
		citations = append(citations, apiEnvelopeCitation{
			Site:   "schema-versioning list entry",
			Label:  m[1],
			Target: m[2],
		})
	}
	return citations
}

// apiEnvelopeCitationFindings returns one message per disagreement
// between an envelope citation in the external API surface and the
// heading that defines the envelope. An empty result is the reconciled
// state. pages is keyed by spec file name, so a file-qualified target
// is read against the page it addresses.
func apiEnvelopeCitationFindings(pages map[string]string) []string {
	api := pages[apiSurfaceSpecFile]
	citations := apiEnvelopeCitations(api)

	written := make(map[string]bool, len(citations))
	for _, citation := range citations {
		written[citation.Site] = true
	}

	var findings []string
	for _, site := range []string{"integration-level matrix row", "schema-versioning list entry"} {
		if !written[site] {
			findings = append(findings, fmt.Sprintf("the %s carries no envelope citation, so the envelope field set is cited from nowhere there", site))
		}
	}
	for _, citation := range citations {
		findings = append(findings, apiEnvelopeCitationSiteFindings(pages, citation)...)
	}
	return findings
}

// apiEnvelopeCitationSiteFindings returns the disagreements one
// citation site carries.
func apiEnvelopeCitationSiteFindings(pages map[string]string, citation apiEnvelopeCitation) []string {
	idx := strings.Index(citation.Target, "#")
	if idx < 0 {
		return []string{fmt.Sprintf("the %s citation %q addresses no heading", citation.Site, citation.Target)}
	}
	file, fragment := citation.Target[:idx], citation.Target[idx+1:]
	if file == "" {
		file = apiSurfaceSpecFile
	} else if file == apiSurfaceSpecFile {
		return []string{fmt.Sprintf("the %s citation targets %q, addressing this page in the file-qualified form; the definition sits on this page, so the citation takes the same-page form", citation.Site, citation.Target)}
	}

	page, held := pages[file]
	if !held {
		return []string{fmt.Sprintf("the %s citation addresses %s, which this check does not read", citation.Site, file)}
	}

	heading, body, found := sectionForSlug(page, fragment)
	if !found {
		return []string{fmt.Sprintf("the %s citation targets %q, which resolves to no heading of %s", citation.Site, citation.Target, file)}
	}

	var findings []string
	if missing := missingAPIEnvelopeMaterialTerms(body); len(missing) != 0 {
		findings = append(findings, fmt.Sprintf("the %s citation resolves to %q, which states none of the MessageEnvelope material it is cited for (missing %s)", citation.Site, heading, strings.Join(missing, ", ")))
	}
	if number, ok := envelopeSectionNumber(page, fragment); ok {
		if labelled := envelopeLabelNumberExpr.FindString(citation.Label); labelled != number {
			findings = append(findings, fmt.Sprintf("the %s citation is labelled %q while resolving to section %s", citation.Site, citation.Label, number))
		}
	}
	return findings
}

// missingAPIEnvelopeMaterialTerms returns the envelope field
// identifiers a destination section does not state.
func missingAPIEnvelopeMaterialTerms(body string) []string {
	var missing []string
	for _, term := range apiEnvelopeMaterialTerms {
		if !strings.Contains(body, term) {
			missing = append(missing, term)
		}
	}
	return missing
}
