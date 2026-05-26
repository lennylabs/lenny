// SPDX-License-Identifier: MIT

// Package egress encodes the §13.2 per-pool egress profile enum
// (`restricted`, `provider-direct`, `internet`) and the §13.2
// cross-control that pairs an egress profile with a §5.3 isolation
// profile.
//
// The §13.2 NetworkPolicy resources are pre-created by the Helm chart;
// the warm pool controller only labels pods with the resolved
// `lenny.dev/egress-profile` so the matching policy takes effect. This
// package supplies the enum and the validation predicate the pool
// admission path uses to reject an illegal profile combination before a
// pod is ever labeled.
//
// Like the sibling `isolation` package it exposes only pure functions
// over a closed enum; it does not know about pools, runtimes, or
// sessions. Callers compose it with the context-specific error envelope.
package egress

import "github.com/lennylabs/lenny/pkg/sandbox/isolation"

// Profile is the §13.2 egress profile enum.
type Profile string

const (
	// ProfileRestricted permits egress to the gateway and the DNS proxy
	// only. It is the §13.2 default for agent pods that reach LLM
	// providers through the gateway proxy.
	ProfileRestricted Profile = "restricted"

	// ProfileProviderDirect adds the deployer-configured LLM provider
	// CIDRs to the restricted set so a `deliveryMode: direct` pod can
	// reach provider endpoints with a short-lived lease (§13.2).
	ProfileProviderDirect Profile = "provider-direct"

	// ProfileInternet adds broad public-internet egress (0.0.0.0/0 minus
	// cluster pod/service CIDRs and IMDS addresses) for pods that need
	// package installation or general web access (§13.2). It requires a
	// sandboxed isolation profile.
	ProfileInternet Profile = "internet"
)

// AllProfiles returns the closed §13.2 enum in table order, narrowest
// egress first.
func AllProfiles() []Profile {
	return []Profile{ProfileRestricted, ProfileProviderDirect, ProfileInternet}
}

// Default returns the §13.2 default egress profile. A pool that omits
// `egressProfile` resolves to this value, so the absence of an explicit
// profile yields the narrowest egress rather than the broadest.
func Default() Profile { return ProfileRestricted }

// IsValid reports whether p is a known egress profile. Empty strings and
// unrecognised values return false; callers reject those at the
// admission boundary.
func IsValid(p Profile) bool {
	for _, q := range AllProfiles() {
		if p == q {
			return true
		}
	}
	return false
}

// RequiresSandboxedIsolation reports whether the egress profile may only
// be combined with a sandboxed isolation profile (`sandboxed` or
// `microvm`). Only `internet` carries this requirement per §13.2: a runc
// pod with broad internet egress is the high-blast-radius configuration
// the §5.3 security note targets.
func RequiresSandboxedIsolation(p Profile) bool {
	return p == ProfileInternet
}

// AllowsIsolation reports whether the egress profile e may be combined
// with the isolation profile iso under the §13.2 cross-control. The
// `internet` profile requires a sandboxed isolation profile (`sandboxed`
// or `microvm`); every other egress profile is compatible with any
// valid isolation profile.
//
// An unknown egress profile or an unknown isolation profile yields false
// so a mistyped value fails closed rather than silently bypassing the
// cross-control.
//
// spec: §13.2 — "the `internet` profile requires a sandboxed isolation
// profile (`sandboxed` or `microvm`) — pools with `isolationProfile:
// standard` (runc) cannot use the `internet` egress profile."
func AllowsIsolation(e Profile, iso isolation.Profile) bool {
	if !IsValid(e) || !isolation.IsValid(iso) {
		return false
	}
	if RequiresSandboxedIsolation(e) {
		return iso == isolation.ProfileSandboxed || iso == isolation.ProfileMicrovm
	}
	return true
}
