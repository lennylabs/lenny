// SPDX-License-Identifier: MIT

// Tier-11 documentation check reconciling the session-lifecycle pages'
// citations of the unified message envelope with the section that
// defines it.
//
// The interactive session model and the retry-and-resume section cite
// the envelope material at seven sites: the envelope format itself, the
// `delivery` field definition, and the `message_expired` event schema
// and its `reason` enum. That material is defined under the
// `MessageEnvelope` heading of the external API surface. A pointer that
// resolves to a heading whose section defines none of it sends a reader
// looking for the field set, the closed enum, or the event schema to a
// page that carries none of them, and a fragment-resolution check reads
// only that the anchor exists, so it reports nothing.
//
// This check reads the destination. It requires every envelope citation
// in the session-lifecycle page to resolve, to land in a section that
// states the envelope material, and to carry a label naming the section
// number the destination heading sits under.
//
// These tests are NOT under a build tag because they exercise the
// repository state directly — no external infrastructure required.
//
// spec: §7.2, §7.3, §15.4

package tier11_docs_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// sessionLifecycleSpecFile is the citing page: it carries the message
// routing paths, the DLQ rows, and the expiry prose that cite the
// envelope material.
const sessionLifecycleSpecFile = "07_session-lifecycle.md"

// envelopePointerCount is the number of envelope citations the
// session-lifecycle page writes. Pinning it keeps a citation that is
// repointed at some third page from leaving the domain unnoticed: a
// pointer that no longer reads as an envelope citation shows up here as
// a short count rather than as silence.
const envelopePointerCount = 7

// envelopeLinkExpr reads an inline markdown link, capturing the label
// and the target as written.
var envelopeLinkExpr = regexp.MustCompile(`\[([^\]]+)\]\(([^()\s]+)\)`)

// envelopeLabelNumberExpr reads the section number a citation label
// names, in either of the two forms the specification writes it,
// `Section 15.4` and `§15.4`.
var envelopeLabelNumberExpr = regexp.MustCompile(`\d+(?:\.\d+)*`)

// envelopeHeadingExpr reads a markdown heading, capturing its depth and
// its text.
var envelopeHeadingExpr = regexp.MustCompile(`^(#{1,6}) +(.*\S)`)

// envelopeCitationExpr reads the clause a citation is written in. A
// pointer preceded by one of these verbs is cited as the definition of
// what the clause names, rather than as a cross-reference to a
// neighbouring path that happens to mention the same identifier.
var envelopeCitationExpr = regexp.MustCompile(`\b(see|defined in|definition in|schema in)\b`)

// envelopeMaterialTerms are the identifiers the envelope material is
// stated by: the envelope itself, the closed interrupt enum, and the
// expiry event. A destination section carrying all of them defines the
// material the session-lifecycle citations are written for.
var envelopeMaterialTerms = []string{"MessageEnvelope", "delivery", "message_expired"}

// envelopeCues are the identifiers a citing clause names when it cites
// the envelope material.
var envelopeCues = []string{"MessageEnvelope", "message_expired", "`delivery` field"}

// envelopePointer is one envelope citation in the session-lifecycle
// page, located so a finding names the site rather than the string.
type envelopePointer struct {
	// Line is the 1-based line the citation is written on.
	Line int
	// Label is the citation label as written.
	Label string
	// Target is the link target as written.
	Target string
}

// TestSessionLifecycleEnvelopePointersResolveToTheDefinition_spec_15_4
// reads the specification pages and requires every envelope citation in
// the session-lifecycle page to resolve to the section that defines the
// unified message envelope, under a label naming that section.
//
// diagnosis: a failure means the session-lifecycle page cites the
// envelope format, the `delivery` field, or the `message_expired` event
// against a destination that defines none of them, or under a label
// naming a different section. A reader following the pointer lands on a
// page that does not carry the schema the sentence promises. Repoint
// the citation at the heading that owns the envelope rather than
// widening this check.
//
// spec: §7.2, §7.3, §15.4
func TestSessionLifecycleEnvelopePointersResolveToTheDefinition_spec_15_4(t *testing.T) {
	pages := readEnvelopePointerPages(t)
	for _, finding := range envelopePointerFindings(pages) {
		t.Errorf("spec/%s: %s", sessionLifecycleSpecFile, finding)
	}

	const wantTarget = "15_external-api-surface.md#messageenvelope--unified-message-format"
	for _, pointer := range envelopePointers(pages[sessionLifecycleSpecFile]) {
		if pointer.Target != wantTarget {
			t.Errorf("spec/%s:%d: the envelope citation targets %q, want %q, the heading that defines the envelope", sessionLifecycleSpecFile, pointer.Line, pointer.Target, wantTarget)
		}
		if number := envelopeLabelNumberExpr.FindString(pointer.Label); number != "15.4" {
			t.Errorf("spec/%s:%d: the envelope citation is labelled %q, want a label naming section 15.4", sessionLifecycleSpecFile, pointer.Line, pointer.Label)
		}
	}
}

// TestSessionLifecycleEnvelopePointerCheckReadsItsFailureCases_spec_15_4
// plants each state the reconciliation exists to report and requires
// every one of them reported.
//
// The cases are the empty page, a citing clause left with no pointer, a
// fragment resolving to no heading, a pointer into the intra-pod
// channel card, which states the framing of the intra-pod links and
// none of the envelope material, and a label naming a subsection of the
// section the fragment resolves to. A check that passes the corrected
// tree while reading none of these is green for the wrong reason.
//
// diagnosis: a failure means the reconciliation is blind to one of the
// states it exists to catch, so that state can land in the
// specification with this tier green. Repair the finding function
// rather than the fixture.
//
// spec: §7.2, §7.3, §15.4
func TestSessionLifecycleEnvelopePointerCheckReadsItsFailureCases_spec_15_4(t *testing.T) {
	api := strings.Join([]string{
		"### 15.4 Runtime Adapter Specification",
		"",
		"#### `MessageEnvelope` — Unified Message Format",
		"",
		"All inbound content messages use a unified `MessageEnvelope`. The `delivery` field is a closed enum.",
		"The `message_expired` event schema and its `reason` enum are defined here.",
		"",
		"#### 15.4.2 RPC Lifecycle State Machine",
		"",
		"The adapter opens the session over the control RPC.",
		"",
	}, "\n")
	channels := strings.Join([]string{
		"##### 28.5.3 Intra-pod",
		"",
		"The card states the transport and the framing of the intra-pod links.",
		"Content messages on stdin use the full `MessageEnvelope` format, whose `delivery` field is defined elsewhere.",
		"",
	}, "\n")
	lifecycle := envelopeFixtureLifecyclePage()
	corrected := map[string]string{
		sessionLifecycleSpecFile: lifecycle,
		apiSurfaceSpecFile:       api,
		channelsSpecFile:         channels,
	}

	if findings := envelopePointerFindings(corrected); len(findings) != 0 {
		t.Fatalf("the corrected fixture reported %q, so the reject cases below measure nothing", findings)
	}

	const envelopeTarget = "15_external-api-surface.md#messageenvelope--unified-message-format"
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
			name:    "citing clause without a pointer",
			page:    strings.Replace(lifecycle, "in [Section 15.4]("+envelopeTarget+")", "in the external API surface", 1),
			wantSub: "carries 6 envelope citations",
		},
		{
			name:    "fragment resolving to no heading",
			page:    strings.Replace(lifecycle, "#messageenvelope--unified-message-format", "#1541-adapterbinary-protocol", 1),
			wantSub: "resolves to no heading",
		},
		{
			name:    "pointer into the intra-pod card",
			page:    strings.Replace(lifecycle, "[Section 15.4]("+envelopeTarget+")", "[Section 28.5.3](28_communication-channels.md#2853-intra-pod)", 1),
			wantSub: "states none of the MessageEnvelope material",
		},
		{
			name:    "label naming a subsection of the destination",
			page:    strings.Replace(lifecycle, "[Section 15.4](", "[Section 15.4.1](", 1),
			wantSub: "labelled",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages := map[string]string{
				sessionLifecycleSpecFile: tc.page,
				apiSurfaceSpecFile:       api,
				channelsSpecFile:         channels,
			}
			findings := envelopePointerFindings(pages)
			if !containsSubstring(findings, tc.wantSub) {
				t.Errorf("findings %q report no %q", findings, tc.wantSub)
			}
		})
	}
}

// envelopeFixtureLifecyclePage is a citing page carrying one envelope
// citation of each form the specification writes: the envelope format,
// the `delivery` field, the `message_expired` event schema, and its
// `reason` enum, alongside a same-page cross-reference that names the
// expiry event without citing its definition and so sits outside the
// domain.
func envelopeFixtureLifecyclePage() string {
	const target = "15_external-api-surface.md#messageenvelope--unified-message-format"
	return strings.Join([]string{
		"### 7.2 Interactive Session Model",
		"",
		"All content delivery uses the `MessageEnvelope` format (see [Section 15.4](" + target + ")).",
		"",
		"Buffered even when `delivery: \"immediate\"` is set (see `delivery` field definition in [Section 15.4](" + target + ")).",
		"",
		"The gateway emits a `message_expired` event with `reason: \"durable_inbox_ttl_expired\"` (see [§15.4](" + target + ") `message_expired` reason enum).",
		"",
		"On TTL expiry the gateway emits a `message_expired` event (canonical schema in [§15.4](" + target + ")).",
		"",
		"For queued messages that later expire, the gateway emits a `message_expired` event on the sender's stream. The canonical event schema is defined in [Section 15.4](" + target + ") (`message_expired` event schema). The `reason` field draws from an enum also defined in [Section 15.4](" + target + ").",
		"",
		"### 7.3 Retry and Resume",
		"",
		"The gateway drains the DLQ by emitting a `message_expired` event (canonical schema in [§15.4](" + target + ")) on each sender stream, with `reason: \"target_terminated\"` — the same reason used for the inbox-drain-on-terminal path ([§7.2](#72-interactive-session-model)).",
		"",
	}, "\n")
}

// readEnvelopePointerPages returns the bodies of the citing page and of
// the two pages an envelope citation can address: the external API
// surface that defines the envelope, and the communication-channels
// page that carries the intra-pod card.
func readEnvelopePointerPages(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	return map[string]string{
		sessionLifecycleSpecFile: readDoc(t, root+"/spec/"+sessionLifecycleSpecFile),
		apiSurfaceSpecFile:       readDoc(t, root+"/spec/"+apiSurfaceSpecFile),
		channelsSpecFile:         readDoc(t, root+"/spec/"+channelsSpecFile),
	}
}

// envelopePointers returns the envelope citations the session-lifecycle
// page writes. A link belongs to the domain when the text between the
// previous link on its line and the link names the envelope material
// and the clause the link closes reads as a citation of a definition.
func envelopePointers(lifecycle string) []envelopePointer {
	var pointers []envelopePointer
	for i, line := range strings.Split(lifecycle, "\n") {
		previous := 0
		for _, m := range envelopeLinkExpr.FindAllStringSubmatchIndex(line, -1) {
			segment := line[previous:m[0]]
			previous = m[1]
			if !containsAnyTerm(segment, envelopeCues) {
				continue
			}
			clause := segment
			if idx := strings.LastIndex(clause, "."); idx >= 0 {
				clause = clause[idx+1:]
			}
			if !envelopeCitationExpr.MatchString(clause) {
				continue
			}
			pointers = append(pointers, envelopePointer{
				Line:   i + 1,
				Label:  line[m[2]:m[3]],
				Target: line[m[4]:m[5]],
			})
		}
	}
	return pointers
}

// envelopePointerFindings returns one message per disagreement between
// an envelope citation in the session-lifecycle page and the section
// that defines the envelope material. An empty result is the reconciled
// state. pages is keyed by spec file name, so a file-qualified target
// is read against the page it addresses.
func envelopePointerFindings(pages map[string]string) []string {
	pointers := envelopePointers(pages[sessionLifecycleSpecFile])
	if len(pointers) == 0 {
		return []string{"the page carries no envelope citation, so the envelope material is cited from nowhere"}
	}

	var findings []string
	if len(pointers) != envelopePointerCount {
		findings = append(findings, fmt.Sprintf("the page carries %d envelope citations, want %d", len(pointers), envelopePointerCount))
	}
	for _, pointer := range pointers {
		findings = append(findings, envelopePointerSiteFindings(pages, pointer)...)
	}
	return findings
}

// envelopePointerSiteFindings returns the disagreements one citation
// site carries.
func envelopePointerSiteFindings(pages map[string]string, pointer envelopePointer) []string {
	idx := strings.Index(pointer.Target, "#")
	if idx < 0 {
		return []string{fmt.Sprintf("line %d: the envelope citation %q addresses no heading", pointer.Line, pointer.Target)}
	}
	file, fragment := pointer.Target[:idx], pointer.Target[idx+1:]
	if file == "" {
		file = sessionLifecycleSpecFile
	}

	page, held := pages[file]
	if !held {
		return []string{fmt.Sprintf("line %d: the envelope citation addresses %s, which this check does not read", pointer.Line, file)}
	}

	heading, body, found := sectionForSlug(page, fragment)
	if !found {
		return []string{fmt.Sprintf("line %d: the envelope citation targets %q, which resolves to no heading of %s", pointer.Line, pointer.Target, file)}
	}

	var findings []string
	if missing := missingEnvelopeMaterialTerms(body); len(missing) != 0 {
		findings = append(findings, fmt.Sprintf("line %d: the envelope citation resolves to %q, which states none of the MessageEnvelope material it is cited for (missing %s)", pointer.Line, heading, strings.Join(missing, ", ")))
	}
	if number, ok := envelopeSectionNumber(page, fragment); ok {
		if labelled := envelopeLabelNumberExpr.FindString(pointer.Label); labelled != number {
			findings = append(findings, fmt.Sprintf("line %d: the envelope citation is labelled %q while resolving to section %s", pointer.Line, pointer.Label, number))
		}
	}
	return findings
}

// missingEnvelopeMaterialTerms returns the envelope identifiers a
// destination section does not state.
func missingEnvelopeMaterialTerms(body string) []string {
	var missing []string
	for _, term := range envelopeMaterialTerms {
		if !strings.Contains(body, term) {
			missing = append(missing, term)
		}
	}
	return missing
}

// envelopeSectionNumber returns the section number a fragment resolves
// under: the number the destination heading opens with, or, when the
// heading is unnumbered, the number of the nearest enclosing heading
// above it.
func envelopeSectionNumber(page, fragment string) (string, bool) {
	type heading struct {
		depth  int
		number string
	}
	var stack []heading
	for _, line := range strings.Split(page, "\n") {
		m := envelopeHeadingExpr.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		depth, text := len(m[1]), m[2]
		for len(stack) != 0 && stack[len(stack)-1].depth >= depth {
			stack = stack[:len(stack)-1]
		}
		number := sectionNumberExpr.FindString(text)
		if slugify(text) == fragment {
			if number != "" {
				return number, true
			}
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].number != "" {
					return stack[i].number, true
				}
			}
			return "", false
		}
		stack = append(stack, heading{depth: depth, number: number})
	}
	return "", false
}

// containsAnyTerm reports whether text states any of the terms.
func containsAnyTerm(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}
