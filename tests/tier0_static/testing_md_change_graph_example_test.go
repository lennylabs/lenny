// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// changeGraphExampleBlock extracts the ```json fenced code block that
// immediately follows the "### Change graph" heading in TESTING.md. It
// is the illustrative format example for tests/change-graph.json, kept
// distinct from the live tests/change-graph.json file validated by the
// TestChangeGraph*SelectsTier tests elsewhere in this package.
func changeGraphExampleBlock(t *testing.T, content string) string {
	t.Helper()
	idx := strings.Index(content, "### Change graph")
	if idx < 0 {
		t.Fatal(`TESTING.md: no "### Change graph" heading found`)
	}
	rest := content[idx:]
	fenceStart := strings.Index(rest, "```json")
	if fenceStart < 0 {
		t.Fatal("TESTING.md: no ```json fence found after the \"### Change graph\" heading")
	}
	rest = rest[fenceStart+len("```json"):]
	fenceEnd := strings.Index(rest, "```")
	if fenceEnd < 0 {
		t.Fatal("TESTING.md: unterminated ```json fence after the \"### Change graph\" heading")
	}
	return rest[:fenceEnd]
}

// spec: TESTING.md §5 "`tests/change-graph.json` maps source packages,
// schemas, migrations, and chart templates to the tests that exercise
// them," illustrated by the format example JSON block immediately
// following that sentence.
// diagnosis: The TESTING.md change-graph format example is illustrative
// prose, not the live tests/change-graph.json, so `lenny-test
// validate-maps` never inspects it. A reader who copies an entry from
// the example (the migrations/ "static" test, or the pkg/store/session
// "integration" test) as a template for a new tests/change-graph.json
// mapping would copy a path that resolves to nothing. Every test-file
// path this test names must exist on disk under the tier the example
// claims for it, and the example must not regress to a path this test
// was added to catch.
func TestTESTINGmdChangeGraphExampleReferencesResolve(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "TESTING.md"))
	if err != nil {
		t.Fatalf("read TESTING.md: %v", err)
	}
	content := string(body)

	block := changeGraphExampleBlock(t, content)

	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(block), &doc); err != nil {
		t.Fatalf("parse the TESTING.md change-graph example JSON block: %v", err)
	}

	// Two entries the example previously got wrong: the migrations/
	// static test (was tests/tier0_static/schema_lint_test.go, a file
	// that was never written; the real static schema check lives at
	// tests/tier0_static/schemas_test.go) and the pkg/store/session
	// integration test (tests/tier4_integration/checkpoint_resume_test.go),
	// which now exists on disk and must stay cited at this tier rather
	// than drift to a different one.
	for _, tc := range []struct {
		sourceKey string
		tier      string
		wantPath  string
	}{
		{"migrations/", "static", "tests/tier0_static/schemas_test.go"},
		{"pkg/store/session", "integration", "tests/tier4_integration/checkpoint_resume_test.go"},
	} {
		raw, ok := doc[tc.sourceKey]
		if !ok {
			t.Errorf("TESTING.md change-graph example no longer has a %q entry; update this test if the example intentionally changed", tc.sourceKey)
			continue
		}
		var perTier map[string][]string
		if err := json.Unmarshal(raw, &perTier); err != nil {
			t.Fatalf("parse the %q entry of the TESTING.md change-graph example: %v", tc.sourceKey, err)
		}
		paths := perTier[tc.tier]
		found := false
		for _, p := range paths {
			if p == tc.wantPath {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("TESTING.md change-graph example %q %q list is %v; expected it to include %q", tc.sourceKey, tc.tier, paths, tc.wantPath)
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(tc.wantPath))
		if info, err := os.Stat(full); err != nil || info.IsDir() {
			t.Errorf("TESTING.md change-graph example references %q, but it does not exist on disk: %v", tc.wantPath, err)
		}
	}

	// Guard against reintroducing the dangling reference this test was
	// added to catch.
	if strings.Contains(block, "schema_lint_test.go") {
		t.Errorf("TESTING.md change-graph example references the nonexistent tests/tier0_static/schema_lint_test.go; the real static schema check is tests/tier0_static/schemas_test.go")
	}
}
