// SPDX-License-Identifier: MIT

package embedded_test

import (
	"testing"

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

// spec: §17.4 — Runtime falls back to the §26.7 chat default when the
// selector env is unset, and honors an operator override (the catalog
// ships placeholder-pinned images, so an operator points the smoke test
// at a pullable runtime via LENNY_EMBEDDED_SMOKE_RUNTIME).
func TestRuntimeSelectorDefaultAndOverride(t *testing.T) {
	t.Setenv(embedded.RuntimeEnv, "")
	if got := embedded.Runtime(); got != embedded.DefaultRuntime {
		t.Errorf("Runtime() default = %q, want %q", got, embedded.DefaultRuntime)
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
