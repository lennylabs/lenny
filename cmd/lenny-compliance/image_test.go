// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// spec: §12.10 (TESTING.md) — "lenny-compliance --image <ref> --level
// <level>" drives a container-deployed runtime through the battery.
// diagnosis: a file:// reference must resolve straight to the named
// binary with no docker involvement (see
// tests/testinfra/runtimes/conformance-fixtures/README.md, which
// documents driving an intentionally-malformed fixture this way); a
// failure here means the conformance-fixture harness invocation the
// README documents no longer works.
func TestResolveImageBinaryFileScheme(t *testing.T) {
	path, cleanup, err := resolveImageBinary(context.Background(), "file://"+echoBinary)
	if err != nil {
		t.Fatalf("resolveImageBinary(file://): %v", err)
	}
	defer cleanup()
	if path != echoBinary {
		t.Errorf("resolved path = %q, want %q", path, echoBinary)
	}
}

// diagnosis: an unrecognized --image with no docker on PATH must fail
// with an actionable error rather than a panic or a silent no-op; this
// pins the error path without requiring docker in the test environment.
func TestResolveImageBinaryDockerUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, _, err := resolveImageBinary(context.Background(), "ghcr.io/example/does-not-matter:1.0"); err == nil {
		t.Fatal("resolveImageBinary with no docker on PATH: want error, got nil")
	}
}

// diagnosis: the generated wrapper script's docker invocation must
// single-quote the image reference so a reference is never split into
// extra docker run arguments or, worse, executed as shell.
func TestShellSingleQuoteEscapesEmbeddedQuotes(t *testing.T) {
	got := shellSingleQuote("ghcr.io/example/it's-a-tag:1.0")
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Fatalf("shellSingleQuote(%q) = %q, want a single-quoted string", "ghcr.io/example/it's-a-tag:1.0", got)
	}
	script := "#!/bin/sh\nexec docker run --rm -i " + got + "\n"
	f, err := os.CreateTemp(t.TempDir(), "quote-check-*.sh")
	if err != nil {
		t.Fatalf("create temp script: %v", err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatalf("write temp script: %v", err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0o755); err != nil {
		t.Fatalf("chmod temp script: %v", err)
	}
	// `sh -n` parses the script without executing it. It demonstrates
	// the quoting is well-formed without requiring docker or network
	// access in the test environment.
	out, err := exec.Command("sh", "-n", f.Name()).CombinedOutput()
	if err != nil {
		t.Errorf("generated wrapper script fails shell syntax check: %v\n%s\nscript:\n%s", err, out, script)
	}
}
