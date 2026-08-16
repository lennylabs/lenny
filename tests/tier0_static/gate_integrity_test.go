// SPDX-License-Identifier: MIT

package tier0_static

import (
	"errors"
	"fmt"
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
//
// The list covers the gates the migration lands itself, the running tier-0 gates
// whose predicate it rewrites or widens, and the pre-existing tooling gates it
// makes green by seeding a baseline register. A tooling gate the migration
// neither lands, rewrites, nor seeds a baseline for is outside the list: the
// proto no-drift check is the one such gate, and it holds no population this
// migration closed.
var tierZeroGates = []tierZeroGate{
	{"TestNamingLintReportsNoBareReservedNounPhraseInTheTree", "naming_lint_test.go", "the naming law over channel identifiers"},
	{"TestIdentifierResolutionCertifiesTheTree", "identifier_resolution_test.go", "one live spelling per canonical identifier"},
	{"TestFragmentLinkGateCertifiesTheTree", "fragment_link_test.go", "every intra-repo markdown fragment resolves"},
	{"TestDegradationMatrixCorrespondsOneToOneWithTheChannelRegister", "matrix_completeness_test.go", "the §28.8 matrix covers every channel"},
	{"TestEverySpecificationHeadingCarriesAnIndexEntry", "heading_walker_test.go", "every heading carries a resolving index entry"},
	{"TestClaimRegisterSaysWhatTheSpecificationRequires", "claim_register_test.go", "the §28.4 claim register is well-formed"},
	{"TestClaimRegisterAgreesWithTheAdapterProto", "claim_register_proto_agreement_test.go", "the claim register agrees with the adapter proto"},
	{"TestEveryCitationNamesADocumentAReaderCanReach", "citation_document_test.go", "every citation names a document a reader can reach"},
	{"TestResidualGateCertifiesTheTree", "residual_gate_test.go", "no member of a migration class is unclassified"},
	{"TestSpecCitationResolutionCertifiesTheTree", "spec_citation_resolution_test.go", "a line citation resolves inside the section it names"},
	{"TestLineCitationRatchetCertifiesTheTree", "line_citation_ratchet_test.go", "the line-citation population does not grow"},
	{"TestCoordinatorHoldAllowlistNamesMethodsTheAdapterServes", "coordinator_hold_allowlist_test.go", "every coordinator-hold allowlist entry names a method the adapter serves"},
	{"TestSpec254DegradationWarningLineCitationsAreFresh", "degradation_lock_line_citation_test.go", "each §25.4 citation names a heading whose body still carries the cited sentence"},
	{"TestSkipReasonClassifierCertifiesTheTree", "skip_reason_classifier_test.go", "every skipped test names a classified skip reason"},
	// The two checks below run on the second hard-gated channel, inside
	// runValidateMaps, so they are named by the file that carries that
	// function rather than by a tier-0 test file.
	{"validateChangeGraphCompleteness", validateMapsFile, "every tracked source path is covered by the change graph"},
	{"validateSpecMapExceptionsYAML", validateMapsFile, "every spec-map exception is well-formed, which is the heading walker's escape hatch"},
	// The meta-gate names itself, because the list is the whole set of gates the
	// migration registers at tier 0 and nothing in the domain split excludes it.
	// The entry catches a rename or a move of the meta-gate's own registration.
	// It cannot catch the file's deletion, which stops the meta-gate running at
	// all; that is a limit of any self-check rather than a gap in the list.
	{"TestEveryMigrationGateIsRegisteredAtTierZero", "gate_integrity_test.go", "every gate the migration registers at tier 0 is still hard-gated"},
}

// gatesOutsideTierZero are registered by the migration through another tier's
// channel. They are named so the list above reads as a domain rather than an
// omission.
var gatesOutsideTierZero = []tierZeroGate{
	{"TestReducedSectionsPointAtTheHeadingThatOwnsTheirContent", "tests/tier11_docs/successor_pointer_test.go", "tier 11"},
	{"TestSpec287RegisterSupersedesEveryArtifactEnumerationInItsDomain", "tests/tier11_docs/artifact_register_supersession_test.go", "tier 11"},
	{"TestReferenceDocumentIsFrozen_spec_28_1", "tests/tier11_docs/reference_document_freeze_test.go", "tier 11"},
	{
		"TestServedClientArtifactsCarryNoRetiredLineCitation",
		"tests/tier3_contract/rest_sessions/openapi_document_test.go",
		"tier 3",
	},
}

// goTestFunc matches a Go test declaration.
var goTestFunc = regexp.MustCompile(`(?m)^func (Test\w+)\(`)

// validateMapsBody matches the body of runValidateMaps, up to the first
// closing brace in the first column.
var validateMapsBody = regexp.MustCompile(`(?s)func runValidateMaps\(.*?\n}`)

// validateCheck matches a check invoked inside runValidateMaps.
var validateCheck = regexp.MustCompile(`\b(validate\w+)\(`)

// scriptInvocation matches a gate invoked from a shell script, which is not a
// channel the repository hard-gates.
var scriptInvocation = regexp.MustCompile(`\.sh\b`)

// tierZeroPackage is the tier-0 Go test package, relative to a tree root.
var tierZeroPackage = filepath.Join("tests", "tier0_static")

// validateMapsFile is the file that carries runValidateMaps, relative to a
// tree root, in the slash-separated form a gate names it by.
const validateMapsFile = "cmd/lenny-test/cmd_validate.go"

// gateFilePath resolves a gate's File to a path under root. A name with no
// separator is a file in the tier-0 package; a slash-separated name is
// relative to the root, which is how the runValidateMaps channel is named.
func gateFilePath(root string, g tierZeroGate) string {
	if strings.Contains(g.File, "/") {
		return filepath.Join(root, filepath.FromSlash(g.File))
	}
	return filepath.Join(root, tierZeroPackage, g.File)
}

// hardGatedGates enumerates every gate the two channels the repository
// hard-gates carry under root, mapped to the file that carries it: a Go test
// declared in the tier-0 package, and a check invoked inside runValidateMaps.
// It takes a root and returns the enumeration so that the meta-gate's own
// cases can drive it over a fixture tree rather than over the live repository.
func hardGatedGates(root string) (map[string]string, error) {
	declared := map[string]string{}

	entries, err := os.ReadDir(filepath.Join(root, tierZeroPackage))
	if err != nil {
		return nil, fmt.Errorf("read the tier-0 package: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, tierZeroPackage, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		for _, m := range goTestFunc.FindAllStringSubmatch(string(body), -1) {
			declared[m[1]] = e.Name()
		}
	}

	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(validateMapsFile)))
	switch {
	case err == nil:
		for _, m := range validateCheck.FindAllStringSubmatch(validateMapsBody.FindString(string(body)), -1) {
			declared[m[1]] = validateMapsFile
		}
	case errors.Is(err, os.ErrNotExist):
		// A tree that carries no runValidateMaps has only the one channel.
		// The registration check below reports any gate that named it.
	default:
		return nil, fmt.Errorf("read %s: %w", validateMapsFile, err)
	}

	return declared, nil
}

// unregisteredGates returns one finding per named gate that the hard-gated
// channels do not carry, or that they carry in a file other than the one the
// gate names. The findings are sorted so the report is stable.
func unregisteredGates(declared map[string]string, gates []tierZeroGate) []string {
	var findings []string
	for _, g := range gates {
		file, ok := declared[g.Test]
		if !ok {
			findings = append(findings, fmt.Sprintf(
				"no hard-gated channel registers %s (%s), expected in %s", g.Test, g.What, g.File,
			))
			continue
		}
		if file != g.File {
			findings = append(findings, fmt.Sprintf(
				"%s is in %s, expected %s", g.Test, file, g.File,
			))
		}
	}
	sort.Strings(findings)
	return findings
}

// tolerantChannelInvocations returns one finding per named gate whose file
// under root reaches its check by shelling out to a script. A gate whose file
// is absent produces no finding here, because unregisteredGates reports that
// absence and a second voice on one finding is noise.
func tolerantChannelInvocations(root string, gates []tierZeroGate) []string {
	var findings []string
	for _, g := range gates {
		body, err := os.ReadFile(gateFilePath(root, g))
		if err != nil {
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
			findings = append(findings, fmt.Sprintf(
				"%s:%d invokes a shell script; a script that is absent exits zero, so the gate "+
					"would stop failing without anything reporting it\n    %s",
				g.File, i+1, trimmed,
			))
		}
	}
	sort.Strings(findings)
	return findings
}

// spec: 28.2 (the gates the naming law needs), 28.4 (claim register)
// diagnosis: a gate the channel migration relies on is no longer registered at
// tier 0. Either its file was deleted, its test was renamed, or it moved to a
// channel whose failure the harness tolerates. Whatever population that gate
// held is now unguarded.
func TestEveryMigrationGateIsRegisteredAtTierZero(t *testing.T) {
	t.Parallel()
	declared, err := hardGatedGates(schematest.RepoRoot(t))
	if err != nil {
		t.Fatalf("enumerate the hard-gated channels: %v", err)
	}
	if len(declared) == 0 {
		t.Fatalf("the hard-gated channels carry no gate; the meta-gate would pass vacuously")
	}
	for _, finding := range unregisteredGates(declared, tierZeroGates) {
		t.Errorf("%s", finding)
	}
	t.Logf("%d gate(s) named, %d gate(s) carried by a hard-gated channel, %d gate(s) registered outside tier 0",
		len(tierZeroGates), len(declared), len(gatesOutsideTierZero))
}

// spec: 28.2 (the gates the naming law needs)
// diagnosis: a gate the meta-gate names is invoked from a shell script, whose
// absence or non-zero exit the repository tolerates, so the gate no longer
// fails anything.
func TestNoMigrationGateIsInvokedThroughATolerantChannel(t *testing.T) {
	t.Parallel()
	for _, finding := range tolerantChannelInvocations(schematest.RepoRoot(t), tierZeroGates) {
		t.Errorf("%s", finding)
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

// The meta-gate's own cases follow. They drive the enumeration over the
// fixture trees under testdata/gate-integrity/ so that each condition the
// meta-gate exists to detect is observed against a tree that holds it, rather
// than inferred from a green run over the live repository, which holds none of
// them.
//
// They carry no `// spec:` tie. The meta-gate asserts a property of the
// repository's gate registrations rather than a behavior the specification
// defines, so there is no section for a case to name.

// gateIntegrityFixtures is the fixture tree root, relative to this package.
const gateIntegrityFixtures = "testdata/gate-integrity"

// exampleGate is the tier-0 Go test the fixture trees register, or fail to.
var exampleGate = tierZeroGate{
	"TestExampleGateCertifiesTheTree",
	"example_gate_test.go",
	"the fixture gate",
}

// exampleRegisterCheck is the runValidateMaps check the fixture trees
// register, which is the second channel the repository hard-gates.
var exampleRegisterCheck = tierZeroGate{
	"validateExampleRegister",
	validateMapsFile,
	"the fixture register check",
}

func TestAGateOnEitherHardGatedChannelIsRegistered(t *testing.T) {
	t.Parallel()
	root := filepath.Join(gateIntegrityFixtures, "registered")
	declared, err := hardGatedGates(root)
	if err != nil {
		t.Fatalf("enumerate the hard-gated channels: %v", err)
	}
	if got := declared[exampleGate.Test]; got != exampleGate.File {
		t.Errorf("the tier-0 Go test channel carries %s in %q, expected %q",
			exampleGate.Test, got, exampleGate.File)
	}
	if got := declared[exampleRegisterCheck.Test]; got != exampleRegisterCheck.File {
		t.Errorf("the runValidateMaps channel carries %s in %q, expected %q",
			exampleRegisterCheck.Test, got, exampleRegisterCheck.File)
	}
	gates := []tierZeroGate{exampleGate, exampleRegisterCheck}
	if findings := unregisteredGates(declared, gates); len(findings) != 0 {
		t.Errorf("a gate on each hard-gated channel is registered, and the meta-gate reported %d finding(s): %s",
			len(findings), strings.Join(findings, "; "))
	}
	if findings := tolerantChannelInvocations(root, gates); len(findings) != 0 {
		t.Errorf("neither fixture gate shells out, and the meta-gate reported %d finding(s): %s",
			len(findings), strings.Join(findings, "; "))
	}
}

func TestADeletedGateFileFailsAndNamesTheGate(t *testing.T) {
	t.Parallel()
	root := filepath.Join(gateIntegrityFixtures, "deleted")
	declared, err := hardGatedGates(root)
	if err != nil {
		t.Fatalf("enumerate the hard-gated channels: %v", err)
	}
	// The tree still carries a second tier-0 test and the register check, so
	// the deletion is the only difference from the registered tree.
	if len(declared) == 0 {
		t.Fatalf("the fixture tree carries no gate, so the deletion is not what this case observes")
	}
	findings := unregisteredGates(declared, []tierZeroGate{exampleGate, exampleRegisterCheck})
	if len(findings) != 1 {
		t.Fatalf("a deleted gate file is one finding, and the meta-gate reported %d: %s",
			len(findings), strings.Join(findings, "; "))
	}
	if !strings.Contains(findings[0], exampleGate.Test) || !strings.Contains(findings[0], exampleGate.File) {
		t.Errorf("the finding names neither the gate nor its file: %s", findings[0])
	}

	t.Run("a tree with no tier-0 package fails rather than certifying", func(t *testing.T) {
		t.Parallel()
		if _, err := hardGatedGates(t.TempDir()); err == nil {
			t.Errorf("the enumeration read a tree with no tier-0 package and returned no error, " +
				"so a tree that lost the package would certify as green")
		}
	})
}

func TestAGateReachedOnlyThroughAShellScriptFails(t *testing.T) {
	t.Parallel()
	root := filepath.Join(gateIntegrityFixtures, "script-invoked")
	declared, err := hardGatedGates(root)
	if err != nil {
		t.Fatalf("enumerate the hard-gated channels: %v", err)
	}
	gates := []tierZeroGate{exampleGate}
	// The gate is registered as a tier-0 Go test, so the registration check
	// passes and the shell-script condition is the only one left to detect.
	if findings := unregisteredGates(declared, gates); len(findings) != 0 {
		t.Fatalf("the fixture gate is registered, and the registration check reported %d finding(s): %s",
			len(findings), strings.Join(findings, "; "))
	}
	findings := tolerantChannelInvocations(root, gates)
	if len(findings) != 1 {
		t.Fatalf("a gate that shells out to a script is one finding, and the meta-gate reported %d: %s",
			len(findings), strings.Join(findings, "; "))
	}
	if !strings.Contains(findings[0], exampleGate.File) || !strings.Contains(findings[0], "scripts/") {
		t.Errorf("the finding names neither the gate's file nor the script it reaches: %s", findings[0])
	}
}

func TestTheFixedListNamesExactlyTheTierZeroGates(t *testing.T) {
	t.Parallel()
	// One side: a name on the fixed list that no hard-gated channel carries
	// fails, which is what makes the list a predicate rather than a comment.
	declared, err := hardGatedGates(schematest.RepoRoot(t))
	if err != nil {
		t.Fatalf("enumerate the hard-gated channels: %v", err)
	}
	unregistered := tierZeroGate{
		"TestNoChannelOfThisTreeCarriesThisGate",
		"absent_gate_test.go",
		"a gate this tree does not register",
	}
	findings := unregisteredGates(declared, append(append([]tierZeroGate{}, tierZeroGates...), unregistered))
	if len(findings) != 1 {
		t.Fatalf("the live list plus one unregistered name is one finding, and the meta-gate reported %d: %s",
			len(findings), strings.Join(findings, "; "))
	}
	if !strings.Contains(findings[0], unregistered.Test) {
		t.Errorf("the finding does not name the unregistered gate: %s", findings[0])
	}

	// The other side: the gates the migration registers through another tier's
	// channel are absent from the tier-0 list rather than unsatisfiable entries
	// on it, and each is named as living outside tier 0.
	named := map[string]bool{}
	for _, g := range tierZeroGates {
		named[g.Test] = true
	}
	outside := map[string]string{}
	for _, g := range gatesOutsideTierZero {
		outside[g.Test] = g.What
	}
	for _, g := range []struct{ test, tier string }{
		{"TestReducedSectionsPointAtTheHeadingThatOwnsTheirContent", "tier 11"},
		{"TestSpec287RegisterSupersedesEveryArtifactEnumerationInItsDomain", "tier 11"},
		{"TestReferenceDocumentIsFrozen_spec_28_1", "tier 11"},
		{"TestServedClientArtifactsCarryNoRetiredLineCitation", "tier 3"},
	} {
		if named[g.test] {
			t.Errorf("%s is registered at %s, and the tier-0 list names it, where it can never be satisfied",
				g.test, g.tier)
		}
		tier, ok := outside[g.test]
		if !ok {
			t.Errorf("%s is registered at %s, and the meta-gate names it neither as a tier-0 gate "+
				"nor as a gate outside tier 0, so the domain reads as an omission", g.test, g.tier)
			continue
		}
		if tier != g.tier {
			t.Errorf("%s is registered at %s, and the meta-gate names it as %s", g.test, g.tier, tier)
		}
	}
}
