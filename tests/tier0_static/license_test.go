// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
// diagnosis: A Go source file under pkg/, cmd/, or tests/ is missing the
//
//	"// SPDX-License-Identifier: MIT" header. ADR-0008 requires
//	every Go file to carry the SPDX identifier so license
//	compliance can be machine-checked. Add the header on the
//	first line (or the second line if the file starts with a
//	//go:build directive).
func TestEveryGoFileHasSPDXHeader(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	// Enumerate git-tracked files rather than walking the filesystem so
	// that generated, gitignored artifacts (build output, the Kind
	// bootstrap overlay) are never flagged. The check applies to files
	// committed under pkg/, cmd/, and tests/.
	roots := []string{"pkg/", "cmd/", "tests/"}
	var missing []string
	for _, rel := range schematest.TrackedFiles(t) {
		if filepath.Ext(rel) != ".go" {
			continue
		}
		if !underAny(rel, roots) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(data), "SPDX-License-Identifier: MIT") {
			missing = append(missing, rel)
		}
	}
	for _, m := range missing {
		t.Errorf("missing SPDX header: %s", m)
	}
}

// underAny reports whether rel sits under any of the given top-level
// prefixes (each prefix ends in a slash).
func underAny(rel string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

// spec: 13.0 (license headers on non-Go files: shell, YAML, Python, TypeScript)
// diagnosis: A tracked .sh / .yaml / .yml / .py / .ts file is missing the
//
//	# SPDX-License-Identifier: MIT header. ADR-0008 covers
//	source files of every language. Add the header at the top
//	of the file using the appropriate comment syntax (#  for
//	shell/yaml/python; // for typescript).
func TestEveryNonGoFileHasSPDXHeader(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	// Extensions and the syntactic comment marker that may carry
	// the SPDX header. Each entry: (ext, requiredPrefix).
	exts := map[string]string{
		".sh":   "# SPDX-License-Identifier:",
		".yaml": "# SPDX-License-Identifier:",
		".yml":  "# SPDX-License-Identifier:",
		".py":   "# SPDX-License-Identifier:",
		".ts":   "// SPDX-License-Identifier:",
	}
	// Files exempt from the header (generated, vendored, fixtures).
	exempt := func(rel string) bool {
		for _, prefix := range []string{
			"tests/results/",
			"tests/testdata/",
			"sdks/vendor/",
			"compose/otel-config.yaml",          // upstream-style config; SPDX would clutter
			"tests/tier7b_load_kind/baselines/", // generated baselines
		} {
			if strings.HasPrefix(rel, prefix) {
				return true
			}
		}
		// .github action workflows have their own license tooling.
		return false
	}
	// Enumerate git-tracked files so generated, gitignored artifacts
	// (build output under dist/ and node_modules/, the Kind bootstrap
	// overlay, cloud values overrides) are never flagged.
	rootPrefixes := []string{"scripts/", "compose/", "sdks/", "tests/"}
	var missing []string
	for _, rel := range schematest.TrackedFiles(t) {
		marker, ok := exts[filepath.Ext(rel)]
		if !ok {
			continue
		}
		if !underAny(rel, rootPrefixes) {
			continue
		}
		if exempt(rel) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(data), marker) {
			missing = append(missing, rel)
		}
	}
	for _, m := range missing {
		t.Errorf("missing SPDX header: %s", m)
	}
}
