// SPDX-License-Identifier: MIT

package egress_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/sandbox/egress"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func TestIsValid_spec_13_2(t *testing.T) {
	for _, p := range egress.AllProfiles() {
		if !egress.IsValid(p) {
			t.Errorf("AllProfiles() member %q reported invalid", p)
		}
	}
	for _, bad := range []egress.Profile{"", "open", "RESTRICTED", "provider", "internet "} {
		if egress.IsValid(bad) {
			t.Errorf("IsValid(%q) = true, want false", bad)
		}
	}
}

func TestDefaultIsRestricted_spec_13_2(t *testing.T) {
	// The §13.2 default is the narrowest egress so an omitted profile
	// never resolves to broad internet access.
	if got := egress.Default(); got != egress.ProfileRestricted {
		t.Fatalf("Default() = %q, want %q", got, egress.ProfileRestricted)
	}
}

// TestUnrestrictedEgressProfileAcceptedForCodingAgentOptIn_spec_26_2 pins
// §26.2's coding-agent-runtime opt-in citation against the canonical §13.2
// egress-profile enum this package implements.
//
// spec: §26.2 (spec/26_reference-runtime-catalog.md line 49): "Coding-agent
// runtimes default to `egressProfile: restricted` ([§6.1]): HTTPS to the
// operator-configured allowlist (package registries, container registries,
// LLM provider domains, the operator's Git hosts). Unrestricted egress is
// explicitly supported but MUST be opted into per pool via `egressProfile:
// unrestricted` — the operator accepts the risk."
func TestUnrestrictedEgressProfileAcceptedForCodingAgentOptIn_spec_26_2(t *testing.T) {
	t.Skip("spec self-contradiction: §26.2 requires a pool to opt a coding-agent " +
		"runtime into broader egress via `egressProfile: unrestricted`, but the " +
		"canonical §13.2 enum this package implements (restricted, provider-direct, " +
		"internet — also the literal set named in the pool-config-validator rejection " +
		"message) has no `unrestricted` member, so there is no profile value to accept " +
		"or reject a session against. Whether §26.2 should say `internet` instead, or " +
		"the enum should gain an `unrestricted` member (and, either way, whether the " +
		"restricted profile's HTTPS-allowlist-to-registries description matches the " +
		"gateway+DNS-only restricted policy §13.2 actually defines) needs a spec " +
		"decision, tracked separately in TEST-GAPS.md.")
	if !egress.IsValid("unrestricted") {
		t.Errorf("IsValid(%q) = false, want true per §26.2's coding-agent per-pool opt-in citation", "unrestricted")
	}
}

func TestRequiresSandboxedIsolation_spec_13_2(t *testing.T) {
	cases := map[egress.Profile]bool{
		egress.ProfileRestricted:     false,
		egress.ProfileProviderDirect: false,
		egress.ProfileInternet:       true,
	}
	for p, want := range cases {
		if got := egress.RequiresSandboxedIsolation(p); got != want {
			t.Errorf("RequiresSandboxedIsolation(%q) = %v, want %v", p, got, want)
		}
	}
}

// TestAllowsIsolation_spec_13_2 covers the full egress×isolation matrix,
// including the spec-named rejection (internet + standard) and the
// fail-closed handling of unknown values.
func TestAllowsIsolation_spec_13_2(t *testing.T) {
	cases := []struct {
		name string
		e    egress.Profile
		iso  isolation.Profile
		want bool
	}{
		{"restricted+standard", egress.ProfileRestricted, isolation.ProfileStandard, true},
		{"restricted+sandboxed", egress.ProfileRestricted, isolation.ProfileSandboxed, true},
		{"restricted+microvm", egress.ProfileRestricted, isolation.ProfileMicrovm, true},
		{"provider-direct+standard", egress.ProfileProviderDirect, isolation.ProfileStandard, true},
		{"provider-direct+sandboxed", egress.ProfileProviderDirect, isolation.ProfileSandboxed, true},
		{"internet+standard rejected", egress.ProfileInternet, isolation.ProfileStandard, false},
		{"internet+sandboxed", egress.ProfileInternet, isolation.ProfileSandboxed, true},
		{"internet+microvm", egress.ProfileInternet, isolation.ProfileMicrovm, true},
		{"unknown egress fails closed", egress.Profile("open"), isolation.ProfileSandboxed, false},
		{"unknown isolation fails closed", egress.ProfileInternet, isolation.Profile("vm"), false},
		{"empty egress fails closed", egress.Profile(""), isolation.ProfileSandboxed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := egress.AllowsIsolation(tc.e, tc.iso); got != tc.want {
				t.Errorf("AllowsIsolation(%q, %q) = %v, want %v", tc.e, tc.iso, got, tc.want)
			}
		})
	}
}
