// SPDX-License-Identifier: MIT

package tier0_static

import (
	"io/fs"
	"os"
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
// lists". `lenny-test validate-maps` covers the files under
// tests/tier2_component and above plus the in-package test files of the
// packages a section claims, and the second population carries a waiver
// file. A playground test that sits in-package next to the code it
// exercises (the §27.6 effective-cap arithmetic in
// pkg/gateway/sessionserver, the §10.2 mint scope narrowing in
// pkg/gateway/mcpfabric/playground, the §27.2 preflight gate in
// pkg/preflight) therefore has two ways to encode a §27 guarantee and
// never appear in the map: it can live in a package no section claims,
// or its path can be waived. A reader of the §27.5 or §27.6 entry then
// concludes the behavior is untested. This sweep is the unconditional
// counterpart of the validator: every playground test file on disk,
// wherever it lives, is referenced from some section's `tests` list.
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

// inPackageWaivers returns the repo-relative paths listed in
// tests/spec-map-inpackage-pending.txt, the file through which
// `lenny-test validate-maps` tolerates an in-package test that no
// spec-map section references.
func inPackageWaivers(t *testing.T, root string) []string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(root, "tests", "spec-map-inpackage-pending.txt"))
	if err != nil {
		t.Fatalf("read tests/spec-map-inpackage-pending.txt: %v", err)
	}
	out := []string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// spec: §27.5 (web playground — protocol), §27.6 (web playground —
//
//	session lifecycle and cleanup)
//
// TESTING.md §5 requires the spec map to trace every section to "the
// tests, packages, migrations, and chart templates that encode it", and
// `lenny-test validate-maps` now sweeps the in-package test files of
// every package a section claims, not the tier directories alone. The
// sweep carries a waiver file for the backlog it inherited. A waiver is
// an escape hatch, and the §27 in-package suites (the cap arithmetic in
// pkg/gateway/sessionserver, the mint and session-record suites in
// pkg/gateway/mcpfabric/playground, the gating checks in pkg/preflight)
// are all mapped today. Waiving one of them would reopen exactly the
// drift the sweep exists to close, while leaving the gate green.
//
// diagnosis: A playground test file was added to
//
//	tests/spec-map-inpackage-pending.txt instead of to the `tests` list
//	of the section its `// spec:` annotation names. Remove the waiver and
//	reference the file from tests/spec-map.json.
func TestInPackageWaiversNamePlaygroundNoTest(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	waived := []string{}
	for _, entry := range inPackageWaivers(t, root) {
		if strings.Contains(strings.ToLower(entry), "playground") {
			waived = append(waived, entry)
		}
	}
	if len(waived) > 0 {
		t.Errorf("%d playground test file(s) are waived in tests/spec-map-inpackage-pending.txt: %v; §27 coverage must be referenced from tests/spec-map.json rather than waived", len(waived), waived)
	}
}

// spec: §27.5 (web playground — protocol), §27.6 (web playground —
//
//	session lifecycle and cleanup)
//
// A waiver that names a file which no longer exists, or one that the map
// has since gained a reference for, silently widens the escape hatch:
// the entry stays behind and a later file with the same path inherits
// the waiver. TESTING.md §5 makes the map the record of what encodes a
// section, so the waiver list has to stay a list of live, still-unmapped
// files.
//
// diagnosis: An entry in tests/spec-map-inpackage-pending.txt is stale.
//
//	Either the file was renamed or deleted (drop the line), or
//	tests/spec-map.json now references it (drop the line, the map covers
//	it), or the entry is not a test-file path at all.
func TestInPackageWaiversAreLiveAndStillUnmapped(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)

	referenced := map[string]bool{}
	for _, paths := range readSpecMapTests(t) {
		for _, path := range paths {
			referenced[strings.TrimSuffix(strings.TrimSuffix(path, "/..."), "/")] = true
		}
	}

	seen := map[string]bool{}
	for _, entry := range inPackageWaivers(t, root) {
		if seen[entry] {
			t.Errorf("tests/spec-map-inpackage-pending.txt lists %s twice", entry)
			continue
		}
		seen[entry] = true
		if !strings.HasSuffix(entry, "_test.go") {
			t.Errorf("tests/spec-map-inpackage-pending.txt entry %s is not a _test.go path; the file waives in-package test files only", entry)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, entry)); err != nil {
			t.Errorf("tests/spec-map-inpackage-pending.txt waives %s, which is not on disk: %v", entry, err)
			continue
		}
		if referenced[entry] || referenced[filepath.Dir(entry)] {
			t.Errorf("tests/spec-map-inpackage-pending.txt waives %s, which tests/spec-map.json already references; drop the waiver", entry)
		}
	}
}
