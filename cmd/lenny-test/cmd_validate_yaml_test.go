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
