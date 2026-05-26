// SPDX-License-Identifier: MIT

package isolation

// RuntimeClassName returns the Kubernetes RuntimeClass name for the
// isolation profile per the §5.3 mapping: standard runs under runc,
// sandboxed under gVisor, and microvm under Kata. The boolean is false
// for an unrecognized profile.
func RuntimeClassName(p Profile) (string, bool) {
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
