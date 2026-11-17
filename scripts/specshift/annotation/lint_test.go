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
