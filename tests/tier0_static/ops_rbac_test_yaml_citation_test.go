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

// internalTrackingIDPattern matches an internal BUILD-GAPS.md finding id
// (e.g. "F-25.4.11") or TEST-GAPS.md finding id (e.g. "T-25.4.42") of the
// kind that must never appear in durable test code. These trackers are
// renumbered and rewritten as findings open and close; a citation embedded
// in test comments goes stale silently and leaks internal tracking
// structure into code meant to outlive it.
var internalTrackingIDPattern = regexp.MustCompile(`\b[FT]-\d+(?:\.\d+){1,2}\b`)

// TestOpsRBACHelmTestCitesOnlyDurableSpecSections asserts that
// charts/lenny/tests/ops-rbac_test.yaml cites the RBAC rules it pins with
// durable "// spec: §X.Y" references (plus prose), not internal
// BUILD-GAPS.md/TEST-GAPS.md finding ids. Finding ids are renumbered and
// closed over time; a citation naming one goes stale the moment the
// tracker entry moves, while a spec-section citation remains valid for as
// long as the section number does.
//
// spec: test-coverage.md's test-naming/citation rule: "Every test carries
// a `// spec:` annotation naming the spec sections it exercises." This
// citation form applies to this chart's helm-unittest fixtures the same
// way it applies to Go test comments.
//
// diagnosis: a match reports the offending line's 1-based line number and
// text. Replace the finding id with a "// spec: §25.4 (...)" citation and
// prose describing the rule being pinned, mirroring the surrounding
// comments in the same file.
func TestOpsRBACHelmTestCitesOnlyDurableSpecSections(t *testing.T) {
	root := schematest.RepoRoot(t)
	rel := filepath.Join("charts", "lenny", "tests", "ops-rbac_test.yaml")
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}

	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		if m := internalTrackingIDPattern.FindString(trimmed); m != "" {
			t.Errorf("%s:%d: comment cites internal tracking id %q; cite a durable "+
				"\"// spec: §X.Y\" section reference plus prose instead:\n\t%s",
				rel, i+1, m, trimmed)
		}
	}
}
