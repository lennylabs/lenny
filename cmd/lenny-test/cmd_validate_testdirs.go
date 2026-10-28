// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// testDirsRoot is the tree whose tier directories the keyed-test-directory
// check walks.
const testDirsRoot = "tests"

// validateTestDirsKeyed fails a tracked test directory under tests/tier*/
// that no glob key of tests/change-graph.json covers.
//
// validateChangeGraphCompleteness gates source files and leaves _test.go
// files out of its domain, because a test file is a target of the graph.
// A test directory is a target, and a source of change as well: a diff
// confined to tests/tier2_component/<dir>/ resolves its tiers through the
// change graph like any other path, and with no key covering the
// directory `lenny-test --since <ref>` selects only the static tier,
// runs none of the directory's tests, and reports a pass (TESTING.md §6
// **Selection algorithm**). T-17.4.35 records two directories that did
// this before their keys were added by hand.
//
// A test directory is a directory under tests/tier*/ that holds a
// git-tracked _test.go file outside any testdata/ tree. It is covered
// when a glob key names it or one of its ancestor directories, read
// through changegraph.CoveredByGlob, the reading the selector and the
// completeness check share. There is no baseline: every test directory
// was keyed when the check landed, so an unkeyed one is a defect in the
// change that added it.
//
// The check fails rather than passing when the change graph cannot be
// read or when git lists no test directory at all, because a gate that
// inspects nothing must not certify the tree.
func validateTestDirsKeyed(changeGraphPath, root string) checkResult {
	const name = "change-graph test directories keyed"

	globs, err := readChangeGraphGlobKeys(changeGraphPath)
	if err != nil {
		return newResult(name, false, err.Error())
	}
	tracked, err := gitTrackedPaths(root, []string{testDirsRoot})
	if err != nil {
		return newResult(name, false, err.Error())
	}
	dirs := tierTestDirs(tracked)
	if len(dirs) == 0 {
		return newResult(name, false,
			"git lists no tracked _test.go file under tests/tier*/; a gate that inspects nothing cannot certify the tree")
	}
	unkeyed := []string{}
	for _, d := range dirs {
		if !coveredByChangeGraphGlob(d, globs) {
			unkeyed = append(unkeyed, d+"/")
		}
	}
	if len(unkeyed) > 0 {
		return newResult(name, false, fmt.Sprintf(
			"%d test director(ies) covered by no tests/change-graph.json glob key, so `--since` never selects them on their own changes: %s",
			len(unkeyed), strings.Join(unkeyed, "; "),
		))
	}
	return newResult(name, true, fmt.Sprintf("%d test directories keyed", len(dirs)))
}

// tierTestDirs returns the sorted, de-duplicated directories under
// tests/tier*/ that hold a _test.go path from the given list, excluding
// anything under a testdata/ directory, whose files are fixtures rather
// than tests the harness runs.
func tierTestDirs(paths []string) []string {
	seen := map[string]bool{}
	for _, p := range paths {
		if !strings.HasPrefix(p, testDirsRoot+"/tier") || !strings.HasSuffix(p, "_test.go") {
			continue
		}
		if strings.Contains("/"+p, "/testdata/") {
			continue
		}
		seen[path.Dir(p)] = true
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
