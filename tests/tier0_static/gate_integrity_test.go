// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The gate-integrity meta-gate asserts that every gate the channel migration
// registers at tier 0 is still reachable through a channel the repository
// hard-gates, and that none of them has quietly become optional.
//
// A gate is only worth what its invocation is worth. The repository hard-gates
// two channels: a Go test under `tests/tier0_static/`, which the harness runs
// and fails the tier on, and a check inside `runValidateMaps`, which
// `lenny-test validate-maps` runs. A check invoked from a shell script under
// `scripts/` is not a third channel, because the lint invocation is downgraded
// to a warning and a script that is absent exits zero, so a gate moved there
// stops failing anything while still appearing to exist.
//
// The predicate is a fixed list rather than a discovery walk, and that is the
// point. A walk over whatever tests happen to exist cannot tell a deleted gate
// from one that was never there, so deleting a gate's file would shrink the
// walk's population and pass. Naming the gates makes a deletion fail here,
// which is the failure the migration needs: several of these gates are the only
// thing standing between the tree and the populations the migration closed.
//
// This gate was itself missing for the whole of the migration's first pass. Its
// absence is why four other gates could be absent without anything reporting
// it, which is the precise argument for landing it.
//
// Its domain is the tier-0 gates alone. The gates the migration registers
// outside tier 0 are named below and excluded deliberately: the harness runs
// those suites as Go test packages and fails their tier on a failing test, so
// each is already registered through the channel its own tier uses.

// tierZeroGate is one gate the migration registers at tier 0, named by the test
// function that runs it and the file that must carry it.
type tierZeroGate struct {
	Test string
	File string
	What string
}

// tierZeroGates is the fixed list. A gate added to tier 0 by a later step of the
// migration is added here in the same change.
var tierZeroGates = []tierZeroGate{
	{"TestNamingLintReportsNoBareReservedNounPhraseInTheTree", "naming_lint_test.go", "the naming law over channel identifiers"},
	{"TestIdentifierResolutionCertifiesTheTree", "identifier_resolution_test.go", "one live spelling per canonical identifier"},
	{"TestFragmentLinkGateCertifiesTheTree", "fragment_link_test.go", "every intra-repo markdown fragment resolves"},
	{"TestDegradationMatrixCorrespondsOneToOneWithTheChannelRegister", "matrix_completeness_test.go", "the §28.8 matrix covers every channel"},
	{"TestEverySpecificationHeadingCarriesAnIndexEntry", "heading_walker_test.go", "every heading carries a resolving index entry"},
	{"TestClaimRegisterSaysWhatTheSpecificationRequires", "claim_register_test.go", "the §28.4 claim register is well-formed"},
	{"TestEveryCitationNamesADocumentAReaderCanReach", "citation_document_test.go", "every citation names a document a reader can reach"},
	{"TestResidualGateCertifiesTheTree", "residual_gate_test.go", "no member of a migration class is unclassified"},
	{"TestSpecCitationResolutionCertifiesTheTree", "spec_citation_resolution_test.go", "a line citation resolves inside the section it names"},
	{"TestLineCitationRatchetCertifiesTheTree", "line_citation_ratchet_test.go", "the line-citation population does not grow"},
}

// gatesOutsideTierZero are registered by the migration through another tier's
// channel. They are named so the list above reads as a domain rather than an
// omission.
var gatesOutsideTierZero = []tierZeroGate{
	{"TestReducedSectionsPointAtTheHeadingThatOwnsTheirContent", "tests/tier11_docs/successor_pointer_test.go", "tier 11"},
	{"TestSpec287RegisterSupersedesEveryArtifactEnumerationInItsDomain", "tests/tier11_docs/artifact_register_supersession_test.go", "tier 11"},
	{"TestReferenceDocumentIsFrozen_spec_28_1", "tests/tier11_docs/reference_document_freeze_test.go", "tier 11"},
}

// goTestFunc matches a Go test declaration.
var goTestFunc = regexp.MustCompile(`(?m)^func (Test\w+)\(`)

// scriptInvocation matches a gate invoked from a shell script, which is not a
// channel the repository hard-gates.
var scriptInvocation = regexp.MustCompile(`\.sh\b`)

// spec: 28.2 (the gates the naming law needs), 28.4 (claim register)
// diagnosis: a gate the channel migration relies on is no longer registered at
// tier 0. Either its file was deleted, its test was renamed, or it moved to a
// channel whose failure the harness tolerates. Whatever population that gate
// held is now unguarded.
func TestEveryMigrationGateIsRegisteredAtTierZero(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	dir := filepath.Join(root, "tests", "tier0_static")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the tier-0 package: %v", err)
	}
	declared := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range goTestFunc.FindAllStringSubmatch(string(body), -1) {
			declared[m[1]] = e.Name()
		}
	}
	if len(declared) == 0 {
		t.Fatalf("the tier-0 package declares no test; the meta-gate would pass vacuously")
	}

	var missing, misfiled []string
	for _, g := range tierZeroGates {
		file, ok := declared[g.Test]
		if !ok {
			missing = append(missing, g.Test+" ("+g.What+"), expected in "+g.File)
			continue
		}
		if file != g.File {
			misfiled = append(misfiled, g.Test+" is in "+file+", expected "+g.File)
		}
	}
	sort.Strings(missing)
	sort.Strings(misfiled)
	for _, m := range missing {
		t.Errorf("no tier-0 test registers %s", m)
	}
	for _, m := range misfiled {
		t.Errorf("%s", m)
	}
	t.Logf("%d gate(s) named, %d tier-0 test(s) declared, %d gate(s) registered outside tier 0",
		len(tierZeroGates), len(declared), len(gatesOutsideTierZero))
}

// spec: 28.2 (the gates the naming law needs)
// diagnosis: a gate the meta-gate names is invoked from a shell script, whose
// absence or non-zero exit the repository tolerates, so the gate no longer
// fails anything.
func TestNoMigrationGateIsInvokedThroughATolerantChannel(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	dir := filepath.Join(root, "tests", "tier0_static")
	for _, g := range tierZeroGates {
		body, err := os.ReadFile(filepath.Join(dir, g.File))
		if err != nil {
			// The registration test above reports the absence; here it would
			// only be a second voice on the same finding.
			continue
		}
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if !scriptInvocation.MatchString(line) || strings.HasPrefix(trimmed, "//") {
				continue
			}
			// A script path written into a fixture tree is the gate's input
			// rather than its invocation. What this check looks for is the gate
			// shelling out, which is exec.Command or a Makefile hop.
			if !strings.Contains(line, "exec.Command") && !strings.Contains(line, "exec.CommandContext") {
				continue
			}
			t.Errorf("%s:%d invokes a shell script; a script that is absent exits zero, so the gate "+
				"would stop failing without anything reporting it\n    %s",
				g.File, i+1, strings.TrimSpace(line))
		}
	}
}

// spec: 28.2 (the gates the naming law needs)
// diagnosis: a gate named as living outside tier 0 is not where the meta-gate
// says it is, so the domain split the meta-gate documents no longer holds.
func TestGatesNamedOutsideTierZeroAreWhereTheyAreSaidToBe(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	for _, g := range gatesOutsideTierZero {
		body, err := os.ReadFile(filepath.Join(root, g.File))
		if err != nil {
			t.Errorf("%s names %s as registered at %s, and the file is unreadable: %v",
				g.Test, g.File, g.What, err)
			continue
		}
		if !strings.Contains(string(body), "func "+g.Test+"(") {
			t.Errorf("%s does not declare %s, which the meta-gate names as its %s registration",
				g.File, g.Test, g.What)
		}
	}
}
