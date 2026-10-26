// SPDX-License-Identifier: MIT

package main

import (
	"os"

	"github.com/lennylabs/lenny/tests/testinfra/testtmp"
)

// applyTestTempEnv resolves the fixture root (testtmp.Root) and exports
// it as TMPDIR and LENNY_TEST_TMPDIR, and as GOTMPDIR unless the operator
// already points GOTMPDIR off /tmp. Every process lenny-test starts
// inherits these, so go test temp directories, TestMain fixture binaries,
// socket directories, and go-build work directories stay out of /tmp,
// which a host cleanup can empty part-way through a run (TEST-GAPS
// T-4.7.23). The root is never removed here: go test and the fixtures
// clean up what they create, and a concurrent run shares the root.
//
// spec: TESTING.md §6 (The lenny-test harness), §17.10 (Flake budget).
func applyTestTempEnv() (string, error) {
	root, err := testtmp.Root()
	if err != nil {
		return "", err
	}
	for _, kv := range [][2]string{{"TMPDIR", root}, {testtmp.EnvRoot, root}} {
		if err := os.Setenv(kv[0], kv[1]); err != nil {
			return "", err
		}
	}
	if g := os.Getenv("GOTMPDIR"); g == "" || testtmp.UnderTmp(g) {
		if err := os.Setenv("GOTMPDIR", root); err != nil {
			return "", err
		}
	}
	return root, nil
}
