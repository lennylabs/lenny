// SPDX-License-Identifier: MIT

package tier0_static

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// skippedTreeDirs are directories the playground test-file sweep does not
// descend into: version-control metadata, build output, and vendored or
// generated dependency trees never hold a Go test the spec map should
// reference.
var skippedTreeDirs = map[string]bool{
	".git":          true,
	"node_modules":  true,
	"vendor":        true,
	"dist":          true,
	"build":         true,
	"tests/results": true,
}

// playgroundTestFiles walks the repository and returns every `_test.go`
// file whose repo-relative path names the playground, in sorted order.
// The match is on the whole path, so it picks up both the files inside
// pkg/gateway/mcpfabric/playground and the playground-named files that
// live with the packages they exercise (pkg/preflight, pkg/gateway/
// sessionserver, cmd/lenny-gateway, and the tier directories).
func playgroundTestFiles(t *testing.T, root string) []string {
	t.Helper()

	found := []string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if skippedTreeDirs[d.Name()] || skippedTreeDirs[rel] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		if strings.Contains(strings.ToLower(rel), "playground") {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository for playground test files: %v", err)
	}
	sort.Strings(found)
	return found
}

// spec: §27.5 (web playground — protocol), §27.6 (web playground —
//
//	session lifecycle and cleanup), §10.2 (authentication — the minted
//	playground scope is intersection(subject_token.scope,
//	playground_allowed_scope))
//
// TESTING.md §5 states that "`tests/spec-map.json` maps every spec
// section to the tests, packages, migrations, and chart templates that
// encode it" and that validation confirms "Every test function with a
// `// spec:` annotation appears in the spec map under each section it
// lists". `lenny-test validate-maps` enforces that only for the files
// under tests/tier2_component and above, so a playground test that sits
// in-package next to the code it exercises (the §27.6 effective-cap
// arithmetic in pkg/gateway/sessionserver, the §10.2 mint scope
// narrowing in pkg/gateway/mcpfabric/playground, the §27.2 preflight
// gate in pkg/preflight) can encode a §27 guarantee and never appear in
// the map. A reader of the §27.5 or §27.6 entry then concludes the
// behavior is untested. This sweep is the file-level counterpart of the
// validator: every playground test file on disk, wherever it lives, is
// referenced from some section's `tests` list.
//
// diagnosis: A playground test file exists on disk but no section of
//
//	tests/spec-map.json references it, so §27 does not trace to its real
//	coverage. Add the file (with its `::TestName` suffix, one entry per
//	test function) to the `tests` list of the section its `// spec:`
//	annotation names. If the file was renamed, update the reference
//	rather than adding a second one.
func TestSpecMapReferencesEveryPlaygroundTestFile(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	files := playgroundTestFiles(t, root)
	if len(files) == 0 {
		t.Fatal("found no playground test files under the repository root; the sweep is broken, since §27 coverage lives in pkg/gateway/mcpfabric/playground at minimum")
	}

	referenced := map[string]bool{}
	for _, paths := range readSpecMapTests(t) {
		for _, path := range paths {
			referenced[strings.TrimSuffix(strings.TrimSuffix(path, "/..."), "/")] = true
		}
	}

	missing := []string{}
	for _, rel := range files {
		if !referenced[rel] {
			missing = append(missing, rel)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d playground test file(s) are absent from tests/spec-map.json: %v; each encodes a §27 (or §10.2 mint) guarantee and must appear in the tests list of the section it annotates", len(missing), missing)
	}
}

// spec: §27.6 (web playground — session lifecycle and cleanup), §10.2
//
//	(authentication)
//
// diagnosis: A section that claims a package in `packages` but names no
//
//	test from that package leaves the package's in-package coverage
//	untraceable. §27.6's effective-cap arithmetic is implemented in
//	pkg/gateway/sessionserver and §10.2's playground scope narrowing in
//	pkg/gateway/mcpfabric/playground; if either package is dropped from
//	the section's `packages` list, `lenny-test --spec` selection stops
//	reaching the code that implements the behavior.
func TestSpecMapSectionsClaimPlaygroundCapAndScopePackages(t *testing.T) {
	t.Parallel()

	packages := readSpecMapPackages(t)

	want := map[string]string{
		// The min() cap arithmetic §27.6 mandates lives in the session
		// server, not in the playground package.
		"27.6": "pkg/gateway/sessionserver",
		// The playground mint narrows the subject scope to the
		// §10.2 ceiling inside the playground package.
		"10.2": "pkg/gateway/mcpfabric/playground",
	}
	for section, pkg := range want {
		claimed := packages[section]
		if len(claimed) == 0 {
			t.Errorf("spec-map section %s claims no packages; it must claim %s", section, pkg)
			continue
		}
		found := false
		for _, got := range claimed {
			if got == pkg {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("spec-map section %s claims packages %v, which omit %s", section, claimed, pkg)
		}
	}
}
