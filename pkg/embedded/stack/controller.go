// SPDX-License-Identifier: MIT

package stack

import "os"

// ControllerSpec configures the embedded controller child process. It is
// exported so the tier-2 bring-up test can start the production controller
// against a Docker-backed substrate's host-rewritten kubeconfig, the same
// call Up makes — the controllers-start leg the non-Linux gate previously
// skipped.
//
// spec: §17.4 (Embedded Mode runs the production controllers against the
// launcher's host-rewritten kubeconfig on every supported host).
type ControllerSpec struct {
	// BinPath is the production lenny-controller binary.
	BinPath string
	// PostgresDSN points the controller's agent_pod_state mirror at
	// the embedded Postgres.
	PostgresDSN string
	// Kubeconfig is the embedded k3s admin kubeconfig the controller
	// resolves its cluster connection from. On the Docker-backed launcher
	// (macOS and Windows) it is the host-rewritten kubeconfig whose server
	// URL points at the published host port, so the host-process
	// controller reaches the in-container API server across the
	// host/Docker boundary.
	Kubeconfig string
	// GatewayGRPCAddr is the §8.6/§9.1 gateway GatewayControl address
	// (host:port) the controller stamps onto every agent pod's adapter so a
	// type:agent runtime's platform tool calls and ExtendLease reach the
	// gateway. It is the gateway's externally-reachable address from inside
	// the cluster: the launcher's GatewayHost (127.0.0.1 on the Linux
	// child-process launcher, host.docker.internal on the Docker-backed
	// launcher) joined to the gateway's gRPC host port. Threading it here
	// carries the §4.7 gateway↔adapter callback across the host/Docker
	// boundary, because the gateway runs as a host process while the pods
	// run in-cluster. Empty leaves the platform MCP server unstarted in the
	// pod. spec: §4.7, §8.6, §9.1, §17.4.
	GatewayGRPCAddr string
	// LogPath is the controller log file.
	LogPath string
}

// Controller is a handle to a running embedded controller child process.
// StartController returns it so a caller (the tier-2 bring-up test) can
// assert the controller comes up and stays alive against the launcher's
// host-rewritten kubeconfig, then tear it down. The handle exposes the
// liveness and teardown surface without leaking the internal process type.
type Controller struct {
	proc *managedProcess
}

// PID returns the controller process identifier, or zero when it is not
// running.
func (c *Controller) PID() int { return c.proc.PID() }

// Running reports whether the controller child process is still alive.
// It probes the OS for the PID (signal-0 on unix, OpenProcess on Windows)
// rather than reading cmd.ProcessState, because nothing calls cmd.Wait on
// the controller in this path, so ProcessState stays nil and would report
// a self-exited controller as still running. status.go and lifecycle.go
// determine component liveness the same way (processAlive), so a
// controller that exits on its own — the failure this handle exists to
// detect, a controller that cannot reach the in-container API server
// across the host/Docker boundary and exits non-zero during startup — is
// observed as not running. spec: §24.19 (the controller health probe).
func (c *Controller) Running() bool { return processAlive(c.proc.PID()) }

// Stop terminates the controller child process tree. It is idempotent.
func (c *Controller) Stop() error { return c.proc.Stop() }

// StartController launches the production controllers against the
// embedded Kubernetes cluster. §17.4: Embedded Mode uses the
// production controllers; the controller resolves its cluster
// connection from KUBECONFIG, which is pointed at the embedded k3s
// admin kubeconfig (the launcher's host-rewritten kubeconfig on the
// Docker-backed launcher). Leader election is left off — the embedded
// stack runs a single replica. It is exported so the tier-2 bring-up
// test can pin the controllers-start leg against a Docker-backed
// substrate, the same way InstallCRDs pins the CRD-install leg.
//
// spec: §17.4 (the production controllers run against the launcher's
// host-rewritten kubeconfig on every supported host).
func StartController(spec ControllerSpec) (*Controller, error) {
	proc, err := startController(spec)
	if err != nil {
		return nil, err
	}
	return &Controller{proc: proc}, nil
}

// startController launches the production controller as a child process
// per spec. It returns the internal managedProcess the stack supervisor
// owns; StartController wraps it in the exported Controller handle.
func startController(spec ControllerSpec) (*managedProcess, error) {
	return startProcess(processSpec{
		Name:    "controller",
		BinPath: spec.BinPath,
		Args:    controllerArgs(spec),
		Env:     controllerEnv(spec, os.Environ()),
		LogPath: spec.LogPath,
	})
}

// controllerArgs builds the command-line arguments for the embedded
// controller child process from spec. It is separated from startController
// so the §4.7 gateway-callback-address threading is testable without
// launching a process, mirroring gatewayArgs.
func controllerArgs(spec ControllerSpec) []string {
	args := []string{
		// The embedded stack runs one replica, so leader election is
		// unnecessary; omitting --leader-elect keeps it off.
		"--postgres-dsn", spec.PostgresDSN,
	}
	if spec.GatewayGRPCAddr != "" {
		// §4.7/§9.1: the controller stamps this externally-reachable gateway
		// address onto every agent pod's adapter so the in-cluster adapter
		// dials the host gateway's GatewayControl listener across the
		// host/Docker boundary. The substrate launcher supplied the host
		// portion (loopback on Linux, host.docker.internal under Docker), so
		// the §4.7 pod-spec/adapter business logic stays substrate-agnostic.
		args = append(args, "--gateway-grpc-addr", spec.GatewayGRPCAddr)
	}
	return args
}

// controllerEnv builds the environment for the embedded controller child
// process by extending base (typically os.Environ()) with the cluster
// connection and the §17.4 embedded-mode driver selector. It is separated
// from startController so the env construction is testable without
// launching a process, mirroring gatewayEnv.
func controllerEnv(spec ControllerSpec, base []string) []string {
	return append(
		append([]string(nil), base...),
		"KUBECONFIG="+spec.Kubeconfig,
		"LENNY_POSTGRES_DSN="+spec.PostgresDSN,
		"LENNY_EMBEDDED_MODE=true",
	)
}
