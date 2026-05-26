// SPDX-License-Identifier: MIT

package podsecurity

import (
	"errors"
	"testing"
)

// canonicalRCPolicy mirrors the §5.3 default profile->RuntimeClass
// mapping (gvisor/kata) the lenny-webhook binary derives from
// isolation.RuntimeClassName and the chart's runtimeClasses.profiles.
var canonicalRCPolicy = RuntimeClassPolicy{
	GVisorRuntimeClass: "gvisor",
	KataRuntimeClass:   "kata",
}

// noSeccompSpec returns a well-formed agent pod whose containers and
// pod-level SecurityContext set no seccomp profile. Under full §13.1
// enforcement this is a violation; §17.2 exempts gVisor pods.
func noSeccompSpec() PodSpec {
	spec := wellFormedSpec()
	spec.SeccompProfileType = ""
	for i := range spec.Containers {
		spec.Containers[i].SeccompProfileType = ""
	}
	return spec
}

// TestValidateGVisorPodSkipsSeccompCheck_spec_17_2 asserts the §17.2
// split-enforcement relaxation: a gVisor pod (RuntimeDefault is a no-op
// under gVisor's userspace syscall interception) skips the seccomp
// profile check and validates even with no seccomp profile set.
func TestValidateGVisorPodSkipsSeccompCheck_spec_17_2(t *testing.T) {
	spec := noSeccompSpec()
	spec.RuntimeClassName = "gvisor"
	if err := ValidateAgentPod(spec, lennyCredReadersGID, canonicalRCPolicy); err != nil {
		t.Errorf("§17.2: a gVisor pod skips the seccomp check, got %v", err)
	}
}

// TestValidateGVisorPodStillEnforcesOtherControls_spec_17_2 asserts
// §17.2 relaxes only seccomp for gVisor: non-root, all-caps-dropped,
// read-only rootfs, and allowPrivilegeEscalation: false all still apply.
func TestValidateGVisorPodStillEnforcesOtherControls_spec_17_2(t *testing.T) {
	spec := noSeccompSpec()
	spec.RuntimeClassName = "gvisor"
	spec.Containers[0].AllowPrivilegeEscalation = Ptr(true)
	spec.Containers[1].ReadOnlyRootFilesystem = Ptr(false)
	spec.Containers[1].CapabilitiesDrop = nil
	spec.RunAsNonRoot = Ptr(false)

	err := ValidateAgentPod(spec, lennyCredReadersGID, canonicalRCPolicy)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected a *PodSecurityError, got %v", err)
	}
	for _, want := range []string{
		"allowPrivilegeEscalation must be false",
		"readOnlyRootFilesystem must be true",
		"capabilities.drop must contain ALL",
		"runAsNonRoot must be true",
	} {
		if !pe.HasViolation(want) {
			t.Errorf("§17.2: gVisor still enforces %q, got %v", want, pe.Violations)
		}
	}
	// The seccomp check is the only one relaxed; it must not appear.
	if pe.HasViolation("seccompProfile.type must be") {
		t.Errorf("§17.2: gVisor must skip the seccomp check, got %v", pe.Violations)
	}
}

// TestValidateKataPodAllowsPrivilegeEscalation_spec_17_2 asserts §17.2:
// a Kata pod may set allowPrivilegeEscalation: true for its device
// plugins. Every other Restricted control (including seccomp) still
// applies, so the spec sets RuntimeDefault.
func TestValidateKataPodAllowsPrivilegeEscalation_spec_17_2(t *testing.T) {
	spec := wellFormedSpec()
	spec.RuntimeClassName = "kata"
	spec.Containers[0].AllowPrivilegeEscalation = Ptr(true)
	spec.Containers[1].AllowPrivilegeEscalation = Ptr(true)
	if err := ValidateAgentPod(spec, lennyCredReadersGID, canonicalRCPolicy); err != nil {
		t.Errorf("§17.2: a Kata pod may allow privilege escalation, got %v", err)
	}
}

// TestValidateKataPodStillEnforcesSeccomp_spec_17_2 asserts §17.2: Kata
// relaxes only privilege escalation. A Kata pod without RuntimeDefault
// seccomp is still rejected.
func TestValidateKataPodStillEnforcesSeccomp_spec_17_2(t *testing.T) {
	spec := noSeccompSpec()
	spec.RuntimeClassName = "kata"
	err := ValidateAgentPod(spec, lennyCredReadersGID, canonicalRCPolicy)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected a *PodSecurityError, got %v", err)
	}
	if !pe.HasViolation("seccompProfile.type must be") {
		t.Errorf("§17.2: Kata still enforces the seccomp check, got %v", pe.Violations)
	}
}

// TestValidateUnknownRuntimeClassGetsFullEnforcement_spec_17_2 asserts
// the fail-closed default: a runc, empty, or unrecognized RuntimeClass
// receives full §13.1 enforcement, so a missing seccomp profile and an
// escalating container are both rejected.
func TestValidateUnknownRuntimeClassGetsFullEnforcement_spec_17_2(t *testing.T) {
	for _, rc := range []string{"", "runc", "made-up"} {
		spec := noSeccompSpec()
		spec.RuntimeClassName = rc
		spec.Containers[0].AllowPrivilegeEscalation = Ptr(true)
		err := ValidateAgentPod(spec, lennyCredReadersGID, canonicalRCPolicy)
		var pe *PodSecurityError
		if !errors.As(err, &pe) {
			t.Fatalf("runtimeClass %q: expected a *PodSecurityError, got %v", rc, err)
		}
		if !pe.HasViolation("seccompProfile.type must be") {
			t.Errorf("runtimeClass %q: full enforcement keeps the seccomp check, got %v", rc, pe.Violations)
		}
		if !pe.HasViolation("allowPrivilegeEscalation must be false") {
			t.Errorf("runtimeClass %q: full enforcement keeps the privesc check, got %v", rc, pe.Violations)
		}
	}
}

// TestValidateEmptyPolicyNoRelaxation_spec_17_2 asserts a zero-value
// RuntimeClassPolicy applies full enforcement to every pod regardless of
// its RuntimeClass name: a gVisor-named pod with no policy configured is
// still seccomp-checked.
func TestValidateEmptyPolicyNoRelaxation_spec_17_2(t *testing.T) {
	spec := noSeccompSpec()
	spec.RuntimeClassName = "gvisor"
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected a *PodSecurityError, got %v", err)
	}
	if !pe.HasViolation("seccompProfile.type must be") {
		t.Errorf("an empty policy must not relax any RuntimeClass, got %v", pe.Violations)
	}
}
