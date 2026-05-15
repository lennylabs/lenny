// SPDX-License-Identifier: MIT

package isolation

import "testing"

// spec: §5.3 (Profile enum + Default), §7.1 derive rule 5, §8.3 SEC-001,
//       §15.1 ISOLATION_MONOTONICITY_VIOLATED.

func TestAllProfilesIsExhaustive(t *testing.T) {
	got := AllProfiles()
	want := []Profile{ProfileStandard, ProfileSandboxed, ProfileMicrovm}
	if len(got) != len(want) {
		t.Fatalf("AllProfiles length: got %d, want %d", len(got), len(want))
	}
	for i, p := range got {
		if p != want[i] {
			t.Errorf("AllProfiles[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestProfileStringMatchesSpec(t *testing.T) {
	cases := map[Profile]string{
		ProfileStandard:  "standard",
		ProfileSandboxed: "sandboxed",
		ProfileMicrovm:   "microvm",
	}
	for p, want := range cases {
		if string(p) != want {
			t.Errorf("Profile %q.String() = %q, want %q", p, string(p), want)
		}
	}
}

func TestDefaultProfileIsSandboxed(t *testing.T) {
	// §5.3: "Default for all workloads" is sandboxed (gVisor).
	if Default() != ProfileSandboxed {
		t.Errorf("Default() = %q, want %q", Default(), ProfileSandboxed)
	}
}

func TestRuntimeClassMapping(t *testing.T) {
	// §5.3 table: standard→runc, sandboxed→gvisor, microvm→kata.
	cases := map[Profile]string{
		ProfileStandard:  "runc",
		ProfileSandboxed: "gvisor",
		ProfileMicrovm:   "kata",
	}
	for p, want := range cases {
		if got := p.RuntimeClass(); got != want {
			t.Errorf("Profile %q.RuntimeClass() = %q, want %q", p, got, want)
		}
	}
}

func TestRuntimeClassOfUnknownIsEmpty(t *testing.T) {
	if got := Profile("ferrous").RuntimeClass(); got != "" {
		t.Errorf("RuntimeClass(unknown) = %q, want empty", got)
	}
}

func TestIsValid(t *testing.T) {
	for _, p := range AllProfiles() {
		if !IsValid(p) {
			t.Errorf("IsValid(%q) = false, want true", p)
		}
	}
	if IsValid("") {
		t.Error("IsValid(\"\") = true, want false")
	}
	if IsValid("hardened") {
		t.Error("IsValid(\"hardened\") = true, want false")
	}
}

func TestRankOrderingMatchesSpec(t *testing.T) {
	// §5.3 / §8.3 SEC-001: standard < sandboxed < microvm.
	if Rank(ProfileStandard) >= Rank(ProfileSandboxed) {
		t.Errorf("Rank(standard) %d should be < Rank(sandboxed) %d", Rank(ProfileStandard), Rank(ProfileSandboxed))
	}
	if Rank(ProfileSandboxed) >= Rank(ProfileMicrovm) {
		t.Errorf("Rank(sandboxed) %d should be < Rank(microvm) %d", Rank(ProfileSandboxed), Rank(ProfileMicrovm))
	}
}

func TestRankOfUnknownIsZero(t *testing.T) {
	// Treating unknown as the most permissive forces monotonicity
	// checks to reject any unknown target. We sentinel with -1 to
	// distinguish from `standard`.
	if got := Rank(""); got != -1 {
		t.Errorf("Rank(\"\") = %d, want -1", got)
	}
	if got := Rank("hardened"); got != -1 {
		t.Errorf("Rank(unknown) = %d, want -1", got)
	}
}

func TestCompare(t *testing.T) {
	// Compare(a, b) returns -1 if a < b, 0 if equal, +1 if a > b.
	cases := []struct {
		a, b Profile
		want int
	}{
		{ProfileStandard, ProfileStandard, 0},
		{ProfileStandard, ProfileSandboxed, -1},
		{ProfileStandard, ProfileMicrovm, -1},
		{ProfileSandboxed, ProfileStandard, +1},
		{ProfileSandboxed, ProfileSandboxed, 0},
		{ProfileSandboxed, ProfileMicrovm, -1},
		{ProfileMicrovm, ProfileStandard, +1},
		{ProfileMicrovm, ProfileSandboxed, +1},
		{ProfileMicrovm, ProfileMicrovm, 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestAtLeastAsRestrictive(t *testing.T) {
	// §7.1 derive rule 5 / §8.3 SEC-001: a target satisfies
	// monotonicity iff Rank(target) >= Rank(source).
	cases := []struct {
		target, source Profile
		want           bool
	}{
		// equal — always satisfies.
		{ProfileStandard, ProfileStandard, true},
		{ProfileSandboxed, ProfileSandboxed, true},
		{ProfileMicrovm, ProfileMicrovm, true},
		// stricter — satisfies.
		{ProfileSandboxed, ProfileStandard, true},
		{ProfileMicrovm, ProfileStandard, true},
		{ProfileMicrovm, ProfileSandboxed, true},
		// weaker — violates.
		{ProfileStandard, ProfileSandboxed, false},
		{ProfileStandard, ProfileMicrovm, false},
		{ProfileSandboxed, ProfileMicrovm, false},
	}
	for _, c := range cases {
		if got := AtLeastAsRestrictive(c.target, c.source); got != c.want {
			t.Errorf("AtLeastAsRestrictive(target=%q, source=%q) = %v, want %v", c.target, c.source, got, c.want)
		}
	}
}

func TestAtLeastAsRestrictiveRejectsUnknown(t *testing.T) {
	// Unknown values must never satisfy monotonicity in either slot,
	// even compared against another unknown — this forces validation
	// to happen upstream.
	cases := []struct {
		target, source Profile
	}{
		{Profile(""), ProfileStandard},
		{ProfileStandard, Profile("")},
		{Profile("hardened"), ProfileSandboxed},
		{ProfileSandboxed, Profile("hardened")},
		{Profile(""), Profile("")},
	}
	for _, c := range cases {
		if AtLeastAsRestrictive(c.target, c.source) {
			t.Errorf("AtLeastAsRestrictive(target=%q, source=%q) = true, want false", c.target, c.source)
		}
	}
}
