// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
// Every row carries the schema rules: that it is well-formed, that its status
// is one the closed set holds, that a `WIRED` row names a surface, and one that
// carries a file path or a symbol rather than a bare line number, that every
// other row names a step the remediation plan carries, and that it anchors
// itself to the §28 heading whose statement it carries a status for. A register that fails any of those is
// one no later step can use, whatever the tree does.
//
// The credential rows are held to more than their schema. This file builds a
// reachability gate over the production trees, `adapterClientReachableRPCs`,
// which reports the non-test files under `pkg/` and `cmd/` that call an adapter
// RPC through a value of the adapter client type, and it joins the three
// per-slot credential rows to that gate's output: a row is WIRED when the RPC
// it names has a production caller and UNWIRED when it has none, and a WIRED
// row's surface must name every production file that reaches its RPC. §28.4
// draws WIRED from reachability rather than from an implemented surface, so the
// gate is what keeps those rows from asserting a reach the tree does not have.
// Rows outside the credential set carry the schema rules alone.
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

	// The two credential operations the gateway calls resolve the addressed
	// session's own lease rather than a pod-global one, so each carries its
	// own WIRED row naming the behavior rather than a request field. The
	// revocation handler resolves the same per-session file and has no
	// gateway caller, so its row is UNWIRED and is checked below.
	wanted := []string{
		"Credential rotation addressed to the session's own lease file",
		"Credential lease extension addressed to the session's own lease set",
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

	// The revocation handler addresses the session's own credential file and
	// no gateway code calls it, which is the state §28.4 labels UNWIRED. The
	// row still names the adapter path, because the claim is about what the
	// handler addresses rather than about who reaches it.
	const revocation = "Credential revocation addressed to the session's own lease file"
	if row, ok := rows[revocation]; !ok {
		t.Errorf("%s carries no row for %q", claimRegisterPath, revocation)
	} else {
		if row.Status != "UNWIRED" {
			t.Errorf("the row %q is %s; the handler is implemented and has no gateway caller, which §28.4 labels UNWIRED",
				revocation, row.Status)
		}
		if row.DeferralID == "" {
			t.Errorf("the row %q names no deferral; a row that is not WIRED hands its mechanism to a step", revocation)
		}
		if !strings.Contains(row.Surface, "pkg/adapter/") {
			t.Errorf("the row %q names surface %q, which does not reach the adapter path that implements it",
				revocation, row.Surface)
		}
	}

	// The rotation row records the landed order. The per-session file rewrite
	// happens first and the pod-wide in-flight gate runs after it, inside
	// rotateProviderFull, so a note that places the gate before the rewrite
	// would claim a stronger safety property than the adapter provides.
	// spec: §4.7.
	if row, ok := rows["Credential rotation addressed to the session's own lease file"]; ok {
		if !strings.Contains(row.Note, "rotateProviderFull") {
			t.Errorf("the rotation row's note does not name rotateProviderFull, where the in-flight gate runs: %q",
				row.Note)
		}
		if !strings.Contains(row.Note, "after that rewrite") {
			t.Errorf("the rotation row's note does not place the in-flight gate after the per-session rewrite: %q",
				row.Note)
		}
	}
}

// spec: 28.4 (claim register), 4.7 (runtime adapter)
// diagnosis: a per-slot credential row cites a §28 heading that states nothing
// about credential addressing. §28.4 has a row record how far the tree has
// reached a statement §28 makes, so the anchor names the heading that makes it.
// The statement these rows carry a status for is the CH-RUNTIMEOPS contract
// card, which states the rewrite of the addressed session's own
// /run/lenny/slots/{sessionId}/credentials.json and the in-flight gate that
// follows it. The exclusivity heading states the one-holder rule and the
// pod-level operation lock and makes no statement about credential addressing,
// so a row anchored there points a reader at a heading it does not restate.
func TestClaimRegisterCredentialRowsAnchorToTheRuntimeOpsContractCard(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, claimRegisterPath))
	if err != nil {
		t.Fatalf("%s: %v", claimRegisterPath, err)
	}
	rows, err := claimRegisterRows(body)
	if err != nil {
		t.Fatalf("%s: %v", claimRegisterPath, err)
	}

	const runtimeOpsAnchor = "#2853-intra-pod"
	for _, name := range []string{
		"Credential rotation addressed to the session's own lease file",
		"Credential lease extension addressed to the session's own lease set",
		"Credential revocation addressed to the session's own lease file",
	} {
		row, ok := rows[name]
		if !ok {
			t.Errorf("%s carries no row for %q", claimRegisterPath, name)
			continue
		}
		if row.SpecAnchor != runtimeOpsAnchor {
			t.Errorf("the row %q anchors to %q; the heading that states the adapter's per-session credential-file handling is %q",
				name, row.SpecAnchor, runtimeOpsAnchor)
		}
	}
}

// spec: 28.4 (claim register), 4.7 (runtime adapter)
// diagnosis: a credential-operation row's status disagrees with whether a
// production call site reaches the RPC. §28.4 draws the status from a closed
// set in which WIRED means the mechanism is reachable from production code and
// UNWIRED means it is implemented and has no production caller, so a row that
// claims WIRED for an RPC nothing calls records a reachability the tree does
// not have. A method the adapter client merely declares is the UNWIRED state,
// so the gate resolves the status against a caller outside the client package
// rather than against the declaration.
func TestClaimRegisterCredentialRowStatusMatchesTheGatewayCaller(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, claimRegisterPath))
	if err != nil {
		t.Fatalf("%s: %v", claimRegisterPath, err)
	}
	rows, err := claimRegisterRows(body)
	if err != nil {
		t.Fatalf("%s: %v", claimRegisterPath, err)
	}
	callers, err := adapterClientReachableRPCs(root)
	if err != nil {
		t.Fatalf("%s: %v", adapterClientDir, err)
	}

	// Each row names one adapter credential RPC. The gateway reaches the
	// adapter only through the adapter client, so the RPC is reachable from
	// production code when the client declares a method for it and some
	// non-test code outside the client package calls that method.
	rpcOfRow := map[string]string{
		"Credential rotation addressed to the session's own lease file":       "RotateCredentials",
		"Credential lease extension addressed to the session's own lease set": "ExtendCredentialLease",
		"Credential revocation addressed to the session's own lease file":     "RevokeCredentials",
	}
	for claim, rpc := range rpcOfRow {
		row, ok := rows[claim]
		if !ok {
			t.Errorf("%s carries no row for %q", claimRegisterPath, claim)
			continue
		}
		want := "UNWIRED"
		if len(callers[rpc]) > 0 {
			want = "WIRED"
		}
		if row.Status != want {
			calls := "no production code calls the adapter client's"
			if len(callers[rpc]) > 0 {
				calls = "production code calls the adapter client's"
			}
			t.Errorf("the row %q is %s; %s %s, so §28.4 makes the row %s",
				claim, row.Status, calls, rpc, want)
		}
	}
}

// spec: 28.4 (claim register), 4.7 (runtime adapter)
// diagnosis: a WIRED credential row cites the adapter client library instead
// of the production file that calls it. §28.4 has a WIRED row name the
// production surface that reaches the mechanism, and the adapter client is a
// declaration every credential RPC has, including the revocation RPC whose row
// is UNWIRED for want of a caller. A surface spelled the same way on both rows
// records a reachability its own text does not demonstrate, so the gate holds
// each WIRED row to naming every production file that reaches its RPC.
func TestClaimRegisterWiredCredentialRowNamesItsProductionCaller(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, claimRegisterPath))
	if err != nil {
		t.Fatalf("%s: %v", claimRegisterPath, err)
	}
	rows, err := claimRegisterRows(body)
	if err != nil {
		t.Fatalf("%s: %v", claimRegisterPath, err)
	}
	callers, err := adapterClientReachableRPCs(root)
	if err != nil {
		t.Fatalf("%s: %v", adapterClientDir, err)
	}

	rpcOfRow := map[string]string{
		"Credential rotation addressed to the session's own lease file":       "RotateCredentials",
		"Credential lease extension addressed to the session's own lease set": "ExtendCredentialLease",
		"Credential revocation addressed to the session's own lease file":     "RevokeCredentials",
	}
	for claim, rpc := range rpcOfRow {
		row, ok := rows[claim]
		if !ok {
			t.Errorf("%s carries no row for %q", claimRegisterPath, claim)
			continue
		}
		if row.Status != "WIRED" {
			continue
		}
		for _, caller := range callers[rpc] {
			if !strings.Contains(row.Surface, caller) {
				t.Errorf("the row %q is WIRED and names surface %q, which does not name %s, a production file that calls %s",
					claim, row.Surface, caller, rpc)
			}
		}
	}
}

// adapterClientDir is the gateway package that holds every call the gateway
// makes into the adapter's gRPC surface.
const adapterClientDir = "pkg/gateway/runtime/adapterclient"

// productionCallerRoots are the trees a production call site can live in. Test
// files under them are excluded, because a test caller does not make a
// mechanism reachable from production code.
var productionCallerRoots = []string{"pkg", "cmd"}

// adapterClientImportPath is the import path of the adapter client package. A
// file that does not import it cannot declare a value of the client type, so
// the handle scan reads declarations out of the importing files alone.
const adapterClientImportPath = "github.com/lennylabs/lenny/" + adapterClientDir

// adapterHandleSet records where production code holds an adapter client. A
// handle is harvested from a declaration in the syntax tree, so prose in a
// comment that happens to spell the client type cannot mint one.
type adapterHandleSet struct {
	// locals maps a file path to the identifiers that file declares with the
	// client type: parameters, named results, variables, and the short
	// declarations that take a client from the package constructor or a
	// dialer. A local identifier is attributed only inside its own file, so a
	// receiver named `s` in an unrelated package is never read as a client.
	locals map[string]map[string]bool
	// fields maps a struct field name of the client type to the import paths
	// of the packages that declare it. A call through a field is attributed
	// only in a file that imports the adapter client or one of those packages,
	// which is what keeps a same-named field on an unrelated struct from
	// scoring as a caller.
	fields map[string]map[string]bool
}

// adapterClientReachableRPCs reports, per adapter RPC, the repository-relative
// paths of the production files that reach it. §28.4 draws WIRED from a
// production caller rather than from an implemented surface, so a method the
// adapter client declares counts only once non-test code outside the client
// package calls it through a value of the client type. The paths are returned
// rather than a bare boolean so a register row can be held to naming the call
// site it claims reachability from.
func adapterClientReachableRPCs(root string) (map[string][]string, error) {
	methods, err := adapterClientMethods(root)
	if err != nil {
		return nil, err
	}
	handles, err := adapterClientHandles(root)
	if err != nil {
		return nil, err
	}
	reached := map[string][]string{}
	if len(methods) == 0 {
		return reached, nil
	}
	err = walkProductionGoFiles(root, func(path string, src []byte) error {
		file, err := parseProductionGoFile(path, src)
		if err != nil {
			return err
		}
		imports := goImportSet(file)
		self, err := goImportPathOf(root, path)
		if err != nil {
			return err
		}
		locals := handles.locals[path]
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relativize %s: %w", path, err)
		}
		rel = filepath.ToSlash(rel)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if !methods[sel.Sel.Name] {
				return true
			}
			if handles.holds(sel.X, locals, imports, self) {
				reached[sel.Sel.Name] = appendOnce(reached[sel.Sel.Name], rel)
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for rpc := range reached {
		sort.Strings(reached[rpc])
	}
	return reached, nil
}

// appendOnce adds path to paths when it is not already present, so a file that
// calls the same RPC twice is counted as one call site.
func appendOnce(paths []string, path string) []string {
	for _, have := range paths {
		if have == path {
			return paths
		}
	}
	return append(paths, path)
}

// holds reports whether expr names a value of the adapter client type at a
// call site in a file with the given imports and import path.
func (h *adapterHandleSet) holds(expr ast.Expr, locals map[string]bool, imports map[string]bool, self string) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return locals[e.Name]
	case *ast.SelectorExpr:
		declarers, ok := h.fields[e.Sel.Name]
		if !ok {
			return false
		}
		if imports[adapterClientImportPath] || declarers[self] {
			return true
		}
		for path := range declarers {
			if imports[path] {
				return true
			}
		}
	}
	return false
}

// adapterClientHandles reports the declarations production code holds an
// adapter client in. Only a file that imports the client package can declare
// one, and only a declaration in the syntax tree counts, so a same-named RPC
// on an unrelated stub (the Token Service's own RevokeCredentials, for one) is
// not read as a caller of the adapter's.
func adapterClientHandles(root string) (*adapterHandleSet, error) {
	handles := &adapterHandleSet{
		locals: map[string]map[string]bool{},
		fields: map[string]map[string]bool{},
	}
	err := walkProductionGoFiles(root, func(path string, src []byte) error {
		file, err := parseProductionGoFile(path, src)
		if err != nil {
			return err
		}
		pkg, ok := adapterClientPackageName(file)
		if !ok {
			return nil
		}
		self, err := goImportPathOf(root, path)
		if err != nil {
			return err
		}
		local := func(name string) {
			if name == "" || name == "_" {
				return
			}
			if handles.locals[path] == nil {
				handles.locals[path] = map[string]bool{}
			}
			handles.locals[path][name] = true
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.StructType:
				collectClientFields(node.Fields, pkg, func(name string) {
					if handles.fields[name] == nil {
						handles.fields[name] = map[string]bool{}
					}
					handles.fields[name][self] = true
				})
			case *ast.FuncType:
				collectClientFields(node.Params, pkg, local)
				collectClientFields(node.Results, pkg, local)
			case *ast.ValueSpec:
				if isAdapterClientPointer(node.Type, pkg) {
					for _, name := range node.Names {
						local(name.Name)
					}
				}
			case *ast.AssignStmt:
				if name, ok := dialedClientName(node, pkg); ok {
					local(name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return handles, nil
}

// collectClientFields reports the name of every named entry in list whose type
// is a pointer to the adapter client.
func collectClientFields(list *ast.FieldList, pkg string, emit func(name string)) {
	if list == nil {
		return
	}
	for _, field := range list.List {
		if !isAdapterClientPointer(field.Type, pkg) {
			continue
		}
		for _, name := range field.Names {
			emit(name.Name)
		}
	}
}

// isAdapterClientPointer reports whether expr spells *<pkg>.Client, the only
// form the gateway holds an adapter client in.
func isAdapterClientPointer(expr ast.Expr, pkg string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Client" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

// dialedClientName reports the identifier a short declaration binds a freshly
// dialed adapter client to, for the call sites that take one from the package
// constructor or from a dialer seam rather than from a typed declaration.
func dialedClientName(assign *ast.AssignStmt, pkg string) (string, bool) {
	if assign.Tok != token.DEFINE || len(assign.Rhs) != 1 || len(assign.Lhs) == 0 {
		return "", false
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok || !isAdapterClientConstructor(call.Fun, pkg) {
		return "", false
	}
	return identName(assign.Lhs[0])
}

// isAdapterClientConstructor reports whether fn names a call returning a
// freshly dialed adapter client: the client package's own Dial or New
// constructor, or the DialAdapter seam the binder holds one behind.
func isAdapterClientConstructor(fn ast.Expr, pkg string) bool {
	switch f := fn.(type) {
	case *ast.Ident:
		return f.Name == "DialAdapter"
	case *ast.SelectorExpr:
		if f.Sel.Name == "DialAdapter" {
			return true
		}
		ident, ok := f.X.(*ast.Ident)
		return ok && ident.Name == pkg &&
			(strings.HasPrefix(f.Sel.Name, "Dial") || strings.HasPrefix(f.Sel.Name, "New"))
	}
	return false
}

// identName reports the name of expr when it is a plain identifier.
func identName(expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

// adapterClientPackageName reports the name a file refers to the adapter
// client package by, honouring an import alias.
func adapterClientPackageName(file *ast.File) (string, bool) {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != adapterClientImportPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, spec.Name.Name != "_"
		}
		return "adapterclient", true
	}
	return "", false
}

// goImportSet reports the import paths a file names.
func goImportSet(file *ast.File) map[string]bool {
	set := map[string]bool{}
	for _, spec := range file.Imports {
		if path, err := strconv.Unquote(spec.Path.Value); err == nil {
			set[path] = true
		}
	}
	return set
}

// goImportPathOf reports the import path of the package the file at path
// belongs to.
func goImportPathOf(root, path string) (string, error) {
	rel, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("import path of %s: %w", path, err)
	}
	return goModulePath + "/" + filepath.ToSlash(rel), nil
}

// goModulePath is this repository's module path, which prefixes the import
// path of every package under it.
const goModulePath = "github.com/lennylabs/lenny"

// parseProductionGoFile parses one production file's declarations. Comments
// are discarded, so prose that spells the client type is never read as code.
func parseProductionGoFile(path string, src []byte) (*ast.File, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return file, nil
}

// walkProductionGoFiles calls visit for every non-test Go file under the
// production trees, excluding the adapter client package itself so that the
// client's own body is never read as its caller.
func walkProductionGoFiles(root string, visit func(path string, src []byte) error) error {
	skip := filepath.Join(root, filepath.FromSlash(adapterClientDir))
	for _, tree := range productionCallerRoots {
		dir := filepath.Join(root, tree)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path == skip {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return fmt.Errorf("read %s: %w", path, readErr)
			}
			return visit(path, src)
		})
		if err != nil {
			return fmt.Errorf("walk %s: %w", tree, err)
		}
	}
	return nil
}

// adapterClientMethods reports which exported methods the adapter client
// declares in non-test source, keyed by method name. A declaration alone is the
// state §28.4 labels UNWIRED; adapterClientReachableRPCs joins it to a caller.
func adapterClientMethods(root string) (map[string]bool, error) {
	entries, err := os.ReadDir(filepath.Join(root, adapterClientDir))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", adapterClientDir, err)
	}
	decl := regexp.MustCompile(`(?m)^func \(\w+ \*Client\) (\w+)\(`)
	methods := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, adapterClientDir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		for _, m := range decl.FindAllSubmatch(src, -1) {
			methods[string(m[1])] = true
		}
	}
	return methods, nil
}

// spec: 28.4 (claim register), 4.7 (runtime adapter)
// diagnosis: the register's status gate reads an adapter RPC as reachable from
// production code on the strength of the adapter client declaring a method for
// it. §28.4 makes a declared surface with no production caller UNWIRED, so a
// gate that stops at the declaration would demand WIRED for a row §28.4 makes
// UNWIRED as soon as an uncalled client wrapper is added.
func TestAdapterClientReachabilityRequiresAProductionCaller(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := writeGoTree(t, root)

	// The client declares three RPCs. Only RotateCredentials has a caller in
	// production code; ExtendCredentialLease is reached from a test alone, and
	// RevokeCredentials is declared and never called.
	write(adapterClientDir+"/client.go", `package adapterclient

type Client struct{}

func (c *Client) RotateCredentials()     {}
func (c *Client) ExtendCredentialLease() {}
func (c *Client) RevokeCredentials()     {}
`)
	write(adapterClientDir+"/client_test.go", `package adapterclient

func drive(c *Client) { c.RevokeCredentials() }
`)
	write("pkg/gateway/podlifecycle/bind.go", `package podlifecycle

import "`+adapterClientImportPath+`"

type Bind struct {
	Adapter *adapterclient.Client
}
`)
	write("cmd/gw/main.go", `package main

import "`+goModulePath+`/pkg/gateway/podlifecycle"

func renew(bind podlifecycle.Bind) { bind.Adapter.RotateCredentials() }
`)
	write("cmd/gw/main_test.go", `package main

func testExtend(bind Bind) { bind.Adapter.ExtendCredentialLease() }
`)
	// An unrelated stub carrying the same RPC name is not the adapter client,
	// so its call site does not make the adapter's RPC reachable.
	write("pkg/gateway/credentials/tokens.go", `package credentials

type assigner struct{ stub tokenServiceClient }

func (a *assigner) revoke() { a.stub.RevokeCredentials() }
`)

	reachable, err := adapterClientReachableRPCs(root)
	if err != nil {
		t.Fatalf("adapterClientReachableRPCs: %v", err)
	}
	if len(reachable["RotateCredentials"]) == 0 {
		t.Errorf("RotateCredentials has a production caller and is not reported reachable")
	}
	if got := reachable["RotateCredentials"]; len(got) != 1 || got[0] != "cmd/gw/main.go" {
		t.Errorf("RotateCredentials reports call sites %v; the sole production caller is cmd/gw/main.go", got)
	}
	for _, rpc := range []string{"ExtendCredentialLease", "RevokeCredentials"} {
		if len(reachable[rpc]) > 0 {
			t.Errorf("%s is reported reachable; it is declared with no production caller, which §28.4 labels UNWIRED", rpc)
		}
	}
}

// spec: 28.4 (claim register), 4.7 (runtime adapter)
// diagnosis: the register's status gate attributes a call site to the adapter
// client on the strength of text that is not a declaration of the client type.
// §28.4 draws WIRED from a production caller of the mechanism the row names, so
// a scan that mints a handle out of prose in a comment, or that carries a
// handle from one package into every other, reports an unrelated method call as
// a caller of the adapter's RPC and holds a row green that §28.4 makes UNWIRED.
func TestAdapterClientReachabilityIgnoresCommentTextAndForeignReceivers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := writeGoTree(t, root)

	write(adapterClientDir+"/client.go", `package adapterclient

type Client struct{}

func (c *Client) RevokeCredentials() {}
`)
	// A comment spelling the client type is prose. The apostrophe-s of
	// "binding's" is not a declaration and must not become a handle.
	write("cmd/gw/direct_usage.go", `package main

// usage defaults to the binding's *adapterclient.Client and is overridden in
// the tests.
type usage struct{}
`)
	// An unrelated service declares the same RPC name on its own receiver. The
	// receiver is named s, which is the identifier the comment above would have
	// minted, and this package holds no adapter client at all.
	write("pkg/tokenservice/grpc.go", `package tokenservice

type GRPCServer struct{}

func (s *GRPCServer) RevokeCredentials() {}

func (s *GRPCServer) sweep() { s.RevokeCredentials() }
`)

	reachable, err := adapterClientReachableRPCs(root)
	if err != nil {
		t.Fatalf("adapterClientReachableRPCs: %v", err)
	}
	if len(reachable["RevokeCredentials"]) > 0 {
		t.Errorf("RevokeCredentials is reported reachable; the only call site is the Token Service's own method, and §28.4 makes an adapter RPC with no production caller UNWIRED")
	}

	handles, err := adapterClientHandles(root)
	if err != nil {
		t.Fatalf("adapterClientHandles: %v", err)
	}
	for path, idents := range handles.locals {
		for ident := range idents {
			t.Errorf("%s yields the adapter client handle %q; no file in this tree declares one", path, ident)
		}
	}
}

// writeGoTree returns a helper that writes one Go source file into root.
func writeGoTree(t *testing.T, root string) func(rel, body string) {
	t.Helper()
	return func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}
