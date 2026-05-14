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
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Compile loads a schema from a path relative to the repository root.
func Compile(t *testing.T, rel string) *jsonschema.Schema {
	t.Helper()
	c := NewCompiler(t)
	return MustCompile(t, c, rel)
}

// NewCompiler returns a JSON-Schema-2020 compiler.
func NewCompiler(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	return c
}

// MustCompile compiles a schema or fails the test.
func MustCompile(t *testing.T, c *jsonschema.Compiler, rel string) *jsonschema.Schema {
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
func MustAddLocalSchema(t *testing.T, c *jsonschema.Compiler, url, rel string) {
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
func ReadJSON(t *testing.T, path string) any {
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
