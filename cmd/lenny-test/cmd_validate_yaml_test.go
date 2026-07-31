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
kind: exception-register
version: 1
entries:
  - subject: spec/04_system-components.md line 437
    verdict: tracked
    owner: alice
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-1.2.3
    reason: The citation resolves once the section split lands.
`

func TestCheckRegisterWellFormed(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", wellFormedRegister)
	r := checkRegister("register", path, []string{"spec/04_system-components.md line 437"}, testRegisterRules())
	expectPass(t, r)
}

func TestCheckRegisterUnregisteredViolationFails(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", wellFormedRegister)
	r := checkRegister("register", path,
		[]string{"spec/04_system-components.md line 437", "spec/10_gateway.md line 12"},
		testRegisterRules())
	expectFail(t, r, "unregistered violation", "spec/10_gateway.md line 12")
}

func TestCheckRegisterPassedExpiryFails(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: tracked
    owner: alice
    opened_at: 2026-01-01
    expiry: 2026-07-30
    blocker: F-1.2.3
    reason: Stale entry.
`)
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "expiry 2026-07-30 has passed")
}

func TestCheckRegisterBlockerWithNoOpenItemFails(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: tracked
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
	path := writeYAML(t, "exceptions-spec-citations.yaml", wellFormedRegister)
	rules := registerRules{now: testRegisterRules().now}
	expectFail(t, checkRegister("register", path, nil, rules), "does not resolve to an open item")
}

func TestCheckRegisterMissingFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "read register")
}

func TestCheckRegisterMalformedFileFails(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", "kind: exception-register\nversion: 1\nentries: [oops\n")
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "parse register")
}

func TestCheckRegisterWrongVersionFails(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", "kind: exception-register\nversion: 2\nentries: []\n")
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "expected version 1")
}

// TestCheckRegisterUndeclaredKindFails pins that a file the shared
// contract is asked to read must declare that it uses the shared entry
// schema. A file that declares nothing is not an empty exception
// register; reading it as one would apply the shared rules to a
// document written for another schema.
func TestCheckRegisterUndeclaredKindFails(t *testing.T) {
	path := writeYAML(t, "change-graph-exceptions.yaml", "version: 1\nentries: []\n")
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "expected kind", "exception-register")
}

// TestCheckRegisterVerdictIsTheExemptionDisposition pins the entry
// schema's verdict domain: an entry records why the violation is
// exempt, using intentional, tracked, or deferred. A harness verdict
// such as FAIL is not a disposition and is rejected, so a register
// cannot be written in a vocabulary that loses the distinction between
// a deliberate carve-out and work still queued.
func TestCheckRegisterVerdictIsTheExemptionDisposition(t *testing.T) {
	entry := func(verdict string) string {
		return fmt.Sprintf(`
kind: exception-register
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: %s
    owner: alice
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-1.2.3
    reason: One disposition per case.
`, verdict)
	}
	for _, verdict := range []string{"intentional", "tracked", "deferred"} {
		path := writeYAML(t, "exceptions-spec-citations.yaml", entry(verdict))
		expectPass(t, checkRegister("register", path, nil, testRegisterRules()))
	}
	for _, verdict := range []string{"FAIL", "UNVERIFIED", "PASS"} {
		path := writeYAML(t, "exceptions-spec-citations.yaml", entry(verdict))
		expectFail(t, checkRegister("register", path, nil, testRegisterRules()),
			"verdict must be one of", "intentional, tracked, deferred")
	}
}

func TestCheckRegisterIncompleteEntryFails(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: unclear
`)
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()),
		"verdict must be", "missing owner", "missing opened_at", "missing expiry")
}

func TestCheckRegisterDuplicateSubjectFails(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: tracked
    owner: alice
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-1.2.3
    reason: First.
  - subject: spec/10_gateway.md line 12
    verdict: intentional
    owner: bob
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-1.2.3
    reason: Second.
`)
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "already declared")
}

func TestValidateRegistersDirAcceptsWellFormedRegister(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", wellFormedRegister)
	r := validateRegistersDir(filepath.Dir(path), testRegisterRules())
	expectPass(t, r)
	if !strings.Contains(r.detail, "1 register") {
		t.Errorf("detail should count the register: %s", r.detail)
	}
}

func TestValidateRegistersDirReportsBadRegister(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", "kind: exception-register\nversion: 1\nentries: [oops\n")
	expectFail(t, validateRegistersDir(filepath.Dir(path), testRegisterRules()),
		"exceptions-spec-citations.yaml", "parse register")
}

// TestValidateRegistersDirMissingDirectoryFails pins that a register
// directory that is not there fails rather than reporting an empty set
// of registers as a clean tree.
func TestValidateRegistersDirMissingDirectoryFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	expectFail(t, validateRegistersDir(dir, testRegisterRules()), "could not read directory")
}

// TestValidateRegistersDirHoldsEveryFileDeclaringTheSharedSchema pins
// that the sweep selects by the kind a file declares rather than by its
// filename. A register that carries the shared entry schema under any
// name is held to the ratchet rules, so an expired entry fails the tier
// instead of going unseen because the file was named outside a
// convention.
func TestValidateRegistersDirHoldsEveryFileDeclaringTheSharedSchema(t *testing.T) {
	path := writeYAML(t, "change-graph-exceptions.yaml", `
kind: exception-register
version: 1
entries:
  - subject: pkg/adapter
    verdict: deferred
    owner: alice
    opened_at: 2026-01-01
    expiry: 2026-07-30
    blocker: F-1.2.3
    reason: Named outside the filename convention.
`)
	expectFail(t, validateRegistersDir(filepath.Dir(path), testRegisterRules()),
		"change-graph-exceptions.yaml", "expiry 2026-07-30 has passed")
}

// TestValidateRegistersDirFailsUnrecognizedKind pins that a file no
// schema claims fails the sweep. A register validated by neither the
// shared contract nor another gate would otherwise sit in the tree
// unchecked while the sweep reported a clean directory.
func TestValidateRegistersDirFailsUnrecognizedKind(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"undeclared.yaml":   "version: 1\nentries: []\n",
		"mistyped.yaml":     "kind: exceptions\nversion: 1\nentries: []\n",
		"exceptions-a.yaml": wellFormedRegister,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	expectFail(t, validateRegistersDir(dir, testRegisterRules()),
		"undeclared.yaml", "mistyped.yaml", "exception-register, residual-register, baseline, sense-map")
}

// TestValidateRegistersDirSkipsRegistersWithTheirOwnSchema pins that
// the shared entry schema ranges over the registers that declare it
// rather than over every file the directory holds. A residual register,
// a baseline keyed for the rewrite it drives, and a sense map keyed by
// file and occurrence carry no subject, verdict, owner, expiry, or
// blocker, and the gate that reads each of them validates it against
// its own schema.
func TestValidateRegistersDirSkipsRegistersWithTheirOwnSchema(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"residual-line-citations.yaml": `
kind: residual-register
version: 1
entries:
  - member: spec/10_gateway.md
    class: line-citations
    disposition: excluded
    reason: The record states findings as they were written.
`,
		"line-citations.yaml": `
kind: baseline
version: 1
entries:
  - file: spec/10_gateway.md
    citations: 14
`,
		"identifier-senses.yaml": `
kind: sense-map
version: 1
occurrences:
  - file: spec/10_gateway.md
    line: 12
    identifier: runtime-lifecycle-channel
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	r := validateRegistersDir(dir, testRegisterRules())
	expectPass(t, r)
	if !strings.Contains(r.detail, "0 register") {
		t.Errorf("a residual register, a baseline, and a sense map hold no shared-contract entry: %s", r.detail)
	}
}

func TestCheckRegisterNonDateFieldsFail(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: intentional
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
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - verdict: tracked
    owner: alice
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-1.2.3
    reason: No subject.
`)
	expectFail(t, checkRegister("register", path, nil, testRegisterRules()), "missing subject")
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

// TestRemediationStepIDsReadsTrackedPlanDocuments pins that a step
// identifier is read from a remediation plan in every spelling the
// tracked plans use, which is a lettered step, a lettered step with a
// lowercase sub-step suffix, and a dashed prefix, and that ordinary
// prose and numbered section headings do not enter the domain.
func TestRemediationStepIDsReadsTrackedPlanDocuments(t *testing.T) {
	root := t.TempDir()
	plan := "## 3. Step 1: naming\n" +
		"### R3. Specification and test tooling\n" +
		"### R11a. One proxy-dialect enum\n" +
		"### XX-1. A dashed step identifier\n" +
		"### 3.1 What this step fixes\n" +
		"The step R9 is named in prose and is not a heading.\n"
	if err := os.WriteFile(filepath.Join(root, "gateway-remediation.md"), []byte(plan), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	steps := remediationStepIDs(root)
	for _, want := range []string{"R3", "R11a", "XX-1"} {
		if !steps[want] {
			t.Errorf("%s should be a declared step: %v", want, steps)
		}
	}
	if steps["R9"] {
		t.Errorf("prose must not declare a step: %v", steps)
	}
	if len(steps) != 3 {
		t.Errorf("a numbered section heading must not declare a step: %v", steps)
	}
}

// TestRemediationSubStepInTrackedPlanResolvesAsAnOpenItem pins the
// sub-step spelling against the tracked plans themselves: a plan that
// declares a sub-step declares outstanding work, so a register entry
// blocked on that sub-step resolves rather than failing the third
// ratchet rule.
func TestRemediationSubStepInTrackedPlanResolvesAsAnOpenItem(t *testing.T) {
	root := repoRoot()
	var subStep string
	for id := range remediationStepIDs(root) {
		last := id[len(id)-1]
		if last >= 'a' && last <= 'z' {
			subStep = id
			break
		}
	}
	if subStep == "" {
		t.Skip("not-yet-applicable: no tracked plan declares a sub-step")
	}
	if !repoRegisterRules(root).resolvesBlocker(subStep) {
		t.Errorf("a blocker naming the open sub-step %s must resolve", subStep)
	}
}

// TestRemediationStepIDsExcludeStagedProposalHeadings pins that a step
// heading in a staged proposal is outside the open-item domain. A
// proposal keeps declaring its steps after they land, so admitting its
// headings would make the blocker rule resolve every identifier the
// plan's vocabulary reuses and no entry could ever be retired by it.
func TestRemediationStepIDsExcludeStagedProposalHeadings(t *testing.T) {
	root := stagedProposalRoot(t)
	steps := remediationStepIDs(root)
	for _, id := range []string{"XX-1", "YY-2"} {
		if steps[id] {
			t.Errorf("a staged proposal heading must not declare an open step: %s in %v", id, steps)
		}
	}
	if !steps["R3"] {
		t.Errorf("the remediation plan still declares its steps: %v", steps)
	}
}

// stagedProposalRoot builds a tree carrying one remediation plan that
// declares step R3 and one staged proposal that declares steps XX-1 and
// YY-2.
func stagedProposalRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "gateway-remediation.md"),
		[]byte("### R3. Specification and test tooling\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "proposals"), 0o755); err != nil {
		t.Fatalf("mkdir proposals: %v", err)
	}
	staged := "### XX-1. Add the enum and its validator\n" +
		"### YY-2. Gate cases\n"
	if err := os.WriteFile(filepath.Join(root, "proposals", "0001_new_x.md"), []byte(staged), 0o644); err != nil {
		t.Fatalf("write proposal: %v", err)
	}
	return root
}

// TestRegisterBlockerNamingOpenRemediationStepResolves pins that a
// blocker naming the remediation step that retires the entry resolves.
// A gate lands green by seeding its register with such an entry, so a
// domain holding the audit findings alone would reject every entry the
// remediation plan writes.
func TestRegisterBlockerNamingOpenRemediationStepResolves(t *testing.T) {
	root := stagedProposalRoot(t)
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: tracked
    owner: alice
    opened_at: 2026-07-01
    expiry: 2099-01-01
    blocker: R3
    reason: Blocked on the remediation step that retires the entry.
`)
	expectPass(t, checkRegister("register", path, nil, repoRegisterRules(root)))
}

// TestRegisterBlockerNamingStagedProposalStepFails pins the third
// ratchet rule: an entry blocked on a step identifier that only a
// staged proposal declares fails, because that step is not outstanding
// work a plan carries. Without this the rule resolves every identifier
// the proposals have ever used and only the expiry rule can end an
// entry.
func TestRegisterBlockerNamingStagedProposalStepFails(t *testing.T) {
	root := stagedProposalRoot(t)
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: spec/10_gateway.md line 12
    verdict: deferred
    owner: alice
    opened_at: 2026-07-01
    expiry: 2099-01-01
    blocker: XX-1
    reason: Blocked on a step a staged proposal declares.
`)
	expectFail(t, checkRegister("register", path, nil, repoRegisterRules(root)),
		"XX-1", "does not resolve to an open item")
}
