// SPDX-License-Identifier: MIT

package tier0_static

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// hostScrubRun matches a scrub run given the real host operations in a test.
var hostScrubRun = regexp.MustCompile(`\bRun\([^\n]*\b(?:DefaultOps\{|newScrubOps\()`)

// TestHostScrubRunMatcher pins the matcher on the call it must refuse and on
// the calls it must accept.
//
// spec: TESTING.md §17.10 (Flake budget)
// diagnosis: the matcher behind TestNoTestRunsTheScrubOnTheHost_spec_17_10 no
// longer recognizes a scrub given the real host operations, or it refuses a
// scrub given a wrapper.
func TestHostScrubRunMatcher(t *testing.T) {
	for src, want := range map[string]bool{
		"rep, err := Run(ctx, killSafeOps{DefaultOps{}}, Config{": true,
		"rep, err := scrub.Run(ctx, scrub.DefaultOps{}, cfg)":     true,
		"rep, err := scrub.Run(ctx, newScrubOps(), cfg)":          true,
		"rep, err := Run(ctx, ops, Config{":                       false,
		"ops := DefaultOps{}":                                     false,
	} {
		if got := hostScrubRun.MatchString(src); got != want {
			t.Errorf("match %q = %v, want %v", src, got, want)
		}
	}
}

// TestNoTestRunsTheScrubOnTheHost_spec_17_10 refuses a test that runs the §5.2
// scrub with the real DefaultOps. Step 4 empties /tmp and /dev/shm, which on a
// test host belong to every other process. One such test removed the sockets
// and fixtures of the packages running alongside it (TEST-GAPS T-4.7.24).
//
// spec: TESTING.md §17.10 (Flake budget)
// diagnosis: a test passes the real scrub operations to scrub.Run. Pass a
// wrapper that intercepts KillUserProcesses, PurgeIPCShm, and ClearContents,
// as hostSafeOps in pkg/adapter/scrub does.
func TestNoTestRunsTheScrubOnTheHost_spec_17_10(t *testing.T) {
	root := schematest.RepoRoot(t)
	var offenders []string
	for _, top := range []string{"pkg", "cmd", "tests", "sdks"} {
		dir := filepath.Join(root, top)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "scrub_host_ops_test.go" {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if hostScrubRun.Match(b) {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("tests running the scrub with the real host operations: %q", offenders)
	}
}
