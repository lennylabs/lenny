// SPDX-License-Identifier: MIT

// Tier-11 documentation check reconciling the planned-post-v1 page's
// citation of the Translation Fidelity Matrix with the section that
// carries the matrix.
//
// The durable-consumer obligation for A2A-mediated
// `MessagePart.schemaVersion` cites the matrix as the authority for the
// `[lossy]` classification it is written about. The matrix sits under
// the runtime adapter specification of the external API surface, beside
// the message-format subsection rather than inside it. A label naming
// the message-format subsection sends a reader to a subsection that
// states the stdin and stdout framing and no fidelity classification at
// all, and a fragment-resolution check reads only that the anchor
// exists, so it reports nothing.
//
// This check reads the destination. It requires the citation to
// resolve, to land in a section that states the matrix material, and to
// carry a label naming the section number the destination heading sits
// under.
//
// These tests are NOT under a build tag because they exercise the
// repository state directly — no external infrastructure required.
//
// spec: §15.4, §21

package tier11_docs_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// plannedSpecFile is the citing page: it carries the durable-consumer
// obligation that cites the Translation Fidelity Matrix.
const plannedSpecFile = "21_planned-post-v1.md"

// fidelityMatrixPointerExpr reads the durable-consumer obligation's
// citation of the matrix, capturing the label and the target as
// written. The leading phrase distinguishes this citation from the
// other links the sentence carries.
var fidelityMatrixPointerExpr = regexp.MustCompile(`The Translation Fidelity Matrix \(\[([^\]]+)\]\(([^()\s]+)\)\)`)

// fidelityMatrixMaterialTerms are the identifiers the matrix material
// is stated by: the translated type, the field the obligation is
// written about, the classification it carries there, and the adapter
// the obligation applies to. A destination section carrying all of them
// states the material the citation is written for; the message-format
// subsection beside the matrix carries none of the last three.
var fidelityMatrixMaterialTerms = []string{"MessagePart", "schemaVersion", "[lossy]", "A2A"}

// TestPlannedFidelityMatrixPointerResolvesToTheMatrix_spec_15_4 reads
// the specification pages and requires the durable-consumer
// obligation's citation to resolve to the heading that carries the
// Translation Fidelity Matrix, under a label naming the section that
// heading sits under.
//
// diagnosis: a failure means the planned-post-v1 durable-consumer
// obligation cites the fidelity classification of
// `MessagePart.schemaVersion` against a destination that states no
// classification, or under a label naming a subsection that does not
// carry the matrix. A reader following the pointer, or reading its
// label, is sent to material that does not support the sentence.
// Repoint or relabel the citation rather than widening this check.
//
// spec: §15.4, §21
func TestPlannedFidelityMatrixPointerResolvesToTheMatrix_spec_15_4(t *testing.T) {
	pages := readFidelityMatrixPointerPages(t)
	for _, finding := range fidelityMatrixPointerFindings(pages) {
		t.Errorf("spec/%s: %s", plannedSpecFile, finding)
	}

	const wantTarget = apiSurfaceSpecFile + "#translation-fidelity-matrix"
	label, target, written := fidelityMatrixPointer(pages[plannedSpecFile])
	if !written {
		return
	}
	if target != wantTarget {
		t.Errorf("spec/%s: the fidelity-matrix citation targets %q, want %q, the heading that carries the matrix", plannedSpecFile, target, wantTarget)
	}
	if number := envelopeLabelNumberExpr.FindString(label); number != "15.4" {
		t.Errorf("spec/%s: the fidelity-matrix citation is labelled %q, want a label naming section 15.4", plannedSpecFile, label)
	}
}

// TestPlannedFidelityMatrixPointerCheckReadsItsFailureCases_spec_15_4
// plants each state the reconciliation exists to report and requires
// every one of them reported.
//
// The cases are the empty page, the obligation left with no pointer, a
// fragment resolving to no heading, a pointer into the message-format
// subsection beside the matrix, a target addressing a page this check
// does not read, a target carrying no fragment, and a label naming that
// neighbouring subsection while the fragment resolves to the matrix,
// which is the state the corrected page replaces. A check that passes
// the corrected tree while reading none of these is green for the wrong
// reason.
//
// diagnosis: a failure means the reconciliation is blind to one of the
// states it exists to catch, so that state can land in the
// specification with this tier green. Repair the finding function
// rather than the fixture.
//
// spec: §15.4, §21
func TestPlannedFidelityMatrixPointerCheckReadsItsFailureCases_spec_15_4(t *testing.T) {
	const matrixTarget = apiSurfaceSpecFile + "#translation-fidelity-matrix"
	api := fidelityMatrixFixtureAPIPage()
	planned := fidelityMatrixFixturePlannedPage()
	corrected := map[string]string{plannedSpecFile: planned, apiSurfaceSpecFile: api}

	if findings := fidelityMatrixPointerFindings(corrected); len(findings) != 0 {
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
			wantSub: "carries no Translation Fidelity Matrix citation",
		},
		{
			name:    "obligation without a pointer",
			page:    strings.Replace(planned, " ([Section 15.4]("+matrixTarget+"))", "", 1),
			wantSub: "carries no Translation Fidelity Matrix citation",
		},
		{
			name:    "fragment resolving to no heading",
			page:    strings.Replace(planned, "#translation-fidelity-matrix", "#1541-adapterbinary-protocol", 1),
			wantSub: "resolves to no heading",
		},
		{
			name:    "pointer into the neighbouring message-format subsection",
			page:    strings.Replace(planned, "[Section 15.4]("+matrixTarget+")", "[Section 15.4.1]("+apiSurfaceSpecFile+"#1541-message-format-and-binary-io-requirements)", 1),
			wantSub: "states none of the Translation Fidelity Matrix material",
		},
		{
			name:    "target addressing a page this check does not read",
			page:    strings.Replace(planned, matrixTarget, "04_system-components.md#47-runtime-adapter", 1),
			wantSub: "does not read",
		},
		{
			name:    "target carrying no fragment",
			page:    strings.Replace(planned, matrixTarget, apiSurfaceSpecFile, 1),
			wantSub: "addresses no heading",
		},
		{
			name:    "label naming the neighbouring subsection",
			page:    strings.Replace(planned, "[Section 15.4](", "[Section 15.4.1](", 1),
			wantSub: "labelled",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages := map[string]string{plannedSpecFile: tc.page, apiSurfaceSpecFile: api}
			findings := fidelityMatrixPointerFindings(pages)
			if !containsSubstring(findings, tc.wantSub) {
				t.Errorf("findings %q report no %q", findings, tc.wantSub)
			}
		})
	}
}

// fidelityMatrixFixtureAPIPage is a destination page carrying the
// matrix heading beside the message-format subsection, in the nesting
// the external API surface writes: both sit under the runtime adapter
// specification, so the matrix heading, which carries no number of its
// own, resolves under section 15.4.
func fidelityMatrixFixtureAPIPage() string {
	return strings.Join([]string{
		"### 15.4 Runtime Adapter Specification",
		"",
		"#### 15.4.1 Message Format and Binary I/O Requirements",
		"",
		"The `message` type carries an `input` field containing a `MessagePart[]` array.",
		"",
		"#### Translation Fidelity Matrix",
		"",
		"| `MessagePart` field | A2A |",
		"| --- | --- |",
		"| `schemaVersion` | **`[lossy]`** — mapped to A2A `metadata.schemaVersion` string. |",
		"",
	}, "\n")
}

// fidelityMatrixFixturePlannedPage is a citing page carrying the
// durable-consumer obligation in the form the planned-post-v1 page
// writes it.
func fidelityMatrixFixturePlannedPage() string {
	return strings.Join([]string{
		"### 21.1 A2A Protocol Support",
		"",
		"**Durable-consumer obligation for A2A-mediated `MessagePart.schemaVersion`.** " +
			"The Translation Fidelity Matrix ([Section 15.4](" + apiSurfaceSpecFile + "#translation-fidelity-matrix)) " +
			"marks `MessagePart.schemaVersion` as `[lossy]` for the `A2AAdapter`.",
		"",
	}, "\n")
}

// readFidelityMatrixPointerPages returns the bodies of the citing page
// and of the page that carries the matrix.
func readFidelityMatrixPointerPages(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	return map[string]string{
		plannedSpecFile:    readDoc(t, root+"/spec/"+plannedSpecFile),
		apiSurfaceSpecFile: readDoc(t, root+"/spec/"+apiSurfaceSpecFile),
	}
}

// fidelityMatrixPointer returns the label and the target of the
// durable-consumer obligation's citation of the matrix, and whether the
// citation is written at all.
func fidelityMatrixPointer(planned string) (label, target string, written bool) {
	m := fidelityMatrixPointerExpr.FindStringSubmatch(planned)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// fidelityMatrixPointerFindings returns one message per disagreement
// between the durable-consumer obligation's citation and the heading
// that carries the Translation Fidelity Matrix. An empty result is the
// reconciled state. pages is keyed by spec file name, so the target is
// read against the page it addresses.
func fidelityMatrixPointerFindings(pages map[string]string) []string {
	label, target, written := fidelityMatrixPointer(pages[plannedSpecFile])
	if !written {
		return []string{"the durable-consumer obligation carries no Translation Fidelity Matrix citation, so the `[lossy]` classification it states is cited from nowhere"}
	}

	idx := strings.Index(target, "#")
	if idx < 0 {
		return []string{fmt.Sprintf("the fidelity-matrix citation %q addresses no heading", target)}
	}
	file, fragment := target[:idx], target[idx+1:]
	if file == "" {
		file = plannedSpecFile
	}

	page, held := pages[file]
	if !held {
		return []string{fmt.Sprintf("the fidelity-matrix citation addresses %s, which this check does not read", file)}
	}

	heading, body, found := sectionForSlug(page, fragment)
	if !found {
		return []string{fmt.Sprintf("the fidelity-matrix citation targets %q, which resolves to no heading of %s", target, file)}
	}

	var findings []string
	if missing := missingFidelityMatrixMaterialTerms(body); len(missing) != 0 {
		findings = append(findings, fmt.Sprintf("the fidelity-matrix citation resolves to %q, which states none of the Translation Fidelity Matrix material it is cited for (missing %s)", heading, strings.Join(missing, ", ")))
	}
	if number, ok := envelopeSectionNumber(page, fragment); ok {
		if labelled := envelopeLabelNumberExpr.FindString(label); labelled != number {
			findings = append(findings, fmt.Sprintf("the fidelity-matrix citation is labelled %q while resolving to section %s", label, number))
		}
	}
	return findings
}

// missingFidelityMatrixMaterialTerms returns the matrix identifiers a
// destination section does not state.
func missingFidelityMatrixMaterialTerms(body string) []string {
	var missing []string
	for _, term := range fidelityMatrixMaterialTerms {
		if !strings.Contains(body, term) {
			missing = append(missing, term)
		}
	}
	return missing
}
