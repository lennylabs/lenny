// SPDX-License-Identifier: MIT

package embedded_test

import (
	"runtime"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
	"github.com/lennylabs/lenny/tests/testinfra/embedded"
)

// spec: §17.4 — the Embedded Mode smoke harness skips, rather than
// fails, when the host has not opted in. The bring-up downloads and
// runs k3s + PostgreSQL and pulls a runtime image, so it must not run
// on a developer laptop or a CI runner that has not declared itself
// able to host it. Mirrors the envtest skip-gate test
// (tests/testinfra/envtest/envtest_test.go).
func TestSkipUnlessAvailableSkipsWithoutOptIn(t *testing.T) {
	// Force the opt-in off so the gate skips deterministically
	// regardless of the ambient environment or host OS.
	t.Setenv(embedded.SmokeOptInEnv, "")

	t.Run("inner", func(inner *testing.T) {
		defer func() {
			if inner.Failed() {
				t.Errorf("SkipUnlessAvailable failed instead of skipping")
			}
			if !inner.Skipped() {
				t.Errorf("SkipUnlessAvailable did not skip without the opt-in")
			}
		}()
		embedded.SkipUnlessAvailable(inner)
	})
}

// spec: §17.4 — the Embedded Mode smoke gate is no longer Linux-only: with
// the opt-in set, it admits the test exactly on the hosts the production
// launcher can provision the substrate on (k3s.SupportedPlatform — Linux
// unconditionally, a non-Linux host when Docker is on PATH). This pins
// that S8's relaxation tracks the production gate rather than re-deriving
// a separate OS check, so the test runs on macOS and Windows with Docker
// present instead of always skipping off Linux.
func TestSkipUnlessAvailableTracksSupportedPlatform(t *testing.T) {
	// Opt in so the gate's decision is the substrate prerequisite rather
	// than the opt-in. SkipUnlessAvailable skips when the substrate is
	// unavailable and runs (does not skip) when it is available, so its
	// skip/run outcome must equal k3s.SupportedPlatform() on this host.
	t.Setenv(embedded.SmokeOptInEnv, "1")

	wantRun := k3s.SupportedPlatform()
	t.Run("inner", func(inner *testing.T) {
		defer func() {
			ranToCompletion := !inner.Skipped()
			if ranToCompletion != wantRun {
				t.Errorf("with the opt-in set, SkipUnlessAvailable ran=%v, want ran=%v "+
					"(k3s.SupportedPlatform()=%v on GOOS=%s); the smoke gate must track the production substrate gate",
					ranToCompletion, wantRun, wantRun, runtime.GOOS)
			}
		}()
		embedded.SkipUnlessAvailable(inner)
	})

	// On Linux the substrate is supported unconditionally, so the opt-in
	// gate must admit the test there. This pins the Linux path explicitly
	// so a regression that breaks Linux is caught even on a Linux-only CI
	// runner.
	if runtime.GOOS == "linux" && !wantRun {
		t.Fatalf("k3s.SupportedPlatform() = false on linux; the embedded substrate must be supported unconditionally on Linux")
	}
}

// spec: §17.4, §26.7 — Runtime falls back to the §26.7 chat user-facing
// default when the selector env is unset, and honors an operator override.
// The test-smoke-embedded Makefile target sets the override to echo, the
// credential-free runtime `lenny up` auto-seeds with a runnable image, an
// applied Runtime CRD, and a single-pod warm pool; the §26 reference
// runtimes ship placeholder-pinned, so pointing the smoke at one of them
// requires the operator to register a pullable image, apply a Runtime CRD,
// and create a warm pool first.
func TestRuntimeSelectorDefaultAndOverride(t *testing.T) {
	t.Setenv(embedded.RuntimeEnv, "")
	if got := embedded.Runtime(); got != embedded.DefaultRuntime {
		t.Errorf("Runtime() default = %q, want %q", got, embedded.DefaultRuntime)
	}
	// The Makefile default override the smoke runs under: echo, the
	// auto-seeded runnable runtime. DefaultRuntime stays chat (the §26.7
	// user-facing default), so the override is what makes the smoke target
	// the runnable runtime.
	t.Setenv(embedded.RuntimeEnv, "echo")
	if got := embedded.Runtime(); got != "echo" {
		t.Errorf("Runtime() echo override = %q, want %q", got, "echo")
	}
	t.Setenv(embedded.RuntimeEnv, "claude-code")
	if got := embedded.Runtime(); got != "claude-code" {
		t.Errorf("Runtime() override = %q, want %q", got, "claude-code")
	}
	// Surrounding whitespace is trimmed so a stray newline in a CI
	// env-file does not select an empty runtime name.
	t.Setenv(embedded.RuntimeEnv, "  langgraph \n")
	if got := embedded.Runtime(); got != "langgraph" {
		t.Errorf("Runtime() trimmed = %q, want %q", got, "langgraph")
	}
}

// spec: §17.4 — the foreground `lenny up` deadline the smoke test waits
// under is operator-tunable so a cold-cache or slow-network host (where
// the first-run PostgreSQL + k3s + runtime-image downloads exceed the
// default) can raise it without editing the test (the test-coverage
// escape hatch for resource-dependent heavy tiers). An unset, empty,
// malformed, or non-positive value falls back to the default so a typo
// cannot silently disable the deadline.
func TestUpTimeoutDefaultAndOverride(t *testing.T) {
	t.Setenv(embedded.UpTimeoutEnv, "")
	if got := embedded.UpTimeout(); got != embedded.DefaultUpTimeout {
		t.Errorf("UpTimeout() default = %s, want %s", got, embedded.DefaultUpTimeout)
	}
	t.Setenv(embedded.UpTimeoutEnv, "10m")
	if got := embedded.UpTimeout(); got != 10*time.Minute {
		t.Errorf("UpTimeout() override = %s, want %s", got, 10*time.Minute)
	}
	// Surrounding whitespace is trimmed so a stray newline in a CI
	// env-file does not defeat the parse.
	t.Setenv(embedded.UpTimeoutEnv, "  15m \n")
	if got := embedded.UpTimeout(); got != 15*time.Minute {
		t.Errorf("UpTimeout() trimmed = %s, want %s", got, 15*time.Minute)
	}
	// A malformed or non-positive value falls back to the default rather
	// than silently disabling the deadline.
	for _, bad := range []string{"not-a-duration", "0", "-5m"} {
		t.Setenv(embedded.UpTimeoutEnv, bad)
		if got := embedded.UpTimeout(); got != embedded.DefaultUpTimeout {
			t.Errorf("UpTimeout() with %q = %s, want default %s", bad, got, embedded.DefaultUpTimeout)
		}
	}
}
