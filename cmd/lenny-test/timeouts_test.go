// SPDX-License-Identifier: MIT

package main

import (
	"testing"
	"time"
)

// Per-tier go-test budgets are an operator-tunable
// default rule (code-best-practices: a non-spec default is overridable
// and degrades to the documented default when the override is invalid).
//
// tierTimeout gates the `go test -timeout` budget for the component,
// integration, and other tiers. A tier whose suite legitimately runs
// longer than its budget is aborted mid-run by `go test` (a package
// panic with zero per-test failures) and reported as a tier failure, so
// the override must take effect when set and must never collapse to a
// zero or negative budget on a typo.
func TestTierTimeoutResolvesOverrideAndDefaults(t *testing.T) {
	const def = 600 * time.Second

	t.Run("unset uses default", func(t *testing.T) {
		t.Setenv("LENNY_TEST_TIMEOUT_FIXTURE", "")
		if got := tierTimeout("LENNY_TEST_TIMEOUT_FIXTURE", def); got != def {
			t.Errorf("unset override: got %s, want default %s", got, def)
		}
	})

	t.Run("valid override takes effect", func(t *testing.T) {
		t.Setenv("LENNY_TEST_TIMEOUT_FIXTURE", "8m")
		if got, want := tierTimeout("LENNY_TEST_TIMEOUT_FIXTURE", def), 8*time.Minute; got != want {
			t.Errorf("valid override: got %s, want %s", got, want)
		}
	})

	t.Run("unparseable override degrades to default", func(t *testing.T) {
		t.Setenv("LENNY_TEST_TIMEOUT_FIXTURE", "not-a-duration")
		got := tierTimeout("LENNY_TEST_TIMEOUT_FIXTURE", def)
		if got != def {
			t.Errorf("unparseable override: got %s, want default %s", got, def)
		}
		if got <= 0 {
			t.Errorf("unparseable override must never yield a non-positive budget; got %s", got)
		}
	})
}

// The unit tier budget is operator-tunable (code-best-practices).
//
// The unit tier runs `go test ./...` with the race detector, and one
// package under it starts an envtest control plane per test. Its default
// budget must exceed that package's observed runtime, or a passing
// package is reported as a tier failure for a timeout alone.
func TestUnitTimeoutExceedsObservedRuntime(t *testing.T) {
	// Observed: 832.6s for pkg/gateway/podlifecycle/podsession without the
	// race detector, which slows it further. Go's own default is 600s, so
	// the package cannot pass without an explicit budget.
	const observedSlowest = 833 * time.Second
	if tierUnitTimeout <= observedSlowest {
		t.Errorf("tierUnitTimeout %s does not exceed observed unit runtime %s; a passing package will be aborted as a timeout",
			tierUnitTimeout, observedSlowest)
	}
}

// The integration tier budget is operator-tunable (code-best-practices).
//
// The integration tier suite boots the gateway per test against the
// compose stack and runs end-to-end. Its default budget must exceed that
// observed runtime with margin so a passing suite is not reported as a
// tier failure for a timeout alone.
func TestIntegrationTimeoutExceedsObservedRuntime(t *testing.T) {
	// Observed: 768.0s for a passing suite on a developer host. The
	// earlier 280s figure was recorded while most of the suite was refused
	// at session creation and returned without doing its work, so it
	// measured the refusal rather than the tier.
	const observedSlowest = 768 * time.Second
	if tierIntegrationTimeout <= observedSlowest {
		t.Errorf("tierIntegrationTimeout %s does not exceed observed integration runtime %s; a passing suite will be aborted as a timeout",
			tierIntegrationTimeout, observedSlowest)
	}
}
