// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/testtmp"
)

// spec: TESTING.md §6 (The lenny-test harness), TESTING.md §17.10 (Flake budget)
//
// TestHarnessExportsTestTempRoot pins the environment lenny-test passes to
// every go test, go build, and fixture process it starts (TEST-GAPS
// T-4.7.23). With the default /tmp it exports TMPDIR, GOTMPDIR, and
// LENNY_TEST_TMPDIR at a root under the home directory, so a host cleanup
// of /tmp cannot remove a fixture binary, a socket directory, or a
// go-build work directory part-way through a run. An operator GOTMPDIR off
// /tmp is kept, a GOTMPDIR under /tmp is replaced, and a child process
// inherits the exported values.
func TestHarnessExportsTestTempRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(testtmp.EnvRoot, "")
	t.Setenv("TMPDIR", "/tmp")
	t.Setenv("GOTMPDIR", "/tmp/go-build-root")
	root, err := applyTestTempEnv()
	if err != nil {
		t.Fatalf("applyTestTempEnv: %v", err)
	}
	want := filepath.Join(home, testtmp.HomeDirName)
	if root != want {
		t.Fatalf("root = %q, want %q", root, want)
	}
	for _, k := range []string{"TMPDIR", "GOTMPDIR", testtmp.EnvRoot} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Fatalf("the root %q was not created: %v", want, err)
	}
	out, err := exec.Command("sh", "-c", `printf '%s|%s|%s' "$TMPDIR" "$GOTMPDIR" "$LENNY_TEST_TMPDIR"`).Output()
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	if got := string(out); got != strings.Join([]string{want, want, want}, "|") {
		t.Fatalf("a child process sees %q, want the root in all three variables", got)
	}

	operatorGo := filepath.Join(t.TempDir(), "gocache-tmp")
	t.Setenv("GOTMPDIR", operatorGo)
	t.Setenv("TMPDIR", "/tmp")
	if _, err := applyTestTempEnv(); err != nil {
		t.Fatalf("applyTestTempEnv: %v", err)
	}
	if got := os.Getenv("GOTMPDIR"); got != operatorGo {
		t.Errorf("GOTMPDIR = %q, want the operator value %q kept", got, operatorGo)
	}
}
