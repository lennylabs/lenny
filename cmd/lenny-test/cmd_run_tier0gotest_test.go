// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// writeMarkerPackage writes a module holding one passing test that
// writes the unverified marker to stdout, and returns its directory.
// The package stands in for a tier-0 test that ran, proved nothing, and
// said so.
func writeMarkerPackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module tier0marker\n\ngo 1.21\n",
		"marker_test.go": "package tier0marker\n\nimport (\n\t\"fmt\"\n\t\"testing\"\n)\n\n" +
			"func TestReachesNoConclusion(t *testing.T) {\n" +
			"\tfmt.Println(\"" + verdictstatus.UnverifiedMarker + " protoc not on PATH\")\n}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// runGoTest runs `go` with the given argv in dir and returns the
// combined output. A build or toolchain failure fails the test; a
// non-zero exit from the test binary itself does not, because the
// caller asserts on the output.
func runGoTest(t *testing.T, dir string, args []string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOPROXY=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("go %v exited with %v", args, err)
	}
	return string(out)
}

// TestTier0GoTestArgsSurfaceTheMarkerFromAPassingPackage runs the argv
// the tier-0 Go-test check uses against a package that passes and
// writes the unverified marker, and holds the composer to reporting
// unverified. The invocation is what makes the status reachable: in
// package-list mode cmd/go drops a passing package's output, so a
// non-verbose run hands the classifier nothing and a check that proved
// nothing reads as a check that passed.
func TestTier0GoTestArgsSurfaceTheMarkerFromAPassingPackage(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	dir := writeMarkerPackage(t)

	out := runGoTest(t, dir, goTestArgs("./..."))
	status, detail := classifyUnverified(out)
	if status != verdictstatus.Unverified {
		t.Fatalf("go %v produced output the classifier read as %q; want %q\noutput:\n%s",
			goTestArgs("./..."), status, verdictstatus.Unverified, out)
	}
	if detail == "" {
		t.Fatalf("classifier dropped the reason from:\n%s", out)
	}

	// The negative half records why the flag is load-bearing: the same
	// package run without it emits no marker at all.
	terse := runGoTest(t, dir, []string{"test", "-count=1", "./..."})
	if _, ok := verdictstatus.ScanUnverified(terse); ok {
		t.Fatalf("package-list output carried the marker unexpectedly; the check's invocation no longer needs to ask for it:\n%s", terse)
	}
}
