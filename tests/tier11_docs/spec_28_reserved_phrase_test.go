// SPDX-License-Identifier: MIT

// Tier-11 documentation check holding the communication-channels
// section to the reserved-word prohibition the section itself states,
// and holding the domain that prohibition states to the predicate the
// name pass and the naming lint share. These tests are NOT under a
// build tag because they exercise the repository state directly — no
// external infrastructure required.

package tier11_docs_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/name"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// namingTableHeading is the heading of §28.3's naming table. A retired
// spelling standing in the row that retires it is the declaration of
// that spelling rather than a reference to the channel, so the rows of
// that one table are the section's only exempt lines.
//
// spec: §28.3
const namingTableHeading = "Naming table"

// namingTableRowLines returns the 1-based source lines of the rows of
// §28.3's naming table. A row is a table line standing between that
// heading and the next heading of the section. The scan skips fenced
// blocks for the same reason the heading scan does: a table drawn inside
// a fence is example content and declares no spelling.
//
// spec: §28.3
func namingTableRowLines(section string) map[int]bool {
	rows := make(map[int]bool)
	inTable := false
	inFence := false
	for i, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if m := atxHeading.FindStringSubmatch(line); m != nil {
			inTable = strings.TrimSpace(m[2]) == namingTableHeading
			continue
		}
		if inTable && strings.HasPrefix(trimmed, "|") {
			rows[i+1] = true
		}
	}
	return rows
}

// reservedPhraseLinesOutsideTheNamingTable returns the source line of
// every reserved noun phrase a section carries outside a naming-table
// row, in source order.
//
// The matcher is the one the name pass and the naming lint read, so this
// check and the lint enumerate one population, and the phrase itself is
// never restated here: every tracked Go file is a carrier of the
// prohibition, so a specimen written in this source would be a site of
// the class the pass removes. The matcher applies the comment
// continuation join before it runs, so a phrase wrapped across two
// consecutive comment lines is one site reported on the line it opens
// on.
//
// spec: §28.1
func reservedPhraseLinesOutsideTheNamingTable(section string) []int {
	exempt := namingTableRowLines(section)
	var out []int
	for _, line := range name.FindReservedPhrases(section) {
		if exempt[line] {
			continue
		}
		out = append(out, line)
	}
	return out
}

// reservedPhraseSpecimen reads one specimen fixture and returns its
// first line with the trailing newline removed.
//
// The specimens are held under testdata rather than inline in this
// source because scope.ReservedPhraseCarrier admits every tracked Go
// file, so a specimen written here would itself be a site the naming
// lint reports. A testdata directory is outside the read domain of every
// gate and the write domain of every pass, so a specimen there is read
// by this test alone.
//
// spec: §28.1
func reservedPhraseSpecimen(t *testing.T, base string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", base))
	if err != nil {
		t.Fatalf("read specimen fixture: %v", err)
	}
	specimen := strings.TrimSpace(strings.SplitN(string(content), "\n", 2)[0])
	if specimen == "" {
		t.Fatalf("specimen fixture %s carries no specimen on its first line", base)
	}
	return specimen
}

// spaceSeparatedSpecimenFile and hyphenatedSpecimenFile name the two
// fixtures, one per spelling the prohibition covers.
const (
	spaceSeparatedSpecimenFile = "reserved-phrase-space-separated.md"
	hyphenatedSpecimenFile     = "reserved-phrase-hyphenated.md"
)

// n3DomainOpening and n3DomainAftermath bracket the sentence of N3 that
// states the prohibition's domain. The sentence that follows states the
// exclusions and names further trees, so the extraction stops at its
// first word: a scan over the whole rule would read an excluded tree as
// a member of the domain.
//
// spec: §28.1
const (
	n3DomainOpening   = "The prohibition's domain is"
	n3DomainAftermath = "Outside that"
)

// treeReference matches one backticked directory name, which is the form
// N3's domain sentence names a whole tree in.
var treeReference = regexp.MustCompile("`([a-z]+/)`")

// n3DomainSentence returns the sentence of §28.1's N3 that states the
// prohibition's domain.
//
// spec: §28.1
func n3DomainSentence(section string) (string, error) {
	start := strings.Index(section, n3DomainOpening)
	if start < 0 {
		return "", fmt.Errorf("§28.1 states no sentence opening %q", n3DomainOpening)
	}
	rest := section[start:]
	end := strings.Index(rest, n3DomainAftermath)
	if end < 0 {
		return "", fmt.Errorf("§28.1's domain sentence is not followed by one opening %q", n3DomainAftermath)
	}
	return rest[:end], nil
}

// n3DomainTrees returns the trees N3's domain sentence names, sorted, so
// the assertion reads the section's own statement of the domain rather
// than a list restated here.
//
// spec: §28.1
func n3DomainTrees(section string) ([]string, error) {
	sentence, err := n3DomainSentence(section)
	if err != nil {
		return nil, err
	}
	var trees []string
	for _, m := range treeReference.FindAllStringSubmatch(sentence, -1) {
		trees = append(trees, m[1])
	}
	sort.Strings(trees)
	return trees, nil
}

// diagnosis: a failure means spec/28_communication-channels.md carries a
// site of the reserved noun phrase the naming lint will report and that
// no pass is scheduled to write, because the section sits inside the
// domain of the prohibition it states and its sense register is keyed by
// the position of each site within the file. A failure of the domain
// case means §28.1's statement of the domain and
// scope.ReservedPhraseCarrier, the predicate the name pass and the
// naming lint share, no longer describe the same set of carriers.
//
// spec: §28.1
func TestSection28StatesN3WithoutViolatingIt_spec_28_1(t *testing.T) {
	t.Run("landed section", func(t *testing.T) { assertSection28CarriesNoReservedPhrase(t) })
	t.Run("space-separated specimen", func(t *testing.T) {
		assertSpecimenInProseIsReported(t, spaceSeparatedSpecimenFile)
	})
	t.Run("hyphenated specimen", func(t *testing.T) {
		assertSpecimenInProseIsReported(t, hyphenatedSpecimenFile)
	})
	t.Run("naming-table row exempt", func(t *testing.T) { assertNamingTableRowIsExempt(t) })
	t.Run("stated domain", func(t *testing.T) { assertN3DomainMatchesTheSharedPredicate(t) })
}

// assertSection28CarriesNoReservedPhrase holds the landed section to the
// rule it states.
//
// spec: §28.1
func assertSection28CarriesNoReservedPhrase(t *testing.T) {
	t.Helper()
	section := readChannelsSection(t)

	if len(namingTableRowLines(section)) == 0 {
		t.Fatalf("%s carries no naming-table row; the exemption this check applies is derived from none", channelsSpecFile)
	}
	for _, line := range reservedPhraseLinesOutsideTheNamingTable(section) {
		t.Errorf("%s:%d carries a reserved noun phrase outside a naming-table row", channelsSpecFile, line)
	}
}

// assertSpecimenInProseIsReported pins that a specimen standing in prose
// is reported. Without this case the landed-section assertion passes
// over a section a matcher stopped reading, which is the state that
// leaves the naming lint red on the section that states its own rule.
//
// spec: §28.1
func assertSpecimenInProseIsReported(t *testing.T, fixture string) {
	t.Helper()
	specimen := reservedPhraseSpecimen(t, fixture)
	section := "## 28.1 Naming law\n\nThe adapter opens the " + specimen + " to the runtime.\n"

	got := reservedPhraseLinesOutsideTheNamingTable(section)
	if len(got) != 1 || got[0] != 3 {
		t.Errorf("reserved-phrase lines for the %s specimen = %v, want [3]", fixture, got)
	}
}

// assertNamingTableRowIsExempt pins both directions of the exemption: a
// retired spelling standing in the row that retires it is a declaration
// and is not reported, and the same spelling standing in the prose of
// the same subsection is.
//
// spec: §28.1, §28.3
func assertNamingTableRowIsExempt(t *testing.T) {
	t.Helper()
	specimen := reservedPhraseSpecimen(t, hyphenatedSpecimenFile)
	const header = "### " + namingTableHeading + "\n\n" +
		"| channel | carrier | retired spelling | canonical spelling |\n" +
		"|:--|:--|:--|:--|\n"

	row := header + "| `CH-RUNTIMEOPS` | path | `" + specimen + "` | `runtime-ops-events` |\n"
	if got := reservedPhraseLinesOutsideTheNamingTable(row); len(got) != 0 {
		t.Errorf("naming-table row reported as a site: %v", got)
	}

	prose := header + "The adapter opens the " + specimen + " to the runtime.\n"
	if got := reservedPhraseLinesOutsideTheNamingTable(prose); len(got) != 1 || got[0] != 5 {
		t.Errorf("prose under the naming-table heading = %v, want [5]", got)
	}

	fenced := "```\n" + header + "```\n" +
		"| `CH-RUNTIMEOPS` | path | `" + specimen + "` | `runtime-ops-events` |\n"
	if got := reservedPhraseLinesOutsideTheNamingTable(fenced); len(got) != 1 {
		t.Errorf("table row outside a naming-table heading = %v, want one site", got)
	}
}

// assertN3DomainMatchesTheSharedPredicate holds §28.1's statement of the
// domain and scope.ReservedPhraseCarrier to one another. The predicate
// is the one statement of the domain the name pass and the naming lint
// both read, so a section stating a wider or narrower domain than the
// predicate admits describes a lint the tree does not run.
//
// spec: §28.1
func assertN3DomainMatchesTheSharedPredicate(t *testing.T) {
	t.Helper()
	section := readChannelsSection(t)

	trees, err := n3DomainTrees(section)
	if err != nil {
		t.Fatalf("read the domain N3 states: %v", err)
	}
	if want := []string{"docs/", "schemas/", "spec/"}; !equalStrings(trees, want) {
		t.Fatalf("N3 names trees %v; the predicate covers %v", trees, want)
	}
	for _, tree := range trees {
		if !scope.ReservedPhraseCarrier(tree + "example.md") {
			t.Errorf("the predicate does not admit a path under %s, which N3 names", tree)
		}
	}

	sentence, err := n3DomainSentence(section)
	if err != nil {
		t.Fatalf("read the domain N3 states: %v", err)
	}
	for _, carrier := range []string{"README.md", "TESTING.md"} {
		if !strings.Contains(sentence, carrier) {
			t.Errorf("N3's domain sentence does not name %s, the root-level document it states carries the phrase today", carrier)
		}
	}

	for _, admitted := range []string{
		"pkg/gateway/session.go",
		"sdks/go/agent.go",
		"CONTRIBUTING.md",
	} {
		if !scope.ReservedPhraseCarrier(admitted) {
			t.Errorf("the predicate rejects %s, which N3 places inside the domain", admitted)
		}
	}

	for _, outside := range []string{
		"charts/lenny/values.yaml",
		"sdks/python/lenny/agent.py",
		"tests/registers/pinned-spec-literals.yaml",
	} {
		if scope.ReservedPhraseCarrier(outside) {
			t.Errorf("the predicate admits %s, which N3 leaves outside the domain", outside)
		}
	}
}

// readChannelsSection reads the landed communication-channels section.
func readChannelsSection(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoRoot(t), "spec", channelsSpecFile))
	if err != nil {
		t.Fatalf("read %s: %v", channelsSpecFile, err)
	}
	return string(content)
}
