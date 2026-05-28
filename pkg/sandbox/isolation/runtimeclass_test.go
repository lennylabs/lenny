// SPDX-License-Identifier: MIT

package isolation_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func TestRuntimeClassNameMapsEveryProfile(t *testing.T) {
	want := map[isolation.Profile]string{
		isolation.ProfileStandard:  "runc",
		isolation.ProfileSandboxed: "gvisor",
		isolation.ProfileMicrovm:   "kata",
	}
	for _, p := range isolation.AllProfiles() {
		got, ok := isolation.RuntimeClassName(p)
		if !ok {
			t.Errorf("RuntimeClassName(%q) returned ok=false; every profile must map", p)
			continue
		}
		if got != want[p] {
			t.Errorf("RuntimeClassName(%q) = %q, want %q", p, got, want[p])
		}
	}
}

func TestRuntimeClassNameRejectsUnknownProfile(t *testing.T) {
	if _, ok := isolation.RuntimeClassName(isolation.Profile("teleport")); ok {
		t.Error("RuntimeClassName should return ok=false for an unrecognized profile")
	}
}

// spec: §17.5 line 3 — operators whose cluster ships the gVisor or
// Kata RuntimeClass under a non-default name (e.g. `runsc`, `kata-qemu`)
// MUST be able to remap each profile via a chart-supplied override map
// without renaming the in-cluster RuntimeClass objects to Lenny's
// literal defaults.
func TestResolveRuntimeClassNameAppliesOverrides_spec_17_5(t *testing.T) {
	overrides := map[isolation.Profile]string{
		isolation.ProfileSandboxed: "runsc",
		isolation.ProfileMicrovm:   "kata-qemu",
	}
	cases := map[isolation.Profile]string{
		isolation.ProfileStandard:  "runc",       // no override → default
		isolation.ProfileSandboxed: "runsc",      // overridden
		isolation.ProfileMicrovm:   "kata-qemu",  // overridden
	}
	for p, want := range cases {
		got, ok := isolation.ResolveRuntimeClassName(p, overrides)
		if !ok {
			t.Errorf("ResolveRuntimeClassName(%q) returned ok=false", p)
			continue
		}
		if got != want {
			t.Errorf("ResolveRuntimeClassName(%q) = %q, want %q", p, got, want)
		}
	}
}

// An empty-string override falls back to the §5.3 default — covers the
// chart's empty-string default for an unspecified override.
func TestResolveRuntimeClassNameEmptyOverrideFallsBack_spec_17_5(t *testing.T) {
	overrides := map[isolation.Profile]string{
		isolation.ProfileSandboxed: "",
	}
	got, ok := isolation.ResolveRuntimeClassName(isolation.ProfileSandboxed, overrides)
	if !ok || got != "gvisor" {
		t.Errorf("ResolveRuntimeClassName(sandboxed, empty-override) = %q,%v; want gvisor,true", got, ok)
	}
}

func TestResolveRuntimeClassNameNilMapIsDefault_spec_17_5(t *testing.T) {
	got, ok := isolation.ResolveRuntimeClassName(isolation.ProfileMicrovm, nil)
	if !ok || got != "kata" {
		t.Errorf("ResolveRuntimeClassName(microvm, nil) = %q,%v; want kata,true", got, ok)
	}
}
