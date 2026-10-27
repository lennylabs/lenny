// SPDX-License-Identifier: MIT

package testtmp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/testtmp"
)

// spec: TESTING.md §17.10 (Flake budget), TESTING.md §6 (The lenny-test harness)
//
// TestRootKeepsFixturesOutOfTmp pins where test fixtures live. A fixture
// binary or socket directory under /tmp disappears when the host cleans
// /tmp part-way through a run, and every later test in the package then
// fails with fork/exec or a socket error (TEST-GAPS T-4.7.23). Root
// honours the harness root, keeps an operator TMPDIR that is off /tmp, and
// otherwise falls back to a directory under the home directory.
func TestRootKeepsFixturesOutOfTmp(t *testing.T) {
	home := t.TempDir()
	harness := filepath.Join(t.TempDir(), "harness")
	operator := filepath.Join(t.TempDir(), "operator")
	cases := []struct {
		name, harness, tmpdir, want string
	}{
		{"harness root wins", harness, "/tmp", harness},
		{"default /tmp falls back under home", "", "/tmp", filepath.Join(home, testtmp.HomeDirName)},
		{"a directory under /tmp falls back under home", "", "/tmp/lenny", filepath.Join(home, testtmp.HomeDirName)},
		{"unset TMPDIR falls back under home", "", "", filepath.Join(home, testtmp.HomeDirName)},
		{"an operator TMPDIR off /tmp is kept", "", operator, operator},
		{"a /tmpx sibling is not under /tmp", "", "/tmpx-not-tmp", "/tmpx-not-tmp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv(testtmp.EnvRoot, tc.harness)
			t.Setenv("TMPDIR", tc.tmpdir)
			if got := testtmp.Resolve(); got != tc.want {
				t.Fatalf("Resolve() = %q, want %q", got, tc.want)
			}
		})
	}
}

// spec: TESTING.md §17.10 (Flake budget)
//
// TestMkdirTempCreatesUnderRoot pins that MkdirTemp creates the root on
// first use and the fixture directory inside it, never under /tmp, and
// that ShortDir does the same under the short home root a Unix socket
// path needs.
func TestMkdirTempCreatesUnderRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(testtmp.EnvRoot, "")
	t.Setenv("TMPDIR", "/tmp")
	dir, err := testtmp.MkdirTemp("fixture-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if want := filepath.Join(home, testtmp.HomeDirName) + "/"; !strings.HasPrefix(dir, want) {
		t.Fatalf("MkdirTemp created %q, want a directory under %q", dir, want)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("MkdirTemp did not create %q: %v", dir, err)
	}

	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), strings.Repeat("long-", 20)))
	sock, err := testtmp.ShortDir(t, "sock-")
	if err != nil {
		t.Fatalf("ShortDir: %v", err)
	}
	if !strings.HasPrefix(sock, filepath.Join(home, testtmp.HomeDirName)+"/") {
		t.Fatalf("ShortDir created %q, want a directory under the home root whatever TMPDIR says", sock)
	}
}

// spec: TESTING.md §17.10 (Flake budget)
//
// TestEnvNamesBothGoTempVariables pins the environment a fixture build
// passes to `go build`: TMPDIR for the program and GOTMPDIR for the
// toolchain's work directory, both at the root.
func TestEnvNamesBothGoTempVariables(t *testing.T) {
	got := testtmp.Env("/home/alice/.lenny-tmp")
	want := []string{"TMPDIR=/home/alice/.lenny-tmp", "GOTMPDIR=/home/alice/.lenny-tmp"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Env() = %v, want %v", got, want)
	}
}
