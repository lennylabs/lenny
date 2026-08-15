// SPDX-License-Identifier: MIT

// Tier-11 documentation check over the three published statements of the
// artifact set the external-adapter compliance suite asserts against: the
// §24.8 compliance-suite row, the "Canonical artifacts" table in the
// adapter-contract reference, and the schema list in the runtime-author
// publishing guide. Each is scoped to one consumer, so the §28.7
// supersession gate exempts all three and reports nothing when one of
// them omits an artifact the suite is required to assert against. These
// tests read the enumerations themselves.
//
// The frame attribution is checked from the schema artifacts rather than
// from a hand-written list, so a frame added to either artifact moves the
// expectation with it.
//
// These tests are NOT under a build tag; they read the repository spec,
// docs, and schemas directly and require no external infrastructure.

package tier11_docs_test

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	// runtimeOpsArtifact schematizes the CH-RUNTIMEOPS frames.
	runtimeOpsArtifact = "runtime-ops-events.schema.json"
	// stdioFrameArtifact schematizes the stdin/stdout frames the adapter
	// and the agent binary exchange.
	stdioFrameArtifact = "lenny-adapter-jsonl.schema.json"
)

// complianceSuiteArtifacts is the artifact set the schema-driven
// external-adapter compliance suite is required to assert against, as the
// §24.8 command row states it.
//
// spec: §24.8 (external adapter management), §15.4 (runtime adapter specification)
var complianceSuiteArtifacts = []string{
	"schemas/lenny-adapter.proto",
	"schemas/" + stdioFrameArtifact,
	"schemas/messagepart.schema.json",
	"schemas/" + runtimeOpsArtifact,
}

// frameTypeConstPattern captures the frame type one JSON Schema branch
// fixes for its `type` property. Both wire artifacts discriminate their
// top-level `oneOf` branches that way, so the captures are the frame set
// the artifact admits.
var frameTypeConstPattern = regexp.MustCompile(`"type"\s*:\s*\{\s*"const"\s*:\s*"([a-z_]+)"`)

// artifactCountPattern matches a sentence that fixes the size of the
// artifact set in words. A count goes stale the moment the table gains a
// row, which is the defect that left the adapter-contract lead sentence
// naming three artifacts over a four-row table.
var artifactCountPattern = regexp.MustCompile(`(?i)\b(one|two|three|four|five|six|\d+)\s+published schema artifacts\b`)

// tableRowFirstCellPattern captures the backticked artifact name in a
// markdown table row's first cell.
var tableRowFirstCellPattern = regexp.MustCompile("^\\|\\s*`([^`]+)`\\s*\\|")

// frameTypesIn returns the sorted frame types a JSON Schema artifact's
// branches discriminate on.
func frameTypesIn(schema string) []string {
	seen := map[string]bool{}
	for _, m := range frameTypeConstPattern.FindAllStringSubmatch(schema, -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for frame := range seen {
		out = append(out, frame)
	}
	sort.Strings(out)
	return out
}

// complianceSuiteRowFindings reports every artifact the §24.8
// compliance-suite command row leaves out of its enumeration.
func complianceSuiteRowFindings(content string, artifacts []string) []string {
	var row string
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "external-adapters validate --name") {
			row = line
			break
		}
	}
	if row == "" {
		return []string{"the external-adapter validate command row is absent, so the artifact set the compliance suite asserts against is unstated"}
	}
	var findings []string
	for _, artifact := range artifacts {
		if !strings.Contains(row, "`"+artifact+"`") {
			findings = append(findings, fmt.Sprintf("the compliance-suite row does not name %s, so the suite it gates asserts the frames that artifact schematizes against nothing", artifact))
		}
	}
	return findings
}

// canonicalArtifactsSection returns the "Canonical artifacts" section of
// the adapter-contract reference.
func canonicalArtifactsSection(content string) (string, bool) {
	const heading = "## Canonical artifacts"
	start := strings.Index(content, heading)
	if start < 0 {
		return "", false
	}
	rest := content[start+len(heading):]
	if next := strings.Index(rest, "\n## "); next >= 0 {
		rest = rest[:next]
	}
	return rest, true
}

// canonicalArtifactRows maps each artifact the section's table names to
// that row's text.
func canonicalArtifactRows(section string) map[string]string {
	rows := map[string]string{}
	for _, line := range strings.Split(section, "\n") {
		if m := tableRowFirstCellPattern.FindStringSubmatch(line); m != nil {
			rows[m[1]] = line
		}
	}
	return rows
}

// canonicalArtifactsFindings reports where the "Canonical artifacts"
// table and its lead sentence disagree with the artifacts the reference
// documents and with the frames each artifact admits. A frame the
// runtime-operations artifact admits, named in another artifact's row,
// credits two artifacts with the same frames.
func canonicalArtifactsFindings(content string, opsFrames, stdioFrames []string) []string {
	section, ok := canonicalArtifactsSection(content)
	if !ok {
		return []string{"the Canonical artifacts section is absent, so the reference states no artifact set"}
	}
	var findings []string
	lead := section
	if table := strings.Index(section, "\n|"); table >= 0 {
		lead = section[:table]
	}
	if m := artifactCountPattern.FindString(lead); m != "" {
		findings = append(findings, fmt.Sprintf("the lead sentence fixes the artifact set at %q, which the table's rows rather than a count state", strings.TrimSpace(m)))
	}

	rows := canonicalArtifactRows(section)
	if _, ok := rows[runtimeOpsArtifact]; !ok {
		findings = append(findings, fmt.Sprintf("the table has no %s row, so the runtime-operations frames are attributed to no artifact", runtimeOpsArtifact))
	}
	stdioRow, ok := rows[stdioFrameArtifact]
	if !ok {
		findings = append(findings, fmt.Sprintf("the table has no %s row, so the stdin/stdout frames are attributed to no artifact", stdioFrameArtifact))
		return findings
	}
	for _, frame := range stdioFrames {
		if !strings.Contains(stdioRow, "`"+frame+"`") {
			findings = append(findings, fmt.Sprintf("the %s row does not name the %s frame its own oneOf admits", stdioFrameArtifact, frame))
		}
	}
	for artifact, row := range rows {
		if artifact == runtimeOpsArtifact {
			continue
		}
		for _, frame := range opsFrames {
			if strings.Contains(row, "`"+frame+"`") {
				findings = append(findings, fmt.Sprintf("the %s row names the %s frame, which %s schematizes, so two rows credit one frame set to two artifacts", artifact, frame, runtimeOpsArtifact))
			}
		}
		if strings.Contains(row, "lifecycle frames") {
			findings = append(findings, fmt.Sprintf("the %s row attributes lifecycle frames to itself, a mechanism the artifact does not schematize", artifact))
		}
	}
	sort.Strings(findings)
	return findings
}

// publishingSchemaListFindings reports every artifact the publishing
// guide's compliance-suite sentence leaves out. The sentence quantifies
// over every JSON Lines frame a runtime emits, and a Full-level runtime
// emits frames on the runtime-operations socket as well as on stdout.
func publishingSchemaListFindings(content string) []string {
	var sentence string
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "validates every JSON Lines frame") {
			sentence = line
			break
		}
	}
	if sentence == "" {
		return []string{"the compliance-suite sentence is absent, so the publishing guide states no schema list"}
	}
	var findings []string
	for _, artifact := range []string{stdioFrameArtifact, "messagepart.schema.json", runtimeOpsArtifact} {
		if !strings.Contains(sentence, "`"+artifact+"`") {
			findings = append(findings, fmt.Sprintf("the publishing guide's schema list does not name %s", artifact))
		}
	}
	return findings
}

// The compliance suite gates a third-party adapter from
// pending_validation to active against a stated artifact set, and the two
// published runtime-author pages restate that set. All three name the
// runtime-operations events schema, and the frames each artifact admits
// are attributed to that artifact alone.
//
// diagnosis: a failure means one of the three published statements of the
// compliance-suite artifact set has drifted from the artifacts under
// schemas/. Either a carrier omits the runtime-operations events schema,
// in which case the suite it describes asserts the CH-RUNTIMEOPS frames
// against nothing, or the adapter-contract table credits one artifact
// with frames another artifact's oneOf admits, in which case a runtime
// author validating against the named artifact gets no coverage of those
// frames.
//
// spec: §24.8 (external adapter management), §15.4 (runtime adapter specification), §28.7 (wire-contract artifact register)
func TestPublishedComplianceArtifactSetsNameTheRuntimeOpsEventsSchema(t *testing.T) {
	root := repoRoot(t)
	opsFrames := frameTypesIn(readRepoFile(t, root, "schemas", runtimeOpsArtifact))
	stdioFrames := frameTypesIn(readRepoFile(t, root, "schemas", stdioFrameArtifact))
	if len(opsFrames) == 0 || len(stdioFrames) == 0 {
		t.Fatalf("read no frame types from the wire artifacts (%d runtime-operations, %d stdin/stdout); the check has nothing to attribute", len(opsFrames), len(stdioFrames))
	}

	for _, f := range complianceSuiteRowFindings(readRepoFile(t, root, "spec", "24_lenny-ctl-command-reference.md"), complianceSuiteArtifacts) {
		t.Errorf("spec/24_lenny-ctl-command-reference.md: %s", f)
	}
	for _, f := range canonicalArtifactsFindings(readRepoFile(t, root, "docs", "reference", "adapter-contract.md"), opsFrames, stdioFrames) {
		t.Errorf("docs/reference/adapter-contract.md: %s", f)
	}
	for _, f := range publishingSchemaListFindings(readRepoFile(t, root, "docs", "runtime-author-guide", "publishing.md")) {
		t.Errorf("docs/runtime-author-guide/publishing.md: %s", f)
	}
}

// The three predicates have to report an enumeration that omits an
// artifact or misattributes a frame while accepting the corrected text,
// and neither direction is exercised by the tree once the correction has
// landed. These cases pin both.
//
// diagnosis: a failure means one of the enumeration predicates has
// drifted. A failing reject case means the gate would certify a carrier
// that omits the runtime-operations events schema or credits its frames
// to another artifact. A failing accept case means the gate reports the
// corrected text, so the correction cannot land.
//
// spec: §24.8 (external adapter management), §15.4 (runtime adapter specification)
func TestComplianceArtifactEnumerationPredicates(t *testing.T) {
	opsFrames := []string{"checkpoint_request", "credentials_rotated", "terminate"}
	stdioFrames := []string{"message", "response"}

	correctedRow := "| `lenny-ctl admin external-adapters validate --name <name>` | The suite is **schema-driven**: assertions are generated from the published `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`, `schemas/messagepart.schema.json`, and `schemas/runtime-ops-events.schema.json` artifacts. | `POST /v1/admin/external-adapters/{name}/validate` | `platform-admin` |\n"
	threeArtifactRow := strings.Replace(correctedRow, ", and `schemas/runtime-ops-events.schema.json`", "", 1)

	correctedTable := "## Canonical artifacts\n" +
		"\n" +
		"The adapter protocol is defined by the published schema artifacts the table below names.\n" +
		"\n" +
		"| Artifact | Purpose | Canonical URL |\n" +
		"|:---------|:--------|:--------------|\n" +
		"| `lenny-adapter-jsonl.schema.json` | JSON Schema for the stdin/stdout frames (`message` and `response`). | `https://schemas.lenny.dev/adapter/v1/lenny-adapter-jsonl.schema.json` |\n" +
		"| `runtime-ops-events.schema.json` | JSON Schema for the Full-level runtime-operations frames (`checkpoint_request`, `credentials_rotated`, and `terminate`). | `https://schemas.lenny.dev/adapter/v1/runtime-ops-events.schema.json` |\n" +
		"\n" +
		"## Next section\n"

	correctedSentence := "   The suite validates every JSON Lines frame your runtime emits against the canonical schemas -- `lenny-adapter-jsonl.schema.json` for stdin/stdout frames, `messagepart.schema.json` for structured content parts, and `runtime-ops-events.schema.json` for the Full-level runtime-operations frames.\n"

	specCases := []struct {
		name    string
		content string
		reject  bool
	}{
		{name: "the corrected compliance-suite row names every asserted artifact", content: correctedRow},
		{name: "a row omitting the runtime-operations events schema is reported", content: threeArtifactRow, reject: true},
		{name: "an absent command row is reported", content: "### 24.8 External Adapter Management\n", reject: true},
		{name: "empty content is reported", content: "", reject: true},
	}
	for _, tc := range specCases {
		t.Run("compliance suite row: "+tc.name, func(t *testing.T) {
			findings := complianceSuiteRowFindings(tc.content, complianceSuiteArtifacts)
			if tc.reject && len(findings) == 0 {
				t.Errorf("predicate accepted an enumeration it must report")
			}
			if !tc.reject && len(findings) != 0 {
				t.Errorf("predicate reported the corrected enumeration: %v", findings)
			}
		})
	}

	tableCases := []struct {
		name    string
		content string
		reject  bool
	}{
		{name: "the corrected table attributes each frame set to one artifact", content: correctedTable},
		{
			name:    "a table with no runtime-operations row is reported",
			content: strings.Replace(correctedTable, "| `runtime-ops-events.schema.json` | JSON Schema for the Full-level runtime-operations frames (`checkpoint_request`, `credentials_rotated`, and `terminate`). | `https://schemas.lenny.dev/adapter/v1/runtime-ops-events.schema.json` |\n", "", 1),
			reject:  true,
		},
		{
			name:    "a lead sentence fixing the artifact count is reported",
			content: strings.Replace(correctedTable, "the published schema artifacts the table below names", "three published schema artifacts", 1),
			reject:  true,
		},
		{
			name:    "a stdin/stdout row crediting a runtime-operations frame is reported",
			content: strings.Replace(correctedTable, "(`message` and `response`)", "(`message`, `response`, and `checkpoint_request`)", 1),
			reject:  true,
		},
		{
			name:    "a stdin/stdout row claiming lifecycle frames is reported",
			content: strings.Replace(correctedTable, "(`message` and `response`)", "(`message`, `response`, and lifecycle frames)", 1),
			reject:  true,
		},
		{
			name:    "a stdin/stdout row omitting a frame its own oneOf admits is reported",
			content: strings.Replace(correctedTable, "(`message` and `response`)", "(`message`)", 1),
			reject:  true,
		},
		{name: "an absent section is reported", content: "# Adapter contract\n", reject: true},
		{name: "empty content is reported", content: "", reject: true},
	}
	for _, tc := range tableCases {
		t.Run("canonical artifacts table: "+tc.name, func(t *testing.T) {
			findings := canonicalArtifactsFindings(tc.content, opsFrames, stdioFrames)
			if tc.reject && len(findings) == 0 {
				t.Errorf("predicate accepted a table it must report")
			}
			if !tc.reject && len(findings) != 0 {
				t.Errorf("predicate reported the corrected table: %v", findings)
			}
		})
	}

	sentenceCases := []struct {
		name    string
		content string
		reject  bool
	}{
		{name: "the corrected sentence names every artifact the suite asserts against", content: correctedSentence},
		{
			name:    "a sentence omitting the runtime-operations events schema is reported",
			content: strings.Replace(correctedSentence, ", and `runtime-ops-events.schema.json` for the Full-level runtime-operations frames", "", 1),
			reject:  true,
		},
		{name: "an absent sentence is reported", content: "## Publish\n", reject: true},
		{name: "empty content is reported", content: "", reject: true},
	}
	for _, tc := range sentenceCases {
		t.Run("publishing schema list: "+tc.name, func(t *testing.T) {
			findings := publishingSchemaListFindings(tc.content)
			if tc.reject && len(findings) == 0 {
				t.Errorf("predicate accepted a sentence it must report")
			}
			if !tc.reject && len(findings) != 0 {
				t.Errorf("predicate reported the corrected sentence: %v", findings)
			}
		})
	}
}
