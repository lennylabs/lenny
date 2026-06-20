// SPDX-License-Identifier: MIT

package k3s

import (
	"context"
	"fmt"
	"path/filepath"
)

// dockerk3s.go is the Docker-backed launcher: on macOS and Windows the
// embedded k3s runs as a container under Docker Desktop's Linux VM
// rather than as a host child process, because k3s needs a Linux kernel
// the host cannot provide directly. The launcher runs the same pinned
// k3s version (Version) with the same cluster-disabling flag set as the
// Linux child-process launcher, so the embedded cluster is the same k3s
// distribution on every host.
//
// This file compiles on every GOOS so New can reference it from the
// cross-platform selection in launcher.go, but the launcher is selected
// only on non-Linux hosts. The container-provisioning body (the
// docker run, the published API port, the kubeconfig server-URL rewrite,
// and the host.docker.internal egress wiring) is implemented in a
// following build step; until then Start fails closed with a clear
// diagnostic so the stack routes around the absent cluster rather than
// proceeding as if it were up.
//
// spec: §17.4 (on macOS and Windows the embedded k3s runs as a container
// under Docker Desktop's Linux VM), §24.19 (lenny up/down manage the
// substrate).

// dockerLauncher provisions the embedded k3s as a Docker container. It
// implements Launcher for non-Linux hosts.
type dockerLauncher struct {
	cfg Config
}

// newDockerLauncher constructs the Docker-backed launcher. The caller
// has already applied Config defaults via withDefaults.
func newDockerLauncher(cfg Config) Launcher {
	return &dockerLauncher{cfg: cfg}
}

// KubeconfigPath returns the path the launcher writes the admin
// kubeconfig to once the container is up. The host-process controllers
// and gateway resolve their cluster connection from this file, so the
// path is the same convention the Linux launcher uses.
func (d *dockerLauncher) KubeconfigPath() string {
	return filepath.Join(d.cfg.Dir, "kubeconfig.yaml")
}

// Start provisions the k3s container. The container-provisioning body is
// implemented in a following build step. Until then it fails closed:
// when Docker is absent it reports the missing prerequisite, and when
// Docker is present it reports that the Docker-backed substrate is not
// yet wired so the caller does not proceed as if the cluster were up.
func (d *dockerLauncher) Start(_ context.Context) error {
	if !SupportedPlatform() {
		return unsupportedPlatformError()
	}
	return fmt.Errorf("embedded k3s: the Docker-backed substrate launcher is not yet implemented on this host")
}

// Stop tears the container down. With no container provisioned yet it is
// a no-op.
func (d *dockerLauncher) Stop() error {
	return nil
}

// Running reports whether the k3s container is alive. With no container
// provisioned yet it reports false.
func (d *dockerLauncher) Running() bool {
	return false
}

// PID returns zero: the Docker-backed k3s runs inside the Docker VM
// rather than as a host process, so there is no host PID for it.
func (d *dockerLauncher) PID() int {
	return 0
}
