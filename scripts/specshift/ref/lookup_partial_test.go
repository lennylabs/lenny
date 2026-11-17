// SPDX-License-Identifier: MIT

package ref

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This case pins lookup's reading of a partial stub whose successors lie
// outside the specification. The reference pass is migration tooling
// rather than a platform behavior, so it carries no spec-section
// annotation.

func TestLookupOfAPartialStubListsSuccessorsOutsideTheSpecification(t *testing.T) {
	root := copyFixture(t, cleanFixture)
	stub := "# 21. Planned\n\n" + partialStubLead + "\n\n" +
		"- [Status](../docs/about/status.md#planned-after-v1): the work planned after v1.\n\n" +
		"**21.1 Kept.** A kept paragraph that links [a guide](../docs/guide/pools.md) and [Pools](04_system-components.md#46-pod-lifecycle-controllers).\n"
	if err := os.WriteFile(filepath.Join(root, "spec", "21_planned.md"), []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, fixtureTree(root, nil), "lookup", "§"+"21")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "partial successor stub") || !strings.Contains(out, "docs/about/status.md#planned-after-v1") {
		t.Errorf("lookup of a partial stub did not list its successor:\n%s", out)
	}
	if strings.Contains(out, "docs/guide/pools.md") {
		t.Errorf("a link in the kept text was listed as a successor:\n%s", out)
	}
}
