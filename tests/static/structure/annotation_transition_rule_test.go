// SPDX-License-Identifier: MIT

package structure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The rules that tell an author when new code cites an identifier and when
// it may still cite a section number state one trigger, once, in the
// "Transition" section of .claude/rules/spec-annotations.md: the page the
// code implements has landed, which is when its status is neither
// placeholder nor partial. The other rules point there. These are project
// rules rather than a behavior the specification states, so this gate
// carries no specification annotation.

// diagnosis: a rule restates when code switches from section citations to
// identifiers with a different trigger, such as "once the page exists" or
// "in an area that has not migrated", or the annotation rule no longer
// states the landed-page trigger. Edit the rule to point at "Transition" in
// spec-annotations.md.
func TestAnnotationTransitionTriggerIsStatedOnce(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(root, ".claude", "rules", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	const trigger = "cites identifiers once the page it implements has landed, which is when that page's `status` is neither `placeholder` nor `partial`"
	if got := strings.Count(read("spec-annotations.md"), trigger); got != 1 {
		t.Errorf("spec-annotations.md states the transition trigger %d time(s), want once: %q", got, trigger)
	}
	stale := []string{
		"once the page exists",
		"once the page the code implements exists",
		"once the page it implements exists",
		"in an area that has not migrated",
		"until then",
	}
	for _, name := range []string{"spec-annotations.md", "spec-citations.md", "spec-driven-development.md", "test-coverage.md"} {
		text := read(name)
		for _, s := range stale {
			if strings.Contains(text, s) {
				t.Errorf("%s states the transition with %q; point at \"Transition\" in spec-annotations.md instead", name, s)
			}
		}
		if name != "spec-annotations.md" && !strings.Contains(text, "\"Transition\"") {
			t.Errorf("%s does not point at \"Transition\" in spec-annotations.md", name)
		}
	}
}
