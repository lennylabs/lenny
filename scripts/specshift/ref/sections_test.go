// SPDX-License-Identifier: MIT

package ref

import (
	"reflect"
	"strings"
	"testing"
)

// NumberedHeadings is the stub reader the coverage view of
// cmd/lenny-test reads section citations through. It is contributor
// tooling over the specification's own structure, so these cases carry
// no spec-section annotation.

// numberedFixture is a numbered file with a section that has not
// migrated, a full stub, a partial stub, and an unnumbered heading.
func numberedFixture() string {
	return strings.Join([]string{
		"# 5. Pools",
		"",
		"Intro text.",
		"",
		"## 5.1 Warm pools",
		"",
		"Text that stays.",
		"",
		"### 5.1.1 Claims {#claims}",
		"",
		StubMarker,
		"",
		fullStubLead,
		"",
		"- [POOL-R5](capabilities/pools/pool.md#pool-r5): Claim ordering",
		"- [POOL](capabilities/pools/pool.md): Pools",
		"",
		"## 5.2 Sizing",
		"",
		partialStubLead,
		"",
		"- [`setting-minwarm`](capabilities/pools/sizing.md#setting-minwarm): Minimum warm",
		"",
		"Kept text with a [link](04_system-components.md#41-x).",
		"",
		"#### Unnumbered",
		"",
		"Text.",
	}, "\n")
}

func TestNumberedHeadingsReadEachSectionAndItsStub(t *testing.T) {
	t.Parallel()
	got := NumberedHeadings("spec/05_pools.md", numberedFixture())
	type row struct {
		Number, Anchor, Stub string
		Successors           []StubLink
	}
	var rows []row
	for _, h := range got {
		rows = append(rows, row{h.Number, h.Anchor, h.Stub, h.Successors})
	}
	want := []row{
		{"5", "5-pools", NotStub, nil},
		{"5.1", "51-warm-pools", NotStub, nil},
		{"5.1.1", "claims", FullStub, []StubLink{
			{Path: "spec/capabilities/pools/pool.md", Fragment: "pool-r5"},
			{Path: "spec/capabilities/pools/pool.md"},
		}},
		{"5.2", "52-sizing", PartialStub, []StubLink{
			{Path: "spec/capabilities/pools/sizing.md", Fragment: "setting-minwarm"},
		}},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("NumberedHeadings:\n got %+v\nwant %+v", rows, want)
	}
	if got[2].File != "spec/05_pools.md" || got[2].Line != 9 || !strings.HasPrefix(got[2].Title, "5.1.1 Claims") {
		t.Fatalf("heading place: %+v", got[2])
	}
}

func TestNumberedHeadingsOfAFileWithNoNumberedHeadingIsEmpty(t *testing.T) {
	t.Parallel()
	if got := NumberedHeadings("spec/05_pools.md", "# Title\n\nText.\n"); len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
	if got := NumberedHeadings("spec/05_pools.md", ""); len(got) != 0 {
		t.Fatalf("empty content: got %+v, want none", got)
	}
}

func TestIsNumberedFileMatchesOnlyTheNumberedFiles(t *testing.T) {
	t.Parallel()
	for p, want := range map[string]bool{
		"spec/04_system-components.md":    true,
		"spec/conventions.md":             false,
		"spec/capabilities/pools/pool.md": false,
		"docs/04_x.md":                    false,
	} {
		if got := IsNumberedFile(p); got != want {
			t.Errorf("IsNumberedFile(%q) = %v, want %v", p, got, want)
		}
	}
}
