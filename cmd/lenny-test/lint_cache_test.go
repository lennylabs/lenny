// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// The golangci-lint cache fallback is contributor tooling (the merge
// gate), so these cases carry no specification annotation.

// lintStaticResult runs the check through the static tier's classifier
// and reducer and returns the tier status, the tier detail, and the
// check's output.
func lintStaticResult(l golangciLint) (string, string, string) {
	o := runStaticCheck(staticCheck{name: "golangci-lint run", run: l.run, classify: classifyGolangciLint})
	status, detail := reduceStaticOutcomes([]staticOutcome{o})
	return status, detail, o.out
}

// lintCacheEntries returns the entries of dir whose names open
// with the private cache's prefix.
func lintCacheEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "lenny-lint-cache-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestLintUsesAPrivateCacheWhenTheFillDidNotFinish checks the fallback of
// a gate whose cache fill did not finish: both golangci-lint runs get the
// same new, empty, writable directory under the temporary directory as
// their cache, a clean tree passes, the directory is removed after the
// runs, and the output, the static tier's detail, and the gate's summary
// notes report the fallback.
func TestLintUsesAPrivateCacheWhenTheFillDidNotFinish(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	f := &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused"), wholeErr: exitError(t, 1)}
	l := stubLint(t, f, testLintBase, nil)
	l.privateCache = true
	calls := 0
	l.exec = func(ctx context.Context, bin, root, cache string, args ...string) (string, error) {
		calls++
		if cache == "" || !pathWithin(cache, tmp) {
			t.Errorf("run %d: cache %q; want a private directory under the temporary directory %s", calls, cache, tmp)
			return f.exec(ctx, bin, root, cache, args...)
		}
		entries, err := os.ReadDir(cache)
		if err != nil {
			t.Errorf("run %d: the private cache %s cannot be read: %v", calls, cache, err)
		}
		if calls == 1 && len(entries) != 0 {
			t.Errorf("the private cache was not empty before the first run: %v", entries)
		}
		if err := os.WriteFile(filepath.Join(cache, "entry-"+strconv.Itoa(calls)), []byte("x"), 0o600); err != nil {
			t.Errorf("run %d: the private cache is not writable: %v", calls, err)
		}
		return f.exec(ctx, bin, root, cache, args...)
	}
	status, detail, out := lintStaticResult(l)
	if status != verdictstatus.Pass {
		t.Fatalf("status %q; want pass on a clean tree (output %q)", status, out)
	}
	if len(f.caches) != 2 || f.caches[0] == "" || f.caches[0] != f.caches[1] {
		t.Errorf("caches %q; want the changed-line and the whole-tree runs in one private cache", f.caches)
	}
	if left := lintCacheEntries(t, tmp); len(left) != 0 {
		t.Errorf("the private cache was left behind: %v", left)
	}
	if !strings.HasPrefix(out, lintCacheFallbackMarker) {
		t.Errorf("the output does not open with the fallback line:\n%s", out)
	}
	if !strings.Contains(detail, lintCacheFallbackNote) {
		t.Errorf("the static tier's detail does not report the fallback: %q", detail)
	}
	if notes := gateSummaryNotes(detail); len(notes) != 1 || !strings.Contains(notes[0], lintCacheFallbackNote) {
		t.Errorf("the gate summary notes %q; want the fallback", notes)
	}
}

// TestLintPrivateCacheKeepsFindingsAndUnverifiedRuns checks that the
// fallback changes only the cache: a finding on a changed line still
// fails the tier with the fallback in its detail, a run with an
// unverifiable linter still opens with the unverified marker and is
// unverified, and a check whose fill finished uses the checkout's cache
// and reports no fallback.
func TestLintPrivateCacheKeepsFindingsAndUnverifiedRuns(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	f := &fakeLint{
		changedOut: "pkg/a/a.go:3:2: Error return value is not checked (errcheck)",
		changedErr: exitError(t, 1),
		wholeJSON:  lintJSON(t, "pkg/b/b.go:9:unused"),
		wholeErr:   exitError(t, 1),
	}
	l := stubLint(t, f, testLintBase, nil)
	l.privateCache = true
	status, detail, out := lintStaticResult(l)
	if status != verdictstatus.Fail || !strings.Contains(out, "pkg/a/a.go:3:2") {
		t.Errorf("a finding with the private cache: status %q; want fail naming the finding (output %q)", status, out)
	}
	if notes := gateSummaryNotes(detail); len(notes) != 1 {
		t.Errorf("a failing tier's summary notes %q; want the fallback", notes)
	}

	f = &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused"), wholeErr: exitError(t, 1)}
	l = stubLint(t, f, testLintBase, nil)
	l.privateCache = true
	l.version = func(context.Context, string, string) error { return os.ErrNotExist }
	status, detail, out = lintStaticResult(l)
	first, second, _ := strings.Cut(out, "\n")
	if status != verdictstatus.Unverified || !strings.HasPrefix(first, verdictstatus.UnverifiedMarker) || !strings.HasPrefix(second, lintCacheFallbackMarker) {
		t.Errorf("an unverifiable linter with the private cache: status %q; want unverified, the unverified marker first and the fallback second (output %q)", status, out)
	}
	if !strings.Contains(detail, lintCacheFallbackNote) {
		t.Errorf("an unverified tier's detail does not report the fallback: %q", detail)
	}

	f = &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused"), wholeErr: exitError(t, 1)}
	l = stubLint(t, f, testLintBase, nil)
	status, detail, out = lintStaticResult(l)
	if status != verdictstatus.Pass || strings.Contains(out, lintCacheFallbackMarker) || strings.Contains(detail, lintCacheFallbackNote) {
		t.Errorf("a finished fill: status %q, detail %q; want a pass with no fallback", status, detail)
	}
	for _, c := range f.caches {
		if c != "" {
			t.Errorf("a finished fill ran golangci-lint with the cache %q; want the checkout's cache", c)
		}
	}
}

// TestLintFailsWhenThePrivateCacheCannotBeCreated checks that a fallback
// whose cache directory cannot be created fails the tier without running
// golangci-lint against the read-only shared cache.
func TestLintFailsWhenThePrivateCacheCannotBeCreated(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	f := &fakeLint{}
	l := stubLint(t, f, testLintBase, nil)
	l.privateCache = true
	status, _, out := lintStaticResult(l)
	if status != verdictstatus.Fail || !strings.Contains(out, "private cache could not be created") {
		t.Errorf("status %q; want fail naming the private cache (output %q)", status, out)
	}
	if len(f.calls) != 0 {
		t.Errorf("golangci-lint ran %d times; want none", len(f.calls))
	}
}

// TestLintReadsTheFillOutcomeFromTheGateEnvironment checks that the check
// uses the private cache only when the container script reports that the
// fill did not finish, and that the fill itself always writes the
// checkout's shared cache.
func TestLintReadsTheFillOutcomeFromTheGateEnvironment(t *testing.T) {
	for value, want := range map[string]bool{gateLintFillFailed: true, "": false, "ok": false} {
		t.Setenv(gateEnvLintFill, value)
		if got := newGolangciLint(t.TempDir(), "main").privateCache; got != want {
			t.Errorf("%s=%q: private cache %t; want %t", gateEnvLintFill, value, got, want)
		}
	}
	f := &fakeLint{}
	l := stubLint(t, f, testLintBase, nil)
	l.privateCache = true
	if err := l.fillCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.caches) != 1 || f.caches[0] != "" {
		t.Errorf("the fill ran with the caches %q; want the checkout's shared cache", f.caches)
	}
}

// TestLintEnvNamesThePrivateCache checks that a private cache replaces the
// checkout's cache directory and any inherited GOLANGCI_LINT_CACHE.
func TestLintEnvNamesThePrivateCache(t *testing.T) {
	env := golangciLintEnv([]string{"GOLANGCI_LINT_CACHE=/shared"}, "/work/a", "/home/alice/.cache", "/tmp/private")
	var caches []string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "GOLANGCI_LINT_CACHE="); ok {
			caches = append(caches, v)
		}
	}
	if len(caches) != 1 || caches[0] != "/tmp/private" {
		t.Errorf("caches %v; want the private cache alone", caches)
	}
}

// TestLintWithTheInstalledLinterPassesWithAPrivateCache runs the installed
// golangci-lint over a clean branch of a module whose shared cache
// directory is read-only and empty, as it is after a fill the kernel
// killed. With the private cache the check passes, writes nothing to the
// shared cache, and removes the private cache afterwards.
func TestLintWithTheInstalledLinterPassesWithAPrivateCache(t *testing.T) {
	if resolveGoBin("golangci-lint") == "" {
		t.Skip("blocked: golangci-lint is not installed (run scripts/setup-dev.sh)")
	}
	r := newLintRepo(t, "main")
	r.commit("go.mod", "module example.com/m\n\ngo 1.22\n", "module")
	r.commit(".golangci.yml", "version: \"2\"\n", "config")
	r.commit("old.go", lintModuleBase, "pre-existing finding")
	writeTestLintRegister(t, r.root, map[string]int{". errcheck": 1})
	r.git("add", golangciLintRegister)
	r.git("commit", "-q", "-m", "register")
	r.git("checkout", "-q", "-b", "feature")
	r.commit("clean.go", "package m\n\n// Clean returns one.\nfunc Clean() int { return 1 }\n", "clean change")
	shared := t.TempDir()
	if err := os.Chmod(shared, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(shared, 0o755) })
	tmp := t.TempDir()
	t.Setenv("GOLANGCI_LINT_CACHE", shared)
	t.Setenv("GOFLAGS", "-buildvcs=false")
	t.Setenv("TMPDIR", tmp)
	t.Setenv(gateEnvLintFill, gateLintFillFailed)
	l := newGolangciLint(r.root, "main")
	l.version = func(context.Context, string, string) error { return nil }
	l.deps = stubLintDeps
	status, detail, out := lintStaticResult(l)
	if status != verdictstatus.Pass {
		t.Fatalf("status %q; want pass on a clean branch with the private cache:\n%s", status, out)
	}
	if !strings.Contains(detail, lintCacheFallbackNote) {
		t.Errorf("the static tier's detail does not report the fallback: %q", detail)
	}
	if entries, err := os.ReadDir(shared); err != nil || len(entries) != 0 {
		t.Errorf("the shared cache holds %v (%v); want it untouched", entries, err)
	}
	if left := lintCacheEntries(t, tmp); len(left) != 0 {
		t.Errorf("the private cache was left behind: %v", left)
	}
}
