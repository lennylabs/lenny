// SPDX-License-Identifier: MIT

// Package schematest is a Tier 0 test helper that wraps
// github.com/santhosh-tekuri/jsonschema/v5 with convenience
// constructors and assertion helpers keyed off *testing.T.
//
// The package lives under tests/testinfra so the jsonschema import is
// anchored in a regular Go package (not a *_test.go-only package); some
// golangci-lint releases fail to typecheck imports that appear only in
// test files. Hosting helpers here keeps the lint clean.
//
// See TESTING.md §12.0 and tests/tier0_static.
package schematest

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Compile loads a schema from a path relative to the repository root.
func Compile(t testing.TB, rel string) *jsonschema.Schema {
	t.Helper()
	c := NewCompiler(t)
	return MustCompile(t, c, rel)
}

// NewCompiler returns a JSON-Schema-2020 compiler.
func NewCompiler(t testing.TB) *jsonschema.Compiler {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	return c
}

// MustCompile compiles a schema or fails the test.
func MustCompile(t testing.TB, c *jsonschema.Compiler, rel string) *jsonschema.Schema {
	t.Helper()
	full := filepath.Join(RepoRoot(t), rel)
	s, err := c.Compile(full)
	if err != nil {
		t.Fatalf("compile %s: %v", rel, err)
	}
	return s
}

// MustAddLocalSchema teaches the compiler that a remote $id URL is
// satisfied by a local file. Used to keep Tier 0 tests offline.
func MustAddLocalSchema(t testing.TB, c *jsonschema.Compiler, url, rel string) {
	t.Helper()
	full := filepath.Join(RepoRoot(t), rel)
	f, err := os.Open(full)
	if err != nil {
		t.Fatalf("open %s: %v", rel, err)
	}
	defer f.Close()
	if err := c.AddResource(url, f); err != nil {
		t.Fatalf("AddResource(%s): %v", url, err)
	}
}

// ReadJSON loads and unmarshals a JSON file relative to absolute path.
func ReadJSON(t testing.TB, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return v
}

// RepoRoot walks upward from the test's working directory to find the
// directory containing go.mod.
func RepoRoot(t testing.TB) string {
	t.Helper()
	root, err := RepoRootCwd()
	if err != nil {
		t.Fatalf("%v", err)
	}
	return root
}

// RepoRootCwd is the test-independent version of RepoRoot — usable
// from non-test contexts (e.g., harness subcommands). Returns an
// error rather than calling t.Fatalf.
func RepoRootCwd() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
	}
	return "", fmt.Errorf("could not find repo root containing go.mod from %s", wd)
}

// TrackedFiles returns every git-tracked file path, relative to the
// repository root and using forward slashes. License and other
// repository-hygiene checks enumerate tracked files rather than walking
// the filesystem so that generated, gitignored artifacts (the Kind
// bootstrap overlay, build output under dist/ and node_modules/, cloud
// values overrides) are never flagged. The check's contract is the set
// of files committed to the repository, which is exactly `git ls-files`.
//
// When git is unavailable or the working directory is not a git checkout
// (a source tarball, for example), the tracked set cannot be determined,
// so the calling test skips rather than reporting spurious results.
func TrackedFiles(t testing.TB) []string {
	t.Helper()
	root := RepoRoot(t)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files unavailable (not a git checkout?): %v", err)
	}
	var files []string
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel != "" {
			files = append(files, rel)
		}
	}
	return files
}
