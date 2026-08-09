// SPDX-License-Identifier: MIT

// Tier-11 documentation check over the §28.7 wire-contract artifact
// register. These tests are NOT under a build tag because they exercise
// the repository state directly — no external infrastructure required.

package tier11_docs_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The artifact-register supersession check asserts that no enumeration
// outside §28.7 that stands for the register's artifact set names a
// subset of it. §28.7 is derived from the artifact set the schemas
// directory holds rather than hand-enumerated, so it is the complete
// set, and a shorter list elsewhere that stands for the same set states
// a set that is wrong the moment the directory gains an artifact. The
// check is what stops schemas/README.md from drifting back to the
// hand-written artifact table the register replaced.
//
// The read domain is the tracked markdown under spec/, docs/, and
// schemas/, together with the tracked root-level markdown documents the
// naming law leaves in scope, which is the markdown subset of the walk
// the naming lint reads. It is taken from scripts/specshift/scope rather
// than restated here, so the check and the lint read one domain.
//
// Three grounds exempt an enumeration, each of which states a subset
// deliberately rather than standing for the register's set. An
// enumeration names the artifacts the enumerating section's own prose
// documents; it names the artifact subset a named consumer asserts
// against; or it names what a build phase delivers. §28.7's own prose
// states the same three grounds, and the predicate below reads them as a
// rule over the enumeration's scoping context rather than as a list of
// sites, because the domain carries further enumerations beyond the one
// the register replaced.
//
// The first ground carries a completeness clause the other two do not.
// An enumeration exempt on that ground names the artifacts its own
// section's prose documents, so an enumeration that names fewer
// artifacts than its section's prose already names is not exempt on it.
// The other two grounds are scope statements a reader can check on the
// page: the consumer's asserted set and the phase's deliverable set are
// stated elsewhere, so the check reads the declared scope and leaves the
// content of that scope to review.
//
// An enumeration that declares no scoping ground stands for the
// register's artifact set, which is the fail-closed direction: the
// burden is on the site to state what it is scoped to.

// artifactRegisterSection is the heading that opens the register. The
// register's own rows are the enumeration every other enumeration is
// measured against, so the section is outside the domain the check
// scans.
//
// spec: §28.7
const artifactRegisterSection = "### 28.7"

// artifactRegisterFile is the section file that carries the register.
const artifactRegisterFile = "spec/28_communication-channels.md"

// artifactRowPattern matches one register row, capturing the artifact
// path in the row's first cell.
var artifactRowPattern = regexp.MustCompile("^\\|\\s*`(schemas/[^`]+)`\\s*\\|")

// artifactSetNounPattern matches the set nouns an enumeration standing
// for the register's artifact set is introduced by. A sentence that
// states a property of two named artifacts without quantifying over the
// artifact set carries none of them and is not an enumeration for this
// check, which is why the versioning prose in schemas/README.md that
// names two artifacts in one sentence is not read as one.
var artifactSetNounPattern = regexp.MustCompile(`(?i)\bartifacts\b|\bcanonical schemas\b|\bpublished schemas\b|\bschemas directory\b`)

// artifactConsumerPattern matches the second exemption ground, which is
// an enumeration scoped to the artifact subset a named consumer asserts
// against.
var artifactConsumerPattern = regexp.MustCompile(`(?i)compliance suite|conformance suite|\bvalidat(e|es|ed|ing|ion)\b|\bassert(s|ed|ion|ions)?\b|\bruntime authors\b`)

// artifactPhasePattern matches the third exemption ground, which is an
// enumeration naming what a build phase delivers.
var artifactPhasePattern = regexp.MustCompile(`(?i)\bphase\s*\d|\bdeliverables?\b`)

// artifactOwnProsePattern matches the first exemption ground's scoping
// clause, which is an enumeration introduced as the published form of a
// contract, protocol, or specification the enumerating section itself
// documents. The clause alone does not exempt: artifactStandsForSet
// applies the completeness half as well.
var artifactOwnProsePattern = regexp.MustCompile(`(?i)\b(contract|protocol|specification|surface)\b[^.]{0,120}?\b(is|are)\s+(published as|defined by|composed of|comprised of|made up of|expressed as)\b`)

// artifactListItemPattern matches a markdown list item of any marker.
var artifactListItemPattern = regexp.MustCompile(`^\s*([-*+]|\d+\.)\s`)

// artifactTableSeparatorPattern matches a markdown table's alignment
// row, which names no artifact and carries no prose.
var artifactTableSeparatorPattern = regexp.MustCompile(`^\s*\|[\s:|-]*\|\s*$`)

// artifactEnumeration is one candidate site: the lines that name the
// artifacts, the members those lines name in member position, and the
// scoping context the exemption grounds are read from.
type artifactEnumeration struct {
	File    string
	Line    int
	Members []string
	// LeadIn is the enumeration's own text together with the paragraph
	// that introduces it. The exemption grounds are read from here
	// rather than from the whole section, because a section that names a
	// consumer somewhere below would otherwise exempt every enumeration
	// it carries.
	LeadIn string
	// Heading is the enclosing markdown heading, read for the build-phase
	// ground alone, because a phase's deliverable list sits under the
	// phase's own heading.
	Heading string
	// SectionArtifacts is every artifact named anywhere in the enclosing
	// heading's section, which is what the first ground's completeness
	// clause compares the members against.
	SectionArtifacts []string
}

// artifactFinding is one enumeration the check reports.
type artifactFinding struct {
	Site    string
	Members []string
	Missing []string
	Reason  string
}

// String renders the finding for the gate's message, naming the site,
// what the enumeration names, and what it leaves out.
func (f artifactFinding) String() string {
	return fmt.Sprintf("%s: enumeration stands for the §28.7 artifact set and names a subset of it (%s); missing %s: %s",
		f.Site, strings.Join(f.Members, ", "), strings.Join(f.Missing, ", "), f.Reason)
}

// artifactRegisterSet returns the artifact set the §28.7 register holds,
// in register order. It fails rather than returning an empty set when
// the section carries no rows, so a check run against a register that
// moved or lost its table cannot report green.
func artifactRegisterSet(t testing.TB, content string) []string {
	t.Helper()
	body, ok := artifactRegisterBody(content)
	if !ok {
		t.Fatalf("§28.7 register section %q not found in %s", artifactRegisterSection, artifactRegisterFile)
	}
	var set []string
	for _, line := range strings.Split(body, "\n") {
		if m := artifactRowPattern.FindStringSubmatch(line); m != nil {
			set = append(set, m[1])
		}
	}
	if len(set) == 0 {
		t.Fatalf("§28.7 carries no artifact rows; the register is the set every other enumeration is measured against")
	}
	return set
}

// artifactRegisterBody returns the text of the §28.7 section, from its
// heading to the next heading of the same or a shallower depth.
func artifactRegisterBody(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, artifactRegisterSection+" ") {
			start = i
			continue
		}
		if start >= 0 && isArtifactHeadingAtMost(line, 3) {
			return strings.Join(lines[start:i], "\n"), true
		}
	}
	if start >= 0 {
		return strings.Join(lines[start:], "\n"), true
	}
	return "", false
}

// isArtifactHeadingAtMost reports whether the line opens a markdown
// heading of depth at most depth.
func isArtifactHeadingAtMost(line string, depth int) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	return n <= depth && n < len(line) && line[n] == ' '
}

// artifactSupersessionDomain returns the tracked markdown the check
// reads: the markdown subset of the reserved-phrase carrier set, less
// the shared read exclusion and less the root-level planning records the
// naming law places outside its domain.
func artifactSupersessionDomain(t testing.TB) []string {
	t.Helper()
	planning := map[string]bool{}
	for _, p := range scope.PlanningRecords() {
		planning[p] = true
	}
	var domain []string
	for _, p := range schematest.TrackedFiles(t) {
		if filepath.Ext(p) != ".md" || planning[p] {
			continue
		}
		if !scope.ReservedPhraseCarrier(p) || !scope.Readable(p) {
			continue
		}
		domain = append(domain, p)
	}
	sort.Strings(domain)
	return domain
}

// artifactNamesIn returns the register artifacts the text names, longest
// name first so that lenny-adapter-jsonl.schema.json is never read as a
// mention of lenny-adapter.proto.
func artifactNamesIn(text string, register []string) []string {
	ordered := append([]string(nil), register...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	seen := map[string]bool{}
	rest := text
	for _, artifact := range ordered {
		base := strings.TrimPrefix(artifact, "schemas/")
		if strings.Contains(rest, base) {
			seen[artifact] = true
			rest = strings.ReplaceAll(rest, base, "")
		}
	}
	var found []string
	for _, artifact := range register {
		if seen[artifact] {
			found = append(found, artifact)
		}
	}
	return found
}

// artifactMemberText returns the part of a line that holds the
// enumeration's members. A table row enumerates in its first cell when
// that cell names an artifact and in the row as a whole when it does
// not, which is the form the command-reference row takes. A list item
// enumerates in its head, before the dash or the sentence break that
// opens the item's description, so an artifact named in a member's
// description is a mention rather than a member.
func artifactMemberText(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "|") {
		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		if len(cells) > 0 && strings.Contains(cells[0], "schemas") {
			return cells[0]
		}
		if len(cells) > 0 && strings.Contains(cells[0], ".") {
			return cells[0]
		}
		return trimmed
	}
	if loc := artifactListItemPattern.FindStringIndex(line); loc != nil {
		item := line[loc[1]:]
		return artifactItemHead(item)
	}
	return line
}

// artifactItemHead returns a list item's head, which is the text before
// the first description separator.
func artifactItemHead(item string) string {
	cut := len(item)
	for _, sep := range []string{" — ", " -- ", ". "} {
		if i := strings.Index(item, sep); i >= 0 && i < cut {
			cut = i
		}
	}
	return item[:cut]
}

// artifactEnumerationsIn returns every candidate enumeration in one
// file. A candidate is a run of consecutive table rows or list items
// that name artifacts, or a single other line naming two or more, and it
// stands for the register's artifact set only when its lead-in
// quantifies over the artifact set. Fenced code blocks are outside the
// scan: a directory tree or a command transcript is a visual element
// rather than a prose enumeration, and the artifact set it shows is
// bounded by what the fence illustrates.
func artifactEnumerationsIn(file, content string, register []string) []artifactEnumeration {
	lines := strings.Split(content, "\n")
	sections := artifactSectionArtifacts(lines, register)
	headings := artifactHeadings(lines)
	fenced := artifactFencedLines(lines)
	skipSection := artifactRegisterSectionLines(lines, file)

	var out []artifactEnumeration
	for i := 0; i < len(lines); i++ {
		if fenced[i] || skipSection[i] {
			continue
		}
		line := lines[i]
		if len(artifactNamesIn(line, register)) == 0 {
			continue
		}
		trimmed := strings.TrimSpace(line)
		isRun := strings.HasPrefix(trimmed, "|") || artifactListItemPattern.MatchString(line)
		start, end := i, i
		if isRun {
			for end+1 < len(lines) && !fenced[end+1] && !skipSection[end+1] {
				next := strings.TrimSpace(lines[end+1])
				isNext := strings.HasPrefix(next, "|") || artifactListItemPattern.MatchString(lines[end+1])
				if !isNext || len(artifactNamesIn(lines[end+1], register)) == 0 {
					break
				}
				end++
			}
		}
		members := map[string]bool{}
		for n := start; n <= end; n++ {
			for _, a := range artifactNamesIn(artifactMemberText(lines[n]), register) {
				members[a] = true
			}
		}
		i = end
		if len(members) < 2 {
			continue
		}
		var ordered []string
		for _, a := range register {
			if members[a] {
				ordered = append(ordered, a)
			}
		}
		out = append(out, artifactEnumeration{
			File:             file,
			Line:             start + 1,
			Members:          ordered,
			LeadIn:           artifactLeadIn(lines, start, end),
			Heading:          headings[start],
			SectionArtifacts: sections[start],
		})
	}
	return out
}

// artifactLeadIn returns the enumeration's own text together with the
// paragraph introducing it. A table's header and alignment rows and one
// blank line are stepped over on the way up, because a table's
// introducing sentence sits above them.
func artifactLeadIn(lines []string, start, end int) string {
	parts := lines[start : end+1]
	blanks := 0
	i := start - 1
	for i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			blanks++
			if blanks > 1 {
				break
			}
			i--
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			break
		}
		if strings.HasPrefix(trimmed, "|") || artifactTableSeparatorPattern.MatchString(lines[i]) {
			i--
			continue
		}
		break
	}
	collected := 0
	var above []string
	for j := i; j >= 0 && collected < 3; j-- {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			break
		}
		above = append([]string{lines[j]}, above...)
		collected++
	}
	return strings.Join(append(above, parts...), "\n")
}

// artifactHeadings returns, per line, the enclosing markdown heading.
func artifactHeadings(lines []string) []string {
	out := make([]string, len(lines))
	current := ""
	for i, line := range lines {
		if strings.HasPrefix(line, "#") {
			current = line
		}
		out[i] = current
	}
	return out
}

// artifactSectionArtifacts returns, per line, every register artifact
// named anywhere in that line's enclosing section. It is what the first
// exemption ground's completeness clause reads: the artifacts the
// enumerating section's own prose documents.
func artifactSectionArtifacts(lines []string, register []string) [][]string {
	bounds := make([][2]int, 0, 8)
	start := 0
	for i, line := range lines {
		if strings.HasPrefix(line, "#") && i > start {
			bounds = append(bounds, [2]int{start, i})
			start = i
		}
	}
	bounds = append(bounds, [2]int{start, len(lines)})
	out := make([][]string, len(lines))
	for _, b := range bounds {
		found := artifactNamesIn(strings.Join(lines[b[0]:b[1]], "\n"), register)
		for i := b[0]; i < b[1]; i++ {
			out[i] = found
		}
	}
	return out
}

// artifactFencedLines marks every line inside a fenced code block.
func artifactFencedLines(lines []string) []bool {
	out := make([]bool, len(lines))
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			out[i] = true
			continue
		}
		out[i] = inFence
	}
	return out
}

// artifactRegisterSectionLines marks the §28.7 section's own lines, which
// are the register itself rather than an enumeration measured against it.
func artifactRegisterSectionLines(lines []string, file string) []bool {
	out := make([]bool, len(lines))
	if file != artifactRegisterFile {
		return out
	}
	in := false
	for i, line := range lines {
		if strings.HasPrefix(line, artifactRegisterSection+" ") {
			in = true
		} else if in && isArtifactHeadingAtMost(line, 3) {
			in = false
		}
		out[i] = in
	}
	return out
}

// artifactStandsForSet reports whether an enumeration stands for the
// register's artifact set while naming a subset of it, and returns the
// artifacts it leaves out together with why no exemption ground covers
// it. An enumeration naming the whole register set stands for the set
// and is complete, so it is not reported.
func artifactStandsForSet(e artifactEnumeration, register []string) (artifactFinding, bool) {
	if len(e.Members) >= len(register) {
		return artifactFinding{}, false
	}
	if !artifactSetNounPattern.MatchString(e.LeadIn) {
		return artifactFinding{}, false
	}
	if artifactPhasePattern.MatchString(e.LeadIn) || artifactPhasePattern.MatchString(e.Heading) {
		return artifactFinding{}, false
	}
	if artifactConsumerPattern.MatchString(e.LeadIn) {
		return artifactFinding{}, false
	}
	if artifactOwnProsePattern.MatchString(e.LeadIn) {
		missing := artifactMissing(e.SectionArtifacts, e.Members)
		if len(missing) == 0 {
			return artifactFinding{}, false
		}
		return artifactFinding{
			Site:    fmt.Sprintf("%s:%d", e.File, e.Line),
			Members: e.Members,
			Missing: artifactMissing(register, e.Members),
			Reason: fmt.Sprintf("the first ground does not cover it: the enumerating section's own prose documents %s, which the enumeration does not name",
				strings.Join(missing, ", ")),
		}, true
	}
	return artifactFinding{
		Site:    fmt.Sprintf("%s:%d", e.File, e.Line),
		Members: e.Members,
		Missing: artifactMissing(register, e.Members),
		Reason:  "no exemption ground covers it: it names neither the artifacts the enumerating section's own prose documents, nor the subset a named consumer asserts against, nor what a build phase delivers",
	}, true
}

// artifactMissing returns the members of want that are absent from got,
// in want's order.
func artifactMissing(want, got []string) []string {
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	var missing []string
	for _, w := range want {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	return missing
}

// artifactSupersessionFindings runs the predicate over one file.
func artifactSupersessionFindings(file, content string, register []string) []artifactFinding {
	var out []artifactFinding
	for _, e := range artifactEnumerationsIn(file, content, register) {
		if f, bad := artifactStandsForSet(e, register); bad {
			out = append(out, f)
		}
	}
	return out
}

// The §28.7 register is derived from the schemas directory and states
// the complete artifact set, so an enumeration elsewhere that stands for
// the same set and names fewer artifacts is superseded by it and states
// a set that is wrong. This gate reports every such enumeration in the
// tracked markdown the naming law's domain covers.
//
// diagnosis: a failure means the domain carries an enumeration standing
// for the wire-contract artifact set that names a subset of §28.7 and
// declares none of the three exemption scopes. Either the enumeration is
// a hand-written artifact list that the register supersedes, in which
// case it is replaced by a reference to §28.7, or it is scoped to one
// consumer, one section's own contract, or one build phase and the
// scoping is missing from the page, or the enumeration names fewer
// artifacts than its own section's prose documents and the correction
// that completes it has not landed.
//
// spec: §28.7 (wire-contract artifact register), §28.1 N8
func TestSpec287RegisterSupersedesEveryArtifactEnumerationInItsDomain(t *testing.T) {
	root := repoRoot(t)
	registerContent := readRepoFile(t, root, artifactRegisterFile)
	register := artifactRegisterSet(t, registerContent)

	domain := artifactSupersessionDomain(t)
	if len(domain) == 0 {
		t.Fatalf("read domain is empty; the check cannot certify a tree it did not read")
	}

	inspected := 0
	var findings []artifactFinding
	for _, file := range domain {
		body, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		content := string(body)
		inspected += len(artifactEnumerationsIn(file, content, register))
		findings = append(findings, artifactSupersessionFindings(file, content, register)...)
	}
	if inspected == 0 {
		t.Fatalf("inspected zero enumerations across %d files; the predicate selects nothing and the run is inert", len(domain))
	}
	for _, f := range findings {
		t.Errorf("%s", f)
	}
}

// Every row of the register names an artifact the schemas directory
// holds, because the register is derived from that directory rather than
// hand-enumerated. A row naming a file that is not there would put the
// register itself in the position it supersedes other enumerations for.
//
// diagnosis: a failure means the §28.7 register names an artifact absent
// from schemas/, so either the artifact was renamed or removed without
// the register moving with it, or the row was written by hand for an
// artifact that does not exist.
//
// spec: §28.7 (wire-contract artifact register)
func TestSpec287RegisterRowsNameArtifactsTheSchemasDirectoryHolds(t *testing.T) {
	root := repoRoot(t)
	register := artifactRegisterSet(t, readRepoFile(t, root, artifactRegisterFile))
	for _, artifact := range register {
		if _, err := os.Stat(filepath.Join(root, artifact)); err != nil {
			t.Errorf("§28.7 names %s, which the schemas directory does not hold: %v", artifact, err)
		}
	}
}

// artifactCorrectedSites holds the corrected text of the enumerations
// TEST-1 names as accept cases. Each is quoted at the state the
// supersession lands them in, because the exemption is stated over the
// corrected enumeration rather than over an earlier one.
var artifactCorrectedSites = map[string]string{
	"spec/24_lenny-ctl-command-reference.md": "### 24.8 External Adapter Management\n" +
		"\n" +
		"| Command | Description | API Mapping | Min Role |\n" +
		"|---------|-------------|-------------|----------|\n" +
		"| `lenny-ctl admin external-adapters validate --name <name>` | Run the compliance suite against a registered adapter. The suite is **schema-driven**: assertions are generated from the published `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`, `schemas/messagepart.schema.json`, and `schemas/runtime-ops-events.schema.json` artifacts rather than hand-coded against prose. | `POST /v1/admin/external-adapters/{name}/validate` | `platform-admin` |\n",

	"docs/reference/adapter-contract.md": "## Canonical artifacts\n" +
		"\n" +
		"The adapter protocol is defined by the published schema artifacts below. Runtime authors, and the adapter compliance suite, validate against these files rather than the narrative prose in this guide.\n" +
		"\n" +
		"| Artifact | Purpose | Canonical URL |\n" +
		"|:---------|:--------|:--------------|\n" +
		"| `lenny-adapter.proto` | gRPC service definition for the gateway and adapter control plane. | `https://schemas.lenny.dev/adapter/v1/lenny-adapter.proto` |\n" +
		"| `lenny-adapter-jsonl.schema.json` | JSON Schema for the stdin/stdout JSON Lines frames. | `https://schemas.lenny.dev/adapter/v1/lenny-adapter-jsonl.schema.json` |\n" +
		"| `messagepart.schema.json` | JSON Schema for the structured `messageParts` field. | `https://schemas.lenny.dev/adapter/v1/messagepart.schema.json` |\n" +
		"| `runtime-ops-events.schema.json` | JSON Schema for the Full-level runtime-operations frames. | `https://schemas.lenny.dev/adapter/v1/runtime-ops-events.schema.json` |\n",

	"docs/runtime-author-guide/publishing.md": "## Publish\n" +
		"\n" +
		"   The suite validates every JSON Lines frame your runtime emits against the canonical schemas published at [schemas.lenny.dev/adapter/v1/](https://schemas.lenny.dev/adapter/v1/) -- `lenny-adapter-jsonl.schema.json` for stdin/stdout frames, `messagepart.schema.json` for structured content parts, and `runtime-ops-events.schema.json` for the Full-level runtime-operations frames.\n",

	"spec/15_external-api-surface.md": "### 15.4 Runtime Adapter Specification\n" +
		"\n" +
		"The runtime adapter contract is published as machine-readable artifacts committed to the repository and released alongside each Lenny release:\n" +
		"\n" +
		"- **`schemas/lenny-adapter.proto`** — Protobuf service and message definitions for the gateway and adapter gRPC surface.\n" +
		"- **`schemas/lenny-adapter-jsonl.schema.json`** — JSON Schema for every adapter and binary stdin/stdout message. The CH-RUNTIMEOPS frames are schematized in `schemas/runtime-ops-events.schema.json` rather than in this artifact.\n" +
		"- **`schemas/messagepart.schema.json`** — JSON Schema for the `MessagePart` envelope.\n" +
		"- **`schemas/runtime-ops-events.schema.json`** — JSON Schema for the Full-level runtime-operations frames.\n",
}

// The predicate has a large false-positive and false-negative surface:
// it has to reject a hand-written artifact table that stands for the
// register's set while accepting the enumerations the three exemption
// grounds cover, none of which is distinguishable from the others by the
// artifacts it names alone. These cases pin both directions.
//
// diagnosis: a failure means the supersession predicate has drifted from
// the rule §28.7 states. A failing reject case means the predicate no
// longer reports an enumeration that stands for the artifact set, so the
// gate would certify a tree that has drifted back to a hand-written
// list. A failing accept case means the predicate reports an enumeration
// one of the three grounds exempts, so a deliberate subset is read as
// drift.
//
// spec: §28.7 (wire-contract artifact register), §28.1 N8
func TestArtifactEnumerationSupersessionPredicate(t *testing.T) {
	root := repoRoot(t)
	register := artifactRegisterSet(t, readRepoFile(t, root, artifactRegisterFile))

	readdedTable := "# `schemas/`\n" +
		"\n" +
		"Wire-contract artifacts. Spec-first: each file is the authoritative shape of the corresponding interface, and the implementation in `pkg/` is verified by CI to match.\n" +
		"\n" +
		"| File | Surface | Spec section |\n" +
		"|:-----|:--------|:-------------|\n" +
		"| `lenny-adapter.proto` | Gateway and pod gRPC control protocol | [§15.4](../spec/15_external-api-surface.md) |\n" +
		"| `lenny-interceptor.proto` | Gateway and external interceptor gRPC SPI | [§4.8](../spec/04_system-components.md) |\n" +
		"| `lenny-adapter-jsonl.schema.json` | Adapter and agent binary stdin/stdout JSONL | [§15.4](../spec/15_external-api-surface.md) |\n" +
		"| `messagepart.schema.json` | MessagePart envelope | [§15.4](../spec/15_external-api-surface.md) |\n" +
		"| `workspaceplan-v1.json` | WorkspacePlan used at session creation | [§14](../spec/14_workspace-plan-schema.md) |\n" +
		"| `ocsf-mapping.yaml` | Event-type to OCSF class mapping mirror | [§11.7](../spec/11_security-trust-model.md) |\n" +
		"| `audit-events/v1.json` | Audit-event canonical-record schema | [§11.7](../spec/11_security-trust-model.md) |\n" +
		"\n" +
		"## Validation\n"

	newUnexemptPage := "## Wire contract artifacts\n" +
		"\n" +
		"The wire-contract artifacts released with each Lenny version are:\n" +
		"\n" +
		"- `schemas/lenny-adapter.proto`\n" +
		"- `schemas/lenny-adapter-jsonl.schema.json`\n" +
		"- `schemas/messagepart.schema.json`\n"

	phaseDeliverables := "### 18.4 Phase 1 — Core types and wire contracts\n" +
		"\n" +
		"**Deliverables.**\n" +
		"\n" +
		"- Wire-contract artifacts under `schemas/`: `lenny-adapter.proto`, `lenny-adapter-jsonl.schema.json`, `messagepart.schema.json`, and `workspaceplan-v1.json`.\n" +
		"- Generated Go stubs from `lenny-adapter.proto`.\n"

	registerReference := "# `schemas/`\n" +
		"\n" +
		"The wire-contract artifact register in [`spec/28_communication-channels.md`](../spec/28_communication-channels.md) §28.7 states the artifact set this directory holds.\n" +
		"\n" +
		"## Versioning\n" +
		"\n" +
		"`messagepart.schema.json` and `lenny-adapter-jsonl.schema.json` carry a `schemaVersion` field internally.\n"

	fencedTree := "## 4. Repository and directory layout\n" +
		"\n" +
		"```\n" +
		"lenny/\n" +
		"├── schemas/                       # Wire-contract artifacts\n" +
		"│   ├── lenny-adapter.proto\n" +
		"│   ├── lenny-adapter-jsonl.schema.json\n" +
		"│   └── messagepart.schema.json\n" +
		"```\n"

	cases := []struct {
		name    string
		file    string
		content string
		reject  bool
	}{
		{
			name:    "re-added artifact table in schemas/README.md is reported",
			file:    "schemas/README.md",
			content: readdedTable,
			reject:  true,
		},
		{
			name:    "a further page enumerating the set under no exemption ground is reported",
			file:    "docs/reference/wire-artifacts.md",
			content: newUnexemptPage,
			reject:  true,
		},
		{
			name:    "an enumeration naming fewer artifacts than its own section documents is reported",
			file:    "spec/15_external-api-surface.md",
			content: strings.Replace(artifactCorrectedSites["spec/15_external-api-surface.md"], "- **`schemas/runtime-ops-events.schema.json`** — JSON Schema for the Full-level runtime-operations frames.\n", "", 1),
			reject:  true,
		},
		{
			name:    "the corrected compliance-suite sentence passes on the named-consumer ground",
			file:    "spec/24_lenny-ctl-command-reference.md",
			content: artifactCorrectedSites["spec/24_lenny-ctl-command-reference.md"],
		},
		{
			name:    "the corrected canonical-artifacts table passes on the named-consumer ground",
			file:    "docs/reference/adapter-contract.md",
			content: artifactCorrectedSites["docs/reference/adapter-contract.md"],
		},
		{
			name:    "the corrected publishing schema list passes on the named-consumer ground",
			file:    "docs/runtime-author-guide/publishing.md",
			content: artifactCorrectedSites["docs/runtime-author-guide/publishing.md"],
		},
		{
			name:    "the corrected wire-artifact pointer passes on the own-prose ground",
			file:    "spec/15_external-api-surface.md",
			content: artifactCorrectedSites["spec/15_external-api-surface.md"],
		},
		{
			name:    "a phase deliverable list passes on the build-phase ground",
			file:    "spec/18_build-sequence.md",
			content: phaseDeliverables,
		},
		{
			name:    "a reference to the register with no enumeration passes",
			file:    "schemas/README.md",
			content: registerReference,
		},
		{
			name:    "a directory tree inside a code fence is outside the domain",
			file:    "TESTING.md",
			content: fencedTree,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := artifactSupersessionFindings(tc.file, tc.content, register)
			if tc.reject && len(findings) == 0 {
				t.Errorf("expected the predicate to report an enumeration standing for the §28.7 artifact set, got none")
			}
			if !tc.reject {
				for _, f := range findings {
					t.Errorf("expected no finding, got %s", f)
				}
			}
		})
	}
}

// The register set is the predicate's own input, so a check that reads
// it wrongly is silently satisfiable: a set of one artifact makes every
// enumeration complete and a set read from the wrong table makes every
// enumeration a subset.
//
// diagnosis: a failure means the §28.7 row parser no longer reads the
// register's rows, so every other assertion in this file rests on the
// wrong artifact set.
//
// spec: §28.7 (wire-contract artifact register)
func TestSpec287RegisterSetIsReadFromTheRegisterRows(t *testing.T) {
	root := repoRoot(t)
	register := artifactRegisterSet(t, readRepoFile(t, root, artifactRegisterFile))
	for _, want := range []string{
		"schemas/lenny-adapter.proto",
		"schemas/lenny-adapter-jsonl.schema.json",
		"schemas/messagepart.schema.json",
		"schemas/runtime-ops-events.schema.json",
	} {
		if len(artifactMissing([]string{want}, register)) != 0 {
			t.Errorf("§28.7 register set does not carry %s; read %v", want, register)
		}
	}
	if len(register) < 4 {
		t.Errorf("§28.7 register set has %d rows, which is too few for the artifacts the schemas directory holds", len(register))
	}
}
