// SPDX-License-Identifier: MIT

package stack

import "os"

// controllerSpec configures the embedded controller child process.
type controllerSpec struct {
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
	// LogPath is the controller log file.
	LogPath string
}

// startController launches the production controllers against the
// embedded Kubernetes cluster. §17.4: Embedded Mode uses the
// production controllers; the controller resolves its cluster
// connection from KUBECONFIG, which is pointed at the embedded k3s
// admin kubeconfig. Leader election is left off — the embedded stack
// runs a single replica.
func startController(spec controllerSpec) (*managedProcess, error) {
	args := []string{
		// The embedded stack runs one replica, so leader election is
		// unnecessary; omitting --leader-elect keeps it off.
		"--postgres-dsn", spec.PostgresDSN,
	}
	env := append(
		os.Environ(),
		"KUBECONFIG="+spec.Kubeconfig,
		"LENNY_POSTGRES_DSN="+spec.PostgresDSN,
		"LENNY_EMBEDDED_MODE=true",
	)
	return startProcess(processSpec{
		Name:    "controller",
		BinPath: spec.BinPath,
		Args:    args,
		Env:     env,
		LogPath: spec.LogPath,
	})
}
