// SPDX-License-Identifier: MIT

package podsecurity

import (
	"errors"
	"testing"
)

const lennyCredReadersGID int64 = 65532

// Reserved UIDs the fixtures pass as PodSpec.AdapterUID and
// PodSpec.AgentUID: the chart defaults for security.podUIDs.adapter and
// security.podUIDs.agent. The fixture containers run at UIDs outside
// both, so the existing cases keep testing the controls they target.
const (
	testAdapterUID int64 = 65532
	testAgentUID   int64 = 65533
)

func wellFormedSpec() PodSpec {
	return PodSpec{
		FSGroup:            Ptr[int64](lennyCredReadersGID),
		SupplementalGroups: []int64{lennyCredReadersGID},
		RunAsNonRoot:       Ptr(true),
		SeccompProfileType: SeccompRuntimeDefault,
		AdapterUID:         testAdapterUID,
		AgentUID:           testAgentUID,
		Containers: []ContainerSpec{
			{
				Name:                     "adapter",
				AllowPrivilegeEscalation: Ptr(false),
				Privileged:               Ptr(false),
				ReadOnlyRootFilesystem:   Ptr(true),
				CapabilitiesDrop:         []string{"ALL"},
				RunAsUser:                Ptr[int64](1001),
				RunAsGroup:               Ptr[int64](1001),
			},
			{
				Name:                     "agent",
				AllowPrivilegeEscalation: Ptr(false),
				Privileged:               Ptr(false),
				ReadOnlyRootFilesystem:   Ptr(true),
				CapabilitiesDrop:         []string{"ALL"},
				RunAsUser:                Ptr[int64](1002),
				RunAsGroup:               Ptr[int64](1002),
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
		RunAsUser:                Ptr[int64](1003),
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
// §13.1: the pod-level supplementalGroups must declare the
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
// asserts §13.1: a non-adapter, non-agent container that mounts
// the credential volume by name reaches every session's credential file
// under /run/lenny/slots/ and is rejected with
// POD_SPEC_CRED_GROUP_OVERBROAD. This closes the
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
		RunAsUser:                Ptr[int64](1004),
		RunAsGroup:               Ptr[int64](1004),
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
// §13.1: a non-credential container that mounts a path under
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
		RunAsUser:                Ptr[int64](1005),
		RunAsGroup:               Ptr[int64](1005),
		VolumeMounts:             []VolumeMount{{Name: "shadow", MountPath: "/run/lenny/sub"}},
	})
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
	var pe *PodSecurityError
	if !errors.As(err, &pe) || !pe.HasViolation("POD_SPEC_CRED_GROUP_OVERBROAD") {
		t.Fatalf("expected POD_SPEC_CRED_GROUP_OVERBROAD, got %v", err)
	}
}

// TestValidateAcceptsSiblingPathMount_spec_13_1 asserts the egress-capture
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
		RunAsUser:                Ptr[int64](1006),
		RunAsGroup:               Ptr[int64](1006),
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

// spec: §5.2 — concurrent-workspace slots share a network
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

// The §5.2 NET_RAW drop holds for a standard agent pod: the
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

// identityContainer returns a container that satisfies every §13.1
// per-container baseline control, with the given container-level
// runAsUser and runAsGroup (nil leaves the field unset).
func identityContainer(name string, uid, gid *int64) ContainerSpec {
	return ContainerSpec{
		Name:                     name,
		AllowPrivilegeEscalation: Ptr(false),
		Privileged:               Ptr(false),
		ReadOnlyRootFilesystem:   Ptr(true),
		CapabilitiesDrop:         []string{"ALL"},
		RunAsUser:                uid,
		RunAsGroup:               gid,
	}
}

// sidecarIdentitySpec returns a sidecar-model agent pod: an adapter
// container at the adapter UID and a runtime container at the agent UID,
// each with its primary GID equal to its UID, and both in the credential
// set, as the pod builder renders it.
func sidecarIdentitySpec() PodSpec {
	spec := wellFormedSpec()
	spec.CredentialContainerNames = []string{"adapter", "runtime"}
	spec.Containers = []ContainerSpec{
		identityContainer("adapter", Ptr(testAdapterUID), Ptr(testAdapterUID)),
		identityContainer("runtime", Ptr(testAgentUID), Ptr(testAgentUID)),
	}
	return spec
}

// embeddedIdentitySpec returns an embedded-model agent pod: only the
// runtime container, which is the only credential container.
func embeddedIdentitySpec() PodSpec {
	spec := wellFormedSpec()
	spec.CredentialContainerNames = []string{"runtime"}
	spec.Containers = []ContainerSpec{
		identityContainer("runtime", Ptr(testAgentUID), Ptr(testAgentUID)),
	}
	return spec
}

// identityMsgUnset and identityMsgReserved are the stable substrings of
// the two §13.1 (Container identity) violations: an unset or zero
// identity, and a container running at a UID reserved for another.
const (
	identityMsgUnset    = "must set non-zero runAsUser and runAsGroup (§13.1 Container identity)"
	identityMsgReserved = "is reserved for the"
)

// identityCase is one §13.1 (Container identity) table case: a pod and
// the violation substring it must produce, or "" when it is admitted.
type identityCase struct {
	name string
	spec func() PodSpec
	want string
}

// withContainer returns base with c appended to its container list.
// Init, regular, and ephemeral containers are flattened into one list by
// the webhook, so an init container is modelled as an appended container
// that is not in the credential set.
func withContainer(base PodSpec, c ContainerSpec) PodSpec {
	base.Containers = append(base.Containers, c)
	return base
}

func identityCases() []identityCase {
	const free int64 = 1000
	return []identityCase{
		// An unset or zero identity.
		{"runAsUser absent", func() PodSpec {
			return withContainer(sidecarIdentitySpec(), identityContainer("injected", nil, Ptr(free)))
		}, `container "injected" ` + identityMsgUnset},
		{"runAsGroup absent", func() PodSpec {
			return withContainer(sidecarIdentitySpec(), identityContainer("injected", Ptr(free), nil))
		}, `container "injected" ` + identityMsgUnset},
		{"runAsUser zero", func() PodSpec {
			return withContainer(sidecarIdentitySpec(), identityContainer("injected", Ptr[int64](0), Ptr(free)))
		}, `container "injected" ` + identityMsgUnset},
		{"runAsGroup zero", func() PodSpec {
			return withContainer(sidecarIdentitySpec(), identityContainer("injected", Ptr(free), Ptr[int64](0)))
		}, `container "injected" ` + identityMsgUnset},
		{"pod-level runAsUser only", func() PodSpec {
			// A pod-level runAsUser is never translated into the
			// container's RunAsUser, so the container reads as unset.
			spec := sidecarIdentitySpec()
			spec.Containers[1].RunAsUser = nil
			return spec
		}, `container "runtime" ` + identityMsgUnset},
		// A reserved UID on a container that does not own it.
		{"injected regular container at the agent UID", func() PodSpec {
			return withContainer(sidecarIdentitySpec(), identityContainer("injected", Ptr(testAgentUID), Ptr(free)))
		}, `container "injected" runAsUser 65533 is reserved for the "runtime" container (§13.1 Container identity)`},
		{"injected regular container at the adapter UID", func() PodSpec {
			return withContainer(sidecarIdentitySpec(), identityContainer("injected", Ptr(testAdapterUID), Ptr(free)))
		}, `container "injected" runAsUser 65532 is reserved for the "adapter" container (§13.1 Container identity)`},
		{"init container at the adapter UID", func() PodSpec {
			return withContainer(sidecarIdentitySpec(), identityContainer("init-setup", Ptr(testAdapterUID), Ptr(free)))
		}, `container "init-setup" runAsUser 65532 is reserved for the "adapter" container`},
		{"native sidecar init container at the agent UID", func() PodSpec {
			// An init container with restartPolicy: Always carries no
			// exemption; restartPolicy is not part of the projection.
			return withContainer(sidecarIdentitySpec(), identityContainer("mesh-proxy", Ptr(testAgentUID), Ptr(free)))
		}, `container "mesh-proxy" runAsUser 65533 is reserved for the "runtime" container`},
		{"embedded pod init container named adapter at the adapter UID", func() PodSpec {
			// The name matches the reservation owner but the container is
			// not in the credential set (only runtime is, in the embedded
			// model), so a name-only check would wrongly admit it.
			return withContainer(embeddedIdentitySpec(), identityContainer("adapter", Ptr(testAdapterUID), Ptr(testAdapterUID)))
		}, `container "adapter" runAsUser 65532 is reserved for the "adapter" container`},
		// Admitted.
		{"adapter runAsGroup equal to the cred-readers GID", func() PodSpec {
			spec := sidecarIdentitySpec()
			spec.Containers[0].RunAsGroup = Ptr(lennyCredReadersGID)
			return spec
		}, ""},
		{"sidecar pod at the default UIDs with GID equal to UID", sidecarIdentitySpec, ""},
		{"embedded pod with runtime only", embeddedIdentitySpec, ""},
		{"non-reserved injected sidecar with explicit identity", func() PodSpec {
			return withContainer(sidecarIdentitySpec(), identityContainer("injected", Ptr(free), Ptr(free)))
		}, ""},
	}
}

// TestValidateContainerIdentity_spec_13_1 pins the §13.1 container
// identity clause: every container sets a nonzero container-level
// runAsUser and runAsGroup, and a reserved UID belongs only to the
// regular container of the matching name.
//
// spec: 13.1 (Pod Security)
func TestValidateContainerIdentity_spec_13_1(t *testing.T) {
	for _, tc := range identityCases() {
		t.Run(tc.name, func(t *testing.T) {
			assertIdentityOutcome(t, tc.spec(), RuntimeClassPolicy{}, tc.want)
		})
	}
}

// assertIdentityOutcome validates spec and checks the outcome: no error
// when want is empty, otherwise a *PodSecurityError carrying want.
func assertIdentityOutcome(t *testing.T, spec PodSpec, rc RuntimeClassPolicy, want string) {
	t.Helper()
	err := ValidateAgentPod(spec, lennyCredReadersGID, rc)
	if want == "" {
		if err != nil {
			t.Fatalf("expected admission, got %v", err)
		}
		return
	}
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected a *PodSecurityError carrying %q, got %v", want, err)
	}
	if !pe.HasViolation(want) {
		t.Errorf("expected violation %q, got %v", want, pe.Violations)
	}
}

// TestValidateContainerIdentityOverriddenUIDs_spec_13_1 pins the
// reservation to the configured UIDs rather than the chart defaults: with
// security.podUIDs overridden, a container at the old default UIDs is
// admitted and one at the new values is rejected.
//
// spec: 13.1 (Pod Security)
func TestValidateContainerIdentityOverriddenUIDs_spec_13_1(t *testing.T) {
	const adapterUID, agentUID int64 = 70000, 70001
	base := func() PodSpec {
		spec := wellFormedSpec()
		spec.AdapterUID, spec.AgentUID = adapterUID, agentUID
		spec.CredentialContainerNames = []string{"adapter", "runtime"}
		spec.Containers = []ContainerSpec{
			identityContainer("adapter", Ptr(adapterUID), Ptr(adapterUID)),
			identityContainer("runtime", Ptr(agentUID), Ptr(agentUID)),
		}
		return spec
	}
	cases := []struct {
		name string
		uid  int64
		want string
	}{
		{"old default adapter UID admitted", testAdapterUID, ""},
		{"old default agent UID admitted", testAgentUID, ""},
		{"overridden adapter UID rejected", adapterUID, `runAsUser 70000 is reserved for the "adapter" container`},
		{"overridden agent UID rejected", agentUID, `runAsUser 70001 is reserved for the "runtime" container`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := withContainer(base(), identityContainer("injected", Ptr(tc.uid), Ptr[int64](1000)))
			assertIdentityOutcome(t, spec, RuntimeClassPolicy{}, tc.want)
		})
	}
}

// TestValidateContainerIdentityViolationsPerContainer_spec_13_1 pins
// that a container breaching both halves of the clause reports both
// violations, so an operator sees every cause in one rejection.
//
// spec: 13.1 (Pod Security)
func TestValidateContainerIdentityViolationsPerContainer_spec_13_1(t *testing.T) {
	spec := withContainer(sidecarIdentitySpec(), identityContainer("injected", Ptr(testAgentUID), nil))
	err := ValidateAgentPod(spec, lennyCredReadersGID, RuntimeClassPolicy{})
	var pe *PodSecurityError
	if !errors.As(err, &pe) {
		t.Fatalf("expected a *PodSecurityError, got %v", err)
	}
	if len(pe.Violations) != 2 || !pe.HasViolation(identityMsgUnset) || !pe.HasViolation(identityMsgReserved) {
		t.Errorf("expected exactly the unset and reserved violations, got %v", pe.Violations)
	}
}
