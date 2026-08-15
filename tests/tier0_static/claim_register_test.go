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
// `WIRED` row names a surface, that every other row names a step, and that every
// row anchors itself to the §28 heading whose statement it carries a status for.
// A register that fails any of those is one no later step can use, whatever the
// tree does.
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
)

// claimStatuses is the closed set §28.4 draws a status from. The reference
// document also uses UNVERIFIED for what it could not establish, which is not a
// statement the specification makes and carries no row.
var claimStatuses = map[string]bool{"WIRED": true, "UNWIRED": true, "ABSENT": true}

// deferralID is the form the plan gives a step: R followed by its number, with
// an optional sub-step letter.
var deferralID = regexp.MustCompile(`^R\d{1,2}[a-z]?$`)

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
// anchors is the set every row's `spec_anchor` is resolved against.
func validateClaimRegister(body []byte, anchors map[string]bool) []string {
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
		}
	}
	sort.Strings(findings)
	return findings
}

// spec: 28.4 (claim register)
// diagnosis: the claim register no longer says what §28.4 requires of it, so a
// later step cannot read it as a work queue. Either a row is malformed, a status
// is outside the closed set, a row's spec_anchor names no §28 heading, a WIRED
// row names no surface, or a row that is not WIRED names no step that closes it.
func TestClaimRegisterSaysWhatTheSpecificationRequires(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	path := filepath.Join(root, claimRegisterPath)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (the specification requires this register to exist; it is not optional)",
			claimRegisterPath, err)
	}
	for _, f := range validateClaimRegister(body, claimRegisterSpecHeadings(t, root)) {
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
	anchors := claimRegisterSpecHeadings(t, schematest.RepoRoot(t))
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
			got := validateClaimRegister([]byte(c.body), anchors)
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
      {"claim":"an implemented mechanism with no caller","status":"UNWIRED","spec_anchor":"#2853-intra-pod","surface":"pkg/x/z.go:8","deferral_id":"R22"},
      {"claim":"a specified mechanism that is not implemented","status":"ABSENT","spec_anchor":"#286-exclusivity-and-concurrency-model","surface":"spec/28_communication-channels.md","deferral_id":"R16"}]}`
	if got := validateClaimRegister([]byte(body), claimRegisterSpecHeadings(t, schematest.RepoRoot(t))); len(got) != 0 {
		t.Errorf("the validator refused a well-formed register: %q", got)
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
