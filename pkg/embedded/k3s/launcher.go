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

// Host aliases an in-cluster agent pod uses to reach a host process (the
// gateway and its §8.6/§9.1 GatewayControl listener). They are
// substrate-specific and returned by Launcher.GatewayHost.
const (
	// loopbackHost is the host a pod uses on the Linux child-process
	// launcher, where k3s runs on the host and the gateway binds loopback.
	loopbackHost = "127.0.0.1"
	// dockerHostAlias is the Docker-Desktop alias a container resolves to
	// the host's gateway IP. On the Docker-backed launcher an in-cluster
	// pod reaches the host gateway through it. The k3s container is started
	// with --add-host so the alias resolves inside the Docker VM.
	dockerHostAlias = "host.docker.internal"
)

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
	// ReadyTimeout elapses. A persisted substrate (a stopped Docker-backed
	// container or an existing Linux data directory) is reused rather than
	// re-provisioned, so a warm lenny up restarts the embedded cluster
	// instead of paying the one-time provisioning cost again. spec: §17.4
	// (the substrate and the imported-image store persist across
	// lenny down/up).
	Start(ctx context.Context) error
	// Stop pauses the substrate while persisting its state so a warm lenny
	// up can restart it: it stops the Docker-backed container (which keeps
	// the container and its containerd image store) and terminates the Linux
	// child process (whose data directory persists on disk). Stop is
	// idempotent and safe to call before Start. lenny down (without --purge)
	// calls Stop. spec: §17.4 (lenny down persists the substrate and the
	// imported-image store; --purge removes them).
	Stop() error
	// Remove tears the substrate down and discards its persisted state: it
	// force-removes the Docker-backed container (and its containerd image
	// store) and, on the Linux child process, terminates the process (the
	// data directory is then removed by lenny down --purge's purgeRoot).
	// Remove is idempotent and safe to call before Start. lenny down --purge
	// and a failed bring-up call Remove. spec: §17.4 (--purge removes the
	// persisted substrate and the imported-image store).
	Remove() error
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
	// GatewayHost returns the hostname an in-cluster agent pod uses to
	// reach a host process (the gateway and its §8.6/§9.1 GatewayControl
	// listener). It is substrate-specific: the Linux child-process
	// launcher runs k3s on the host, so a pod reaches the host gateway at
	// loopback (127.0.0.1); the Docker-backed launcher runs k3s inside the
	// Docker VM, so a pod reaches the host gateway at host.docker.internal,
	// the Docker-Desktop alias for the host. The stack combines this host
	// with the gateway's gRPC host port to compute the §4.7 gateway↔adapter
	// callback address it stamps onto agent pods, so the substrate branch
	// stays confined to this provisioning layer and the §4.7
	// placement/adapter/mTLS business logic above it is byte-identical
	// across operating systems.
	//
	// spec: §17.4 (the substrate is provisioned per host operating system),
	// §4.7 (the gateway↔adapter gRPC+mTLS callback traverses the host/Docker
	// boundary; the in-cluster adapter dials the gateway at this host).
	GatewayHost() string
}

// Config configures the embedded k3s launcher.
type Config struct {
	// Dir holds the launcher's state: the downloaded k3s binary, the data
	// directory, and the generated kubeconfig. §17.4 places it at
	// ~/.lenny/k3s/.
	Dir string
	// APIPort is the host port the k3s API server is reachable on.
	APIPort int
	// GatewayNodePort is the fixed NodePort the development profile pins the
	// in-cluster gateway Service to. The Docker-backed launcher publishes it
	// to host loopback (-p 127.0.0.1:<nodePort>:<nodePort>) so the host-side
	// forwarder reaches the in-VM node port; the Linux launcher constrains
	// the kube-proxy NodePort bind to loopback instead (the node port is
	// already on the host's interfaces). Zero leaves the publish off. spec:
	// §17.4 (the CLI reaches the in-cluster gateway through the loopback-only
	// host-side forwarder in front of the node port; EMBEDDED_MODE_LOCAL_ONLY).
	GatewayNodePort int
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

// NewDockerLauncher constructs the Docker-backed launcher explicitly,
// independent of the host operating system. New selects it only on
// non-Linux hosts; this constructor lets the tier-2 bring-up test exercise
// the Docker-backed substrate (the macOS/Windows code path) on a
// Docker-equipped Linux CI host, where New would otherwise return the
// child-process launcher. The launcher is the same one New returns off
// Linux, so the test exercises the production substrate path rather than a
// test double.
//
// spec: §17.4 (on macOS and Windows the embedded k3s runs as a Docker-
// backed container; the same launcher provisions it).
func NewDockerLauncher(cfg Config) Launcher {
	return newDockerLauncher(cfg.withDefaults())
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
