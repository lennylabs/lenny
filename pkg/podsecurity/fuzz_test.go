// SPDX-License-Identifier: MIT

package podsecurity

import (
	"testing"
)

// fuzzProbeNames are the names the fuzzed probe container takes: a
// non-reserved name and the two names §13.1 (Container identity) binds
// the reserved UIDs to.
var fuzzProbeNames = []string{"injected", "adapter", "runtime"}

// FuzzValidateAgentPod exercises the §13.1 pod-spec admission
// validator on randomized inputs. Invariants: it never panics, no matter
// how malformed the SecurityContext is, and an admitted pod satisfies
// the §13.1 container identity clause. A panic here would let an
// adversarial pod admission request crash the gateway-side validator.
//
// The pod carries one probe container whose name, identity, and
// credential-set membership are fuzzed alongside the configured adapter
// and agent UIDs, on top of an otherwise compliant baseline so the
// identity clause decides admission when the other inputs are valid.
//
// The seed corpus mixes a valid baseline with the known violations:
// host-sharing flags, missing fsGroup, missing supplemental group,
// a reserved UID on the wrong container, and an unset identity.
//
// spec: 13.1 (Pod Security)
func FuzzValidateAgentPod(f *testing.F) {
	f.Add(false, false, false, false, true, int64(2000), int64(2000), int64(1000), int64(1000), int64(65532), int64(65533), uint8(0), false)
	f.Add(true, false, false, false, true, int64(2000), int64(2000), int64(1000), int64(1000), int64(65532), int64(65533), uint8(0), false)    // hostShare
	f.Add(false, true, false, false, true, int64(2000), int64(2000), int64(1000), int64(1000), int64(65532), int64(65533), uint8(0), false)    // hostPID
	f.Add(false, false, false, false, false, int64(0), int64(2001), int64(1000), int64(1000), int64(65532), int64(65533), uint8(0), false)     // missing fsGroup
	f.Add(false, false, false, false, true, int64(2000), int64(3000), int64(1000), int64(1000), int64(65532), int64(65533), uint8(0), false)   // wrong cred-readers GID
	f.Add(false, false, false, false, true, int64(2000), int64(2000), int64(65533), int64(1000), int64(65532), int64(65533), uint8(0), false)  // injected at agent UID
	f.Add(false, false, false, false, true, int64(2000), int64(2000), int64(65532), int64(65532), int64(65532), int64(65533), uint8(1), false) // init named adapter, not credential
	f.Add(false, false, false, false, true, int64(2000), int64(2000), int64(65533), int64(65533), int64(65532), int64(65533), uint8(2), true)  // runtime at agent UID
	f.Add(false, false, false, false, true, int64(2000), int64(2000), int64(0), int64(1000), int64(65532), int64(65533), uint8(0), false)      // zero runAsUser

	f.Fuzz(func(t *testing.T,
		shareProcessNamespace, hostPID, hostNetwork, hostIPC, includeCredReadersGID bool,
		fsGroup, credReadersGID, runAsUser, runAsGroup, adapterUID, agentUID int64,
		nameSel uint8, inCredentialSet bool,
	) {
		probe := ContainerSpec{
			Name:                     fuzzProbeNames[int(nameSel)%len(fuzzProbeNames)],
			AllowPrivilegeEscalation: Ptr(false),
			Privileged:               Ptr(false),
			ReadOnlyRootFilesystem:   Ptr(true),
			CapabilitiesDrop:         []string{"ALL"},
		}
		// Zero stands in for an unset field so the nil branch is reached.
		if runAsUser != 0 {
			probe.RunAsUser = Ptr(runAsUser)
		}
		if runAsGroup != 0 {
			probe.RunAsGroup = Ptr(runAsGroup)
		}
		spec := PodSpec{
			ShareProcessNamespace: shareProcessNamespace,
			HostPID:               hostPID,
			HostNetwork:           hostNetwork,
			HostIPC:               hostIPC,
			RunAsNonRoot:          Ptr(true),
			SeccompProfileType:    SeccompRuntimeDefault,
			AdapterUID:            adapterUID,
			AgentUID:              agentUID,
			Containers:            []ContainerSpec{probe},
		}
		if inCredentialSet {
			spec.CredentialContainerNames = []string{probe.Name}
		}
		if fsGroup != 0 {
			spec.FSGroup = Ptr(fsGroup)
		}
		if includeCredReadersGID {
			spec.SupplementalGroups = []int64{credReadersGID}
		}
		if ValidateAgentPod(spec, credReadersGID, RuntimeClassPolicy{}) != nil {
			return
		}
		assertAdmittedIdentity(t, spec)
	})
}

// assertAdmittedIdentity fails t when an admitted spec breaks the §13.1
// container identity clause: an unset or zero identity, or a reserved
// UID on a container other than the credential container that owns it.
func assertAdmittedIdentity(t *testing.T, spec PodSpec) {
	t.Helper()
	credential := map[string]bool{}
	for _, n := range spec.CredentialContainerNames {
		credential[n] = true
	}
	for _, c := range spec.Containers {
		if c.RunAsUser == nil || *c.RunAsUser == 0 || c.RunAsGroup == nil || *c.RunAsGroup == 0 {
			t.Fatalf("admitted container %q with an unset or zero identity", c.Name)
		}
		uid := *c.RunAsUser
		if uid == spec.AdapterUID && (c.Name != "adapter" || !credential[c.Name]) {
			t.Fatalf("admitted container %q at the adapter UID %d", c.Name, uid)
		}
		if uid == spec.AgentUID && (c.Name != "runtime" || !credential[c.Name]) {
			t.Fatalf("admitted container %q at the agent UID %d", c.Name, uid)
		}
	}
}
