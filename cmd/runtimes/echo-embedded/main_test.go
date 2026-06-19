// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// spec: §9.1/§4.7 — the §4.7 reconciler renders the platform MCP flags
// (--mcp-socket, --gateway-grpc-addr) onto the embedded-model runtime
// container, the same flags the sidecar adapter accepts. The embedded
// main must define them; the default flag.ExitOnError parser exits 2 on
// an unknown flag, which would crash-loop every §4.7 embedded pool. This
// test pins that the binary parses both injected flags without the
// flag-parse exit. F-9.1.1.
func TestEmbeddedMainAcceptsPlatformMCPFlags_spec_9_1(t *testing.T) {
	bin := buildEchoEmbedded(t)

	// Run with the reconciler-injected flags plus a bind address that
	// fails net.Listen immediately, so the process exits past flag parsing
	// without serving. Before the fix the unknown --mcp-socket flag made
	// flag.Parse exit 2 before any of this ran.
	cmd := exec.Command(
		bin,
		"--addr=:-1", // invalid port: net.Listen fails fast, log.Fatalf exits 1.
		"--mcp-socket=@lenny-platform-mcp",
		"--gateway-grpc-addr=lenny-gateway.lenny-system.svc:50051",
		"--credentials-dir="+t.TempDir(),
		"--workspace-root="+filepath.Join(t.TempDir(), "current"),
		"--staging-dir="+filepath.Join(t.TempDir(), "staging"),
		"--shared-assets-dir="+filepath.Join(t.TempDir(), "shared"),
	)
	out, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected a non-zero exit from the failed listen, got err=%v output=%q", err, out)
	}
	// flag.ExitOnError uses exit code 2 for an undefined flag. Any other
	// non-zero exit means the flags parsed and the process proceeded to
	// the (deliberately failing) listen.
	if exitErr.ExitCode() == 2 {
		t.Errorf("exit code 2 indicates a flag-parse failure; the platform MCP flags are not defined. output=%q", out)
	}
	if strings.Contains(string(out), "flag provided but not defined") {
		t.Errorf("the embedded main rejected a reconciler-injected flag: %q", out)
	}
}

// buildEchoEmbedded compiles the echo-embedded binary into a temp dir and
// returns its path. Building the real binary exercises the actual flag
// registration in main rather than a re-declared copy.
func buildEchoEmbedded(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "echo-embedded")
	if _, err := os.Stat("main.go"); err != nil {
		t.Fatalf("expected to run from the package directory: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build echo-embedded: %v\n%s", err, out)
	}
	return bin
}
