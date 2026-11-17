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
