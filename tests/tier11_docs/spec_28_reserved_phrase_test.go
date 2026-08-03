// SPDX-License-Identifier: MIT

// Tier-11 documentation check holding the communication-channels
// section to the reserved-word prohibition the section itself states,
// and holding the domain the prohibition states to the predicate the
// name pass and the naming lint share. The naming table's rule that a
// retired channel spelling stands only in the row that retires it is
// held in the package that implements the walk
// (scripts/specshift/identifier). These tests
// are NOT under a
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

// reservedPhraseLines returns the source line of every reserved noun
// phrase a section carries, in source order, wherever it stands. The
// prohibition admits no exempt line: the naming table's rows declare a
// retired identifier spelling, which is a separate class the identifier
// pass exempts, and neither the name pass nor the naming lint carves a
// table row out of the phrase prohibition.
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
func reservedPhraseLines(section string) []int {
	return name.FindReservedPhrases(section)
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
// n3ExclusionAftermath closes the exclusion sentence, which opens where
// the domain sentence ends. The sentence after it describes how the
// section states the rule rather than naming a record, so the extraction
// stops there for the same reason.
//
// spec: §28.1
const (
	n3DomainOpening      = "The prohibition's domain is"
	n3DomainAftermath    = "Outside that"
	n3ExclusionAftermath = "This section describes"
)

// treeReference matches one backticked directory name, which is the form
// N3's domain sentence names a whole tree in.
var treeReference = regexp.MustCompile("`([a-z]+/)`")

// excludedRecordReference matches one record N3's exclusion sentence
// names, in the two forms it writes them: a root-level document by its
// file name, and a directory by its name and a trailing slash.
var excludedRecordReference = regexp.MustCompile("`([A-Za-z][A-Za-z0-9._-]*\\.md|[a-z]+/)`")

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

// n3ExclusionSentence returns the sentence of §28.1's N3 that names the
// records the prohibition leaves outside its domain.
//
// spec: §28.1
func n3ExclusionSentence(section string) (string, error) {
	start := strings.Index(section, n3DomainAftermath)
	if start < 0 {
		return "", fmt.Errorf("§28.1 states no sentence opening %q", n3DomainAftermath)
	}
	rest := section[start:]
	end := strings.Index(rest, n3ExclusionAftermath)
	if end < 0 {
		return "", fmt.Errorf("§28.1's exclusion sentence is not followed by one opening %q", n3ExclusionAftermath)
	}
	return rest[:end], nil
}

// n3ExcludedPaths returns one tracked path per record N3's exclusion
// sentence names, so the assertion reads the section's own list rather
// than a list restated here. A record named as a directory is answered
// for through a path inside it: `proposals/` through a staged proposal,
// and a fixture directory through a fixture under a tree the domain
// otherwise carries, which is the position the exclusion has to hold in.
//
// spec: §28.1
func n3ExcludedPaths(section string) (map[string]string, error) {
	sentence, err := n3ExclusionSentence(section)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	for _, m := range excludedRecordReference.FindAllStringSubmatch(sentence, -1) {
		record := m[1]
		switch {
		case record == "testdata/":
			paths[record] = "spec/testdata/specimen.md"
		case strings.HasSuffix(record, "/"):
			paths[record] = record + "0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md"
		default:
			paths[record] = record
		}
	}
	return paths, nil
}

// diagnosis: a failure means spec/28_communication-channels.md carries a
// site of the reserved noun phrase the naming lint will report and that
// no pass is scheduled to write, because the section sits inside the
// domain of the prohibition it states and its sense register is keyed by
// the position of each site within the file. A failure of the domain
// case means §28.1's statement of the domain and
// scope.ReservedPhraseCarrier, the predicate the name pass and the
// naming lint share, no longer describe the same set of carriers. A
// failure of the retired-spelling case means the section names a channel
// by a spelling its own naming table retires, outside the row that
// retires it, which is a site the identifier pass finds no sense-register
// entry for and aborts the run on.
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
	t.Run("naming-table row is no exemption", func(t *testing.T) { assertNamingTableRowIsNoExemption(t) })
	t.Run("stated domain", func(t *testing.T) { assertN3DomainMatchesTheSharedPredicate(t) })
}

// assertSection28CarriesNoReservedPhrase holds the landed section to the
// rule it states.
//
// spec: §28.1
func assertSection28CarriesNoReservedPhrase(t *testing.T) {
	t.Helper()
	for _, line := range reservedPhraseLines(readChannelsSection(t)) {
		t.Errorf("%s:%d carries a reserved noun phrase", channelsSpecFile, line)
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

	got := reservedPhraseLines(section)
	if len(got) != 1 || got[0] != 3 {
		t.Errorf("reserved-phrase lines for the %s specimen = %v, want [3]", fixture, got)
	}
}

// assertNamingTableRowIsNoExemption pins that the phrase prohibition
// reaches §28.3's naming table like every other line of the section. The
// table exempts a retired identifier spelling standing in the row that
// retires it, and that exemption is the identifier pass's own and covers
// that column alone. A reserved noun phrase written into any cell of a
// row is a site the naming lint reports and no pass is scheduled to
// write, so a check that passed over the table would report the section
// clean while the lint reports it red.
//
// spec: §28.1, §28.3
func assertNamingTableRowIsNoExemption(t *testing.T) {
	t.Helper()
	specimen := reservedPhraseSpecimen(t, hyphenatedSpecimenFile)
	const header = "### Naming table\n\n" +
		"| channel | carrier | retired spelling | canonical spelling |\n" +
		"|:--|:--|:--|:--|\n"

	row := header + "| `CH-RUNTIMEOPS` | path | `retired-stem` | `" + specimen + "` |\n"
	if got := reservedPhraseLines(row); len(got) != 1 || got[0] != 5 {
		t.Errorf("reserved-phrase lines for a naming-table row carrying the phrase = %v, want [5]", got)
	}

	prose := header + "The adapter opens the " + specimen + " to the runtime.\n"
	if got := reservedPhraseLines(prose); len(got) != 1 || got[0] != 5 {
		t.Errorf("prose under the naming-table heading = %v, want [5]", got)
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

	assertN3ExclusionsAreOutsideTheClassDomain(t, section)
}

// assertN3ExclusionsAreOutsideTheClassDomain holds the second half of
// N3's domain statement, the records it places outside the prohibition,
// to the domain the migration reads for the reserved-phrase class.
//
// The carrier predicate answers the first half alone. It admits every
// tracked root-level markdown document, so the audit records, the two
// root planning documents, and the build and queue records are inside it
// and the exclusion sits elsewhere, composed on top through the class
// read domain. Asserting the exclusion against the carrier predicate
// would therefore assert nothing, and asserting nothing at all leaves a
// record dropped from the excluded list disagreeing with the section
// with no case reporting it.
//
// spec: §28.1
func assertN3ExclusionsAreOutsideTheClassDomain(t *testing.T, section string) {
	t.Helper()

	excluded, err := n3ExcludedPaths(section)
	if err != nil {
		t.Fatalf("read the records N3 places outside the domain: %v", err)
	}
	// Each of the three forms the sentence writes has to be read, so a
	// matcher that stopped recognizing one reports a shorter list rather
	// than a clean one.
	forms := map[string]string{
		"a root-level record":  "BUILD-GAPS.md",
		"the staged proposals": "proposals/",
		"a fixture directory":  "testdata/",
	}
	for form, record := range forms {
		if _, named := excluded[record]; !named {
			t.Fatalf("N3's exclusion sentence names no %s, so the sweep read %d record(s): %v", form, len(excluded), excluded)
		}
	}

	for record, path := range excluded {
		readable, err := scope.ReadableForClass(scope.ClassReservedPhrase, path)
		if err != nil {
			t.Fatalf("read the class domain for %s: %v", path, err)
		}
		if readable {
			t.Errorf("the reserved-phrase class reads %s, which N3 names as outside the prohibition's domain", record)
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
