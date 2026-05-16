// SPDX-License-Identifier: MIT

package ephemeral_container_cred_guard_test

import (
	"strings"
	"testing"

	guard "github.com/lennylabs/lenny/pkg/admission/ephemeral_container_cred_guard"
)

const (
	adapterUID = 65532
	agentUID   = 65533
	credGID    = 65534
	credVol    = "lenny-credentials"
)

func ptr(v int64) *int64 { return &v }

// safeContainer is an ephemeral container that breaches none of the
// four §13.1 conditions: every securityContext field is set, none to a
// credential-reading identity, and it mounts nothing.
func safeContainer() guard.Container {
	return guard.Container{
		Name:               "debugger",
		RunAsUser:          ptr(99999),
		RunAsGroup:         ptr(99999),
		SupplementalGroups: []int64{},
		VolumeMounts:       nil,
	}
}

func decide(c guard.Container) guard.Decision {
	return guard.Decide(guard.Request{
		NewContainers:  []guard.Container{c},
		AdapterUID:     adapterUID,
		AgentUID:       agentUID,
		CredGID:        credGID,
		CredVolumeName: credVol,
	})
}

func assertRejected(t *testing.T, d guard.Decision, what string) {
	t.Helper()
	if d.Allowed {
		t.Errorf("%s was allowed, want rejection", what)
	}
	if d.Code != 403 {
		t.Errorf("%s rejection code = %d, want 403", what, d.Code)
	}
}

func TestDecideAllowsASafeContainer(t *testing.T) {
	d := decide(safeContainer())
	if !d.Allowed {
		t.Errorf("a safe ephemeral container was rejected: %s", d.Reason)
	}
	if d.Code != 200 {
		t.Errorf("allow code = %d, want 200", d.Code)
	}
}

func TestDecideAllowsAnEmptyRequest(t *testing.T) {
	d := guard.Decide(guard.Request{AdapterUID: adapterUID, AgentUID: agentUID, CredGID: credGID})
	if !d.Allowed {
		t.Error("a request adding no ephemeral containers was rejected")
	}
}

func TestDecideRejectsAdapterUID(t *testing.T) {
	c := safeContainer()
	c.RunAsUser = ptr(adapterUID)
	assertRejected(t, decide(c), "runAsUser = adapter UID")
}

func TestDecideRejectsAgentUID(t *testing.T) {
	c := safeContainer()
	c.RunAsUser = ptr(agentUID)
	assertRejected(t, decide(c), "runAsUser = agent UID")
}

func TestDecideRejectsCredGIDAsRunAsGroup(t *testing.T) {
	c := safeContainer()
	c.RunAsGroup = ptr(credGID)
	assertRejected(t, decide(c), "runAsGroup = lenny-cred-readers GID")
}

func TestDecideRejectsCredGIDInSupplementalGroups(t *testing.T) {
	c := safeContainer()
	c.SupplementalGroups = []int64{1000, credGID}
	assertRejected(t, decide(c), "supplementalGroups includes the cred GID")
}

func TestDecideRejectsAbsentRunAsUser(t *testing.T) {
	c := safeContainer()
	c.RunAsUser = nil
	assertRejected(t, decide(c), "absent runAsUser")
}

func TestDecideRejectsAbsentRunAsGroup(t *testing.T) {
	c := safeContainer()
	c.RunAsGroup = nil
	assertRejected(t, decide(c), "absent runAsGroup")
}

func TestDecideRejectsAbsentSupplementalGroups(t *testing.T) {
	c := safeContainer()
	c.SupplementalGroups = nil
	assertRejected(t, decide(c), "absent supplementalGroups")
}

func TestDecideRejectsCredVolumeMountByName(t *testing.T) {
	c := safeContainer()
	c.VolumeMounts = []guard.VolumeMount{{Name: credVol, MountPath: "/data"}}
	assertRejected(t, decide(c), "mount of the credential volume by name")
}

func TestDecideRejectsMountAtCredPath(t *testing.T) {
	c := safeContainer()
	c.VolumeMounts = []guard.VolumeMount{{Name: "scratch", MountPath: "/run/lenny"}}
	assertRejected(t, decide(c), "mount at the credential path")
}

func TestDecideRejectsMountUnderCredPath(t *testing.T) {
	c := safeContainer()
	c.VolumeMounts = []guard.VolumeMount{{Name: "scratch", MountPath: "/run/lenny/sub"}}
	assertRejected(t, decide(c), "mount under the credential path")
}

func TestDecideAllowsMountWithCredPathAsNamePrefix(t *testing.T) {
	// /run/lennyfoo is not the credential path and is not under it.
	c := safeContainer()
	c.VolumeMounts = []guard.VolumeMount{{Name: "scratch", MountPath: "/run/lennyfoo"}}
	if d := decide(c); !d.Allowed {
		t.Errorf("a mount at /run/lennyfoo was rejected: %s", d.Reason)
	}
}

func TestDecideRejectsWhenOneOfManyContainersViolates(t *testing.T) {
	bad := safeContainer()
	bad.Name = "attacker"
	bad.RunAsUser = ptr(agentUID)
	d := guard.Decide(guard.Request{
		NewContainers:  []guard.Container{safeContainer(), bad},
		AdapterUID:     adapterUID,
		AgentUID:       agentUID,
		CredGID:        credGID,
		CredVolumeName: credVol,
	})
	assertRejected(t, d, "a request whose second container violates")
}

func TestDecideRejectionCarriesTheSpecCode(t *testing.T) {
	c := safeContainer()
	c.RunAsUser = ptr(agentUID)
	d := decide(c)
	if !strings.Contains(d.Reason, guard.RejectionCode) {
		t.Errorf("rejection reason %q does not carry the §15.1 code %s", d.Reason, guard.RejectionCode)
	}
}
