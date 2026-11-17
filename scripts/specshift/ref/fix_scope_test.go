// SPDX-License-Identifier: MIT

package ref

import "testing"

// This case pins the scope of fix's same-page rewrite. The reference
// pass is migration tooling rather than a platform behavior, so it
// carries no spec-section annotation.

func TestFixLeavesSamePageFragmentsOutsideTheNewTree(t *testing.T) {
	dir := copyFixture(t, cleanFixture)
	// "claim" is a term registered on the pod-claims page. Outside the new
	// tree, #claim is a local heading anchor and fix leaves it alone.
	local := "# Local\n\nSee [the claim](#claim).\n\n## Claim\n\nText.\n"
	outside := []string{"docs/guide/local.md", ".claude/rules/local.md", "spec/05_local-section.md"}
	for _, rel := range outside {
		writeFile(t, dir, rel, local)
	}
	if _, err := run(t, fixtureTree(dir, nil), "fix"); err != nil {
		t.Fatal(err)
	}
	for _, rel := range outside {
		if got := readFile(t, dir, rel); got != local {
			t.Errorf("fix rewrote a same-page link in %s:\n%s", rel, got)
		}
	}
	// A page of the new tree still follows a moved identifier.
	mustCheckClean(t, fixtureTree(dir, nil))
}
