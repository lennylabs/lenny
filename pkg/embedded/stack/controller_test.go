// SPDX-License-Identifier: MIT

package stack

import (
	"os"
	"strings"
	"testing"
	"time"
)

// controllerArgs and controllerEnv build the production controller child
// process's flags and environment from a ControllerSpec, the same way
// gatewayArgs/gatewayEnv do for the gateway. They are exercised directly,
// without launching a process, so the §4.7 gateway-callback-address
// threading and the host-rewritten KUBECONFIG threading this step adds are
// pinned independently of a live cluster.

// TestControllerArgsBaseFlags covers the always-present controller flags:
// the §4.6.1 agent_pod_state mirror DSN, and the absence of --leader-elect
// (the embedded stack runs a single replica, so leader election stays off).
//
// spec: §17.4 (the embedded controller runs a single replica against the
// embedded backends), §4.6.1 (the agent_pod_state mirror DSN).
func TestControllerArgsBaseFlags(t *testing.T) {
	const dsn = "postgres://lenny@127.0.0.1:15433/lenny"
	args := controllerArgs(ControllerSpec{PostgresDSN: dsn})
	got, ok := argValue(args, "--postgres-dsn")
	if !ok || got != dsn {
		t.Fatalf("--postgres-dsn = %q (set=%v), want %q", got, ok, dsn)
	}
	if _, ok := argValue(args, "--leader-elect"); ok {
		t.Error("controllerArgs passed --leader-elect; the embedded stack runs one replica")
	}
}

// TestControllerArgsThreadsGatewayGRPCAddr covers the §4.7 callback-address
// threading: when the stack computes the launcher's externally-reachable
// gateway address (GatewayHost joined to the gRPC host port), controllerArgs
// stamps it onto the controller through --gateway-grpc-addr so the
// controller stamps it onto each agent pod's adapter. The host portion
// (127.0.0.1 on the Linux launcher, host.docker.internal on the
// Docker-backed launcher) is supplied by the substrate launcher, so the
// flag carries the §4.7 gateway↔adapter callback across the host/Docker
// boundary.
//
// spec: §4.7, §8.6, §9.1, §17.4 (the controller stamps the gateway callback
// address onto agent pods; the substrate launcher supplies the host).
func TestControllerArgsThreadsGatewayGRPCAddr_spec_4_7(t *testing.T) {
	const addr = "host.docker.internal:50061"
	args := controllerArgs(ControllerSpec{PostgresDSN: "dsn", GatewayGRPCAddr: addr})
	got, ok := argValue(args, "--gateway-grpc-addr")
	if !ok {
		t.Fatal("controllerArgs did not pass --gateway-grpc-addr when the spec set it")
	}
	if got != addr {
		t.Errorf("--gateway-grpc-addr = %q, want %q", got, addr)
	}
}

// TestControllerArgsOmitsGatewayGRPCAddrWhenUnset confirms an empty
// GatewayGRPCAddr leaves the flag off, so a stack without an embedded
// cluster (no in-cluster adapter to serve) does not pass an empty callback
// address.
//
// spec: §4.7 (the gateway callback is stamped only when there is an
// in-cluster adapter to dial it).
func TestControllerArgsOmitsGatewayGRPCAddrWhenUnset(t *testing.T) {
	args := controllerArgs(ControllerSpec{PostgresDSN: "dsn"})
	if _, ok := argValue(args, "--gateway-grpc-addr"); ok {
		t.Error("controllerArgs passed --gateway-grpc-addr with no address set")
	}
}

// TestControllerEnvThreadsKubeconfig covers the host-rewritten KUBECONFIG
// threading this step adds: the controller resolves its cluster connection
// from KUBECONFIG, which the stack points at the launcher's host-rewritten
// kubeconfig (the published-host-port server URL on the Docker-backed
// launcher) so the host-process controller reaches the in-container API
// server across the host/Docker boundary. The embedded-mode driver selector
// and the mirror DSN are also threaded.
//
// spec: §17.4 (the controller runs against the launcher's host-rewritten
// kubeconfig on every supported host; LENNY_EMBEDDED_MODE selects the
// embedded backends).
func TestControllerEnvThreadsKubeconfig_spec_17_4(t *testing.T) {
	const (
		kubeconfig = "/home/alice/.lenny/k3s/kubeconfig.yaml"
		dsn        = "postgres://lenny@127.0.0.1:15433/lenny"
	)
	env := controllerEnv(ControllerSpec{Kubeconfig: kubeconfig, PostgresDSN: dsn}, nil)
	if got, ok := envValue(env, "KUBECONFIG"); !ok || got != kubeconfig {
		t.Fatalf("KUBECONFIG = %q (set=%v), want the host-rewritten kubeconfig %q", got, ok, kubeconfig)
	}
	if got, ok := envValue(env, "LENNY_POSTGRES_DSN"); !ok || got != dsn {
		t.Fatalf("LENNY_POSTGRES_DSN = %q (set=%v), want %q", got, ok, dsn)
	}
	if got, ok := envValue(env, "LENNY_EMBEDDED_MODE"); !ok || got != "true" {
		t.Fatalf("LENNY_EMBEDDED_MODE = %q (set=%v), want true", got, ok)
	}
}

// TestControllerEnvExtendsBase confirms controllerEnv extends the base
// environment rather than replacing it, and does not mutate the caller's
// slice — the same contract gatewayEnv holds, so a controller inherits the
// host environment plus the embedded selectors.
func TestControllerEnvExtendsBase(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/home/alice"}
	env := controllerEnv(ControllerSpec{Kubeconfig: "/k", PostgresDSN: "dsn"}, base)
	if got, ok := envValue(env, "PATH"); !ok || got != "/usr/bin" {
		t.Errorf("controllerEnv dropped the base PATH: %q (set=%v)", got, ok)
	}
	// The base slice must be unmodified: controllerEnv appends to a copy.
	if len(base) != 2 || !strings.HasPrefix(base[0], "PATH=") {
		t.Errorf("controllerEnv mutated the caller's base slice: %v", base)
	}
}

// TestControllerHandleLifecycle covers the exported Controller handle the
// tier-2 bring-up test relies on: a running controller reports a positive
// PID and Running()=true, and Stop() terminates it so Running() goes false.
// The handle's liveness is probed through processAlive (not cmd.Wait), which
// is why a controller that exits on its own is observed as not running. The
// child is a parked sleeper rather than the real controller binary so the
// handle lifecycle is pinned without a live cluster.
//
// diagnosis: a failure means the Controller handle does not track the child
// process correctly — PID/Running/Stop disagree with the OS. The tier-2
// bring-up test would then mis-report whether the production controller stayed
// alive against the launcher's host-rewritten kubeconfig.
//
// spec: §17.4 (Embedded Mode supervises the production controllers as managed
// child processes), §24.19 (the controller health probe).
func TestControllerHandleLifecycle_spec_17_4(t *testing.T) {
	c := &Controller{proc: spawnSleeper(t)}
	pid := c.PID()
	if pid <= 0 {
		t.Fatalf("Controller.PID() = %d, want a positive pid for a running child", pid)
	}
	if !c.Running() {
		t.Fatal("Controller.Running() = false for a just-started child")
	}
	if err := c.Stop(); err != nil {
		t.Fatalf("Controller.Stop(): %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !c.Running() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if c.Running() {
		t.Errorf("Controller.Running() = true after Stop (pid %d)", pid)
	}
	// Stop is idempotent: a second call is a no-op.
	if err := c.Stop(); err != nil {
		t.Errorf("second Controller.Stop() errored: %v", err)
	}
}

// TestStartControllerLaunchesChild covers StartController/startController: the
// launch path builds the controller argv and env from the spec and starts the
// child. It points BinPath at a parked sleeper binary so the process actually
// starts without needing the real controller binary or a cluster, then tears
// it down through the returned handle.
//
// diagnosis: a failure means the controllers-start leg of `lenny up` could not
// launch the production controller process at all (a bad argv/env assembly or
// a process-spawn failure), independent of whether the controller can reach
// the API server.
//
// spec: §17.4 (the production controllers run as managed child processes on
// every supported host).
func TestStartControllerLaunchesChild_spec_17_4(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	c, err := StartController(ControllerSpec{
		BinPath:     self,
		PostgresDSN: "postgres://localhost/lenny",
		Kubeconfig:  t.TempDir() + "/kubeconfig.yaml",
		LogPath:     t.TempDir() + "/controller.log",
	})
	if err != nil {
		t.Fatalf("StartController: %v", err)
	}
	t.Cleanup(func() { _ = c.Stop() })
	if c.PID() <= 0 {
		t.Fatalf("StartController returned a handle with non-positive PID %d", c.PID())
	}
}
