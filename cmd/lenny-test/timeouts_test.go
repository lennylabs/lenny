// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"slices"
	"strings"
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

// The security tier budget is operator-tunable (code-best-practices).
//
// TESTING.md §12.9 (Tier 9, Security) runs the tier-9 package as one
// `go test` binary. Under the 600-second long-tier budget it shared with
// the load and chaos tiers, a loaded cluster pushed it past the budget and
// `go test` aborted the binary with no per-test failure (TEST-GAPS
// T-17.4.19). The tier's argv must carry its own budget, that budget must
// exceed the slowest recorded passing run, and the override must move it.
func TestSecurityTierRunsUnderItsOwnBudget(t *testing.T) {
	timeoutArg := func(args []string) string {
		for _, a := range args {
			if strings.HasPrefix(a, "-timeout=") {
				return strings.TrimPrefix(a, "-timeout=")
			}
		}
		return ""
	}

	t.Run("argv carries the security budget", func(t *testing.T) {
		args := securityTierArgs()
		if got, want := timeoutArg(args), tierSecurityTimeout.String(); got != want {
			t.Errorf("security tier -timeout = %q, want tierSecurityTimeout %q (argv %v)", got, want, args)
		}
		if !slices.Contains(args, "-tags=security") || !slices.Contains(args, "./tests/tier9_security/...") {
			t.Errorf("security tier argv = %v, want -tags=security over ./tests/tier9_security/...", args)
		}
	})

	t.Run("default exceeds the slowest recorded passing run", func(t *testing.T) {
		// Observed: 1634.1s for a passing whole-package run on a loaded
		// development host.
		const observedSlowest = 1635 * time.Second
		if defaultSecurityTimeout <= observedSlowest {
			t.Errorf("defaultSecurityTimeout %s does not exceed observed security runtime %s",
				defaultSecurityTimeout, observedSlowest)
		}
		if defaultSecurityTimeout <= tierLongTimeout && os.Getenv("LENNY_TEST_LONG_TIMEOUT") == "" {
			t.Errorf("defaultSecurityTimeout %s does not exceed the long-tier budget %s it replaced",
				defaultSecurityTimeout, tierLongTimeout)
		}
	})

	t.Run("unset override resolves to the default", func(t *testing.T) {
		t.Setenv(securityTimeoutEnv, "")
		if got := securityTierTimeout(); got != defaultSecurityTimeout {
			t.Errorf("unset %s: got %s, want %s", securityTimeoutEnv, got, defaultSecurityTimeout)
		}
	})

	t.Run("override moves the argv budget", func(t *testing.T) {
		t.Setenv(securityTimeoutEnv, "45m")
		saved := tierSecurityTimeout
		t.Cleanup(func() { tierSecurityTimeout = saved })
		tierSecurityTimeout = securityTierTimeout()
		if got, want := timeoutArg(securityTierArgs()), (45 * time.Minute).String(); got != want {
			t.Errorf("%s=45m: security tier -timeout = %q, want %q", securityTimeoutEnv, got, want)
		}
	})

	t.Run("unparseable override degrades to the default", func(t *testing.T) {
		t.Setenv(securityTimeoutEnv, "forever")
		if got := securityTierTimeout(); got != defaultSecurityTimeout {
			t.Errorf("unparseable %s: got %s, want %s", securityTimeoutEnv, got, defaultSecurityTimeout)
		}
	})

	if securityTimeoutEnv != "LENNY_TEST_SECURITY_TIMEOUT" {
		t.Errorf("security override env = %q, want LENNY_TEST_SECURITY_TIMEOUT", securityTimeoutEnv)
	}
}
