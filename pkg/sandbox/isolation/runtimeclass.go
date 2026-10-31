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
// when present. spec: §17.5 — operators running gVisor as `runsc` or Kata
// as `kata-qemu` / `kata-fc` override the chart-default literal via
// `runtimeClasses.profiles.<profile>.name` Helm values. An override mapped to an
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

// RuntimeClassOverrides assembles the §17.5 per-profile RuntimeClass name
// override map from the operator's three values, in the form
// ResolveRuntimeClassName reads. An empty value leaves its profile out, so the
// profile keeps its default name.
//
// The controller and the gateway both build their map here from the chart's
// `runtimeClasses.profiles.<profile>.name` values, which reach each binary as the same
// `--standard-runtime-class`, `--sandboxed-runtime-class`, and
// `--microvm-runtime-class` flags. The controller stamps the result on the
// pod's `runtimeClassName` and labels its warm-pool series from it, and the
// gateway labels its own `runtime_class` series from it, so one profile maps
// to one RuntimeClass name in both processes. spec: §17.5, §5.3 (Isolation
// Profiles), §16.1 (Metrics).
func RuntimeClassOverrides(standard, sandboxed, microvm string) map[Profile]string {
	out := map[Profile]string{}
	for p, name := range map[Profile]string{
		ProfileStandard:  standard,
		ProfileSandboxed: sandboxed,
		ProfileMicrovm:   microvm,
	} {
		if name != "" {
			out[p] = name
		}
	}
	return out
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
