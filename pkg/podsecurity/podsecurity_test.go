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
		SupplementalGroups: []int64{lennyCredReadersGID},
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
	if err := ValidateAgentPod(wellFormedSpec(), lennyCredReadersGID, RuntimeClassPolicy{}); err != nil {
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
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	if err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{}); err != nil {
		t.Errorf("the adapter container may carry the cred-readers GID, got %v", err)
	}
}

// TestValidateRejectsMissingCredSupplementalGroups_spec_13_1 asserts
// §13.1 line 25: the pod-level supplementalGroups must declare the
// lenny-cred-readers GID. A pod that sets the fsGroup but omits the
// explicit supplementalGroups declaration is rejected (F-13.1.11 /
// F-13.1.15).
func TestValidateRejectsMissingCredSupplementalGroups_spec_13_1(t *testing.T) {
	spec := wellFormedSpec()
	spec.SupplementalGroups = nil
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected a *PodSecurityError, got %v", err)
	}
	if !pe.HasViolation("supplementalGroups must include") {
		t.Errorf("expected a supplementalGroups-missing violation, got %v", pe.Violations)
	}
	if !pe.HasViolation("POD_SPEC_CRED_FSGROUP_MISSING") {
		t.Errorf("expected POD_SPEC_CRED_FSGROUP_MISSING, got %v", pe.Violations)
	}
}

// TestValidateRejectsWrongCredSupplementalGroups_spec_13_1 asserts the
// presence check requires the exact lenny-cred-readers GID: a pod that
// declares some other supplementary group but not the cred-readers GID
// is still rejected.
func TestValidateRejectsWrongCredSupplementalGroups_spec_13_1(t *testing.T) {
	spec := wellFormedSpec()
	spec.SupplementalGroups = []int64{12345}
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
	var pe *PodSecurityError
	if !errors.As(err, &pe) || !pe.HasViolation("supplementalGroups must include") {
		t.Fatalf("expected a supplementalGroups-missing violation, got %v", err)
	}
}

// TestValidateRejectsNonCredContainerMountingCredVolume_spec_13_1
// asserts §13.1 line 27: a non-adapter, non-agent container that mounts
// the credential volume by name reaches /run/lenny/credentials.json and
// is rejected with POD_SPEC_CRED_GROUP_OVERBROAD. This closes the
// fsGroup-inheritance side-channel the per-container runAsGroup check
// alone cannot (F-13.1.10).
func TestValidateRejectsNonCredContainerMountingCredVolume_spec_13_1(t *testing.T) {
	spec := wellFormedSpec()
	spec.CredentialContainerNames = []string{"adapter", "agent"}
	spec.CredVolumeName = "credentials"
	spec.Containers = append(spec.Containers, ContainerSpec{
		Name:                     "sidecar",
		AllowPrivilegeEscalation: Ptr(false),
		Privileged:               Ptr(false),
		ReadOnlyRootFilesystem:   Ptr(true),
		CapabilitiesDrop:         []string{"ALL"},
		VolumeMounts:             []VolumeMount{{Name: "credentials", MountPath: "/somewhere"}},
	})
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected a *PodSecurityError, got %v", err)
	}
	if !pe.HasViolation("POD_SPEC_CRED_GROUP_OVERBROAD") {
		t.Errorf("expected POD_SPEC_CRED_GROUP_OVERBROAD, got %v", pe.Violations)
	}
}

// TestValidateRejectsNonCredContainerMountingCredPath_spec_13_1 asserts
// §13.1 line 27: a non-credential container that mounts a path under
// /run/lenny — even via a differently-named volume — is rejected
// (F-13.1.10, the equivalent of cred-guard condition (iv) by-path
// match).
func TestValidateRejectsNonCredContainerMountingCredPath_spec_13_1(t *testing.T) {
	spec := wellFormedSpec()
	spec.CredentialContainerNames = []string{"adapter", "agent"}
	spec.CredVolumeName = "credentials"
	spec.Containers = append(spec.Containers, ContainerSpec{
		Name:                     "sidecar",
		AllowPrivilegeEscalation: Ptr(false),
		Privileged:               Ptr(false),
		ReadOnlyRootFilesystem:   Ptr(true),
		CapabilitiesDrop:         []string{"ALL"},
		VolumeMounts:             []VolumeMount{{Name: "shadow", MountPath: "/run/lenny/sub"}},
	})
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
	var pe *PodSecurityError
	if !errors.As(err, &pe) || !pe.HasViolation("POD_SPEC_CRED_GROUP_OVERBROAD") {
		t.Fatalf("expected POD_SPEC_CRED_GROUP_OVERBROAD, got %v", err)
	}
}

// TestValidateAcceptsSiblingPathMount_spec_13_1 asserts the §12.9.8
// egress-capture sidecar case: a non-credential container that mounts
// the sibling path /run/lenny-capture (which shares the textual prefix
// /run/lenny but is not nested under /run/lenny/) is allowed. The
// by-path match must not false-positive on sibling directories
// (F-13.1.10, cross-checks F-6.4.16).
func TestValidateAcceptsSiblingPathMount_spec_13_1(t *testing.T) {
	spec := wellFormedSpec()
	spec.CredentialContainerNames = []string{"adapter", "agent"}
	spec.CredVolumeName = "credentials"
	spec.Containers = append(spec.Containers, ContainerSpec{
		Name:                     "egress-capture",
		AllowPrivilegeEscalation: Ptr(false),
		Privileged:               Ptr(false),
		ReadOnlyRootFilesystem:   Ptr(true),
		CapabilitiesDrop:         []string{"ALL"},
		VolumeMounts:             []VolumeMount{{Name: "egress-capture", MountPath: "/run/lenny-capture"}},
	})
	if err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{}); err != nil {
		t.Errorf("a sibling /run/lenny-capture mount must be allowed, got %v", err)
	}
}

// TestValidateAcceptsCredContainerMountingCredVolume_spec_13_1 asserts
// the adapter and agent containers MAY mount the credential volume:
// they are the §13.1 credential containers and the delivery path
// depends on the mount (F-13.1.10).
func TestValidateAcceptsCredContainerMountingCredVolume_spec_13_1(t *testing.T) {
	spec := wellFormedSpec()
	spec.CredentialContainerNames = []string{"adapter", "agent"}
	spec.CredVolumeName = "credentials"
	spec.Containers[0].VolumeMounts = []VolumeMount{{Name: "credentials", MountPath: "/run/lenny"}}
	spec.Containers[1].VolumeMounts = []VolumeMount{{Name: "credentials", MountPath: "/run/lenny"}}
	if err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{}); err != nil {
		t.Errorf("the adapter and agent containers may mount the credential volume, got %v", err)
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
			err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected error")
	}
	if !pe.HasViolation("capabilities.add must be empty") {
		t.Errorf("added cap should be rejected")
	}
}

// spec: §5.2 line 496 — concurrent-workspace slots share a network
// namespace, so the agent container's securityContext MUST drop
// CAP_NET_RAW to prevent one slot sniffing sibling traffic with a raw
// socket. The §13.1 baseline enforces this two ways: every container
// must drop ALL (which subsumes NET_RAW) and must add no capability. A
// pod that re-grants NET_RAW via capabilities.add is rejected even
// though it also drops ALL, because an explicit add overrides the drop.
func TestValidateRejectsNetRawAdd_spec_5_2_496(t *testing.T) {
	spec := wellFormedSpec()
	// Drop ALL is present (NET_RAW dropped) but the container re-adds it.
	spec.Containers[1].CapabilitiesAdd = []string{"NET_RAW"}
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("a container adding CAP_NET_RAW must be rejected, got %v", err)
	}
	if !pe.HasViolation("capabilities.add must be empty") {
		t.Errorf("CAP_NET_RAW add should be rejected; got %v", pe.Violations)
	}
}

// The §5.2 line 496 NET_RAW drop holds for a standard agent pod: the
// well-formed spec drops ALL and adds nothing, so it validates. This
// pins the positive case so the rejection test above cannot pass
// vacuously.
func TestValidateAcceptsNetRawDropped_spec_5_2_496(t *testing.T) {
	if err := ValidateAgentPod(wellFormedSpec(), lennyCredReadersGID, RuntimeClassPolicy{}); err != nil {
		t.Errorf("a pod that drops ALL (NET_RAW included) and adds nothing must validate, got %v", err)
	}
}

func TestValidateRejectsMissingSeccompProfile(t *testing.T) {
	spec := wellFormedSpec()
	spec.SeccompProfileType = "" // no pod-level profile; containers set none either
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	if err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{}); err != nil {
		t.Errorf("container inheriting pod-level RuntimeDefault should validate, got %v", err)
	}
}

func TestValidateAccumulatesMultipleViolations(t *testing.T) {
	spec := wellFormedSpec()
	spec.HostPID = true
	spec.HostNetwork = true
	spec.FSGroup = nil
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
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
	if err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{}); err == nil {
		t.Errorf("empty container list should be rejected")
	}
}

// spec: §13.1 ("Capabilities | All dropped") — CapabilitiesDropped is the
// exported primitive the §13.1 Capabilities row check reduces to
// (exercised indirectly above via TestValidateRequiresDropAll and
// TestValidateRejectsAddedCapabilities); this table pins its own
// input/output contract directly so a caller outside this package, such
// as a cluster-assertion test reading a live pod's
// securityContext.capabilities.drop off the Kubernetes API, has a
// pinned, independently verified rule to call instead of re-deriving
// the "ALL present" check inline.
func TestCapabilitiesDropped_spec_13_1(t *testing.T) {
	cases := []struct {
		name  string
		drops []string
		want  bool
	}{
		{"exactly ALL", []string{"ALL"}, true},
		{"ALL alongside a redundant entry", []string{"ALL", "NET_RAW"}, true},
		{"nil list", nil, false},
		{"empty list", []string{}, false},
		{"single non-ALL entry", []string{"NET_RAW"}, false},
		{"several non-ALL entries", []string{"NET_RAW", "SYS_ADMIN"}, false},
		{"case-sensitive: lowercase all does not count", []string{"all"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CapabilitiesDropped(tc.drops); got != tc.want {
				t.Errorf("CapabilitiesDropped(%v) = %v, want %v", tc.drops, got, tc.want)
			}
		})
	}
}
