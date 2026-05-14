// SPDX-License-Identifier: MIT

// Package fuzz manages the §19.2 fuzz-crash corpus.
//
// Every fuzz target stores its discovered crashes under
// `tests/testinfra/fuzz/crashes/<package>/<target>/` so subsequent
// runs replay them as regression seeds. Replay surfaces the panic
// once at the original PR's authoring time and again on every
// PR until the underlying parser tolerates the input.
//
// Go's testing.F handles crash storage internally under
// `testdata/fuzz/<TargetName>/`. This package mirrors those seeds
// to a stable, repo-rooted location so:
//
//   1. Crashes survive across go-test caches and CI runners.
//   2. The on-disk catalog is browsable: every entry names the
//      package, target, and a content-addressed file.
//   3. Tier 0 lints can count crashes and gate on no-new-crashes.
//
// The package is intentionally small: a single Mirror helper that
// fuzz targets call to copy their `testdata/fuzz` corpus into the
// shared store. The harness's `lenny-test infra prune` removes
// entries marked stale.
package fuzz

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CrashRoot returns the repo-rooted directory where every fuzz
// target's crashes are mirrored.
func CrashRoot(t testing.TB) string {
	t.Helper()
	root := findRepoRoot(t)
	return filepath.Join(root, "tests", "testinfra", "fuzz", "crashes")
}

// Mirror copies the crashes the fuzz engine wrote under
// `testdata/fuzz/<target>/` into the shared CrashRoot. Idempotent;
// existing destination files are overwritten only when their
// contents differ.
//
// Call from a regular test (not the fuzz target itself) so the copy
// happens after `go test -fuzz` has finished writing.
func Mirror(t testing.TB, pkg, target string) {
	t.Helper()
	src := filepath.Join("testdata", "fuzz", target)
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return // no crashes recorded — nothing to mirror
		}
		t.Fatalf("fuzz.Mirror: read %s: %v", src, err)
	}
	dst := filepath.Join(CrashRoot(t), strings.ReplaceAll(pkg, "/", "_"), target)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("fuzz.Mirror: mkdir %s: %v", dst, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := copyIfDiffers(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			t.Fatalf("fuzz.Mirror: %s -> %s: %v", e.Name(), dst, err)
		}
	}
}

// Count returns the number of crash artifacts under CrashRoot. The
// number is the on-disk corpus size; useful for the Tier 0 gate
// that fails when new crashes appear without a corresponding fix
// commit.
func Count(t testing.TB) int {
	t.Helper()
	count := 0
	_ = filepath.WalkDir(CrashRoot(t), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		count++
		return nil
	})
	return count
}

func copyIfDiffers(src, dst string) error {
	srcData, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if dstData, err := os.ReadFile(dst); err == nil && bytesEqual(srcData, dstData) {
		return nil
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.Write(srcData); err != nil {
		return err
	}
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func findRepoRoot(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("findRepoRoot: walked past filesystem root from %s", wd)
	return ""
}

// Compile-time check: io.Discard is referenced so the import is
// retained for the future Replay helper that streams crash content
// to a parser without buffering.
var _ = io.Discard
