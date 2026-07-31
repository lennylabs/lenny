// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeYAML drops a file under t.TempDir() and returns the path.
func writeYAML(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// expectFail asserts a check failed and the detail contains every
// substring in want. expectPass asserts OK status.
func expectFail(t *testing.T, r checkResult, want ...string) {
	t.Helper()
	if r.ok {
		t.Fatalf("expected fail, got ok: %s", r.detail)
	}
	for _, w := range want {
		if !strings.Contains(r.detail, w) {
			t.Errorf("detail missing %q: %s", w, r.detail)
		}
	}
}

func expectPass(t *testing.T, r checkResult) {
	t.Helper()
	if !r.ok {
		t.Fatalf("expected ok, got fail: %s", r.detail)
	}
}

// ---- groups.yaml --------------------------------------------------

func TestValidateGroupsYAMLHappy(t *testing.T) {
	path := writeYAML(t, "groups.yaml", `
version: 1
groups:
  pr-fast:
    description: Fast PR feedback.
    selectors:
      changed: true
      max_tier: component
  phase-0-gate:
    selectors:
      tiers: [static, docs]
`)
	expectPass(t, validateGroupsYAML(path))
}

func TestValidateGroupsYAMLMissingVersion(t *testing.T) {
	path := writeYAML(t, "groups.yaml", `groups: {}`)
	expectFail(t, validateGroupsYAML(path), "expected version 1")
}

func TestValidateGroupsYAMLEmptySelectors(t *testing.T) {
	path := writeYAML(t, "groups.yaml", `
version: 1
groups:
  broken:
    description: Has no selectors.
    selectors: {}
`)
	expectFail(t, validateGroupsYAML(path), "broken", "empty")
}

func TestValidateGroupsYAMLUnknownTier(t *testing.T) {
	path := writeYAML(t, "groups.yaml", `
version: 1
groups:
  bad-tier:
    description: References a tier that does not exist.
    selectors:
      max_tier: nonexistent
`)
	expectFail(t, validateGroupsYAML(path), "nonexistent")
}

func TestValidateGroupsYAMLDescriptionRequiredForNonGate(t *testing.T) {
	path := writeYAML(t, "groups.yaml", `
version: 1
groups:
  nightly:
    selectors:
      tiers: [unit]
`)
	expectFail(t, validateGroupsYAML(path), "nightly", "missing description")
}

func TestValidateGroupsYAMLPhaseGateSkipsDescription(t *testing.T) {
	path := writeYAML(t, "groups.yaml", `
version: 1
groups:
  phase-7-gate:
    selectors:
      tiers: [unit]
`)
	expectPass(t, validateGroupsYAML(path))
}

func TestValidateGroupsYAMLMissing(t *testing.T) {
	expectFail(t, validateGroupsYAML("/nonexistent/path"), "could not read")
}

// ---- groups.subsets.yaml ------------------------------------------

func TestValidateGroupsSubsetsYAMLHappy(t *testing.T) {
	path := writeYAML(t, "groups.subsets.yaml", `
version: 1
subsets:
  critical-path:
    tier: e2e_kind
    tests:
      - tests/tier5_e2e_kind/foo_test.go
  bundled-runtimes:
    tier: conformance
    runtimes: [echo]
  load-ref:
    tier: load_kind
    scenarios_ref: full-system
  load-with-scenarios:
    tier: load_kind
    scenarios:
      - tests/tier7b_load_kind/scenarios/foo.go
  load-local-ref:
    tier: load_local
    scenarios:
      - tests/tier7a_load_local/scenarios/foo/scenario.go
  load-cloud-ref:
    tier: load_cloud
    scenarios:
      - tests/tier12_load_cloud/scenarios/foo/main.js
  matrix:
    tier: integration
    run: TestX|TestY
`)
	expectPass(t, validateGroupsSubsetsYAML(path))
}

func TestValidateGroupsSubsetsYAMLNoContent(t *testing.T) {
	path := writeYAML(t, "groups.subsets.yaml", `
version: 1
subsets:
  empty:
    tier: unit
`)
	expectFail(t, validateGroupsSubsetsYAML(path), "empty", "no tests")
}

func TestValidateGroupsSubsetsYAMLUnknownTier(t *testing.T) {
	path := writeYAML(t, "groups.subsets.yaml", `
version: 1
subsets:
  wrong:
    tier: madeup
    tests: [foo]
`)
	expectFail(t, validateGroupsSubsetsYAML(path), "wrong", "madeup")
}

func TestValidateGroupsSubsetsYAMLAcceptsAllSentinel(t *testing.T) {
	path := writeYAML(t, "groups.subsets.yaml", `
version: 1
subsets:
  full-cloud:
    tier: e2e_cloud
    tests: all
`)
	expectPass(t, validateGroupsSubsetsYAML(path))
}

// ---- spec-map-exceptions.yaml -------------------------------------

func TestValidateSpecMapExceptionsHappy(t *testing.T) {
	path := writeYAML(t, "spec-map-exceptions.yaml", `
version: 1
exceptions:
  - section: "1"
    reason: non-normative
    justification: Executive summary.
  - section: "20"
    reason: empty
    justification: All open questions resolved.
  - section: "27"
    reason: post-v1
    justification: Web playground is post-v1.
`)
	expectPass(t, validateSpecMapExceptionsYAML(path))
}

func TestValidateSpecMapExceptionsUnknownReason(t *testing.T) {
	path := writeYAML(t, "spec-map-exceptions.yaml", `
version: 1
exceptions:
  - section: "1"
    reason: handwave
    justification: just because
`)
	expectFail(t, validateSpecMapExceptionsYAML(path), "handwave")
}

func TestValidateSpecMapExceptionsMissingJustification(t *testing.T) {
	path := writeYAML(t, "spec-map-exceptions.yaml", `
version: 1
exceptions:
  - section: "1"
    reason: non-normative
`)
	expectFail(t, validateSpecMapExceptionsYAML(path), "1", "justification")
}

func TestValidateSpecMapExceptionsDuplicateSection(t *testing.T) {
	path := writeYAML(t, "spec-map-exceptions.yaml", `
version: 1
exceptions:
  - section: "1"
    reason: non-normative
    justification: First entry.
  - section: "1"
    reason: non-normative
    justification: Second entry.
`)
	expectFail(t, validateSpecMapExceptionsYAML(path), "1", "already declared")
}

// ---- flake-budget.yaml --------------------------------------------

func TestValidateFlakeBudgetHappy(t *testing.T) {
	future := time.Now().UTC().AddDate(0, 0, 30).Format("2006-01-02")
	path := writeYAML(t, "flake-budget.yaml", fmt.Sprintf(`
version: 1
quarantined:
  - test: TestFlaky
    package: pkg/foo
    owner: alice
    issue: https://github.com/lennylabs/lenny/issues/42
    eta: %s
    root_cause: flaky-time
`, future))
	expectPass(t, validateFlakeBudgetYAML(path))
}

func TestValidateFlakeBudgetEmpty(t *testing.T) {
	path := writeYAML(t, "flake-budget.yaml", `
version: 1
quarantined: []
`)
	r := validateFlakeBudgetYAML(path)
	expectPass(t, r)
	if !strings.Contains(r.detail, "0 quarantined") {
		t.Errorf("detail should say 0 quarantined: %s", r.detail)
	}
}

func TestValidateFlakeBudgetExpiredETA(t *testing.T) {
	past := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	path := writeYAML(t, "flake-budget.yaml", fmt.Sprintf(`
version: 1
quarantined:
  - test: TestStale
    issue: https://github.com/lennylabs/lenny/issues/1
    eta: %s
`, past))
	expectFail(t, validateFlakeBudgetYAML(path), "has passed")
}

func TestValidateFlakeBudgetBadIssueURL(t *testing.T) {
	future := time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02")
	path := writeYAML(t, "flake-budget.yaml", fmt.Sprintf(`
version: 1
quarantined:
  - test: TestX
    issue: http://example.com/issue/1
    eta: %s
`, future))
	expectFail(t, validateFlakeBudgetYAML(path), "https")
}

func TestValidateFlakeBudgetBadETAFormat(t *testing.T) {
	path := writeYAML(t, "flake-budget.yaml", `
version: 1
quarantined:
  - test: TestX
    issue: https://github.com/lennylabs/lenny/issues/1
    eta: tomorrow
`)
	expectFail(t, validateFlakeBudgetYAML(path), "tomorrow")
}

func TestValidateFlakeBudgetAbsent(t *testing.T) {
	r := validateFlakeBudgetYAML(filepath.Join(t.TempDir(), "missing.yaml"))
	expectPass(t, r)
	if !strings.Contains(r.detail, "absent") {
		t.Errorf("detail should say absent: %s", r.detail)
	}
}

// ---- parity-matrix.yaml -------------------------------------------

func TestValidateParityMatrixHappy(t *testing.T) {
	path := writeYAML(t, "parity-matrix.yaml", `
version: 1
providers: [gke, eks, aks]
capabilities:
  - name: cloud_kms
    spec_section: "13"
    status:
      gke: validated
      eks: planned
      aks:
        state: skip
        reason: Azure Key Vault binding still being scoped.
`)
	expectPass(t, validateParityMatrixYAML(path))
}

func TestValidateParityMatrixUnknownProvider(t *testing.T) {
	path := writeYAML(t, "parity-matrix.yaml", `
version: 1
providers: [gke, eks]
capabilities:
  - name: cloud_kms
    status:
      gke: validated
      digitalocean: validated
`)
	expectFail(t, validateParityMatrixYAML(path), "digitalocean")
}

func TestValidateParityMatrixSkipRequiresReason(t *testing.T) {
	path := writeYAML(t, "parity-matrix.yaml", `
version: 1
providers: [gke]
capabilities:
  - name: cloud_kms
    status:
      gke:
        state: skip
`)
	expectFail(t, validateParityMatrixYAML(path), "skip requires a reason")
}

func TestValidateParityMatrixUnknownState(t *testing.T) {
	path := writeYAML(t, "parity-matrix.yaml", `
version: 1
providers: [gke]
capabilities:
  - name: cloud_kms
    status:
      gke: maybe
`)
	expectFail(t, validateParityMatrixYAML(path), "maybe")
}

func TestValidateParityMatrixEmptyStatus(t *testing.T) {
	path := writeYAML(t, "parity-matrix.yaml", `
version: 1
providers: [gke]
capabilities:
  - name: cloud_kms
    status: {}
`)
	expectFail(t, validateParityMatrixYAML(path), "empty status")
}

func TestValidateParityMatrixEmptyProviders(t *testing.T) {
	path := writeYAML(t, "parity-matrix.yaml", `
version: 1
providers: []
capabilities:
  - name: cloud_kms
    status:
      gke: validated
`)
	expectFail(t, validateParityMatrixYAML(path), "providers list is empty")
}

func TestValidateParityMatrixAbsent(t *testing.T) {
	r := validateParityMatrixYAML(filepath.Join(t.TempDir(), "missing.yaml"))
	expectPass(t, r)
}

// ---- the shared register contract ---------------------------------
//
// These cases carry no `// spec:` annotation, matching the validator
// cases they sit beside: the register contract is test infrastructure
// rather than a spec behavior. Every gate that exempts anything lands
// green by seeding its register, so a ratchet rule that is silently a
// no-op would certify an exempted tree indefinitely, and each rule
// therefore carries a case of its own.

// testRegisterRules pins the two injected dependencies: a fixed clock
// and an open-item domain holding one identifier.
func testRegisterRules() registerRules {
	return registerRules{
		now: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		openItem: func(blocker string) bool {
			return blocker == "F-1.2.3"
		},
	}
}

// wellFormedRegister is one entry that satisfies every rule under
// testRegisterRules.
const wellFormedRegister = `
version: 1
entries:
  - subject: spec/04_system-components.md line 437
    verdict: FAIL
    owner: alice
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-1.2.3
    reason: The citation resolves once the section split lands.
`

func TestCheckRegisterWellFormed(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", wellFormedRegister)
	r := checkRegister("register", path, []string{"spec/04_system-components.md line 437"}, testRegisterRules())
	expectPass(t, r)
}

func TestCheckRegisterUnregisteredViolationFails(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", wellFormedRegister)
	r := checkRegister("register", path,
		[]string{"spec/04_system-components.md line 437", "spec/10_gateway.md line 12"},
		testRegisterRules())
	expectFail(t, r, "unregistered violation", "spec/10_gateway.md line 12")
}

func TestCheckRegisterPassedExpiryFails(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", `
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: FAIL
    owner: alice
    opened_at: 2026-01-01
    expiry: 2026-07-30
    blocker: F-1.2.3
    reason: Stale entry.
`)
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "expiry 2026-07-30 has passed")
}

func TestCheckRegisterBlockerWithNoOpenItemFails(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", `
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: FAIL
    owner: alice
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-9.9.9
    reason: Blocked on work that is already closed.
`)
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()),
		"F-9.9.9", "does not resolve to an open item")
}

// TestCheckRegisterNilResolverFailsClosed pins that a register read
// with no open-item resolver rejects every blocker rather than
// accepting each one.
func TestCheckRegisterNilResolverFailsClosed(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", wellFormedRegister)
	rules := registerRules{now: testRegisterRules().now}
	expectFail(t, checkRegister("register", path, nil, rules), "does not resolve to an open item")
}

func TestCheckRegisterMissingFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "read register")
}

func TestCheckRegisterMalformedFileFails(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", "version: 1\nentries: [oops\n")
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "parse register")
}

func TestCheckRegisterWrongVersionFails(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", "version: 2\nentries: []\n")
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "expected version 1")
}

func TestCheckRegisterIncompleteEntryFails(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", `
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: PASS
`)
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()),
		"verdict must be", "missing owner", "missing opened_at", "missing expiry")
}

func TestCheckRegisterDuplicateSubjectFails(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", `
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: FAIL
    owner: alice
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-1.2.3
    reason: First.
  - subject: spec/10_gateway.md line 12
    verdict: UNVERIFIED
    owner: bob
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-1.2.3
    reason: Second.
`)
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "already declared")
}

func TestValidateRegistersDirAcceptsWellFormedRegister(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", wellFormedRegister)
	r := validateRegistersDir(filepath.Dir(path), testRegisterRules())
	expectPass(t, r)
	if !strings.Contains(r.detail, "1 register") {
		t.Errorf("detail should count the register: %s", r.detail)
	}
}

func TestValidateRegistersDirReportsBadRegister(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", "version: 1\nentries: [oops\n")
	expectFail(t, validateRegistersDir(filepath.Dir(path), testRegisterRules()),
		"line-citations.yaml", "parse register")
}

// TestValidateRegistersDirMissingDirectoryFails pins that a register
// directory that is not there fails rather than reporting an empty set
// of registers as a clean tree.
func TestValidateRegistersDirMissingDirectoryFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	expectFail(t, validateRegistersDir(dir, testRegisterRules()), "could not read directory")
}

// TestValidateRegistersDirSkipsResidualRegisters pins the divergence
// the contract's doc comment states: a residual register carries a
// member, a class, a disposition, and a reason, and the shared
// contract's expiry and blocker rules do not range over it.
func TestValidateRegistersDirSkipsResidualRegisters(t *testing.T) {
	dir := t.TempDir()
	residual := filepath.Join(dir, "residual-line-citations.yaml")
	if err := os.WriteFile(residual, []byte(`
version: 1
entries:
  - member: spec/10_gateway.md
    class: line-citations
    disposition: excluded
    reason: The record states findings as they were written.
`), 0o644); err != nil {
		t.Fatalf("write %s: %v", residual, err)
	}
	r := validateRegistersDir(dir, testRegisterRules())
	expectPass(t, r)
	if !strings.Contains(r.detail, "0 register") {
		t.Errorf("residual register should not be counted: %s", r.detail)
	}
}

func TestOpenFindingIDsReadsTrackedRecords(t *testing.T) {
	dir := t.TempDir()
	body := "### - [ ] F-1.2.3 — Something is missing [High] — OPEN\n" +
		"### - [x] F-1.2.4 — Something else [High] — RESOLVED abc123\n" +
		"Every finding heading is a checklist line ending in ` — OPEN`.\n"
	if err := os.WriteFile(filepath.Join(dir, "BUILD-GAPS.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write BUILD-GAPS.md: %v", err)
	}
	open := openFindingIDs(dir)
	if !open["F-1.2.3"] {
		t.Errorf("F-1.2.3 should be open: %v", open)
	}
	if open["F-1.2.4"] {
		t.Errorf("F-1.2.4 is resolved and must not be open: %v", open)
	}
	if len(open) != 1 {
		t.Errorf("prose should not register an identifier: %v", open)
	}
}

// TestRepoRegisterRulesResolveTrackedOpenFindings pins that the rules
// the harness runs with read a real open-item domain, so the blocker
// rule is not a no-op against the tracked tree.
func TestRepoRegisterRulesResolveTrackedOpenFindings(t *testing.T) {
	rules := repoRegisterRules(repoRoot())
	if rules.openItem == nil {
		t.Fatal("harness rules must carry an open-item resolver")
	}
	if len(openFindingIDs(repoRoot())) == 0 {
		t.Skip("not-yet-applicable: the tracked audit records carry no open finding")
	}
	if rules.resolvesBlocker("no-such-finding") {
		t.Error("an identifier no record carries must not resolve")
	}
}

func TestCheckRegisterNonDateFieldsFail(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", `
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: UNVERIFIED
    owner: alice
    opened_at: yesterday
    expiry: soon
    blocker: F-1.2.3
    reason: Dates written in prose.
`)
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()),
		"opened_at \"yesterday\" not YYYY-MM-DD", "expiry \"soon\" not YYYY-MM-DD")
}

func TestCheckRegisterMissingSubjectFails(t *testing.T) {
	path := writeYAML(t, "line-citations.yaml", `
version: 1
entries:
  - verdict: FAIL
    owner: alice
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-1.2.3
    reason: No subject.
`)
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "missing subject")
}
