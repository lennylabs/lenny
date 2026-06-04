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
// placement. That requires Linux (k3s is Linux-only per §17.4 line
// 224) plus a writable container runtime and, on first run, network
// access to download the k3s and PostgreSQL bundles (§17.4 lines 98,
// 150). Session start additionally pulls the runtime image, which the
// reference catalog ships placeholder-pinned (§17.4 / catalog.go), so
// `session new` succeeds only once an operator points the chosen
// runtime at a pullable image. The bring-up is therefore expensive and
// host-dependent; SkipUnlessAvailable gates the test behind an explicit
// LENNY_EMBEDDED_SMOKE opt-in and a Linux host so the unit and tier-4
// suites stay green on developer laptops and CI runners that lack the
// runtime. This mirrors the envtest and kind opt-in convention.
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

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// SmokeOptInEnv names the opt-in environment variable that enables the
// Embedded Mode smoke test. The bring-up downloads and runs k3s plus
// PostgreSQL and pulls a runtime image, so the test runs only where an
// operator has declared the host able to host it.
const SmokeOptInEnv = "LENNY_EMBEDDED_SMOKE"

// RuntimeEnv names the environment variable that selects the runtime
// `lenny session new` targets in the smoke test. The reference catalog
// ships placeholder-pinned images that cannot be pulled, so an operator
// running the smoke test sets this to a runtime whose image is pullable
// on the host (or pre-loads the chat image). It defaults to DefaultRuntime.
const RuntimeEnv = "LENNY_EMBEDDED_SMOKE_RUNTIME"

// DefaultRuntime is the runtime the smoke test targets when RuntimeEnv
// is unset. chat is the §26.7 zero-config default `lenny up` seeds.
const DefaultRuntime = "chat"

// available reports whether the host can run the Embedded Mode smoke
// test, returning a human-readable reason when it cannot. The checks
// are: the go toolchain is on PATH (the helper builds cmd/lenny), the
// host is Linux (embedded k3s session placement is Linux-only per
// §17.4 line 224), and the LENNY_EMBEDDED_SMOKE opt-in is set (the
// bring-up is expensive and network-dependent on first run).
func available() (bool, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return false, "go toolchain not on PATH"
	}
	if runtime.GOOS != "linux" {
		return false, "embedded k3s session placement requires Linux (§17.4 line 224); GOOS=" + runtime.GOOS
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

// SkipUnlessAvailable skips the test unless the host can run the
// Embedded Mode smoke test. spec: §17.4.
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
