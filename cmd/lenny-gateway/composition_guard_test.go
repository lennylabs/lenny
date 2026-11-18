// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file are the guards that decision 7 of change 0117
// (gateway package layout) puts in place before any of its move steps runs.
// Each move step relocates the wiring of the part it moves out of this
// binary: its constructor, the flags only it reads, and its route
// registration. The guards hold what the binary exposes and the order it
// builds, starts, and stops its parts in, so a move that changes either
// fails here and the change is reviewed as a change to the golden file
// rather than slipping through as a side effect of moving code.
//
// They pin the binary's existing surface rather than a requirement, so they
// carry no specification annotation.
//
// To accept a deliberate change, run the tests with LENNY_GUARD_UPDATE=1,
// which rewrites the golden files under testdata/, and review the diff.

// guardUpdateEnv names the environment variable that rewrites the golden
// files instead of comparing against them.
const guardUpdateEnv = "LENNY_GUARD_UPDATE"

// TestGatewayFlagSurfaceIsFixed builds the gateway binary and compares its
// --help output, run in an empty environment so that no default read from
// an environment variable varies with the host, against the golden file. The
// output names every flag, its type, its default, and its help text,
// including the flags that imported packages register on the process-wide
// flag set, so a flag a move drops, renames, re-defaults, or re-documents is
// a difference.
func TestGatewayFlagSurfaceIsFixed(t *testing.T) {
	repo := repoRootDir(t)
	bin := filepath.Join(t.TempDir(), "lenny-gateway")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lenny-gateway")
	build.Dir = repo
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the gateway binary: %v\n%s", err, out)
	}

	help := exec.Command(bin, "-help")
	help.Env = []string{}
	out, err := help.CombinedOutput()
	if err != nil {
		t.Fatalf("run the gateway binary with -help: %v\n%s", err, out)
	}
	got := normalizeHelp(string(out), bin)
	compareGolden(t, filepath.Join("testdata", "flag-surface.golden"), got)
}

// normalizeHelp replaces the binary's temporary path in the usage line with
// its name, so the output does not depend on where the test built it.
func normalizeHelp(help, bin string) string {
	return strings.ReplaceAll(help, bin, "lenny-gateway")
}

// compareGolden compares got against the golden file, or rewrites the file
// when guardUpdateEnv is set.
func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if os.Getenv(guardUpdateEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file %s: %v (run with %s=1 to create it)", path, err, guardUpdateEnv)
	}
	if msg := goldenDiff(string(want), got); msg != "" {
		t.Errorf("%s no longer matches: %s\nWhen the change is deliberate, rerun with %s=1 and review the diff of %s.", path, msg, guardUpdateEnv, path)
	}
}

// goldenDiff returns a description of the first line at which got differs
// from want, or "" when they are equal.
func goldenDiff(want, got string) string {
	if want == got {
		return ""
	}
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl || i >= len(w) || i >= len(g) {
			return fmt.Sprintf("first difference at line %d: golden %q, now %q", i+1, wl, gl)
		}
	}
	return "the texts differ"
}

// repoRootDir returns the repository root from this package's directory.
func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("repository root %s has no go.mod: %v", dir, err)
	}
	return dir
}
