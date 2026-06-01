// SPDX-License-Identifier: MIT

package preflight_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// TestCheckCosignProductionDevPasses_spec_5_3 confirms a dev install is
// not warned: §5.3 line 669 scopes the prerequisite to production and
// staging, and a stock dev install renders without the signing material
// configured.
func TestCheckCosignProductionDevPasses_spec_5_3(t *testing.T) {
	for _, env := range []string{"dev", "", "Dev", "  development-sandbox  "} {
		d := preflight.CheckCosignProduction(env, false)
		if !d.Passed {
			t.Fatalf("env=%q: dev-like install should pass, got %+v", env, d)
		}
		if d.Reason != "" {
			t.Fatalf("env=%q: dev-like install should be silent, got reason %q", env, d.Reason)
		}
	}
}

// TestCheckCosignProductionWarnsWhenDisabled_F_5_3_5 exercises the core
// gap: a production-or-staging install with cosign disabled emits a
// non-blocking WARNING (Passed stays true) so the install is not aborted
// but the operator is notified.
//
// spec: §5.3 line 669.
func TestCheckCosignProductionWarnsWhenDisabled_F_5_3_5(t *testing.T) {
	for _, env := range []string{"prod", "production", "staging", "stage", "PROD", " Staging "} {
		d := preflight.CheckCosignProduction(env, false)
		if !d.Passed {
			t.Fatalf("env=%q: advisory must be non-blocking, got Passed=false", env)
		}
		if !strings.HasPrefix(d.Reason, "WARNING:") {
			t.Fatalf("env=%q: expected a WARNING advisory, got reason %q", env, d.Reason)
		}
		if !strings.Contains(d.Reason, "cosign") {
			t.Fatalf("env=%q: advisory should name the cosign value, got %q", env, d.Reason)
		}
	}
}

// TestCheckCosignProductionEnabledSilent_F_5_3_5 confirms a production
// install that has already enabled signature verification produces no
// advisory.
func TestCheckCosignProductionEnabledSilent_F_5_3_5(t *testing.T) {
	for _, env := range []string{"prod", "production", "staging", "stage"} {
		d := preflight.CheckCosignProduction(env, true)
		if !d.Passed {
			t.Fatalf("env=%q: enabled cosign should pass, got %+v", env, d)
		}
		if d.Reason != "" {
			t.Fatalf("env=%q: enabled cosign should be silent, got reason %q", env, d.Reason)
		}
	}
}
