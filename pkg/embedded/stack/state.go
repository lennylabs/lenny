// SPDX-License-Identifier: MIT

package stack

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// State is the on-disk record of a running Embedded Mode stack. lenny up
// writes it; lenny down and lenny status read it to locate the substrate
// and the host-side gateway forwarder. The §17.4 control plane runs as
// in-cluster pods rendered from the chart, so the state records the
// substrate handle, the loopback forwarder address, and the deployed image
// tag rather than host process identifiers. It lives at Paths.StateFile().
//
// spec: §17.4 (the control plane runs as in-cluster pods; lenny up records
// the substrate and the host-side gateway forwarder).
type State struct {
	// StartedAt is when lenny up brought the stack up.
	StartedAt time.Time `json:"startedAt"`
	// K3sContainer is the docker container name the Docker-backed launcher
	// runs the embedded k3s under (macOS and Windows). It is the handle
	// lenny status probes for liveness. It is empty on the Linux
	// child-process launcher, which runs k3s as a host process, and empty
	// when k3s did not start.
	K3sContainer string `json:"k3sContainer,omitempty"`
	// K3sPID is the process-group leader PID of the embedded k3s on the Linux
	// child-process launcher. lenny up starts k3s in its own process group so
	// it outlives the foreground lenny up process (the in-cluster pods must
	// survive the CLI), so a later lenny down must terminate the recorded
	// process group out of process. It is zero on the Docker-backed launcher,
	// which records K3sContainer instead, and zero when k3s did not start.
	// spec: §17.4 (the substrate outlives the CLI; lenny down stops it).
	K3sPID int `json:"k3sPid,omitempty"`
	// GatewayForwarderAddr is the loopback host:port the host-side TLS
	// forwarder presents the in-cluster gateway on (the §17.4
	// EMBEDDED_MODE_LOCAL_ONLY 127.0.0.1:8443 endpoint). The CLI resolves
	// its gateway URL from it. The in-cluster gateway serves plaintext HTTP
	// behind the forwarder, which terminates TLS with the per-lenny-up
	// self-signed leaf. Empty until the S7 bring-up records it.
	GatewayForwarderAddr string `json:"gatewayForwarderAddr,omitempty"`
	// DeployedImageTag is the CLI version tag the deployed component images
	// were imported and rendered under. lenny up compares it against the
	// running CLI version on a warm bring-up and re-imports and re-applies
	// on a mismatch, so a stale image is not run after a lenny upgrade
	// (C4). Empty until the S7 bring-up records it.
	DeployedImageTag string `json:"deployedImageTag,omitempty"`
	// KubeconfigPath is the embedded k3s admin kubeconfig. On the Linux
	// launcher it is k3s' generated admin kubeconfig; on the Docker-backed
	// launcher it is the host-rewritten kubeconfig whose server URL points
	// at the published host port. The applier and the cluster-backed status
	// and logs commands resolve their cluster connection from it. Empty when
	// k3s did not start.
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

// RunningGateway returns the loopback HTTPS URL of the running Embedded
// Mode gateway: the host-side TLS forwarder address `lenny up` recorded in
// the stack state file (§17.4 EMBEDDED_MODE_LOCAL_ONLY 127.0.0.1:8443). It
// returns ErrNoRunningStack when no stack is recorded, so callers can
// present a precise diagnostic instead of a connect-refused error. The
// root argument selects the LENNY_HOME-equivalent state directory; an
// empty root uses the default.
//
// spec: §17.4 (the CLI reaches the in-cluster gateway through the
// loopback-only host-side forwarder).
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
	if st.GatewayForwarderAddr == "" {
		return "", fmt.Errorf("embedded: stack state at %s has no gatewayForwarderAddr", paths.StateFile())
	}
	return "https://" + st.GatewayForwarderAddr, nil
}

// Substrate describes how the embedded k3s is provisioned on the running
// stack, so a local command (the §24.19.1 image bridge) can reach the
// embedded containerd through the substrate's mechanism. The Linux
// managed-child-process launcher runs k3s on the host with a host
// containerd socket; the Docker-backed launcher (macOS and Windows) runs
// k3s inside a container with no host socket, so the bridge runs `ctr`
// inside that container.
//
// spec: §17.4 (the substrate is provisioned per host operating system),
// §24.19.1 (the image bridge reaches the embedded containerd image store).
type Substrate struct {
	// Container is the docker container name the Docker-backed launcher runs
	// the embedded k3s under (macOS and Windows). It is empty on the Linux
	// child-process launcher, where k3s runs on the host with a host
	// containerd socket. A non-empty Container selects the container-exec
	// path; an empty Container selects the host-socket path.
	Container string
}

// DockerBacked reports whether the substrate runs k3s inside a Docker
// container (macOS and Windows) rather than as a host child process
// (Linux). The image bridge branches on this to reach the embedded
// containerd: a container-exec path when Docker-backed, a host-socket path
// otherwise.
func (s Substrate) DockerBacked() bool { return s.Container != "" }

// RunningSubstrate returns the embedded k3s substrate handle recorded by
// the running stack. It returns ErrNoRunningStack when no stack is
// recorded, so a caller can surface the §24.19.1 EMBEDDED_MODE_REQUIRED /
// K3S_UNAVAILABLE diagnostic instead of a lower-level error. The root
// argument selects the LENNY_HOME-equivalent state directory; an empty
// root uses the default.
//
// spec: §24.19.1 (the image bridge selects its containerd-reach path from
// the running substrate).
func RunningSubstrate(root string) (Substrate, error) {
	resolved, err := resolveRoot(root)
	if err != nil {
		return Substrate{}, err
	}
	paths := NewPaths(resolved)
	st, ok, err := readState(paths.StateFile())
	if err != nil {
		return Substrate{}, err
	}
	if !ok {
		return Substrate{}, ErrNoRunningStack
	}
	return Substrate{Container: st.K3sContainer}, nil
}
