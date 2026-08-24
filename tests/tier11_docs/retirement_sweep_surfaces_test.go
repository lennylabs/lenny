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
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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

// The sweep has no carrier whitelist. The predicate is stated over the
// directory set, so every authored file under a swept root is read
// whatever its extension: a Helm helper template (.tpl), a packaging
// manifest (.toml), an ECMAScript module (.mjs), a published HTML page,
// and an extensionless script are all carriers a retired literal can
// hide in, and enumerating extensions reintroduces the enumerated-subset
// hole the directory-wide predicate exists to close. Only binary content
// is skipped, because no authored statement lives in it.

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
			if !d.Type().IsRegular() {
				return nil
			}
			if isBinaryFile(t, path) {
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

// isBinaryFile reports whether a swept candidate holds binary content,
// which is decided the way the common text tools decide it: a NUL byte
// in the leading bytes. A binary file carries no authored statement, so
// the sweep skips it rather than matching a literal inside compiled or
// compressed content.
func isBinaryFile(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var head [512]byte
	n, err := f.Read(head[:])
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read %s: %v", path, err)
	}
	return bytes.IndexByte(head[:n], 0) >= 0
}

// spec: 6.4
// diagnosis: the retirement sweeps read a subset of the directories they
//
//	claim to cover. The predicate is stated over the directory set, so an
//	authored file under a swept root that the walker declines to open is a
//	place a retired literal ships green: a Helm helper template, a
//	packaging manifest, an ECMAScript module, or a published HTML page all
//	sat outside the earlier extension whitelist while living under the
//	named roots. A failure names the file the walk skipped.
func TestRetirementSweepReadsEveryAuthoredFileUnderItsRoots(t *testing.T) {
	root := repoRoot(t)
	swept := map[string]bool{}
	for _, path := range retirementSweepSurfaces(t, root) {
		swept[path] = true
	}
	for _, rel := range retirementSweepRoots {
		err := filepath.WalkDir(filepath.Join(root, rel), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if retirementSweepSkipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || isBinaryFile(t, path) {
				return nil
			}
			if !swept[path] {
				t.Errorf("%s is an authored file under %s that no retirement sweep reads", mustRel(t, root, path), rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", rel, err)
		}
	}
}
