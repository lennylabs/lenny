// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The claim register validator reads `tests/claim-map.json` and checks that it
// says what §28.4 requires a claim to say. §28.4 makes every normative statement
// §28 makes about a mechanism carry a row here, so the specification cannot
// assert a mechanism that does not run, and a row that is not `WIRED` names the
// step that closes it, which is what makes the register the work queue for the
// steps that follow.
//
// The check is deliberately schema-only. Whether a `WIRED` row's named surface
// is genuinely reachable from production code is a different question, answered
// by a reachability gate this tree does not yet build; joining a row to that
// gate's output is the work its own step carries. What is checkable here is that
// every row is well-formed, that its status is one the closed set holds, that a
// `WIRED` row names a surface, and one that carries a file path or a symbol
// rather than a bare line number, that every other row names a step the
// remediation plan carries, and that every row anchors itself to the §28 heading
// whose statement it carries a status for. A register that fails any of those is
// one no later step can use, whatever the tree does.
//
// A deferral is resolved against the plan rather than against its spelling. A
// row that is not `WIRED` exists to hand the mechanism to the step that closes
// it, so an identifier of the right form naming a step no plan declares hands
// the mechanism to nobody, and the register stops being a work queue at that
// row.
//
// The anchor is checked by resolution against the headings §28 declares, because
// a row exists to record how far the tree has reached a statement §28 makes, and
// a row that cites no reachable heading leaves a reader unable to find the
// statement. An empty anchor resolves to no heading and fails the same rule as a
// misspelled one.
//
// The validator fails a missing or unparseable register rather than skipping.
// A register that cannot be read excuses nothing, and a gate that passes because
// it found no file is a gate that reports success for the one state it exists to
// prevent.

const (
	claimRegisterPath = "tests/claim-map.json"

	// claimRegisterSpecPath is the section whose statements the register carries
	// a status for. A row's `spec_anchor` names a heading of this document.
	claimRegisterSpecPath = "spec/28_communication-channels.md"

	// remediationPlanPath is the plan whose steps a row that is not WIRED hands
	// its mechanism to. A row's `deferral_id` names a step this document declares.
	remediationPlanPath = "gateway-runtime-comms-remediation.md"
)

// claimStatuses is the closed set §28.4 draws a status from. The reference
// document also uses UNVERIFIED for what it could not establish, which is not a
// statement the specification makes and carries no row.
var claimStatuses = map[string]bool{"WIRED": true, "UNWIRED": true, "ABSENT": true}

// deferralID is the form the plan gives a step: R followed by its number, with
// an optional sub-step letter. The form is the cheap half of the rule; a
// deferral also has to name a step the plan declares, which is resolved against
// the set remediationPlanSteps reads.
var deferralID = regexp.MustCompile(`^R\d{1,2}[a-z]?$`)

// planStepID matches a step identifier wherever the plan writes one.
var planStepID = regexp.MustCompile(`\bR\d{1,2}[a-z]?\b`)

// planStepRow matches a row of the plan's edge list, whose first column is the
// step the row states the dependencies of.
var planStepRow = regexp.MustCompile(`^\|\s*(R\d{1,2}[a-z]?)\s*\|`)

// remediationPlanStepsIn returns every step identifier a plan document declares.
// A step is declared by its own heading or by its row in the plan's edge list,
// which are the two places the plan enumerates its steps rather than refers to
// one. Prose is deliberately outside the scan: a step is a unit of work the plan
// commits to, and a mention in a sentence is not that commitment. Lines inside a
// fenced block are skipped for the same reason the anchor scan skips them.
func remediationPlanStepsIn(body []byte) map[string]bool {
	steps := map[string]bool{}
	fenced := false
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if strings.HasPrefix(line, "#") {
			// A step heading names its step anywhere in the line, because the
			// plan writes some as a trailing parenthetical and others as the
			// leading token.
			for _, id := range planStepID.FindAllString(line, -1) {
				steps[id] = true
			}
			continue
		}
		if m := planStepRow.FindStringSubmatch(line); m != nil {
			steps[m[1]] = true
		}
	}
	return steps
}

// remediationPlanSteps reads the plan's steps from the tree, so a row resolves
// against the steps that exist rather than against a list the test maintains
// beside the plan and lets drift.
func remediationPlanSteps(t *testing.T, root string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, remediationPlanPath))
	if err != nil {
		t.Fatalf("%s: %v (a row's deferral_id resolves against this document)", remediationPlanPath, err)
	}
	steps := remediationPlanStepsIn(body)
	if len(steps) == 0 {
		t.Fatalf("%s declares no steps; every deferred row would fail against an empty step set",
			remediationPlanPath)
	}
	return steps
}

// bareLineSurface matches a surface that is only a line number or a line range,
// carrying no file path and no symbol: `12`, `:141-147`, `lines 71-79`. Such a
// surface names nowhere a reader can go, because the file the line belongs to is
// the part that is missing. The match is against the whole string, so the
// `path:line` form is unaffected and so is a compound surface that cites a line
// fragment alongside a file it has already named.
var bareLineSurface = regexp.MustCompile(`^(?i:lines?\s+)?:?\d+(\s*[-–]\s*:?\d+)?$`)

// surfaceNamesOnlyALine reports whether a surface reduces to a bare line
// reference once the markdown code fencing the register writes surfaces in is
// removed.
func surfaceNamesOnlyALine(surface string) bool {
	return bareLineSurface.MatchString(strings.TrimSpace(strings.Trim(strings.TrimSpace(surface), "`")))
}

// claim is one row of the register. SpecAnchor is the fragment naming the §28
// heading that states the mechanism, in the `#slug` form a markdown link to that
// heading takes, per the citation rule in §28.1 that a citation names a heading.
type claim struct {
	Claim      string `json:"claim"`
	Status     string `json:"status"`
	SpecAnchor string `json:"spec_anchor"`
	Surface    string `json:"surface"`
	DeferralID string `json:"deferral_id"`
	Note       string `json:"note"`
}

// claimRegister is the register as written. Claims is a pointer so a document
// declaring no claims block is distinguishable from one declaring an empty
// list; both are refused, because a register that carries no claim asserts
// nothing and would pass every rule below.
type claimRegister struct {
	Kind    string   `json:"kind"`
	Version int      `json:"version"`
	Claims  *[]claim `json:"claims"`
}

// claimRegisterSpecAnchors returns the anchor of every heading a markdown
// document declares, keyed without the leading `#`. Lines inside a fenced block
// are skipped, because the boundary figures the contract cards carry draw their
// frames with characters a heading scan would otherwise read as a hash.
func claimRegisterSpecAnchors(body []byte) map[string]bool {
	anchors := map[string]bool{}
	fenced := false
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced || !strings.HasPrefix(line, "#") {
			continue
		}
		title := strings.TrimLeft(line, "#")
		if title == line || strings.TrimSpace(title) == "" {
			continue
		}
		anchors[headingSlug(title)] = true
	}
	return anchors
}

// claimRegisterSpecHeadings reads the anchors §28 declares from the tree, so the
// cases resolve a row against the headings that exist rather than against a set
// the test supplies for itself.
func claimRegisterSpecHeadings(t *testing.T, root string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, claimRegisterSpecPath))
	if err != nil {
		t.Fatalf("%s: %v (a row's spec_anchor resolves against this document)", claimRegisterSpecPath, err)
	}
	anchors := claimRegisterSpecAnchors(body)
	if len(anchors) == 0 {
		t.Fatalf("%s declares no headings; every row would fail against an empty anchor set",
			claimRegisterSpecPath)
	}
	return anchors
}

// validateClaimRegister returns the findings the register's own text supports.
// It is separated from the test so the cases below can drive it over fixtures.
// anchors is the set every row's `spec_anchor` is resolved against, and steps is
// the set every deferred row's `deferral_id` is resolved against.
func validateClaimRegister(body []byte, anchors, steps map[string]bool) []string {
	var doc claimRegister
	if err := json.Unmarshal(body, &doc); err != nil {
		return []string{fmt.Sprintf("the register does not parse as JSON: %v", err)}
	}
	if doc.Kind != "claim-register" {
		return []string{fmt.Sprintf("kind is %q, want \"claim-register\"", doc.Kind)}
	}
	if doc.Version != 1 {
		return []string{fmt.Sprintf("version is %d, want 1", doc.Version)}
	}
	if doc.Claims == nil {
		return []string{"the register declares no claims block; an absent block and an empty one are not the same claim"}
	}
	if len(*doc.Claims) == 0 {
		return []string{"the register declares no claims; a register that carries none asserts nothing"}
	}

	var findings []string
	seen := map[string]bool{}
	for i, c := range *doc.Claims {
		where := fmt.Sprintf("claim %d", i)
		if strings.TrimSpace(c.Claim) == "" {
			findings = append(findings, where+" names no mechanism")
			continue
		}
		where = fmt.Sprintf("%q", c.Claim)
		if seen[c.Claim] {
			findings = append(findings, where+" appears more than once; a mechanism carries one row")
		}
		seen[c.Claim] = true

		if anchor := strings.TrimSpace(c.SpecAnchor); !strings.HasPrefix(anchor, "#") ||
			!anchors[strings.TrimPrefix(anchor, "#")] {
			findings = append(findings, fmt.Sprintf(
				"%s carries spec_anchor %q, which does not resolve to a heading of %s",
				where, c.SpecAnchor, claimRegisterSpecPath,
			))
		}

		if !claimStatuses[c.Status] {
			findings = append(findings, fmt.Sprintf(
				"%s carries status %q, which is outside the closed set (WIRED, UNWIRED, ABSENT)",
				where, c.Status,
			))
			continue
		}
		if strings.TrimSpace(c.Surface) == "" {
			findings = append(findings, where+" names no surface; every row cites where the mechanism is")
		}
		if c.Status == "WIRED" {
			if surfaceNamesOnlyALine(c.Surface) {
				findings = append(findings, fmt.Sprintf(
					"%s is WIRED and carries surface %q, which is only a line reference; a wired surface names a file or a symbol",
					where, c.Surface,
				))
			}
			if c.DeferralID != "" {
				findings = append(findings, fmt.Sprintf(
					"%s is WIRED and names deferral %q; a wired mechanism has no step left to close it",
					where, c.DeferralID,
				))
			}
			continue
		}
		if c.DeferralID == "" {
			findings = append(findings, fmt.Sprintf(
				"%s is %s and names no step that closes it", where, c.Status,
			))
		} else if !deferralID.MatchString(c.DeferralID) {
			findings = append(findings, fmt.Sprintf(
				"%s names deferral %q, which is not a step identifier", where, c.DeferralID,
			))
		} else if !steps[c.DeferralID] {
			findings = append(findings, fmt.Sprintf(
				"%s names deferral %q, which %s declares no step for",
				where, c.DeferralID, remediationPlanPath,
			))
		}
	}
	sort.Strings(findings)
	return findings
}

// spec: 28.4 (claim register)
// diagnosis: the claim register no longer says what §28.4 requires of it, so a
// later step cannot read it as a work queue. Either a row is malformed, a status
// is outside the closed set, a row's spec_anchor names no §28 heading, a WIRED
// row names no surface or names one that is only a line reference, or a row that
// is not WIRED names no step that closes it or names one the remediation plan
// does not declare.
func TestClaimRegisterSaysWhatTheSpecificationRequires(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	path := filepath.Join(root, claimRegisterPath)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (the specification requires this register to exist; it is not optional)",
			claimRegisterPath, err)
	}
	for _, f := range validateClaimRegister(body, claimRegisterSpecHeadings(t, root), remediationPlanSteps(t, root)) {
		t.Errorf("%s: %s", claimRegisterPath, f)
	}
	var doc claimRegister
	if err := json.Unmarshal(body, &doc); err == nil && doc.Claims != nil {
		byStatus := map[string]int{}
		for _, c := range *doc.Claims {
			byStatus[c.Status]++
		}
		t.Logf("%d claim(s): %d WIRED, %d UNWIRED, %d ABSENT",
			len(*doc.Claims), byStatus["WIRED"], byStatus["UNWIRED"], byStatus["ABSENT"])
	}
}

// spec: 28.4 (claim register)
// diagnosis: the validator stopped reporting a register it should refuse, so a
// malformed register would be accepted and the work queue silently lost.
func TestClaimRegisterValidatorRefusesAnUnusableRegister(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	anchors := claimRegisterSpecHeadings(t, root)
	steps := remediationPlanSteps(t, root)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"not JSON", "{", "does not parse"},
		{"wrong kind", `{"kind":"something","version":1,"claims":[]}`, "want \"claim-register\""},
		{"wrong version", `{"kind":"claim-register","version":2,"claims":[]}`, "want 1"},
		{"no claims block", `{"kind":"claim-register","version":1}`, "declares no claims block"},
		{"empty claims", `{"kind":"claim-register","version":1,"claims":[]}`, "carries none asserts nothing"},
		{
			"status outside the set",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"UNVERIFIED","spec_anchor":"#2851-gateway-to-pod","surface":"x"}]}`,
			"outside the closed set",
		},
		{
			"wired row naming no surface",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"WIRED","spec_anchor":"#2851-gateway-to-pod"}]}`,
			"names no surface",
		},
		{
			"wired row whose surface is a bare line number",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"WIRED","spec_anchor":"#2851-gateway-to-pod","surface":"12"}]}`,
			"only a line reference",
		},
		{
			"wired row whose surface is a bare line range",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"WIRED","spec_anchor":"#2851-gateway-to-pod","surface":"` + "`" + `:141-147` + "`" + `"}]}`,
			"only a line reference",
		},
		{
			"wired row whose surface is a bare line span written out",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"WIRED","spec_anchor":"#2851-gateway-to-pod","surface":"lines 71-79"}]}`,
			"only a line reference",
		},
		{
			"unwired row naming no step",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"UNWIRED","spec_anchor":"#2851-gateway-to-pod","surface":"x"}]}`,
			"names no step that closes it",
		},
		{
			"deferral that is not a step identifier",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"ABSENT","spec_anchor":"#2851-gateway-to-pod","surface":"x","deferral_id":"later"}]}`,
			"not a step identifier",
		},
		{
			"deferral of the right form naming a step the plan does not declare",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"ABSENT","spec_anchor":"#2851-gateway-to-pod","surface":"x","deferral_id":"R99"}]}`,
			"declares no step for",
		},
		{
			"deferral naming a sub-step the plan does not declare",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"UNWIRED","spec_anchor":"#2851-gateway-to-pod","surface":"x","deferral_id":"R16c"}]}`,
			"declares no step for",
		},
		{
			"wired row carrying a deferral",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"WIRED","spec_anchor":"#2851-gateway-to-pod","surface":"x","deferral_id":"R12"}]}`,
			"no step left to close it",
		},
		{
			"one mechanism carrying two rows",
			`{"kind":"claim-register","version":1,"claims":[` +
				`{"claim":"a","status":"WIRED","spec_anchor":"#2851-gateway-to-pod","surface":"x"},` +
				`{"claim":"a","status":"WIRED","spec_anchor":"#2851-gateway-to-pod","surface":"y"}]}`,
			"appears more than once",
		},
		{
			"anchor naming no heading of the section",
			`{"kind":"claim-register","version":1,"claims":[` +
				`{"claim":"a","status":"WIRED","spec_anchor":"#28999-a-heading-nothing-declares","surface":"x"}]}`,
			"does not resolve to a heading",
		},
		{
			"anchor of a heading another document declares",
			`{"kind":"claim-register","version":1,"claims":[` +
				`{"claim":"a","status":"WIRED","spec_anchor":"#101-horizontal-scaling","surface":"x"}]}`,
			"does not resolve to a heading",
		},
		{
			"row carrying no anchor",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"WIRED","surface":"x"}]}`,
			"does not resolve to a heading",
		},
		{
			"anchor written without the fragment marker",
			`{"kind":"claim-register","version":1,"claims":[` +
				`{"claim":"a","status":"WIRED","spec_anchor":"2851-gateway-to-pod","surface":"x"}]}`,
			"does not resolve to a heading",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validateClaimRegister([]byte(c.body), anchors, steps)
			if len(got) == 0 {
				t.Fatalf("the validator accepted a register it must refuse")
			}
			if !strings.Contains(strings.Join(got, "\n"), c.want) {
				t.Errorf("findings %q do not name %q", got, c.want)
			}
		})
	}
}

// spec: 28.4 (claim register)
// diagnosis: the validator started refusing a register that satisfies §28.4, so
// a correct register cannot be landed.
func TestClaimRegisterValidatorAcceptsAWellFormedRegister(t *testing.T) {
	t.Parallel()
	body := `{"kind":"claim-register","version":1,"claims":[
      {"claim":"a wired mechanism","status":"WIRED","spec_anchor":"#2851-gateway-to-pod","surface":"pkg/x/y.go:12"},
      {"claim":"a wired mechanism cited by a range","status":"WIRED","spec_anchor":"#2851-gateway-to-pod","surface":"` + "`" + `pkg/x/y.go:60-79` + "`" + `"},
      {"claim":"a wired mechanism cited at two sites in one file","status":"WIRED","spec_anchor":"#2851-gateway-to-pod","surface":"` + "`" + `pkg/x/y.go:78` + "`" + `, escalation ` + "`" + `:141-147` + "`" + `"},
      {"claim":"a wired mechanism cited by a symbol","status":"WIRED","spec_anchor":"#2851-gateway-to-pod","surface":"` + "`" + `InterReplicaAddress` + "`" + `"},
      {"claim":"an implemented mechanism with no caller","status":"UNWIRED","spec_anchor":"#2853-intra-pod","surface":"pkg/x/z.go:8","deferral_id":"R22"},
      {"claim":"a specified mechanism that is not implemented","status":"ABSENT","spec_anchor":"#286-exclusivity-and-concurrency-model","surface":"spec/28_communication-channels.md","deferral_id":"R16"},
      {"claim":"a mechanism deferred to a sub-step","status":"ABSENT","spec_anchor":"#286-exclusivity-and-concurrency-model","surface":"spec/28_communication-channels.md","deferral_id":"R11a"}]}`
	root := schematest.RepoRoot(t)
	got := validateClaimRegister([]byte(body), claimRegisterSpecHeadings(t, root), remediationPlanSteps(t, root))
	if len(got) != 0 {
		t.Errorf("the validator refused a well-formed register: %q", got)
	}
}

// spec: 28.4 (claim register)
// diagnosis: the step set a deferred row resolves against no longer comes from
// the remediation plan. A set that holds every string accepts a deferral that
// hands the mechanism to nobody, and a set that holds none rejects every
// deferred row, so the membership rule reports on the register in neither case.
func TestClaimRegisterDeferralsComeFromThePlansSteps(t *testing.T) {
	t.Parallel()
	steps := remediationPlanSteps(t, schematest.RepoRoot(t))
	for _, want := range []string{"R1a", "R1b", "R2b", "R10", "R11a", "R16", "R25"} {
		if !steps[want] {
			t.Errorf("%s declares step %q and the step set does not hold it", remediationPlanPath, want)
		}
	}
	for _, unwanted := range []string{"R99", "R16c", "R0a", ""} {
		if steps[unwanted] {
			t.Errorf("the step set holds %q, which %s declares no step for",
				unwanted, remediationPlanPath)
		}
	}

	// A step is declared by its heading or by its edge-list row. A mention in a
	// sentence, in a fenced figure, or in a table column that is not the first
	// is a reference to a step rather than the declaration of one.
	doc := "### R3. Tooling\n\n```\n### R98. inside a fence\n```\n\n" +
		"| Step | Depends on |\n|:--|:--|\n| R4 | R97 |\n\n" +
		"R96 is discussed here.\n\n## 3. Step 1: naming (R1)\n"
	got := remediationPlanStepsIn([]byte(doc))
	for _, want := range []string{"R3", "R4", "R1"} {
		if !got[want] {
			t.Errorf("the step %q was declared and was not read: %v", want, got)
		}
	}
	for _, unwanted := range []string{"R98", "R97", "R96"} {
		if got[unwanted] {
			t.Errorf("%q was referred to rather than declared and was read as a step: %v", unwanted, got)
		}
	}
}

// spec: 28.4 (claim register), 28.1 (naming law)
// diagnosis: the anchor set a row resolves against no longer comes from the
// headings §28 declares. A set that holds every string accepts an anchor that
// leads nowhere, and a set that holds none rejects every row, so the resolution
// rule reports on the register in neither case.
func TestClaimRegisterAnchorsComeFromTheSectionsHeadings(t *testing.T) {
	t.Parallel()
	anchors := claimRegisterSpecHeadings(t, schematest.RepoRoot(t))
	for _, want := range []string{
		"284-claim-register",
		"2851-gateway-to-pod",
		"2857-gateway-to-store",
		"register-entry-register",
	} {
		if !anchors[want] {
			t.Errorf("%s declares a heading anchored %q and the anchor set does not hold it",
				claimRegisterSpecPath, want)
		}
	}
	for _, unwanted := range []string{
		"101-horizontal-scaling",
		"2851-gateway-to-pod-",
		"",
	} {
		if anchors[unwanted] {
			t.Errorf("the anchor set holds %q, which %s declares no heading for",
				unwanted, claimRegisterSpecPath)
		}
	}

	fenced := "## 28. Communication Channels\n\n```\n#### not a heading\n```\n\n### 28.1 Naming law\n"
	got := claimRegisterSpecAnchors([]byte(fenced))
	if got["not-a-heading"] {
		t.Errorf("a line inside a fenced figure was read as a heading: %v", got)
	}
	if !got["281-naming-law"] || !got["28-communication-channels"] {
		t.Errorf("the headings outside the fenced figure were not read: %v", got)
	}
}

// spec: 28.4 (claim register), 4.7 (runtime adapter), 10.1 (gateway horizontal
// scaling)
// diagnosis: the register no longer records the per-session addressing the
// adapter implements. Either the credential operations' rows are missing or
// carry a status other than WIRED, or the checkpoint restore row still records
// the pod-global extraction and the deferral that went with it. A register that
// understates what the tree does sends a later step to re-close a closed gap.
func TestClaimRegisterRecordsPerSessionAddressingOfCredentialsAndRestore(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile(filepath.Join(schematest.RepoRoot(t), claimRegisterPath))
	if err != nil {
		t.Fatalf("%s: %v", claimRegisterPath, err)
	}
	rows, err := claimRegisterRows(body)
	if err != nil {
		t.Fatalf("%s: %v", claimRegisterPath, err)
	}

	// Each credential operation resolves the addressed session's own lease
	// rather than a pod-global one, so each carries its own WIRED row naming
	// the behavior rather than a request field.
	wanted := []string{
		"Credential rotation addressed to the session's own lease file",
		"Credential lease extension addressed to the session's own lease set",
		"Credential revocation addressed to the session's own lease file",
		// The restore resolves its checkpoint roots from the request's
		// session identifier, so the row that recorded the pod-global
		// extraction is WIRED with no step left to close it.
		"Checkpoint restore onto a concurrent pod",
	}
	for _, name := range wanted {
		row, ok := rows[name]
		if !ok {
			t.Errorf("%s carries no row for %q", claimRegisterPath, name)
			continue
		}
		if row.Status != "WIRED" {
			t.Errorf("the row %q is %s; the adapter implements the behavior it names",
				name, row.Status)
		}
		if row.DeferralID != "" {
			t.Errorf("the row %q names deferral %q; a wired mechanism has no step left to close it",
				name, row.DeferralID)
		}
		if !strings.Contains(row.Surface, "pkg/adapter/") {
			t.Errorf("the row %q names surface %q, which does not reach the adapter path that implements it",
				name, row.Surface)
		}
	}
}
