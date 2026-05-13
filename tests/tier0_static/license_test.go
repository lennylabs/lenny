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
