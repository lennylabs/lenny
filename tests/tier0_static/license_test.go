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
	roots := []string{"pkg", "cmd", "tests"}
	var missing []string
	for _, top := range roots {
		base := filepath.Join(root, top)
		if _, err := os.Stat(base); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !strings.Contains(string(data), "SPDX-License-Identifier: MIT") {
				rel, _ := filepath.Rel(root, path)
				missing = append(missing, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}
	if len(missing) > 0 {
		for _, m := range missing {
			t.Errorf("missing SPDX header: %s", m)
		}
	}
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
	roots := []string{"scripts", "compose", "sdks", "tests"}
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
			"compose/otel-config.yaml",    // upstream-style config; SPDX would clutter
			"tests/tier7_load/baselines/", // generated baselines
		} {
			if strings.HasPrefix(rel, prefix) {
				return true
			}
		}
		// .github action workflows have their own license tooling.
		return false
	}
	var missing []string
	for _, top := range roots {
		base := filepath.Join(root, top)
		if _, err := os.Stat(base); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			ext := filepath.Ext(path)
			marker, ok := exts[ext]
			if !ok {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			if exempt(rel) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !strings.Contains(string(data), marker) {
				missing = append(missing, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}
	if len(missing) > 0 {
		for _, m := range missing {
			t.Errorf("missing SPDX header: %s", m)
		}
	}
}
