// SPDX-License-Identifier: MIT

package ref

import (
	"context"
	"strings"
	"testing"
)

// These cases pin the placeholder rules of "Page status" in the
// conventions: a placeholder registers no identifier, and links-into lists
// the page-level links into a page. The reference pass is migration
// tooling rather than a platform behavior, so they carry no spec-section
// annotation.

func TestCheckReportsAnIdentifierOnAPlaceholder(t *testing.T) {
	dir := copyFixture(t, cleanFixture)
	const placement = "spec/components/placement.md"
	writeFile(t, dir, placement, readFile(t, dir, placement)+strings.Join([]string{
		"",
		`<a id="place-r1"></a>**PLACE-R1. A pool has one node class.** Text.`,
		"",
		"| Term | Definition |",
		"| :-- | :-- |",
		`| <a id="node-class"></a>Node class | A class of nodes. |`,
		"",
		"```lenny-def",
		"kind: setting",
		"name: place.nodeClass",
		"summary: The node class.",
		"```",
		"",
	}, "\n"))
	findings, err := Check(context.Background(), fixtureTree(dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, f := range findings {
		all = append(all, f.String())
	}
	joined := strings.Join(all, "\n")
	for _, want := range []string{
		"requirement PLACE-R1 is registered on PLACE, which is a placeholder",
		"term #node-class is registered on PLACE, which is a placeholder",
		"definition block #setting-place-nodeclass is registered on PLACE, which is a placeholder",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("check does not report %q:\n%s", want, joined)
		}
	}
}

func TestLinksIntoListsEveryPageLevelLink(t *testing.T) {
	dir := copyFixture(t, cleanFixture)
	out, err := run(t, fixtureTree(dir, nil), "links-into", "PLACE")
	if err != nil {
		t.Fatalf("links-into: %v\n%s", err, out)
	}
	for _, want := range []string{
		"PLACE (spec/components/placement.md): 2 page-level link(s)",
		"docs/guide/pools.md:3: ../../spec/components/placement.md",
		"spec/capabilities/pools/pod-claims.md:21: ../../components/placement.md",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("links-into output lacks %q:\n%s", want, out)
		}
	}
	// A link with a fragment is an identifier link rather than a
	// page-level one.
	out, err = run(t, fixtureTree(dir, nil), "links-into", "POOL")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "#pool-r1") {
		t.Errorf("links-into listed a link with a fragment:\n%s", out)
	}
	if _, err := run(t, fixtureTree(dir, nil), "links-into", "NOPE"); err == nil {
		t.Error("links-into accepted a code no page registers")
	}
}
