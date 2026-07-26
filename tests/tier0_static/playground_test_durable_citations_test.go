// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// buildGapIDPattern matches a retired BUILD-GAPS.md finding id of the form
// F-<section>.<n>[.<n>]. It deliberately narrows
// internalTrackingIDPattern (which also matches TEST-GAPS.md T- ids) to
// the build-gap half: the build-gap pass is retired and its ids resolve
// to nothing, whereas a T- id may still name an open finding that a
// skipped test's diagnosis comment legitimately points a reader at.
var buildGapIDPattern = regexp.MustCompile(`\bF-\d+(?:\.\d+){1,2}\b`)

// TestPlaygroundTestsCiteOnlyDurableSpecSections asserts that every
// playground test file on disk traces the behavior it pins through a
// durable "// spec: §X.Y" section reference rather than through a
// retired BUILD-GAPS.md finding id. The build-gap pass that produced the
// F-<section>.<n> ids is retired, so those ids resolve to nothing in the
// repository: a reader who follows one recovers no requirement, while
// the spec citation sitting beside it already carries the traceability
// the harness relies on.
//
// The sweep covers the whole §27 playground test surface, wherever those
// files live, rather than the playground package alone. The ids
// originally landed across pkg/gateway/mcpfabric/playground,
// pkg/gateway/sessionserver, pkg/preflight, and cmd/lenny-gateway, so a
// package-scoped guard would leave the same defect reachable from a
// sibling file.
//
// spec: test-coverage.md's citation rule — "Every test carries a
// `// spec:` annotation naming the spec sections it exercises (form:
// `// spec: 4.6.1 (warm pool controller), 12.3 (postgres ha)`). The
// harness maps tests to spec sections through this annotation." —
// together with spec-driven-development.md, "Cite the spec on
// spec-derived logic with `// spec: §X.Y` ... A reviewer traces any
// behavior to its section through that citation." The annotation is the
// durable tie; a tracker id is not one.
//
// diagnosis: a match reports the offending file, its 1-based line
// number, and the line text. Delete the finding id and keep (or add) the
// "// spec: §X.Y (...)" citation and the prose describing the behavior
// the test pins. Do not renumber the id or move it elsewhere in the
// file; the tracker it names no longer exists.
func TestPlaygroundTestsCiteOnlyDurableSpecSections(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	files := playgroundTestFiles(t, root)
	if len(files) == 0 {
		t.Fatal("found no playground test files under the repository root; the sweep is broken, since §27 coverage lives in pkg/gateway/mcpfabric/playground at minimum")
	}

	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			// The id has appeared in both comments and assertion
			// message literals, so the scan is line-wide rather than
			// comment-only.
			if m := buildGapIDPattern.FindString(line); m != "" {
				t.Errorf("%s:%d: names retired build-gap id %q, which resolves to no "+
					"tracker in the repository; cite a durable \"// spec: §X.Y\" section "+
					"reference plus prose instead:\n\t%s",
					rel, i+1, m, strings.TrimSpace(line))
			}
		}
	}
}
