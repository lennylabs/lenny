// SPDX-License-Identifier: MIT

// Shared surface walker for the tier-11 retired-path sweeps.
//
// Two pod-side paths were retired when every session gained a slot: the
// pod-global working directory and the pod-global credential file. Each
// retirement is asserted as a mechanical sweep rather than as a list of
// the sites a change happened to reach, and both sweeps read the same
// set of surfaces: the specification, the documentation, the schemas,
// the chart, the commands (the scaffold templates, the reference
// runtimes, and the compliance harness), the runtime SDKs, and the
// served OpenAPI document. A missed edit in a template, a chart
// comment, a proto comment, or a comment beside an SDK default is
// invisible to the compiler, which is why the walk is by directory
// rather than by enumerated file.
//
// spec: 6.1 (pod filesystem volumes), 6.4 (per-session workspace layout)

package tier11_docs_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// retirementSweepRoots are the directories a retirement reaches,
// relative to the repository root.
var retirementSweepRoots = []string{
	"spec",
	"docs",
	"schemas",
	"charts",
	"cmd",
	"sdks",
}

// retirementSweepExtensions are the carriers a retired literal can hide
// in. Every reader-facing document, schema (both the JSON schemas and
// the proto definitions), chart template, scaffold template, reference
// runtime, and SDK source under the swept roots is one of these.
var retirementSweepExtensions = map[string]bool{
	".md":    true,
	".yaml":  true,
	".yml":   true,
	".json":  true,
	".go":    true,
	".tmpl":  true,
	".py":    true,
	".ts":    true,
	".tsx":   true,
	".js":    true,
	".sh":    true,
	".txt":   true,
	".proto": true,
}

// retirementSweepSkipDirs are directories whose contents are neither
// authored nor read as the current contract: dependency trees, build
// output, and fixtures recorded as they were written.
var retirementSweepSkipDirs = map[string]bool{
	"node_modules": true,
	"dist":         true,
	"testdata":     true,
	"__pycache__":  true,
	".git":         true,
}

// retirementSweepSurfaces returns every file a retirement sweep reads:
// the carriers under the swept roots, plus the served OpenAPI document,
// which is generated rather than authored and so is swept by path
// rather than by directory.
func retirementSweepSurfaces(t *testing.T, root string) []string {
	t.Helper()
	var swept []string
	for _, rel := range retirementSweepRoots {
		dir := filepath.Join(root, rel)
		walked := 0
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if retirementSweepSkipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !retirementSweepExtensions[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			walked++
			swept = append(swept, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", rel, err)
		}
		if walked == 0 {
			t.Fatalf("walk %s: no files swept (moved or renamed?)", rel)
		}
	}
	return append(swept,
		filepath.Join(root, "pkg", "gateway", "externalapi", "openapi", "openapi.json"))
}
