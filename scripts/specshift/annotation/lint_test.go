// SPDX-License-Identifier: MIT

package annotation

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// errNotExist is the error a reader returns for a file the tree does
// not carry.
var errNotExist = fmt.Errorf("open %s: %w", RegistryPath, fs.ErrNotExist)

// allLines marks every line of a source as changed.
func allLines(src []byte) map[int]bool {
	lines := map[int]bool{}
	for i := 1; i <= bytes.Count(src, []byte("\n"))+1; i++ {
		lines[i] = true
	}
	return lines
}

// fixtureLint returns a lint over the fixture registry and specification.
func fixtureLint(t *testing.T) Lint {
	t.Helper()
	reg, err := LoadRegistry(func(string) ([]byte, error) {
		return readFixture(t, "lint", "registry.json"), nil
	}, RegistryPath)
	if err != nil {
		t.Fatalf("load the fixture registry: %v", err)
	}
	reg.AddIssuePaths([]string{"issues/CLAIM/CLAIM-I4.md"})
	return Lint{Registry: reg, Spec: NewSpecText(string(readFixture(t, "lint", "spec.md")))}
}

// check lints a fixture with the given changed lines, or every line when
// lines is nil, and returns the findings of one rule as line numbers.
func check(t *testing.T, l Lint, fixture, asPath string, lines map[int]bool, rule Rule) []int {
	t.Helper()
	src := readFixture(t, "lint", fixture)
	if lines == nil {
		lines = allLines(src)
	}
	found, err := l.Check(ChangedFile{Path: asPath, Src: src, Lines: lines})
	if err != nil {
		t.Fatalf("lint %s: %v", fixture, err)
	}
	var out []int
	for _, f := range found {
		if f.Rule == rule {
			out = append(out, f.Line)
		}
	}
	sort.Ints(out)
	return out
}

func TestLintRefusesAnnotationsInFunctionBodiesAndOnStructFields(t *testing.T) {
	t.Parallel()
	l := fixtureLint(t)
	if got, want := check(t, l, "placement.go.txt", "pkg/claim/claim.go", nil, InFunctionBody), []int{21, 26}; !reflect.DeepEqual(got, want) {
		t.Errorf("function-body findings at lines %v, want %v", got, want)
	}
	if got, want := check(t, l, "placement.go.txt", "pkg/claim/claim.go", nil, OnStructField), []int{10, 12}; !reflect.DeepEqual(got, want) {
		t.Errorf("struct-field findings at lines %v, want %v", got, want)
	}
}

func TestLintReadsOnlyTheChangedLines(t *testing.T) {
	t.Parallel()
	l := fixtureLint(t)
	got := check(t, l, "placement.go.txt", "pkg/claim/claim.go", map[int]bool{26: true}, InFunctionBody)
	if !reflect.DeepEqual(got, []int{26}) {
		t.Errorf("findings at lines %v, want only the changed line 26", got)
	}
	if got := check(t, l, "placement.go.txt", "pkg/claim/claim.go", map[int]bool{}, InFunctionBody); len(got) != 0 {
		t.Errorf("a file with no changed line produced findings at %v", got)
	}
}

func TestLintRefusesSectionNumbersInTestNames(t *testing.T) {
	t.Parallel()
	l := fixtureLint(t)
	got := check(t, l, "names_test.go.txt", "pkg/claim/claim_test.go", nil, SectionInTestName)
	if want := []int{10, 11, 15, 17, 19, 23}; !reflect.DeepEqual(got, want) {
		t.Errorf("test-name findings at lines %v, want %v", got, want)
	}
	// The same names in a file that is not a test file are not test names.
	if got := check(t, l, "names_test.go.txt", "pkg/claim/claim.go", nil, SectionInTestName); len(got) != 0 {
		t.Errorf("a non-test file produced test-name findings at %v", got)
	}
}

// identifierNameFindings lints the identifier-name fixture as a test file
// with every line changed, and returns the identifier each test-name
// finding reports, keyed by line.
func identifierNameFindings(t *testing.T, l Lint, asPath string, lines map[int]bool) map[int]string {
	t.Helper()
	src := readFixture(t, "lint", "identifier_names_test.go.txt")
	if lines == nil {
		lines = allLines(src)
	}
	found, err := l.Check(ChangedFile{Path: asPath, Src: src, Lines: lines})
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	out := map[int]string{}
	for _, f := range found {
		if f.Rule != IdentifierInTestName {
			continue
		}
		if _, dup := out[f.Line]; dup {
			t.Errorf("line %d reported twice: %s", f.Line, f)
		}
		fields := strings.Fields(f.Detail)
		for i, w := range fields {
			if w == "spells" && i+1 < len(fields) {
				out[f.Line] = strings.TrimSuffix(fields[i+1], ":")
			}
		}
	}
	return out
}

// The lint is contributor tooling, so its tests verify no requirement
// and carry no specification annotation.

func TestLintRefusesIdentifiersInTestAndSubtestNames(t *testing.T) {
	t.Parallel()
	got := identifierNameFindings(t, fixtureLint(t), "pkg/claim/claim_test.go", nil)
	want := map[int]string{
		10: "CLAIM-R4",   // subtest, hyphen kept
		11: "CLAIM-I4",   // subtest, hyphen as underscore, an issue
		12: "CLAIM-R9",   // subtest, hyphen dropped, code in title case
		17: "CLAIM-R4",   // hyphen as underscore
		19: "CLAIM-R4",   // hyphen as underscore, code in title case
		21: "CLAIM-R4",   // hyphen dropped
		23: "CLAIM-R4",   // hyphen dropped, code in title case
		25: "CLAIM-I4",   // an issue inside the name
		27: "POOL-R5",    // an unregistered code with an underscore
		31: "GWCLAIM-R2", // the longer code rather than the CLAIM inside it
		43: "CLAIM-R4",   // a benchmark
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("identifier-name findings %v, want %v", got, want)
	}
}

func TestLintAcceptsTestNamesThatSpellNoIdentifier(t *testing.T) {
	t.Parallel()
	got := identifierNameFindings(t, fixtureLint(t), "pkg/claim/claim_test.go", nil)
	// Line 9 is a plain subtest, 13 a version, 14 a count, 29 an
	// unregistered code with its hyphen dropped, 33 and 35 versions, 37
	// a duration after a registered code, 39 a code inside a word, 41 an
	// operation, and 45 a function `go test` does not run.
	for _, line := range []int{8, 9, 13, 14, 29, 33, 35, 37, 39, 41, 45} {
		if id, ok := got[line]; ok {
			t.Errorf("line %d refused as spelling %s", line, id)
		}
	}
	if got := identifierNameFindings(t, fixtureLint(t), "pkg/claim/claim.go", nil); len(got) != 0 {
		t.Errorf("a file that is not a test file produced identifier-name findings %v", got)
	}
}

func TestLintReadsIdentifiersInTestNamesOnlyOnChangedLines(t *testing.T) {
	t.Parallel()
	got := identifierNameFindings(t, fixtureLint(t), "pkg/claim/claim_test.go", map[int]bool{21: true, 22: true})
	if want := map[int]string{21: "CLAIM-R4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("findings %v, want only the changed line 21", got)
	}
}

func TestLintWithoutARegistryRefusesOnlyIdentifiersWithASeparator(t *testing.T) {
	t.Parallel()
	// Without a registry, a code is recognized only in upper case and
	// followed by a hyphen or an underscore, so line 19's title-case code
	// and the dropped hyphens of lines 12, 21, 23, 25, 31, and 43 pass.
	got := identifierNameFindings(t, Lint{}, "pkg/claim/claim_test.go", nil)
	want := map[int]string{10: "CLAIM-R4", 11: "CLAIM-I4", 17: "CLAIM-R4", 27: "POOL-R5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings without a registry %v, want %v", got, want)
	}
}

func TestLintReportsSectionAndIdentifierInOneNameSeparately(t *testing.T) {
	t.Parallel()
	src := []byte("package claim\n\nimport \"testing\"\n\nfunc TestClaimR4Holds_spec_4_7(t *testing.T) {}\n")
	found, err := fixtureLint(t).Check(ChangedFile{Path: "pkg/claim/claim_test.go", Src: src, Lines: allLines(src)})
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	var rules []Rule
	for _, f := range found {
		rules = append(rules, f.Rule)
	}
	if want := []Rule{SectionInTestName, IdentifierInTestName}; !reflect.DeepEqual(rules, want) {
		t.Errorf("rules %v, want %v", rules, want)
	}
}

func TestLintRefusesSpecificationTextInComments(t *testing.T) {
	t.Parallel()
	l := fixtureLint(t)
	got := check(t, l, "quotes.go.txt", "pkg/claim/claim.go", nil, QuotedSpecText)
	if want := []int{5, 12}; !reflect.DeepEqual(got, want) {
		t.Errorf("quoted-text findings at lines %v, want the quotation at 5 and the copied run at 12", got)
	}
}

func TestLintRefusesIdentifiersTheRegistryDoesNotCarry(t *testing.T) {
	t.Parallel()
	l := fixtureLint(t)
	src := readFixture(t, "lint", "identifiers.go.txt")
	found, err := l.Check(ChangedFile{Path: "pkg/claim/claim.go", Src: src, Lines: allLines(src)})
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	var got []string
	for _, f := range found {
		if f.Rule != UnresolvedIdentifier {
			t.Errorf("unexpected finding %s", f)
			continue
		}
		got = append(got, strings.Fields(f.Detail)[0])
	}
	want := []string{"CLAIM-I7", "CLAIM-acquire", "metric-claimsheld", "NOPAGE"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unresolved identifiers %q, want %q", got, want)
	}

	// An issue record under issues/ registers its identifier.
	l.Registry.AddIssuePaths([]string{"issues/CLAIM/CLAIM-I7-sweep-removal.md"})
	found, err = l.Check(ChangedFile{Path: "pkg/claim/claim.go", Src: src, Lines: allLines(src)})
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	for _, f := range found {
		if strings.HasPrefix(f.Detail, "CLAIM-I7 ") {
			t.Errorf("an issue with a record under issues/ was refused: %s", f)
		}
	}
}

func TestLintSkipsTheChecksWhoseInputsAreAbsent(t *testing.T) {
	t.Parallel()
	src := readFixture(t, "lint", "identifiers.go.txt")
	found, err := Lint{}.Check(ChangedFile{Path: "pkg/claim/claim.go", Src: src, Lines: allLines(src)})
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("a lint with no registry and no specification refused %v", found)
	}
}

func TestLintFailsOnASourceThatDoesNotParse(t *testing.T) {
	t.Parallel()
	_, err := Lint{}.Check(ChangedFile{Path: "pkg/claim/claim.go", Src: []byte("package claim\nfunc {"), Lines: map[int]bool{2: true}})
	if err == nil {
		t.Fatal("the lint certified a file it could not parse")
	}
}

func TestRegistryLoadDistinguishesAbsentFromMalformed(t *testing.T) {
	t.Parallel()
	if _, err := LoadRegistry(func(string) ([]byte, error) { return nil, errNotExist }, RegistryPath); !errors.Is(err, ErrNoRegistry) {
		t.Errorf("an absent registry returned %v, want ErrNoRegistry", err)
	}
	if _, err := LoadRegistry(func(string) ([]byte, error) { return []byte("{"), nil }, RegistryPath); err == nil || errors.Is(err, ErrNoRegistry) {
		t.Errorf("a malformed registry returned %v, want a parse error", err)
	}
	if _, err := LoadRegistry(func(string) ([]byte, error) { return []byte("{}"), nil }, RegistryPath); err == nil {
		t.Error("an empty registry was accepted")
	}
}

func TestQuotedSpansKeepOnlyQuotationsOfFiveWordsOrMore(t *testing.T) {
	t.Parallel()
	got := QuotedSpans(`a "short value" and “a quotation of exactly five words” and "one two three four five six"`)
	want := []string{"a quotation of exactly five words", "one two three four five six"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("QuotedSpans = %q, want %q", got, want)
	}
}
