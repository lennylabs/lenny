// SPDX-License-Identifier: MIT

package podsecurity

import (
	"errors"
	"testing"
)

const lennyCredReadersGID int64 = 65532

func wellFormedSpec() PodSpec {
	return PodSpec{
		FSGroup:            Ptr[int64](lennyCredReadersGID),
		RunAsNonRoot:       Ptr(true),
		SeccompProfileType: SeccompRuntimeDefault,
		Containers: []ContainerSpec{
			{
				Name:                     "adapter",
				AllowPrivilegeEscalation: Ptr(false),
				Privileged:               Ptr(false),
				ReadOnlyRootFilesystem:   Ptr(true),
				CapabilitiesDrop:         []string{"ALL"},
			},
			{
				Name:                     "agent",
				AllowPrivilegeEscalation: Ptr(false),
				Privileged:               Ptr(false),
				ReadOnlyRootFilesystem:   Ptr(true),
				CapabilitiesDrop:         []string{"ALL"},
			},
		},
	}
}

func TestValidateAcceptsWellFormedAgentPod(t *testing.T) {
	if err := ValidateAgentPod(wellFormedSpec(), lennyCredReadersGID); err != nil {
		t.Errorf("well-formed pod should validate, got %v", err)
	}
}

// TestValidateRejectsCredGroupOverbroad asserts §13.1
// POD_SPEC_CRED_GROUP_OVERBROAD: a container that is not the adapter or
// agent must not declare the lenny-cred-readers GID in runAsGroup.
func TestValidateRejectsCredGroupOverbroad(t *testing.T) {
	spec := wellFormedSpec()
	spec.CredentialContainerNames = []string{"adapter", "agent"}
	spec.Containers = append(spec.Containers, ContainerSpec{
		Name:                     "sidecar",
		AllowPrivilegeEscalation: Ptr(false),
		Privileged:               Ptr(false),
		ReadOnlyRootFilesystem:   Ptr(true),
		CapabilitiesDrop:         []string{"ALL"},
		RunAsGroup:               Ptr[int64](lennyCredReadersGID),
	})
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected a *PodSecurityError, got %v", err)
	}
	if !pe.HasViolation("POD_SPEC_CRED_GROUP_OVERBROAD") {
		t.Errorf("expected POD_SPEC_CRED_GROUP_OVERBROAD, got %v", pe.Violations)
	}
}

// TestValidateAcceptsCredGroupOnCredentialContainer asserts the adapter
// and agent containers MAY declare the lenny-cred-readers GID: they are
// the §13.1 credential containers and the cross-UID file-delivery path
// depends on their membership.
func TestValidateAcceptsCredGroupOnCredentialContainer(t *testing.T) {
	spec := wellFormedSpec()
	spec.CredentialContainerNames = []string{"adapter", "agent"}
	spec.Containers[0].RunAsGroup = Ptr[int64](lennyCredReadersGID)
	if err := ValidateAgentPod(spec, lennyCredReadersGID); err != nil {
		t.Errorf("the adapter container may carry the cred-readers GID, got %v", err)
	}
}

func TestValidateRejectsHostSharing(t *testing.T) {
	cases := []struct {
		name string
		set  func(*PodSpec)
	}{
		{"shareProcessNamespace", func(s *PodSpec) { s.ShareProcessNamespace = true }},
		{"hostPID", func(s *PodSpec) { s.HostPID = true }},
		{"hostNetwork", func(s *PodSpec) { s.HostNetwork = true }},
		{"hostIPC", func(s *PodSpec) { s.HostIPC = true }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := wellFormedSpec()
			c.set(&spec)
			err := ValidateAgentPod(spec, lennyCredReadersGID)
			if err == nil {
				t.Fatalf("expected rejection for %s", c.name)
			}
			var pe *PodSecurityError
			if !errors.As(err, &pe) {
				t.Fatalf("expected *PodSecurityError, got %T", err)
			}
			if !pe.HasViolation("POD_SPEC_HOST_SHARING_FORBIDDEN") {
				t.Errorf("violation should cite POD_SPEC_HOST_SHARING_FORBIDDEN; got %v", pe.Violations)
			}
		})
	}
}

func TestValidateRejectsMissingFSGroup(t *testing.T) {
	spec := wellFormedSpec()
	spec.FSGroup = nil
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *PodSecurityError, got %v", err)
	}
	if !pe.HasViolation("POD_SPEC_CRED_FSGROUP_MISSING") {
		t.Errorf("violation should cite POD_SPEC_CRED_FSGROUP_MISSING; got %v", pe.Violations)
	}
}

func TestValidateRejectsWrongFSGroup(t *testing.T) {
	spec := wellFormedSpec()
	spec.FSGroup = Ptr[int64](99999)
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected error")
	}
	if !pe.HasViolation("POD_SPEC_CRED_FSGROUP_MISSING") {
		t.Errorf("wrong fsGroup should cite POD_SPEC_CRED_FSGROUP_MISSING")
	}
}

func TestValidateRejectsRoot(t *testing.T) {
	spec := wellFormedSpec()
	spec.RunAsNonRoot = Ptr(false)
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected error")
	}
	if !pe.HasViolation("runAsNonRoot must be true") {
		t.Errorf("root pod should be rejected; got %v", pe.Violations)
	}
}

func TestValidateRejectsAllowPrivilegeEscalation(t *testing.T) {
	spec := wellFormedSpec()
	spec.Containers[0].AllowPrivilegeEscalation = Ptr(true)
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected error")
	}
	if !pe.HasViolation("allowPrivilegeEscalation must be false") {
		t.Errorf("priv-escalation should be rejected; got %v", pe.Violations)
	}
}

func TestValidateRejectsPrivilegedContainer(t *testing.T) {
	spec := wellFormedSpec()
	spec.Containers[1].Privileged = Ptr(true)
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected error")
	}
	if !pe.HasViolation("privileged is forbidden") {
		t.Errorf("privileged container should be rejected; got %v", pe.Violations)
	}
}

func TestValidateRejectsMutableRootFS(t *testing.T) {
	spec := wellFormedSpec()
	spec.Containers[1].ReadOnlyRootFilesystem = Ptr(false)
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected error")
	}
	if !pe.HasViolation("readOnlyRootFilesystem must be true") {
		t.Errorf("mutable root FS should be rejected; got %v", pe.Violations)
	}
}

func TestValidateRequiresDropAll(t *testing.T) {
	spec := wellFormedSpec()
	spec.Containers[0].CapabilitiesDrop = []string{"NET_ADMIN"} // not ALL
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected error")
	}
	if !pe.HasViolation("capabilities.drop must contain ALL") {
		t.Errorf("missing drop ALL should be rejected")
	}
}

func TestValidateRejectsAddedCapabilities(t *testing.T) {
	spec := wellFormedSpec()
	spec.Containers[0].CapabilitiesAdd = []string{"NET_BIND_SERVICE"}
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected error")
	}
	if !pe.HasViolation("capabilities.add must be empty") {
		t.Errorf("added cap should be rejected")
	}
}

func TestValidateRejectsMissingSeccompProfile(t *testing.T) {
	spec := wellFormedSpec()
	spec.SeccompProfileType = "" // no pod-level profile; containers set none either
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *PodSecurityError, got %v", err)
	}
	if !pe.HasViolation("seccompProfile.type must be RuntimeDefault") {
		t.Errorf("missing seccomp profile should be rejected; got %v", pe.Violations)
	}
}

func TestValidateRejectsUnconfinedSeccompProfile(t *testing.T) {
	spec := wellFormedSpec()
	spec.SeccompProfileType = "Unconfined"
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected error")
	}
	if !pe.HasViolation("seccompProfile.type must be RuntimeDefault, got Unconfined") {
		t.Errorf("Unconfined seccomp profile should be rejected; got %v", pe.Violations)
	}
}

func TestValidateRejectsContainerOverridingSeccompProfile(t *testing.T) {
	// The pod-level profile is RuntimeDefault, but one container
	// overrides it with Unconfined. The container-level value wins, so
	// the override must be rejected.
	spec := wellFormedSpec()
	spec.Containers[1].SeccompProfileType = "Unconfined"
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected error")
	}
	if !pe.HasViolation(`container "agent": seccompProfile.type must be RuntimeDefault`) {
		t.Errorf("container seccomp override should be rejected; got %v", pe.Violations)
	}
}

func TestValidateAcceptsContainerSeccompInheritedFromPod(t *testing.T) {
	// Containers set no seccomp profile of their own; they inherit the
	// pod-level RuntimeDefault. This is the shape the agent podspec
	// produces and must validate.
	spec := wellFormedSpec()
	for i := range spec.Containers {
		spec.Containers[i].SeccompProfileType = ""
	}
	if err := ValidateAgentPod(spec, lennyCredReadersGID); err != nil {
		t.Errorf("container inheriting pod-level RuntimeDefault should validate, got %v", err)
	}
}

func TestValidateAccumulatesMultipleViolations(t *testing.T) {
	spec := wellFormedSpec()
	spec.HostPID = true
	spec.HostNetwork = true
	spec.FSGroup = nil
	err := ValidateAgentPod(spec, lennyCredReadersGID)
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected error")
	}
	if len(pe.Violations) < 3 {
		t.Errorf("expected at least 3 violations, got %d: %v", len(pe.Violations), pe.Violations)
	}
}

func TestValidateRejectsEmptyContainerList(t *testing.T) {
	spec := wellFormedSpec()
	spec.Containers = nil
	if err := ValidateAgentPod(spec, lennyCredReadersGID); err == nil {
		t.Errorf("empty container list should be rejected")
	}
}
