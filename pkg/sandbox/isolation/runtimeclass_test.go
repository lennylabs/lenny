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
