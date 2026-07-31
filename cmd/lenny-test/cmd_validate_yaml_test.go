// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
  - subject: pkg/gateway/binding/registry.go
    verdict: tracked
    owner: alice
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-1.2.3
    reason: The citation resolves once the section split lands.
`

func TestCheckRegisterWellFormed(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", wellFormedRegister)
	r := checkRegister("register", path, []string{"pkg/gateway/binding/registry.go"}, testRegisterRules())
	expectPass(t, r)
}

func TestCheckRegisterUnregisteredViolationFails(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", wellFormedRegister)
	r := checkRegister("register", path,
		[]string{"pkg/gateway/binding/registry.go", "pkg/adapter/controlchannel.go"},
		testRegisterRules())
	expectFail(t, r, "unregistered violation", "pkg/adapter/controlchannel.go")
}

func TestCheckRegisterPassedExpiryFails(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: pkg/adapter/controlchannel.go
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
  - subject: pkg/adapter/controlchannel.go
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

// TestCheckRegisterNilClockFailsClosed pins that a register read with
// no clock fails rather than accepting every entry. A zero clock places
// every expiry in the future, so the expiry ratchet would silently stop
// running and certify stale entries indefinitely.
func TestCheckRegisterNilClockFailsClosed(t *testing.T) {
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: pkg/adapter/controlchannel.go
    verdict: tracked
    owner: alice
    opened_at: 2026-01-01
    expiry: 2026-07-30
    blocker: F-1.2.3
    reason: Stale entry a clockless read would accept.
`)
	rules := registerRules{openItem: testRegisterRules().openItem}
	expectFail(t, checkRegister("register", path, nil, rules), "no clock")
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
  - subject: pkg/adapter/controlchannel.go
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
  - subject: pkg/adapter/controlchannel.go
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
  - subject: pkg/adapter/controlchannel.go
    verdict: tracked
    owner: alice
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-1.2.3
    reason: First.
  - subject: pkg/adapter/controlchannel.go
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

// TestValidateRegistersDirLeavesFilesDeclaringNoSharedKind pins that
// the shared contract imposes no document key on the schemas it does
// not own. A line-citation baseline keyed per file, a resolution
// baseline keyed by file and citation text, and a residual register
// keyed by member and class all carry no kind key, because the schemas
// are specified by their keying alone. Each is validated by the gate
// that reads it, so the sweep passes over them instead of failing the
// tier for a key their schema never states.
func TestValidateRegistersDirLeavesFilesDeclaringNoSharedKind(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"line-citations.yaml": "version: 1\nfiles:\n  spec/10_gateway.md: 14\n",
		"resolutions.yaml": `version: 1
entries:
  - file: spec/10_gateway.md
    citation: the runtime lifecycle channel
    resolves: false
`,
		"residual-line-citations.yaml": `version: 1
entries:
  - member: spec/10_gateway.md
    class: line-citations
    disposition: excluded
    reason: The record states findings as they were written.
`,
		"exceptions-a.yaml": wellFormedRegister,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	r := validateRegistersDir(dir, testRegisterRules())
	expectPass(t, r)
	if !strings.Contains(r.detail, "1 register") {
		t.Errorf("only the exception register is held to the shared contract: %s", r.detail)
	}
}

// TestValidateRegistersDirRejectsEmptyRegister pins that a register
// carrying no document fails rather than being read as a file of
// another schema. A truncated or emptied register exempts nothing, and
// a sweep that passed over it would report a clean directory while the
// gate that reads the file went green over a register nobody
// validated.
func TestValidateRegistersDirRejectsEmptyRegister(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"exceptions-a.yaml": wellFormedRegister,
		"exceptions-b.yaml": "",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	expectFail(t, validateRegistersDir(dir, testRegisterRules()),
		"exceptions-b.yaml", "carries no document")
}

// TestValidateRegistersDirRejectsEntriesWithNoDeclaredKind pins that a
// register whose entries carry the shared schema cannot leave the
// contract by losing its kind key. The subject key paired with a
// blocker or an expiry belongs to no other schema in the directory, so
// a file carrying it and declaring no kind is an edit that dropped the
// declaration rather than a schema of its own.
func TestValidateRegistersDirRejectsEntriesWithNoDeclaredKind(t *testing.T) {
	dir := t.TempDir()
	body := `version: 1
entries:
  - subject: pkg/adapter/controlchannel.go
    verdict: tracked
    owner: alice
    opened_at: 2026-07-01
    expiry: 2026-09-30
    blocker: F-1.2.3
    reason: The kind declaration was dropped in an edit.
`
	if err := os.WriteFile(filepath.Join(dir, "exceptions-spec-citations.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write register: %v", err)
	}
	expectFail(t, validateRegistersDir(dir, testRegisterRules()),
		"declares no kind", "exception-register")
}

// TestValidateRegistersDirReadsBothYAMLExtensions pins that the sweep
// reads a register written with either YAML filename extension. A
// register the sweep never globs is validated by nothing, so the tier
// would go green over an entry whose expiry had passed.
func TestValidateRegistersDirReadsBothYAMLExtensions(t *testing.T) {
	dir := t.TempDir()
	body := `kind: exception-register
version: 1
entries:
  - subject: pkg/adapter/controlchannel.go
    verdict: tracked
    owner: alice
    opened_at: 2026-07-01
    expiry: 2026-07-30
    blocker: F-1.2.3
    reason: The entry has outlived its expiry.
`
	if err := os.WriteFile(filepath.Join(dir, "exceptions-spec-citations.yml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write register: %v", err)
	}
	expectFail(t, validateRegistersDir(dir, testRegisterRules()),
		"exceptions-spec-citations.yml", "expiry 2026-07-30 has passed")
}

// TestValidateRegistersDirLeavesNonMappingDocuments pins that a
// register whose document is a sequence rather than a mapping is left
// to the gate that reads it. Such a document can carry no top-level
// kind key at all, so treating the absent key as a violation would fail
// the tier for a schema the shared contract does not own.
func TestValidateRegistersDirLeavesNonMappingDocuments(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"skip-reasons.yaml": "- file: tests/e2e/gateway_test.go\n  call_site: TestDrain\n  reason: host capability absent\n",
		"exceptions-a.yaml": wellFormedRegister,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	expectPass(t, validateRegistersDir(dir, testRegisterRules()))
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
  - subject: pkg/adapter/controlchannel.go
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
// tracked plans use: the identifier opening the heading, the identifier
// trailing a step heading in parentheses, and the identifier following
// a section number before a colon. Each spelling covers a lettered
// step and a lowercase sub-step suffix. Ordinary prose and a section
// heading carrying no identifier do not enter the domain.
func TestRemediationStepIDsReadsTrackedPlanDocuments(t *testing.T) {
	root := t.TempDir()
	plan := "## 3. Step 1: channel identification and naming (R1)\n" +
		"### 3.6 R1a: register, prose, and Go symbols\n" +
		"### R3. Specification and test tooling\n" +
		"### R11a. One proxy-dialect enum\n" +
		"### 3.1 What this step fixes\n" +
		"The step R9 is named in prose and is not a heading.\n"
	if err := os.WriteFile(filepath.Join(root, "gateway-remediation.md"), []byte(plan), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	steps := remediationStepIDs(root)
	for _, want := range []string{"R1", "R1a", "R3", "R11a"} {
		if !steps[want] {
			t.Errorf("%s should be a declared step: %v", want, steps)
		}
	}
	if steps["R9"] {
		t.Errorf("prose must not declare a step: %v", steps)
	}
	if len(steps) != 4 {
		t.Errorf("a section heading carrying no step identifier must not declare a step: %v", steps)
	}
}

// TestRemediationStepIDsRejectHyphenatedHeadingIdentifiers pins that a
// heading identifier written as an uppercase name, a hyphen, and a
// number declares no plan step. No tracked plan spells a step that way;
// that spelling belongs to the proposal namespace, which a blocker
// names in its qualified form so the proposal that owns the work is
// named alongside it.
func TestRemediationStepIDsRejectHyphenatedHeadingIdentifiers(t *testing.T) {
	root := t.TempDir()
	plan := "### R3. Specification and test tooling\n" +
		"### XX-1. A hyphenated identifier\n" +
		"### 3.6 YY-2: a hyphenated identifier before a colon\n" +
		"## 3. Step 1: a hyphenated identifier in parentheses (ZZ-3)\n"
	if err := os.WriteFile(filepath.Join(root, "gateway-remediation.md"), []byte(plan), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	steps := remediationStepIDs(root)
	for _, id := range []string{"XX-1", "YY-2", "ZZ-3", "1", "2", "3"} {
		if steps[id] {
			t.Errorf("a hyphenated heading identifier must not declare a step: %s in %v", id, steps)
		}
	}
	if !steps["R3"] {
		t.Errorf("the plan's own step spelling still declares a step: %v", steps)
	}
}

// TestEveryStepTheTrackedPlanDeclaresResolvesAsAnOpenItem pins the
// open-item domain against the tracked plans themselves: every step
// identifier a plan heading declares, in any of the spellings the plans
// use, resolves as an open item. A register entry seeded by a gate is
// blocked on the step that retires it, so a spelling the reader misses
// would make those entries unwritable under the shared contract.
func TestEveryStepTheTrackedPlanDeclaresResolvesAsAnOpenItem(t *testing.T) {
	root := repoRoot()
	rules := repoRegisterRules(root)
	steps := remediationStepIDs(root)
	if len(steps) == 0 {
		t.Skip("not-yet-applicable: no tracked plan declares a step")
	}
	for id := range steps {
		if !rules.resolvesBlocker(id) {
			t.Errorf("a blocker naming the declared step %s must resolve", id)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "gateway-runtime-comms-remediation.md")); err != nil {
		return
	}
	for _, want := range []string{"R1", "R1a", "R2"} {
		if !steps[want] {
			t.Errorf("the tracked plan declares %s; it must enter the open-item domain: %v", want, steps)
		}
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

// blockerDomainRoot builds a tree carrying one remediation plan that
// declares step R3, and a proposals directory whose documents declare
// headings of their own in both the plan spelling and a hyphenated
// spelling. A proposal document is not a tracked plan, so nothing it
// declares is an open item, and the tree carries no proposal queue
// because no retirement signal is read for a namespace that does not
// exist.
func blockerDomainRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("gateway-remediation.md", "### R3. Specification and test tooling\n")
	if err := os.Mkdir(filepath.Join(root, "proposals"), 0o755); err != nil {
		t.Fatalf("mkdir proposals: %v", err)
	}
	write(filepath.Join("proposals", "0001_new_x.md"),
		"- **Status:** Approved.\n"+
			"### XX-1. A heading a proposal document declares\n"+
			"### 4.2 YY-2: another heading a proposal document declares\n"+
			"### R7. A heading spelled the way a plan spells a step\n")
	return root
}

// TestRemediationStepIDsExcludeProposalDocumentHeadings pins that the
// headings of a proposal document do not enter the plan-step namespace.
// A step is read from a tracked remediation plan, so a heading that
// sits in another document declares nothing even when it is spelled
// the way a plan spells a step.
func TestRemediationStepIDsExcludeProposalDocumentHeadings(t *testing.T) {
	root := blockerDomainRoot(t)
	steps := remediationStepIDs(root)
	for _, id := range []string{"XX-1", "YY-2", "R7"} {
		if steps[id] {
			t.Errorf("a heading outside a tracked plan must not declare a step: %s in %v", id, steps)
		}
	}
	if !steps["R3"] {
		t.Errorf("the remediation plan still declares its steps: %v", steps)
	}
}

// TestOpenItemDomainHoldsFindingsAndPlanStepsOnly pins the domain a
// blocker resolves against to the two namespaces the register contract
// names: the open findings of the tracked audit records, and the steps
// the tracked remediation plans declare. A heading declared by a
// document that stages work rather than tracking it is not an open
// item, so an entry cannot name the work that builds the gate it is
// exempting itself from.
func TestOpenItemDomainHoldsFindingsAndPlanStepsOnly(t *testing.T) {
	root := blockerDomainRoot(t)
	open := openItemIDs(root)
	if !open["R3"] {
		t.Errorf("a step the tracked plan declares is an open item: %v", open)
	}
	for _, id := range []string{"XX-1", "0001:XX-1", "YY-2", "0001:YY-2", "R7"} {
		if open[id] {
			t.Errorf("a heading of a proposal document must not be an open item: %s in %v", id, open)
		}
	}
}

// TestRegisterBlockerNamingProposalHeadingFails pins the third ratchet
// rule against the identifiers a proposal document declares, in the
// qualified form that names the document alongside the heading. Those
// identifiers name staged work rather than an outstanding item, and a
// tree carrying no proposal queue has no signal that would ever retire
// such an entry, so the entry fails.
func TestRegisterBlockerNamingProposalHeadingFails(t *testing.T) {
	root := blockerDomainRoot(t)
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: pkg/adapter/controlchannel.go
    verdict: deferred
    owner: alice
    opened_at: 2026-07-01
    expiry: 2099-01-01
    blocker: 0001:XX-1
    reason: Blocked on a heading a proposal document declares.
`)
	expectFail(t, checkRegister("register", path, nil, repoRegisterRules(root)),
		"0001:XX-1", "does not resolve to an open item")
}

// TestRegisterBlockerNamingBareProposalHeadingFails pins the same rule
// over the bare spelling: a blocker is measured against the tracked
// plans and the audit records alone, so an identifier only a proposal
// document declares fails whether or not it is qualified.
func TestRegisterBlockerNamingBareProposalHeadingFails(t *testing.T) {
	root := blockerDomainRoot(t)
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: pkg/adapter/controlchannel.go
    verdict: deferred
    owner: alice
    opened_at: 2026-07-01
    expiry: 2099-01-01
    blocker: XX-1
    reason: Blocked on a heading named without any tracked plan.
`)
	expectFail(t, checkRegister("register", path, nil, repoRegisterRules(root)),
		"XX-1", "does not resolve to an open item")
}

// retiredLineCitation matches a citation naming a specification file
// and a line number. The migration replaces every such citation with an
// anchored one, and the gates that drive it measure every tracked file
// outside a small exclusion list, so a citation written as a fixture
// enters the measured population with no route out other than deleting
// the fixture.
var retiredLineCitation = regexp.MustCompile(`spec/[0-9A-Za-z_./-]+\.md line [0-9]+`)

// TestRegisterContractSourcesCarryNoLineCitation pins that neither the
// register directory nor the contract's own cases seed the population
// the citation migration measures. The register subject field is opaque
// to the validator, so a case that needs a subject writes one in any
// other vocabulary, and the directory's documentation models that
// vocabulary for every register seeded later.
func TestRegisterContractSourcesCarryNoLineCitation(t *testing.T) {
	root := repoRoot()
	paths := []string{filepath.Join("cmd", "lenny-test", "cmd_validate_yaml_test.go")}
	entries, err := os.ReadDir(filepath.Join(root, "tests", "registers"))
	if err != nil {
		t.Fatalf("read tests/registers: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			paths = append(paths, filepath.Join("tests", "registers", e.Name()))
		}
	}
	for _, rel := range paths {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if m := retiredLineCitation.FindString(string(body)); m != "" {
			t.Errorf("%s carries the retired citation form %q; write the subject in another vocabulary", rel, m)
		}
	}
}

// TestRegisterBlockerNamingOpenRemediationStepResolves pins that a
// blocker naming the remediation step that retires the entry resolves.
// A gate lands green by seeding its register with such an entry, so a
// domain holding the audit findings alone would reject every entry the
// remediation plan writes.
func TestRegisterBlockerNamingOpenRemediationStepResolves(t *testing.T) {
	root := blockerDomainRoot(t)
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: pkg/adapter/controlchannel.go
    verdict: tracked
    owner: alice
    opened_at: 2026-07-01
    expiry: 2099-01-01
    blocker: R3
    reason: Blocked on the remediation step that retires the entry.
`)
	expectPass(t, checkRegister("register", path, nil, repoRegisterRules(root)))
}

// TestRegisterBlockerNamingStepOnlyAnotherDocumentDeclaresFails pins
// the third ratchet rule over the plan namespace: an identifier is
// measured against the tracked plan documents alone, so an entry
// blocked on a plan-spelled identifier that only another document
// declares fails.
func TestRegisterBlockerNamingStepOnlyAnotherDocumentDeclaresFails(t *testing.T) {
	root := blockerDomainRoot(t)
	path := writeYAML(t, "exceptions-spec-citations.yaml", `
kind: exception-register
version: 1
entries:
  - subject: pkg/adapter/controlchannel.go
    verdict: deferred
    owner: alice
    opened_at: 2026-07-01
    expiry: 2099-01-01
    blocker: R7
    reason: Blocked on a plan-spelled step no tracked plan declares.
`)
	expectFail(t, checkRegister("register", path, nil, repoRegisterRules(root)),
		"R7", "does not resolve to an open item")
}
