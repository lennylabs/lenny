// SPDX-License-Identifier: MIT

package stack

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// State is the on-disk record of a running Embedded Mode stack. lenny
// up writes it; lenny down and lenny status read it to locate the
// child processes and component endpoints. It lives at
// Paths.StateFile().
type State struct {
	// StartedAt is when lenny up brought the stack up.
	StartedAt time.Time `json:"startedAt"`
	// SupervisorPID is the detached lenny process that hosts the
	// in-process components (Redis, the OIDC provider, and the TLS
	// reverse proxy) and supervises the child processes. lenny down
	// signals it to trigger a graceful teardown of the whole stack.
	SupervisorPID int `json:"supervisorPid"`
	// GatewayPID and ControllerPID are the child-process identifiers.
	GatewayPID    int `json:"gatewayPid"`
	ControllerPID int `json:"controllerPid"`
	// K3sPID is the embedded k3s host process identifier on the Linux
	// managed-child-process launcher. It is zero on the Docker-backed
	// launcher (macOS and Windows), where k3s runs inside the Docker VM
	// with no host PID; K3sContainer carries the handle there. It is also
	// zero when k3s did not start (an unsupported host).
	K3sPID int `json:"k3sPid,omitempty"`
	// K3sContainer is the docker container name the Docker-backed launcher
	// runs the embedded k3s under (macOS and Windows). It is the handle
	// lenny status probes for liveness in place of the host PID. It is
	// empty on the Linux child-process launcher, which records a host PID
	// in K3sPID instead, and empty when k3s did not start.
	K3sContainer string `json:"k3sContainer,omitempty"`
	// HTTPAddr and HTTPSAddr are the gateway's plaintext and
	// TLS-terminated listen addresses.
	HTTPAddr  string `json:"httpAddr"`
	HTTPSAddr string `json:"httpsAddr"`
	// PostgresDSN and RedisURL are the embedded-backend connection
	// strings the gateway and controllers were configured with.
	PostgresDSN string `json:"postgresDsn"`
	RedisURL    string `json:"redisUrl"`
	// KubeconfigPath is the embedded k3s admin kubeconfig. On the Linux
	// launcher it is k3s' generated admin kubeconfig; on the Docker-backed
	// launcher it is the host-rewritten kubeconfig whose server URL points
	// at the published host port. The gateway and controllers resolve
	// their cluster connection from it. Empty when k3s did not start.
	KubeconfigPath string `json:"kubeconfigPath,omitempty"`
	// K3sEnabled records whether the embedded Kubernetes layer came up. It
	// is true on every host where the substrate provisioned (Linux
	// unconditionally, macOS and Windows when Docker Desktop is present),
	// and false only on an unsupported host (a non-Linux host without
	// Docker) or when the substrate failed to start.
	K3sEnabled bool `json:"k3sEnabled"`
}

// writeState persists s to path atomically: it writes a temp file and
// renames it so a reader never observes a partial record.
func writeState(path string, s State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("embedded: marshal state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("embedded: write state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("embedded: install state file: %w", err)
	}
	return nil
}

// readState loads the stack state from path. It returns ok=false when
// no state file exists, which means no stack is recorded as running.
func readState(path string) (s State, ok bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, fmt.Errorf("embedded: read state: %w", err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, false, fmt.Errorf("embedded: parse state %s: %w", path, err)
	}
	return s, true, nil
}

// removeState deletes the stack state file. It is a no-op when the
// file is already absent.
func removeState(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("embedded: remove state: %w", err)
	}
	return nil
}

// ErrNoRunningStack is returned by RunningGateway when no Embedded
// Mode stack is recorded as running.
var ErrNoRunningStack = errors.New("embedded: no running stack")

// RunningGateway returns the plaintext HTTP URL of the running
// Embedded Mode gateway (the value `lenny up` recorded in the stack
// state file). It returns ErrNoRunningStack when no stack is
// recorded, so callers can present a precise diagnostic instead of a
// connect-refused error. The root argument selects the LENNY_HOME-
// equivalent state directory; an empty root uses the default.
func RunningGateway(root string) (string, error) {
	resolved, err := resolveRoot(root)
	if err != nil {
		return "", err
	}
	paths := NewPaths(resolved)
	st, ok, err := readState(paths.StateFile())
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNoRunningStack
	}
	if st.HTTPAddr == "" {
		return "", fmt.Errorf("embedded: stack state at %s has no httpAddr", paths.StateFile())
	}
	return "http://" + st.HTTPAddr, nil
}

// processAlive reports whether a process with the given PID is
// currently running. A zero or negative PID is treated as not running.
// The liveness probe itself (signal-0 on unix, OpenProcess on Windows)
// lives in the build-tagged process-control substrate.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return pidAlive(pid)
}
