// SPDX-License-Identifier: MIT

package k3s

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// Version is the k3s release the embedded stack provisions. It is
// pinned so a lenny up is reproducible across hosts and launchers.
const Version = "v1.31.4+k3s1"

// Launcher provisions the embedded k3s substrate for §17.4 Embedded
// Mode. The substrate is provisioned per host operating system: a
// managed k3s child process on Linux, and a Docker-backed k3s container
// on macOS and Windows. Both launchers run the same pinned k3s version
// (Version) with the same cluster-disabling flag set, so the embedded
// cluster is the same k3s distribution on every host. The gateway,
// controllers, CRDs, and storage interfaces above the launcher are
// byte-identical across operating systems; only this provisioning layer
// differs, so the no-mode-dependent-business-logic-split invariant
// holds.
//
// spec: §17.4 (the embedded Kubernetes substrate is provisioned per
// host operating system), §24.19 (lenny up/down manage the substrate).
type Launcher interface {
	// Start provisions the substrate, downloading or pulling the pinned
	// k3s when absent, and blocks until the API server is reachable or
	// ReadyTimeout elapses.
	Start(ctx context.Context) error
	// Stop tears the substrate down. Stop is idempotent and safe to call
	// before Start.
	Stop() error
	// Running reports whether the substrate is alive.
	Running() bool
	// PID returns the host process identifier of the substrate when it is
	// a host process (the Linux child-process launcher), or zero when the
	// substrate is not a host process or is not running. A Docker-backed
	// launcher reports zero because its k3s runs inside the Docker VM
	// rather than as a host process.
	PID() int
	// KubeconfigPath returns the path the admin kubeconfig is written to.
	// Host-process controllers and the gateway resolve their cluster
	// connection from this file through the KUBECONFIG environment
	// variable.
	KubeconfigPath() string
}

// Config configures the embedded k3s launcher.
type Config struct {
	// Dir holds the launcher's state: the downloaded k3s binary, the data
	// directory, and the generated kubeconfig. §17.4 places it at
	// ~/.lenny/k3s/.
	Dir string
	// APIPort is the host port the k3s API server is reachable on.
	APIPort int
	// DownloadTimeout bounds the one-time binary download or image pull.
	// Zero defaults to 5 minutes.
	DownloadTimeout time.Duration
	// ReadyTimeout bounds the wait for the API server to become reachable
	// after the substrate starts. Zero defaults to 2 minutes.
	ReadyTimeout time.Duration
}

// withDefaults fills the zero-valued Config fields with their defaults.
// Both launchers share these defaults so a lenny up behaves the same way
// regardless of which substrate it provisions.
func (c Config) withDefaults() Config {
	if c.APIPort == 0 {
		c.APIPort = 6443
	}
	if c.DownloadTimeout <= 0 {
		c.DownloadTimeout = 5 * time.Minute
	}
	if c.ReadyTimeout <= 0 {
		c.ReadyTimeout = 2 * time.Minute
	}
	return c
}

// New selects and constructs the launcher for the host operating system.
// On Linux it returns the managed child-process launcher; on macOS and
// Windows it returns the Docker-backed launcher. The substrate is not
// started until Start is called. New does not fail when the platform is
// unsupported: the launcher's Start returns the platform diagnostic so a
// caller that constructs eagerly still surfaces a clear error. Callers
// that want to skip the cluster entirely consult SupportedPlatform
// first.
//
// spec: §17.4 (the substrate is provisioned per host operating system).
func New(cfg Config) Launcher {
	cfg = cfg.withDefaults()
	if runtime.GOOS == "linux" {
		return newChildLauncher(cfg)
	}
	return newDockerLauncher(cfg)
}

// SupportedPlatform reports whether the host operating system can
// provision the embedded k3s substrate. Linux is supported
// unconditionally through the managed child-process launcher. macOS and
// Windows are supported when the docker CLI is resolvable on PATH,
// because the substrate then runs as a container under Docker Desktop's
// Linux VM. A non-Linux host without Docker is unsupported: the embedded
// k3s needs a Linux kernel the binary cannot embed, and Docker Desktop
// supplies that kernel. The check fails closed — absent Docker on a
// non-Linux host reports unsupported rather than attempting a launch
// that cannot succeed.
//
// spec: §17.4 (on macOS and Windows the embedded k3s runs under Docker
// Desktop's Linux VM; Docker is a stated prerequisite on those hosts).
func SupportedPlatform() bool {
	if runtime.GOOS == "linux" {
		return true
	}
	_, err := lookPathDocker()
	return err == nil
}

// unsupportedPlatformError returns the diagnostic a launcher surfaces
// when the host cannot provision the substrate. On a non-Linux host the
// cause is an absent Docker prerequisite; the message names it so the
// operator can install Docker Desktop and retry.
func unsupportedPlatformError() error {
	if runtime.GOOS == "linux" {
		// Linux is always supported; this branch is defensive.
		return fmt.Errorf("embedded k3s: unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Errorf("embedded k3s: unsupported platform %s/%s: Docker Desktop is required to run the "+
		"embedded k3s under its Linux VM, but the `docker` binary is not on PATH; install Docker Desktop and retry",
		runtime.GOOS, runtime.GOARCH)
}

// lookPathDocker resolves the `docker` binary on PATH. It is overridden
// in tests so the Docker-present and Docker-absent platform paths can be
// exercised without touching the real PATH. This mirrors the established
// pattern in pkg/embedded/localcli/image.go; the two cannot share one
// definition because localcli imports stack, which imports this package,
// so importing localcli here would close an import cycle.
var lookPathDocker = func() (string, error) {
	return exec.LookPath("docker")
}

// downloadURL returns the k3s release binary URL for the host
// architecture. k3s publishes a bare k3s binary for amd64 and a
// k3s-arm64 binary for arm64. The Linux child-process launcher downloads
// this binary; the Docker-backed launcher pulls the matching container
// image instead.
func downloadURL() string {
	asset := "k3s"
	if runtime.GOARCH == "arm64" {
		asset = "k3s-arm64"
	}
	return fmt.Sprintf("https://github.com/k3s-io/k3s/releases/download/%s/%s", Version, asset)
}
