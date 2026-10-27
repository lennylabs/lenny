// SPDX-License-Identifier: MIT

package tier11_docs_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// retiredProductionKey is the chart value that once carried lenny-ops
// production mode beside the environment value. Production mode is the
// environment value prod alone, so no operator page may name the key.
const retiredProductionKey = "ops.production"

// spec: 17.9.1 (Composition Dimensions) "Production mode"
// spec: 25.11
// diagnosis: An operator page names the retired ops.production chart key
//
//	or describes a capacity-tier preset as turning on lenny-ops production
//	behavior. Production mode is the environment value prod, whichever
//	preset is layered; rewrite the page to key the behavior on the prod
//	environment.
func TestOperatorDocsKeyProductionModeOnEnvironment(t *testing.T) {
	root := repoRoot(t)
	docsDir := filepath.Join(root, "docs")
	err := filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, retiredProductionKey) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d names the retired %s key: %q", rel, i+1, retiredProductionKey, line)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}

	wizard := filepath.Join(docsDir, "operator-guide", "install-wizard.md")
	body, err := os.ReadFile(wizard)
	if err != nil {
		t.Fatalf("read install wizard page: %v", err)
	}
	flat := strings.Join(strings.Fields(string(body)), " ")
	const want = "In the `prod` environment, lenny-ops requires confirmation for full backups, whichever preset is layered."
	if !strings.Contains(flat, want) {
		t.Errorf("install wizard preset paragraph does not key the full-backup confirmation on the prod environment; want %q", want)
	}
}
