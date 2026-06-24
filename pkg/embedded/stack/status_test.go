// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
)

// containerNamedLauncher is a k3s.Launcher that exposes a ContainerName, like
// the Docker-backed launcher. k3sContainerHandle reads the handle through the
// optional-method type assertion, so this stub stands in for the real
// Docker-backed launcher without provisioning a container.
type containerNamedLauncher struct {
	k3s.Launcher
	name string
}

func (l containerNamedLauncher) ContainerName() string { return l.name }

// plainLauncher is a k3s.Launcher with no ContainerName, like the Linux
// managed-child-process launcher. k3sContainerHandle returns "" for it.
type plainLauncher struct{ k3s.Launcher }

// TestK3sContainerHandle covers the substrate-handle extraction Up records in
// State: a Docker-backed launcher (exposes ContainerName) yields its container
// name, and a Linux managed-child-process launcher (no ContainerName) yields
// the empty string, so State.K3sContainer is set only on the Docker path.
//
// spec: §24.19 (a container-backed launcher records a container handle where
// there is no host process; the Linux launcher records a kubeconfig instead).
func TestK3sContainerHandle_spec_24_19(t *testing.T) {
	const name = "lenny-embedded-k3s-demo"
	if got := k3sContainerHandle(containerNamedLauncher{name: name}); got != name {
		t.Errorf("k3sContainerHandle(Docker-backed) = %q, want %q", got, name)
	}
	if got := k3sContainerHandle(plainLauncher{}); got != "" {
		t.Errorf("k3sContainerHandle(Linux child-process) = %q, want \"\" (records a kubeconfig instead)", got)
	}
}

// withContainerRunning swaps the package-level container probe for the
// duration of a test so the Docker-backed k3s status path is exercised
// without invoking a real docker, then restores it.
func withContainerRunning(t *testing.T, fn func(name string) bool) {
	t.Helper()
	prev := containerRunning
	t.Cleanup(func() { containerRunning = prev })
	containerRunning = fn
}

// TestK3sComponentStatusDockerSubstrate covers the Docker-backed substrate
// status row: liveness is a container probe by the recorded container
// handle. Both the running and the stopped-container cases are asserted.
//
// spec: §24.19 (the k3s health probe is a container probe on the
// Docker-backed substrate, where there is no host process).
func TestK3sComponentStatusDockerSubstrate_spec_24_19(t *testing.T) {
	const name = "lenny-embedded-k3s-demo"

	// Running container.
	var probed string
	withContainerRunning(t, func(n string) bool { probed = n; return true })
	st := State{K3sEnabled: true, K3sContainer: name}
	got := k3sComponentStatus(st)
	if probed != name {
		t.Errorf("status probed container %q, want the recorded handle %q", probed, name)
	}
	if !got.Healthy {
		t.Error("k3s status = down for a running container, want up")
	}
	if !strings.Contains(got.Detail, name) {
		t.Errorf("k3s detail %q does not name the container %q", got.Detail, name)
	}

	// Stopped container.
	withContainerRunning(t, func(string) bool { return false })
	got = k3sComponentStatus(State{K3sEnabled: true, K3sContainer: name})
	if got.Healthy {
		t.Error("k3s status = up for a stopped container, want down")
	}
	if !strings.Contains(got.Detail, "not running") {
		t.Errorf("k3s detail %q does not report the stopped container", got.Detail)
	}
}

// TestK3sComponentStatusHostSubstrate covers the Linux managed-child-process
// substrate status row: liveness keys on the recorded kubeconfig and the
// K3sEnabled flag, and the container probe must not be consulted.
//
// spec: §24.19 (the k3s health probe is a host probe on the Linux substrate).
func TestK3sComponentStatusHostSubstrate_spec_24_19(t *testing.T) {
	// A container probe must not be consulted on the host path: fail the
	// test if it is reached.
	withContainerRunning(t, func(string) bool {
		t.Fatal("host k3s status path must not probe a container")
		return false
	})

	got := k3sComponentStatus(State{K3sEnabled: true, KubeconfigPath: "/state/k3s/kubeconfig.yaml"})
	if !got.Healthy {
		t.Error("k3s status = down for a running host substrate, want up")
	}
	if !strings.Contains(got.Detail, "kubeconfig") {
		t.Errorf("k3s detail %q does not name the recorded kubeconfig", got.Detail)
	}
}

// TestK3sComponentStatusUnsupportedHost covers the no-substrate case: when
// neither a container handle nor a kubeconfig is recorded, the substrate did
// not come up (an unsupported host or a failed start) and the row is down
// with the unsupported/failed detail.
//
// spec: §24.19 (k3s status reports down when the substrate did not start).
func TestK3sComponentStatusUnsupportedHost_spec_24_19(t *testing.T) {
	got := k3sComponentStatus(State{K3sEnabled: false})
	if got.Healthy {
		t.Error("k3s status = up with no recorded substrate handle, want down")
	}
	if !strings.Contains(got.Detail, "unsupported host or failed to start") {
		t.Errorf("k3s detail %q does not report the unsupported/failed substrate", got.Detail)
	}
}

// TestCollectStatusReportsDockerK3s threads the Docker-backed substrate
// through CollectStatus end to end: a recorded stack with a k3s container
// handle reports a k3s row probed by container, distinct from the gateway
// row.
//
// spec: §24.19 (lenny status reports the per-component health, including
// the Docker-backed k3s container).
func TestCollectStatusReportsDockerK3s_spec_24_19(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	withContainerRunning(t, func(string) bool { return true })
	st := State{
		GatewayForwarderAddr: "127.0.0.1:1",
		K3sEnabled:           true,
		K3sContainer:         "lenny-embedded-k3s-demo",
	}
	if err := writeState(paths.StateFile(), st); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	status, err := CollectStatus(context.Background(), StatusOptions{})
	if err != nil {
		t.Fatalf("CollectStatus: %v", err)
	}
	byName := map[string]ComponentStatus{}
	for _, c := range status.Components {
		byName[c.Name] = c
	}
	c, ok := byName["k3s"]
	if !ok {
		t.Fatal("CollectStatus produced no k3s row for a Docker-backed substrate")
	}
	if !c.Healthy {
		t.Errorf("k3s row = %+v, want healthy for a running container", c)
	}
	if !strings.Contains(c.Detail, "lenny-embedded-k3s-demo") {
		t.Errorf("k3s row detail %q does not name the container", c.Detail)
	}
}
