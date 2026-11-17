// SPDX-License-Identifier: MIT

package pagegate

import (
	"strings"
	"testing"
)

// This case pins the normative-keyword ban to every page type that
// "Normative language" in the conventions names. It covers specification
// tooling rather than a platform behavior, so it carries no specification
// annotation.

func TestNormativeKeywordsAreRefusedOnEveryNonNormativePageType(t *testing.T) {
	t.Parallel()
	body := "# Page\n\nA pool MUST keep its minimum.\n"
	for _, typ := range []string{"overview", "walkthrough", "index", "catalog"} {
		problems := checkOne(t, page("XPAGE", typ, "", body))
		assertProblem(t, problems, 8, "a page of type "+typ+" holds no normative keyword")
	}
	for _, typ := range []string{"capability", "component", "interface"} {
		for _, p := range checkOne(t, page("XPAGE", typ, "", body)) {
			if strings.Contains(p.Message, "normative keyword") {
				t.Errorf("a %s page was refused a normative keyword: %s", typ, p)
			}
		}
	}
}
