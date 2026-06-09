// SPDX-License-Identifier: MIT

package podspec_test

import (
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

// runAsUser returns the resolved RunAsUser of a named container, failing
// the test when the container or its securityContext is absent.
func runAsUser(t *testing.T, pod *corev1.Pod, name string) int64 {
	t.Helper()
	c := container(t, pod, name)
	if c.SecurityContext == nil || c.SecurityContext.RunAsUser == nil {
		t.Fatalf("%s container has no runAsUser", name)
	}
	return *c.SecurityContext.RunAsUser
}

func supplementalGroupsContain(groups []int64, want int64) bool {
	for _, g := range groups {
		if g == want {
			return true
		}
	}
	return false
}

// TestBuildAppliesDefaultUIDs_spec_13_1_7 pins the §13.1 line 7
// implementation-default non-root identities the controller stamps when
// the deployer supplies no override: adapter 65532, runtime 65533, and
// the lenny-cred-readers fsGroup 65534 (plus the matching --runtime-uid
// peer-check arg). F-13.1.16.
func TestBuildAppliesDefaultUIDs_spec_13_1_7(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := runAsUser(t, pod, "adapter"); got != podspec.AdapterUID {
		t.Errorf("adapter runAsUser = %d, want default %d", got, podspec.AdapterUID)
	}
	if got := runAsUser(t, pod, "runtime"); got != podspec.AgentUID {
		t.Errorf("runtime runAsUser = %d, want default %d", got, podspec.AgentUID)
	}
	sc := pod.Spec.SecurityContext
	if sc == nil || sc.FSGroup == nil || *sc.FSGroup != podspec.CredReadersGID {
		t.Errorf("pod fsGroup = %v, want default %d", sc.FSGroup, podspec.CredReadersGID)
	}
	if !supplementalGroupsContain(sc.SupplementalGroups, podspec.CredReadersGID) {
		t.Errorf("pod supplementalGroups = %v, want it to include %d", sc.SupplementalGroups, podspec.CredReadersGID)
	}
	if !hasArg(container(t, pod, "adapter").Args, fmt.Sprintf("--runtime-uid=%d", podspec.AgentUID)) {
		t.Errorf("adapter args = %v, want --runtime-uid=%d", container(t, pod, "adapter").Args, podspec.AgentUID)
	}
}

// TestBuildAppliesOverriddenUIDs_spec_13_1_16 asserts that a deployer
// override (the chart's security.podUIDs values, threaded through Inputs)
// reaches every UID-bearing site of the sidecar-model pod: the adapter
// and runtime container runAsUser, the adapter's --runtime-uid peer
// check, the pod fsGroup, and the supplementalGroups list. The override
// must replace the constant everywhere or the §4.7 cross-UID credential
// read boundary breaks. F-13.1.16.
func TestBuildAppliesOverriddenUIDs_spec_13_1_16(t *testing.T) {
	const (
		adapter = int64(70000)
		agent   = int64(70001)
		gid     = int64(70002)
	)
	in := inputs()
	in.AdapterUID = adapter
	in.AgentUID = agent
	in.CredReadersGID = gid

	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := runAsUser(t, pod, "adapter"); got != adapter {
		t.Errorf("adapter runAsUser = %d, want override %d", got, adapter)
	}
	if got := runAsUser(t, pod, "runtime"); got != agent {
		t.Errorf("runtime runAsUser = %d, want override %d", got, agent)
	}
	if !hasArg(container(t, pod, "adapter").Args, fmt.Sprintf("--runtime-uid=%d", agent)) {
		t.Errorf("adapter args = %v, want --runtime-uid=%d", container(t, pod, "adapter").Args, agent)
	}
	sc := pod.Spec.SecurityContext
	if sc == nil || sc.FSGroup == nil || *sc.FSGroup != gid {
		t.Errorf("pod fsGroup = %v, want override %d", sc.FSGroup, gid)
	}
	if !supplementalGroupsContain(sc.SupplementalGroups, gid) {
		t.Errorf("pod supplementalGroups = %v, want it to include override %d", sc.SupplementalGroups, gid)
	}
	// The default constant must no longer appear anywhere a UID is stamped.
	if supplementalGroupsContain(sc.SupplementalGroups, podspec.CredReadersGID) {
		t.Errorf("pod supplementalGroups = %v, must not retain the default GID once overridden", sc.SupplementalGroups)
	}
}

// TestBuildEmbeddedAppliesOverriddenUIDs_spec_13_1_16 covers the
// embedded-model branch (a single runtime container, no adapter sidecar):
// the override must reach the runtime container runAsUser and the pod
// fsGroup there too. F-13.1.16.
func TestBuildEmbeddedAppliesOverriddenUIDs_spec_13_1_16(t *testing.T) {
	const (
		agent = int64(70001)
		gid   = int64(70002)
	)
	in := inputs()
	in.DeploymentModel = string(podspec.DeploymentEmbedded)
	in.AgentUID = agent
	in.CredReadersGID = gid

	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := runAsUser(t, pod, "runtime"); got != agent {
		t.Errorf("embedded runtime runAsUser = %d, want override %d", got, agent)
	}
	sc := pod.Spec.SecurityContext
	if sc == nil || sc.FSGroup == nil || *sc.FSGroup != gid {
		t.Errorf("embedded pod fsGroup = %v, want override %d", sc.FSGroup, gid)
	}
}

// TestInputsZeroUIDFallsBackToDefault_spec_13_1_16 asserts a partial
// override (only the GID set) leaves the two unset UIDs at their package
// defaults, so a zero field is a clean "unset" sentinel rather than a
// literal UID 0 (root, which §13.1 forbids). F-13.1.16.
func TestInputsZeroUIDFallsBackToDefault_spec_13_1_16(t *testing.T) {
	in := inputs()
	in.CredReadersGID = 70002 // only the GID overridden

	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := runAsUser(t, pod, "adapter"); got != podspec.AdapterUID {
		t.Errorf("adapter runAsUser = %d, want default %d (unset override)", got, podspec.AdapterUID)
	}
	if got := runAsUser(t, pod, "runtime"); got != podspec.AgentUID {
		t.Errorf("runtime runAsUser = %d, want default %d (unset override)", got, podspec.AgentUID)
	}
	if sc := pod.Spec.SecurityContext; sc == nil || sc.FSGroup == nil || *sc.FSGroup != 70002 {
		t.Errorf("pod fsGroup = %v, want overridden 70002", sc.FSGroup)
	}
}
