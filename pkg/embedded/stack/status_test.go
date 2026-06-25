// SPDX-License-Identifier: MIT

package stack

import (
	"bytes"
	"context"
	"strings"
	"testing"

	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
)

// withPoolReady swaps the package-level echo-pool-readiness seam for the
// duration of a test so the cluster-backed status pool-ready row is driven
// without a real cluster, then restores it.
func withPoolReady(t *testing.T, ready bool) {
	t.Helper()
	prev := poolReadyFn
	t.Cleanup(func() { poolReadyFn = prev })
	poolReadyFn = func(context.Context, string) bool { return ready }
}

// TestCollectStatusNoStack covers the no-stack path: with no state file,
// CollectStatus reports the stack not running and an unknown session count, and
// WriteStatus prints the no-stack message.
//
// spec: §17.4 line 178, §24.19 line 262.
func TestCollectStatusNoStack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	st, err := CollectStatus(context.Background(), StatusOptions{})
	if err != nil {
		t.Fatalf("CollectStatus: %v", err)
	}
	if st.Running {
		t.Error("CollectStatus reported a running stack with no state file")
	}
	if st.ActiveSessions != -1 {
		t.Errorf("ActiveSessions = %d, want -1 when no stack runs", st.ActiveSessions)
	}
	var out bytes.Buffer
	WriteStatus(&out, st)
	if !strings.Contains(out.String(), "no embedded stack is running") {
		t.Errorf("WriteStatus output = %q", out.String())
	}
}

// TestCollectStatusReadsDeploymentReadiness covers the §17.4 cluster-backed
// status: the gateway, controller, and ops rows report their Deployment
// readiness read through the embedded kubeconfig rather than a host probe. A
// ready gateway/controller and a still-rolling ops Deployment produce the
// matching per-component health.
//
// spec: §17.4 line 178 (the control plane runs as in-cluster Deployments;
// status reads their readiness), §24.19 line 262.
func TestCollectStatusReadsDeploymentReadiness_spec_17_4(t *testing.T) {
	recordRunningStack(t)
	client := k8sfake.NewSimpleClientset(
		readyDeployment(gatewayDeploymentName, "gateway"),
		readyDeployment(controllerDeploymentName, "controller"),
		notReadyDeployment(opsDeploymentName, "ops"),
	)
	withClusterClient(t, client)
	withPoolReady(t, false)

	status, err := CollectStatus(context.Background(), StatusOptions{})
	if err != nil {
		t.Fatalf("CollectStatus: %v", err)
	}
	byName := map[string]ComponentStatus{}
	for _, c := range status.Components {
		byName[c.Name] = c
	}
	if c, ok := byName["gateway"]; !ok || !c.Healthy {
		t.Errorf("gateway row = %+v, want healthy for a ready Deployment", c)
	}
	if c, ok := byName["controller"]; !ok || !c.Healthy {
		t.Errorf("controller row = %+v, want healthy for a ready Deployment", c)
	}
	if c, ok := byName["ops"]; !ok || c.Healthy {
		t.Errorf("ops row = %+v, want down for a still-rolling Deployment", c)
	}
}

// TestCollectStatusDistinguishesGatewayUpFromPoolReady covers the §17.4
// honest-readiness requirement: a ready gateway Deployment with a still-warming
// echo pool reports the gateway up but PoolReady false, and WriteStatus renders
// the pool as warming. Once the pool reports a ready idle pod, PoolReady is
// true. The two states are reported independently so lenny up returning
// (gateway answers) does not imply the pool is ready.
//
// spec: §17.4 line 178 (lenny status distinguishes "gateway up" from
// "pool ready").
func TestCollectStatusDistinguishesGatewayUpFromPoolReady_spec_17_4(t *testing.T) {
	recordRunningStack(t)
	client := k8sfake.NewSimpleClientset(
		readyDeployment(gatewayDeploymentName, "gateway"),
		readyDeployment(controllerDeploymentName, "controller"),
		readyDeployment(opsDeploymentName, "ops"),
	)
	withClusterClient(t, client)

	// Gateway up, pool still warming.
	withPoolReady(t, false)
	warming, err := CollectStatus(context.Background(), StatusOptions{})
	if err != nil {
		t.Fatalf("CollectStatus (warming): %v", err)
	}
	if !gatewayHealthyRow(warming.Components) {
		t.Error("gateway row = down, want up for a ready gateway Deployment")
	}
	if warming.PoolReady {
		t.Error("PoolReady = true while the pool is warming, want false")
	}
	var out bytes.Buffer
	WriteStatus(&out, warming)
	if !strings.Contains(out.String(), "echo pool: warming") {
		t.Errorf("WriteStatus output = %q, want the pool-warming line", out.String())
	}

	// Pool now ready.
	withPoolReady(t, true)
	ready, err := CollectStatus(context.Background(), StatusOptions{})
	if err != nil {
		t.Fatalf("CollectStatus (ready): %v", err)
	}
	if !ready.PoolReady {
		t.Error("PoolReady = false once the pool reports a ready idle pod, want true")
	}
	out.Reset()
	WriteStatus(&out, ready)
	if !strings.Contains(out.String(), "echo pool: ready") {
		t.Errorf("WriteStatus output = %q, want the pool-ready line", out.String())
	}
}

// TestCollectStatusMissingDeploymentReportsNotFound covers the path where the
// control plane is reachable but a Deployment has not been applied yet: the
// row reports down with a not-found detail rather than erroring, so status
// reports an un-applied component honestly.
//
// spec: §17.4 (status reports a missing control-plane Deployment as down).
func TestCollectStatusMissingDeploymentReportsNotFound_spec_17_4(t *testing.T) {
	recordRunningStack(t)
	// A reachable but empty cluster carries no control-plane Deployments.
	withClusterClient(t, k8sfake.NewSimpleClientset())
	withPoolReady(t, false)

	status, err := CollectStatus(context.Background(), StatusOptions{})
	if err != nil {
		t.Fatalf("CollectStatus: %v", err)
	}
	byName := map[string]ComponentStatus{}
	for _, c := range status.Components {
		byName[c.Name] = c
	}
	c, ok := byName["gateway"]
	if !ok || c.Healthy {
		t.Errorf("gateway row = %+v, want down for an un-applied Deployment", c)
	}
	if !strings.Contains(c.Detail, "not found") {
		t.Errorf("gateway detail = %q, want a not-found note", c.Detail)
	}
}

// TestCollectStatusNoKubeconfigReportsClusterDown covers the path where a
// recorded stack has no kubeconfig (the substrate did not come up): the
// gateway/controller/ops rows are all down with an unreachable detail and the
// pool is reported not ready, rather than CollectStatus erroring.
//
// spec: §17.4 (an unreachable control plane reports down rather than failing
// the status command).
func TestCollectStatusNoKubeconfigReportsClusterDown_spec_17_4(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LENNY_HOME", home)
	paths := NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := writeState(paths.StateFile(), State{K3sEnabled: false}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	status, err := CollectStatus(context.Background(), StatusOptions{})
	if err != nil {
		t.Fatalf("CollectStatus: %v", err)
	}
	if !status.Running {
		t.Fatal("CollectStatus reported the recorded stack as not running")
	}
	byName := map[string]ComponentStatus{}
	for _, c := range status.Components {
		byName[c.Name] = c
	}
	for _, name := range []string{"gateway", "controller", "ops"} {
		if c, ok := byName[name]; !ok || c.Healthy {
			t.Errorf("%s row = %+v, want down when no embedded kubeconfig is recorded", name, c)
		}
	}
	if status.PoolReady {
		t.Error("PoolReady = true with no kubeconfig, want false")
	}
}

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
