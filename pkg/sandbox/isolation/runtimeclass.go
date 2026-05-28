// SPDX-License-Identifier: MIT

package isolation

// RuntimeClassName returns the Kubernetes RuntimeClass name for the
// isolation profile per the §5.3 mapping: standard runs under runc,
// sandboxed under gVisor, and microvm under Kata. The boolean is false
// for an unrecognized profile.
func RuntimeClassName(p Profile) (string, bool) {
	return ResolveRuntimeClassName(p, nil)
}

// ResolveRuntimeClassName returns the Kubernetes RuntimeClass name for
// the isolation profile, applying the per-profile operator override
// when present. spec: §17.5 line 3 ("RuntimeClass works with any
// conformant runtime") — operators running gVisor as `runsc` or Kata
// as `kata-qemu` / `kata-fc` override the chart-default literal via
// `isolation.runtimeClassNames` Helm values. An override mapped to an
// empty string falls back to the default; a missing key falls back to
// the default. The boolean is false for an unrecognized profile.
func ResolveRuntimeClassName(p Profile, overrides map[Profile]string) (string, bool) {
	if name, ok := overrides[p]; ok && name != "" {
		return name, true
	}
	switch p {
	case ProfileStandard:
		return "runc", true
	case ProfileSandboxed:
		return "gvisor", true
	case ProfileMicrovm:
		return "kata", true
	default:
		return "", false
	}
}

// MustRuntimeClassName returns the §5.3 RuntimeClass name for p and
// panics for an unrecognized profile. It is for call sites that pass a
// compile-time profile constant (for example a flag default), where an
// unknown value is a programming error rather than runtime input.
func MustRuntimeClassName(p Profile) string {
	name, ok := RuntimeClassName(p)
	if !ok {
		panic("isolation: no RuntimeClass name for profile " + string(p))
	}
	return name
}
