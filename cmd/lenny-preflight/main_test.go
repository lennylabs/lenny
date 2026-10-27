// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"strings"
	"testing"
)

func TestParseAcceptDowngradeCollectsFlags(t *testing.T) {
	got := parseAcceptDowngrade("llmProxy, compliance")
	if len(got) != 2 {
		t.Fatalf("parseAcceptDowngrade = %v, want exactly 2 entries", got)
	}
	if !got["llmProxy"] || !got["compliance"] {
		t.Errorf("parseAcceptDowngrade = %v, want llmProxy and compliance", got)
	}
}

func TestParseAcceptDowngradeEmpty(t *testing.T) {
	if got := parseAcceptDowngrade(""); len(got) != 0 {
		t.Errorf("parseAcceptDowngrade(\"\") = %v, want an empty set", got)
	}
}

func TestParseAcceptDowngradeTrimsAndSkipsBlanks(t *testing.T) {
	got := parseAcceptDowngrade(" drainReadiness , , ")
	if len(got) != 1 || !got["drainReadiness"] {
		t.Errorf("parseAcceptDowngrade = %v, want only drainReadiness", got)
	}
}

// spec: 16.1.1 (Attribute Naming) deployment_tier; 25.4 (The lenny-ops Service) "Preflight validation"
//
// TestValidatePlatformTier_spec_16_1_1 pins the lenny-preflight refusal of a
// --platform-tier outside tier1, tier2, and tier3: the three members pass,
// and the empty value, case and whitespace variants, and out-of-range tiers
// are refused with an error naming the value. main calls it before any
// check runs, and the flag defaults to tier1, the chart default.
func TestValidatePlatformTier_spec_16_1_1(t *testing.T) {
	for _, tier := range []string{"tier1", "tier2", "tier3"} {
		if err := validatePlatformTier(tier); err != nil {
			t.Errorf("validatePlatformTier(%q) = %v, want nil", tier, err)
		}
	}
	for _, tier := range []string{"", "TIER2", "Tier3", "tier4", "tier0", " tier2", "tier2 ", "2"} {
		err := validatePlatformTier(tier)
		if err == nil {
			t.Errorf("validatePlatformTier(%q) = nil, want a refusal", tier)
			continue
		}
		if want := `--platform-tier "` + tier + `" is not one of tier1, tier2, or tier3`; !strings.Contains(err.Error(), want) {
			t.Errorf("validatePlatformTier(%q) error = %q, want it to contain %q", tier, err, want)
		}
	}
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(src)
	parse := strings.Index(body, "flag.Parse()")
	validate := strings.Index(body, "validatePlatformTier(*platformTier)")
	run := strings.Index(body, "preflight.Run(")
	if parse < 0 || validate < parse || (run >= 0 && validate > run) {
		t.Errorf("main does not call validatePlatformTier(*platformTier) after flag.Parse and before the checks run")
	}
}
