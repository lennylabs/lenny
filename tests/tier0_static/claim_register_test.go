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
// `WIRED` row names a surface, and that every other row names a step. A register
// that fails any of those is one no later step can use, whatever the tree does.
//
// The validator fails a missing or unparseable register rather than skipping.
// A register that cannot be read excuses nothing, and a gate that passes because
// it found no file is a gate that reports success for the one state it exists to
// prevent.

const claimRegisterPath = "tests/claim-map.json"

// claimStatuses is the closed set §28.4 draws a status from. The reference
// document also uses UNVERIFIED for what it could not establish, which is not a
// statement the specification makes and carries no row.
var claimStatuses = map[string]bool{"WIRED": true, "UNWIRED": true, "ABSENT": true}

// deferralID is the form the plan gives a step: R followed by its number, with
// an optional sub-step letter.
var deferralID = regexp.MustCompile(`^R\d{1,2}[a-z]?$`)

// claim is one row of the register.
type claim struct {
	Claim      string `json:"claim"`
	Status     string `json:"status"`
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

// validateClaimRegister returns the findings the register's own text supports.
// It is separated from the test so the cases below can drive it over fixtures.
func validateClaimRegister(body []byte) []string {
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
// is outside the closed set, a WIRED row names no surface, or a row that is not
// WIRED names no step that closes it.
func TestClaimRegisterSaysWhatTheSpecificationRequires(t *testing.T) {
	t.Parallel()
	path := filepath.Join(schematest.RepoRoot(t), claimRegisterPath)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (the specification requires this register to exist; it is not optional)",
			claimRegisterPath, err)
	}
	for _, f := range validateClaimRegister(body) {
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
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"UNVERIFIED","surface":"x"}]}`,
			"outside the closed set",
		},
		{
			"wired row naming no surface",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"WIRED"}]}`,
			"names no surface",
		},
		{
			"unwired row naming no step",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"UNWIRED","surface":"x"}]}`,
			"names no step that closes it",
		},
		{
			"deferral that is not a step identifier",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"ABSENT","surface":"x","deferral_id":"later"}]}`,
			"not a step identifier",
		},
		{
			"wired row carrying a deferral",
			`{"kind":"claim-register","version":1,"claims":[{"claim":"a","status":"WIRED","surface":"x","deferral_id":"R12"}]}`,
			"no step left to close it",
		},
		{
			"one mechanism carrying two rows",
			`{"kind":"claim-register","version":1,"claims":[` +
				`{"claim":"a","status":"WIRED","surface":"x"},` +
				`{"claim":"a","status":"WIRED","surface":"y"}]}`,
			"appears more than once",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validateClaimRegister([]byte(c.body))
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
      {"claim":"a wired mechanism","status":"WIRED","surface":"pkg/x/y.go:12"},
      {"claim":"an implemented mechanism with no caller","status":"UNWIRED","surface":"pkg/x/z.go:8","deferral_id":"R22"},
      {"claim":"a specified mechanism that is not implemented","status":"ABSENT","surface":"spec/28_communication-channels.md","deferral_id":"R16"}]}`
	if got := validateClaimRegister([]byte(body)); len(got) != 0 {
		t.Errorf("the validator refused a well-formed register: %q", got)
	}
}
