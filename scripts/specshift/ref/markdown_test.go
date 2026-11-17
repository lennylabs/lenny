// SPDX-License-Identifier: MIT

package ref

import (
	"context"
	"strings"
	"testing"
)

// These cases cover the Markdown reading of the reference pass, which is
// migration tooling rather than a platform behavior, so they carry no
// spec-section annotation.

func TestNaturalLess(t *testing.T) {
	if !naturalLess("POOL-R2", "POOL-R10") || naturalLess("POOL-R10", "POOL-R2") {
		t.Error("digit runs are not compared as numbers")
	}
	if !naturalLess("CLAIM-R9", "POOL-R1") || !naturalLess("POOL", "POOL-R1") || naturalLess("A", "A") {
		t.Error("text ordering is wrong")
	}
	if !naturalLess("R3", "R02") || naturalLess("R02", "R3") {
		t.Error("a longer digit run with a leading zero is ordered by length first")
	}
}

func TestLinksReadsEveryRelativeLinkOutsideExampleText(t *testing.T) {
	content := strings.Join([]string{
		"[a](x.md#one)[b](y.md) ![img](pic.png) [ext](https://example.com/z.md) [root](/abs.md)",
		"`[code](no.md)` <!-- [comment](no.md) --> [same](#here) [bare](#)",
		"```",
		"[fenced](no.md)",
		"```",
		"[titled](t.md \"Title\") [nested [label]](n.md)",
		"[ref]: r.md#frag",
		"[angle]: <skip.md>",
	}, "\n")
	var got []string
	for _, l := range links(content) {
		got = append(got, l.path+"#"+l.fragment+"@"+string(rune('0'+l.line)))
		if content[l.pathLo:l.pathHi] != l.path {
			t.Errorf("span of %q reads %q", l.path, content[l.pathLo:l.pathHi])
		}
	}
	want := "x.md#one@1 y.md#@1 #here@2 t.md#@6 n.md#@6 r.md#frag@7"
	if strings.Join(got, " ") != want {
		t.Errorf("links = %q, want %q", strings.Join(got, " "), want)
	}
}

func TestHeadingAnchors(t *testing.T) {
	anchors := headingAnchors("# Title\n\n## 4.6 Pod `Lifecycle` Controllers {#pod-lifecycle}\n\n```\n## Not a heading\n```\n")
	for _, a := range []string{"title", "46-pod-lifecycle-controllers", "pod-lifecycle"} {
		if !anchors[a] {
			t.Errorf("anchor %q missing from %v", a, anchors)
		}
	}
	if anchors["not-a-heading"] {
		t.Error("a heading inside a fence was read")
	}
}

func TestFrontMatter(t *testing.T) {
	if _, ok, err := parseFrontMatter("# No front matter\n"); ok || err != nil {
		t.Errorf("a page with no front matter: ok %v, err %v", ok, err)
	}
	if _, _, err := parseFrontMatter("---\ncode: POOL\n"); err == nil {
		t.Error("an unclosed front matter parsed")
	}
	if _, _, err := parseFrontMatter("---\ncode: [unclosed\n---\n"); err == nil {
		t.Error("malformed YAML parsed")
	}
	fm, ok, err := parseFrontMatter("---\ncode: POOL\naliases: [WPOOL]\n---")
	if err != nil || !ok || fm.Code != "POOL" || fm.Aliases[0] != "WPOOL" {
		t.Errorf("front matter at the end of a file: %+v %v %v", fm, ok, err)
	}
}

func TestBuildReportsMalformedFrontMatter(t *testing.T) {
	dir := copyFixture(t, cleanFixture)
	writeFile(t, dir, "spec/broken.md", "---\ncode: [BROKEN\n---\n# Broken\n")
	writeFile(t, dir, "spec/notes.md", "# A page with no code is not registered\n")
	reg, findings, err := Build(context.Background(), fixtureTree(dir, nil), emptyRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].File != "spec/broken.md" {
		t.Errorf("findings = %v", findings)
	}
	for _, p := range reg.Pages {
		if p.Path == "spec/notes.md" || p.Path == "spec/broken.md" {
			t.Errorf("page %s was registered", p.Path)
		}
	}
}

func TestOperationNeedsItsIdentifierWrittenBesideTheAnchor(t *testing.T) {
	if name, ok := operationName("KPCLAIM", "updatestatus", "\n### `KPCLAIM-updateStatus` {#u}"); !ok || name != "updateStatus" {
		t.Errorf("operationName = %q, %v", name, ok)
	}
	if _, ok := operationName("CLAIM", "set", "Claim set | The pod sets"); ok {
		t.Error("a term anchor that shares the code's prefix read as an operation")
	}
	if _, ok := operationName("CLAIM", "create", "GWCLAIM-create"); ok {
		t.Error("an identifier of a longer code matched")
	}
}
