// SPDX-License-Identifier: MIT

// Package embedded is the testinfra helper that drives the §17.4
// Embedded Mode CLI (cmd/lenny) end to end for the quick-start smoke
// test. It builds the lenny binary, then runs `lenny up`,
// `lenny session new`, and `lenny down` against a temporary LENNY_HOME
// so a test can assert the documented bring-up, session-create, and
// tear-down path through the real supervisor process rather than the
// in-process http.Handler.
//
// Embedded Mode brings up an embedded k3s cluster for session
// placement. The cluster substrate is cross-platform: on Linux k3s runs
// as a managed child process, and on macOS and Windows the same pinned
// k3s version runs as a container under Docker Desktop's Linux VM (§17.4
// Embedded Mode). The host therefore needs a writable container runtime,
// and on macOS and Windows it needs the docker CLI on PATH so Docker
// Desktop supplies the Linux kernel the binary cannot embed. On first
// run the bring-up also needs network access to download the k3s and
// PostgreSQL bundles (§17.4). `lenny up` auto-seeds the echo runtime with
// a runnable image digest, an applied Runtime CRD, and a single-pod warm
// pool, so `session new --runtime echo` places a session on a pod with no
// operator setup; the §26 reference runtimes ship placeholder-pinned
// (§17.4 / catalog.go) and need a registered pullable image, an applied
// Runtime CRD, and a warm pool before a session against one starts. The
// bring-up is therefore expensive and host-dependent; SkipUnlessAvailable
// gates the test behind an explicit LENNY_EMBEDDED_SMOKE opt-in and the
// cross-platform substrate prerequisite (Linux, or a non-Linux host with
// Docker on PATH) so the unit and tier-4 suites stay green on developer
// laptops and CI runners that lack the substrate. This mirrors the
// envtest and kind opt-in convention.
package embedded

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// SmokeOptInEnv names the opt-in environment variable that enables the
// Embedded Mode smoke test. The bring-up downloads and runs k3s plus
// PostgreSQL and pulls a runtime image, so the test runs only where an
// operator has declared the host able to host it.
const SmokeOptInEnv = "LENNY_EMBEDDED_SMOKE"

// RuntimeEnv names the environment variable that selects the runtime
// `lenny session new` targets in the smoke test. `lenny up` auto-seeds the
// echo runtime with a runnable image digest, an applied Runtime CRD, and a
// single-pod warm pool, so the test-smoke-embedded Makefile target defaults
// this to echo (the only invocation surface), and the smoke places a session
// with no operator setup. The §26 reference catalog ships placeholder-pinned
// images, so pointing the smoke at one of those runtimes requires an
// operator to register a pullable image, apply a Runtime CRD, and create a
// warm pool first. When this variable is unset it falls back to
// DefaultRuntime.
const RuntimeEnv = "LENNY_EMBEDDED_SMOKE_RUNTIME"

// DefaultRuntime is the runtime the smoke test targets when RuntimeEnv
// is unset. chat is the §26.7 user-facing default `lenny up` surfaces; the
// test-smoke-embedded Makefile target overrides RuntimeEnv to echo, the
// credential-free runtime `lenny up` runs on a pod out of the box.
const DefaultRuntime = "chat"

// UpTimeoutEnv names the environment variable that overrides the
// foreground `lenny up` deadline the smoke test waits under. The smoke
// test runs against a fresh LENNY_HOME, so first run downloads the
// PostgreSQL bundle, the k3s binary and image, and a runtime image
// before the stack is ready. On a cold cache with slow network those
// downloads can exceed the DefaultUpTimeout default, so an operator
// running the test on a slow or cold-cache host raises the deadline
// here (the test-coverage escape hatch for resource-dependent heavy
// tiers; a warm cache or a faster link keeps the default sufficient).
// The value is a Go duration string (for example "10m"). spec: §17.4.
const UpTimeoutEnv = "LENNY_EMBEDDED_UP_TIMEOUT"

// DefaultUpTimeout is the foreground `lenny up` deadline the smoke test
// waits under when UpTimeoutEnv is unset. It matches the §17.4 lifecycle
// ceiling the product code bounds the foreground wait at
// (pkg/embedded/stack/lifecycle.go), so a warm-cache bring-up completes
// inside it.
const DefaultUpTimeout = 6 * time.Minute

// UpTimeout returns the foreground `lenny up` deadline: the duration
// parsed from UpTimeoutEnv when it is set to a valid positive Go
// duration, otherwise DefaultUpTimeout. An unset, empty, malformed, or
// non-positive value falls back to the default so a typo cannot silently
// disable the deadline.
func UpTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv(UpTimeoutEnv)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return DefaultUpTimeout
}

// available reports whether the host can provision the Embedded Mode
// substrate and run the smoke test, returning a human-readable reason
// when it cannot. The checks are: the go toolchain is on PATH (the
// helper builds cmd/lenny), the host can provision the embedded k3s
// substrate (k3s.SupportedPlatform — Linux unconditionally, macOS and
// Windows when the docker CLI is on PATH so Docker Desktop supplies the
// Linux VM), and the LENNY_EMBEDDED_SMOKE opt-in is set (the bring-up is
// expensive and network-dependent on first run). Reusing the production
// SupportedPlatform gate keeps the test prerequisite in lockstep with
// the launcher-selection gate, so a host that the smoke test runs on is
// exactly a host where `lenny up` provisions a real cluster.
//
// spec: §17.4 (the embedded k3s substrate is provisioned per host
// operating system, as a managed child process on Linux and a
// Docker-backed container on macOS and Windows where Docker Desktop
// supplies the Linux VM).
func available() (bool, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return false, "go toolchain not on PATH"
	}
	if !k3s.SupportedPlatform() {
		return false, "embedded k3s substrate is unavailable: on " + runtime.GOOS +
			" the docker CLI must be on PATH so Docker Desktop supplies the Linux VM the embedded k3s runs in (§17.4 Embedded Mode)"
	}
	if !truthy(os.Getenv(SmokeOptInEnv)) {
		return false, "set " + SmokeOptInEnv + "=1 to run the Embedded Mode smoke test (downloads + runs k3s + PostgreSQL and pulls a runtime image)"
	}
	return true, ""
}

// truthy reports whether v is an affirmative environment-variable value.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// SkipUnlessAvailable skips the test unless the host can provision the
// Embedded Mode substrate and the operator has opted in. The host is
// able when k3s.SupportedPlatform reports the substrate provisionable
// (Linux, or a non-Linux host with the docker CLI on PATH so Docker
// Desktop supplies the Linux VM) and LENNY_EMBEDDED_SMOKE is set.
// spec: §17.4.
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if ok, reason := available(); !ok {
		t.Skipf("embedded: %s", reason)
	}
}

// Runtime returns the runtime the smoke test should target: RuntimeEnv
// when set, otherwise DefaultRuntime.
func Runtime() string {
	if v := strings.TrimSpace(os.Getenv(RuntimeEnv)); v != "" {
		return v
	}
	return DefaultRuntime
}

// Build compiles cmd/lenny into a fresh temp binary and returns its
// path. A build failure fails the test.
func Build(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-embedded-it-*")
	if err != nil {
		t.Fatalf("embedded.Build: mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	bin := filepath.Join(dir, "lenny")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/lenny")
	cmd.Dir = schematest.RepoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("embedded.Build: build cmd/lenny: %v\n%s", err, out)
	}
	return bin
}

// Result is the outcome of one CLI invocation.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run executes `lenny <args...>` with LENNY_HOME=home under a timeout,
// returning its captured stdout, stderr, and exit code. A timeout or a
// start error fails the test; a non-zero exit is returned so the caller
// can assert on it.
func Run(t testing.TB, bin, home string, timeout time.Duration, args ...string) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "LENNY_HOME="+home)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("embedded: `lenny %s` timed out after %s\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), timeout, res.Stdout, res.Stderr)
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
		} else {
			t.Fatalf("embedded: run `lenny %s`: %v", strings.Join(args, " "), err)
		}
	}
	return res
}
