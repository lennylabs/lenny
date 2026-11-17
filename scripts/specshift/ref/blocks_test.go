// SPDX-License-Identifier: MIT

package ref

import (
	"context"
	"strings"
	"testing"
)

// These cases pin the registry's reading of definition blocks to the
// block parser of scripts/specshift/defs. The reference pass is migration
// tooling rather than a platform behavior, so they carry no spec-section
// annotation.

// blockPage returns a page of the new tree with the given code, type, and
// body.
func blockPage(code, typ, body string) string {
	return "---\ncode: " + code + "\ntype: " + typ + "\nsummary: A page.\n---\n\n# " + code + "\n\n" + body
}

func TestRegistryNamesEachBlockByItsKindsNameField(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "spec/capabilities/pools/claims.md", blockPage("CLAIM", "capability", strings.Join([]string{
		"```lenny-def",
		"kind: event",
		"type: dev.lenny.pod_claimed",
		"summary: A pod was claimed.",
		"```",
		"",
		"```lenny-def",
		"kind: label",
		"key: lenny.dev/tenant-id",
		"summary: The tenant.",
		"```",
		"",
		"````markdown",
		"```lenny-def",
		"kind: setting",
		"name: example.inner",
		"```",
		"````",
		"",
	}, "\n")))
	reg, findings, err := Build(context.Background(), fixtureTree(dir, nil), emptyRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings: %v", findings)
	}
	got := map[string]string{}
	for _, b := range reg.Blocks {
		got[b.Anchor] = b.Name
	}
	want := map[string]string{
		"event-dev-lenny-pod-claimed": "dev.lenny.pod_claimed",
		"label-lenny-dev-tenant-id":   "lenny.dev/tenant-id",
	}
	if len(got) != len(want) {
		t.Fatalf("blocks = %v, want %v", got, want)
	}
	for anchor, name := range want {
		if got[anchor] != name {
			t.Errorf("block %s: name %q, want %q", anchor, got[anchor], name)
		}
	}
}

func TestOperationBlocksRegisterOperationIdentifiersPerPage(t *testing.T) {
	dir := t.TempDir()
	for _, code := range []string{"KPA", "KPB"} {
		lower := strings.ToLower(code)
		writeFile(t, dir, "spec/interfaces/k/"+code+".md", blockPage(code, "interface", strings.Join([]string{
			`<a id="` + lower + `-create"></a>`,
			"",
			"```lenny-def",
			"kind: operation",
			"name: create",
			"summary: Creates one.",
			"```",
			"",
		}, "\n")))
	}
	reg, findings, err := Build(context.Background(), fixtureTree(dir, nil), emptyRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("two interface pages with an operation named create: %v", findings)
	}
	if len(reg.Blocks) != 0 || len(reg.Terms) != 0 {
		t.Errorf("an operation block was registered as a block or a term: blocks %+v, terms %+v", reg.Blocks, reg.Terms)
	}
	x := newIndex(reg)
	for _, c := range []struct{ id, anchor, file string }{
		{"KPA-create", "kpa-create", "spec/interfaces/k/KPA.md"},
		{"KPB-create", "kpb-create", "spec/interfaces/k/KPB.md"},
	} {
		if got := x.idAnchor[c.id]; got != c.anchor {
			t.Errorf("operation %s: anchor %q, want %q", c.id, got, c.anchor)
		}
		if got := x.anchorFile[c.anchor]; got != c.file {
			t.Errorf("anchor %s: file %q, want %q", c.anchor, got, c.file)
		}
	}
}

func TestRegistryReportsABlockTheSchemaCannotName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "spec/components/x.md", blockPage("XCOMP", "component", strings.Join([]string{
		"```lenny-def",
		"type: integer",
		"```",
		"",
		"```lenny-def",
		"kind: gadget",
		"name: g",
		"```",
		"",
		"```lenny-def",
		"kind: event",
		"name: not-the-name-field",
		"```",
		"",
	}, "\n")))
	_, findings, err := Build(context.Background(), fixtureTree(dir, nil), emptyRegistry())
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, f := range findings {
		msgs = append(msgs, f.Message)
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{"has no kind: line", `kind "gadget" is not a defined kind`, "of kind event has no type: line"} {
		if !strings.Contains(joined, want) {
			t.Errorf("findings %q do not report %q", joined, want)
		}
	}
}
