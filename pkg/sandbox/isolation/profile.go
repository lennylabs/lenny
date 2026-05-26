// SPDX-License-Identifier: MIT

// Package isolation encodes the §5.3 sandbox isolation profile enum
// (`standard`, `sandboxed`, `microvm`) and the monotonicity arithmetic
// shared by:
//
//   - §7.1 derive rule 5 — the target pool's isolation profile must be
//     at least as restrictive as the source session's, overridable only
//     by a platform-admin via `allowIsolationDowngrade: true`.
//   - §8.3 SEC-001 delegation monotonicity — a child task's pool must
//     be at least as restrictive as the calling session's, with no
//     override (delegation downgrades are categorically rejected).
//   - §15.1 replay monotonicity — the same rule applies to
//     `POST /v1/sessions/{id}/replay` whenever the resolved `targetPool`
//     differs from the source session's pool.
//
// The package exposes only pure functions over the closed enum. It does
// not know about pools, runtimes, or sessions; callers compose it with
// the appropriate context-specific error envelope (e.g.,
// `ISOLATION_MONOTONICITY_VIOLATED` for §15.1).
package isolation

// Profile is the §5.3 enum.
type Profile string

const (
	// ProfileStandard maps to the `runc` RuntimeClass. Development /
	// testing only — requires the explicit `allowStandardIsolation: true`
	// pool flag per §5.3.
	ProfileStandard Profile = "standard"

	// ProfileSandboxed maps to the `gvisor` RuntimeClass. §5.3 default
	// for all production workloads.
	ProfileSandboxed Profile = "sandboxed"

	// ProfileMicrovm maps to the `kata` RuntimeClass. Higher-risk /
	// multi-tenant; only profile that may set `allowCrossTenantReuse:
	// true` (§5.2).
	ProfileMicrovm Profile = "microvm"
)

// AllProfiles returns the closed enum in §5.3 table order, weakest
// first.
func AllProfiles() []Profile {
	return []Profile{ProfileStandard, ProfileSandboxed, ProfileMicrovm}
}

// Default returns the §5.3 default profile for production workloads.
// Pools that omit `isolationProfile` resolve to this value.
func Default() Profile { return ProfileSandboxed }

// DevModeIsolationWarning is the §5.3 line 677 warning a binary logs
// once at startup when global.devMode (LENNY_DEV_MODE=true) is set,
// because the default isolation profile then falls back to runc.
const DevModeIsolationWarning = "Dev mode: using runc isolation. Do not use in production."

// DefaultForMode returns the §5.3 default isolation profile, honoring
// the dev-mode fallback. When devMode is true the default falls back to
// `standard` (runc) so developers can run on a cluster without gVisor
// installed; otherwise it is the production default (`sandboxed`).
// Binaries that apply this fallback log DevModeIsolationWarning once at
// startup so an accidental production dev-mode install is visible in the
// logs.
//
// spec: §5.3 line 677.
func DefaultForMode(devMode bool) Profile {
	if devMode {
		return ProfileStandard
	}
	return Default()
}

// IsValid reports whether p is a known profile. Empty strings and
// unrecognised values return false; callers should reject those at the
// admission boundary.
func IsValid(p Profile) bool {
	for _, q := range AllProfiles() {
		if p == q {
			return true
		}
	}
	return false
}

// RuntimeClass returns the Kubernetes `RuntimeClass` name the §5.3
// table maps p to. Returns "" for unknown profiles so callers can
// distinguish "not set" from "set to a valid value".
func (p Profile) RuntimeClass() string {
	switch p {
	case ProfileStandard:
		return "runc"
	case ProfileSandboxed:
		return "gvisor"
	case ProfileMicrovm:
		return "kata"
	default:
		return ""
	}
}

// Rank returns the §5.3 / §8.3 SEC-001 ordering integer:
// `standard` = 0, `sandboxed` = 1, `microvm` = 2. Returns -1 for any
// value not in the closed enum (including ""); callers must validate
// before invoking the monotonicity helpers below.
func Rank(p Profile) int {
	switch p {
	case ProfileStandard:
		return 0
	case ProfileSandboxed:
		return 1
	case ProfileMicrovm:
		return 2
	default:
		return -1
	}
}

// Compare returns -1 if a is weaker than b, 0 if they are equal, and
// +1 if a is stronger than b. Both arguments must be valid profiles;
// comparisons that involve an unknown value treat the unknown as the
// weakest possible value (rank −1) which always sorts first.
func Compare(a, b Profile) int {
	ra, rb := Rank(a), Rank(b)
	switch {
	case ra < rb:
		return -1
	case ra > rb:
		return +1
	default:
		return 0
	}
}

// AtLeastAsRestrictive reports whether `target` provides isolation at
// least as strong as `source` per the §5.3 / §8.3 ordering. This is
// the canonical predicate used by delegate_task, derive, and replay
// to enforce isolation monotonicity (SEC-001):
//
//	if !AtLeastAsRestrictive(targetPoolProfile, sourceSessionProfile) {
//	    return ISOLATION_MONOTONICITY_VIOLATED
//	}
//
// Unknown profiles never satisfy the predicate; callers must validate
// both arguments via IsValid first so a mistyped pool name does not
// silently pass the monotonicity check.
func AtLeastAsRestrictive(target, source Profile) bool {
	if !IsValid(target) || !IsValid(source) {
		return false
	}
	return Rank(target) >= Rank(source)
}
