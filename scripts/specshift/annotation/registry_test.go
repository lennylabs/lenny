// SPDX-License-Identifier: MIT

package annotation

import (
	"strings"
	"testing"
)

// These cases pin the lint's reading of spec/registry.json by its schema.
// The lint is tooling for the annotation rule rather than a platform
// behavior, so they carry no spec-section annotation.

func TestLintRefusesRetiredAndMoveOnlyIdentifiersAndNamesTheSuccessor(t *testing.T) {
	t.Parallel()
	l := fixtureLint(t)
	src := readFixture(t, "lint", "retired.go.txt")
	found, err := l.Check(ChangedFile{Path: "pkg/claim/hold.go", Src: src, Lines: allLines(src)})
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	byLine := map[int]Finding{}
	for _, f := range found {
		byLine[f.Line] = f
	}
	if f, ok := byLine[7]; !ok || f.Rule != RetiredIdentifier || !strings.Contains(f.Detail, "successor CLAIM-R9") {
		t.Errorf("the citation of CLAIM-R2 gave %+v; want a retired-identifier finding naming CLAIM-R9", f)
	}
	if f, ok := byLine[12]; !ok || f.Rule != RetiredIdentifier || !strings.Contains(f.Detail, "no successor") {
		t.Errorf("the citation of CLAIM-R3 gave %+v; want a retired-identifier finding naming its removal", f)
	}
	if f, ok := byLine[17]; !ok || f.Rule != UnresolvedIdentifier {
		t.Errorf("the citation of CLAIM-R7, which only a move record names, gave %+v; want an unresolved identifier", f)
	}
	if f, ok := byLine[22]; ok {
		t.Errorf("a live citation was refused: %s", f)
	}
	if len(found) != 3 {
		t.Errorf("findings %v, want three", found)
	}
}

func TestRegistryRegistersNoPathOrRecordField(t *testing.T) {
	t.Parallel()
	l := fixtureLint(t)
	for _, name := range []string{
		"spec/capabilities/runtimes-and-pools/pod-claims.md",
		"capability",
		"claimHoldTTLSeconds",
		"removed: the hold no longer expires",
		"claim-r2",
		"CLAIM-R7",
	} {
		if l.Registry.Has(name) {
			t.Errorf("%q resolves, but it is not a live identifier", name)
		}
	}
	for _, name := range []string{"CLAIM", "GWCLAIM", "CLAIM-R4", "claim-r4", "KPCLAIM-create", "reserved-hold", "setting-claimholdttlseconds", "CLAIM-I4"} {
		if !l.Registry.Has(name) {
			t.Errorf("%q does not resolve, but it is a live identifier", name)
		}
	}
}

func TestRegistryPageCodesHoldEveryLiveCodeAndAliasInUpperCase(t *testing.T) {
	t.Parallel()
	doc := `{"version": 1, "pages": [
		{"code": "CLAIM", "path": "spec/a.md", "type": "capability", "status": "", "aliases": ["hold"]},
		{"code": "POOL", "path": "spec/b.md", "type": "capability", "status": ""}
	], "requirements": [], "operations": [], "terms": [], "blocks": [], "retired": [], "moves": []}`
	reg, err := LoadRegistry(func(string) ([]byte, error) { return []byte(doc), nil }, RegistryPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := reg.PageCodes()
	if want := []string{"CLAIM", "HOLD", "POOL"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("PageCodes = %v, want %v", got, want)
	}
	// The returned slice is a copy, so the lint's sort of it leaves the
	// registry's order alone.
	got[0] = "X"
	if reg.PageCodes()[0] != "CLAIM" {
		t.Error("PageCodes returned the registry's own slice")
	}
	// An alias stands for its page in a test name, as the code does.
	src := []byte("package claim\n\nimport \"testing\"\n\nfunc TestHoldR2Expires(t *testing.T) {}\n")
	found, err := Lint{Registry: reg}.Check(ChangedFile{Path: "pkg/claim/claim_test.go", Src: src, Lines: allLines(src)})
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if len(found) != 1 || found[0].Rule != IdentifierInTestName || !strings.Contains(found[0].Detail, "HOLD-R2") {
		t.Errorf("findings %v, want one identifier-name finding for HOLD-R2", found)
	}
}

func TestTestNameCheckIgnoresCodesOutsideThePageCodeForm(t *testing.T) {
	t.Parallel()
	load := func(codes ...string) *Registry {
		var pages []string
		for _, c := range codes {
			pages = append(pages, `{"code": "`+c+`", "path": "spec/a.md", "type": "capability", "status": ""}`)
		}
		doc := `{"version": 1, "pages": [` + strings.Join(pages, ",") +
			`], "requirements": [], "operations": [], "terms": [], "blocks": [], "retired": [], "moves": []}`
		reg, err := LoadRegistry(func(string) ([]byte, error) { return []byte(doc), nil }, RegistryPath)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		return reg
	}
	src := []byte("package claim\n\nimport \"testing\"\n\nfunc TestClaimR4Holds(t *testing.T) {}\n\nfunc TestRetriesR5Times(t *testing.T) {}\n")
	lint := func(reg *Registry) []Finding {
		t.Helper()
		found, err := Lint{Registry: reg}.Check(ChangedFile{Path: "pkg/claim/claim_test.go", Src: src, Lines: allLines(src)})
		if err != nil {
			t.Fatalf("lint: %v", err)
		}
		return found
	}
	// A multi-byte or lower-case code is left out rather than breaking
	// the matcher, and the valid code beside it still matches.
	if found := lint(load("ÉCLAIR", "x", "CLAIM")); len(found) != 1 || !strings.Contains(found[0].Detail, "CLAIM-R4") {
		t.Errorf("findings %v, want one for CLAIM-R4", found)
	}
	// With no code left, a bare `R5` is not read as an identifier.
	if found := lint(load("ÉCLAIR")); len(found) != 0 {
		t.Errorf("findings %v, want none when no registered code has the page-code form", found)
	}
}
