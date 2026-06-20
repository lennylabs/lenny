// SPDX-License-Identifier: MIT

package k3s

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// dockerk3s.go is the Docker-backed launcher: on macOS and Windows the
// embedded k3s runs as a container under Docker Desktop's Linux VM
// rather than as a host child process, because k3s needs a Linux kernel
// the host cannot provide directly. The launcher runs the same pinned
// k3s version (Version) with the same cluster-disabling flag set as the
// Linux child-process launcher, so the embedded cluster is the same k3s
// distribution on every host.
//
// The container is started with `docker run rancher/k3s:<Version>`, the
// k3s API server is published on a host port, and the in-container admin
// kubeconfig is extracted and rewritten so its server URL points at
// https://127.0.0.1:<hostPort>. Host-process controllers and the gateway
// resolve their cluster connection from that rewritten kubeconfig through
// KUBECONFIG, so they reach the in-container API server across the
// host/Docker boundary. The substrate-specific --bind-address and
// --rootless flags the Linux child-process launcher uses do not apply to
// the container path: the API server binds the container's interfaces
// (published to the host port) and the container runs as the image's
// privileged k3s server, so the launcher omits both.
//
// This file compiles on every GOOS so New can reference it from the
// cross-platform selection in launcher.go, but the launcher is selected
// only on non-Linux hosts. The launcher shells out to the `docker` CLI,
// matching the established pattern in pkg/embedded/localcli/image.go; the
// added prerequisite is exactly Docker.
//
// spec: §17.4 (on macOS and Windows the embedded k3s runs as a container
// under Docker Desktop's Linux VM with the identical cluster-disabling
// flag set), §24.19 (lenny up/down manage the substrate).

// containerImage is the rancher/k3s image the Docker-backed launcher
// pulls and runs. It pins the same k3s version as the Linux
// child-process launcher (Version) so the embedded cluster is the same
// k3s distribution on every host.
const containerImage = "rancher/k3s:" + Version

// containerKubeconfigPath is where k3s writes the admin kubeconfig inside
// the rancher/k3s container. The launcher reads it out with
// `docker exec ... cat` and rewrites its server URL for host access.
const containerKubeconfigPath = "/etc/rancher/k3s/k3s.yaml"

// dockerLauncher provisions the embedded k3s as a Docker container. It
// implements Launcher for non-Linux hosts. The container name is derived
// from the state directory so a lenny up and a lenny down address the
// same container deterministically.
type dockerLauncher struct {
	cfg Config
	// name is the docker container name. It is the handle Stop, Running,
	// and the kubeconfig extraction address the container by, in place of
	// the host PID the child-process launcher uses.
	name string
	// runDocker runs the docker CLI and returns its combined output. It is
	// a field so unit tests can substitute a fake without invoking docker.
	runDocker func(ctx context.Context, args ...string) ([]byte, error)
}

// newDockerLauncher constructs the Docker-backed launcher. The caller has
// already applied Config defaults via withDefaults.
func newDockerLauncher(cfg Config) Launcher {
	return &dockerLauncher{
		cfg:       cfg,
		name:      containerName(cfg.Dir),
		runDocker: runDocker,
	}
}

// KubeconfigPath returns the path the launcher writes the rewritten admin
// kubeconfig to once the container is up. The host-process controllers
// and gateway resolve their cluster connection from this file, so the
// path is the same convention the Linux launcher uses.
func (d *dockerLauncher) KubeconfigPath() string {
	return filepath.Join(d.cfg.Dir, "kubeconfig.yaml")
}

// Start provisions the k3s container. It removes any stale container
// under the same name, runs a new detached container that publishes the
// API server on the host port with the same cluster-disabling flag set
// the Linux launcher builds, waits for the in-container API server to
// write its kubeconfig, then extracts that kubeconfig and rewrites its
// server URL to the published host port. On an unsupported host (a
// non-Linux host without Docker) it fails closed with the platform
// diagnostic so the stack routes around the absent cluster.
func (d *dockerLauncher) Start(ctx context.Context) error {
	if !SupportedPlatform() {
		return unsupportedPlatformError()
	}
	if err := os.MkdirAll(d.cfg.Dir, 0o755); err != nil {
		return fmt.Errorf("embedded k3s: create %s: %w", d.cfg.Dir, err)
	}
	// Remove a stale container left by a prior run so the run below does
	// not collide on the container name. A docker rm of an absent
	// container is benign here; the error is ignored deliberately.
	_, _ = d.runDocker(ctx, "rm", "-f", d.name)

	runCtx, cancel := context.WithTimeout(ctx, d.cfg.DownloadTimeout)
	defer cancel()
	if out, err := d.runDocker(runCtx, d.runArgs()...); err != nil {
		return fmt.Errorf("embedded k3s: docker run k3s: %w: %s", err, bytes.TrimSpace(out))
	}
	if err := d.waitReady(ctx); err != nil {
		_ = d.Stop()
		return err
	}
	if err := d.extractKubeconfig(ctx); err != nil {
		_ = d.Stop()
		return err
	}
	return nil
}

// runArgs builds the `docker run` argv for the k3s container. It runs the
// container detached and privileged (k3s needs kernel namespaces its
// embedded container runtime sets up), names it deterministically,
// publishes the in-container API port to the configured host port, and
// passes the same cluster-disabling server flags the Linux launcher uses.
// The --bind-address and --rootless flags are omitted: they are
// substrate-specific to the Linux host process and do not apply to the
// container, whose API server is reached through the published host port.
//
// spec: §17.4 (the same pinned k3s version runs as a container under
// Docker Desktop's Linux VM with the identical cluster-disabling flag
// set).
func (d *dockerLauncher) runArgs() []string {
	return append([]string{
		"run", "-d",
		"--privileged",
		"--name", d.name,
		// Publish the in-container k3s API port to the host port so
		// host-process controllers and the gateway reach the API server
		// at 127.0.0.1:<APIPort>.
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", d.cfg.APIPort, d.cfg.APIPort),
		containerImage,
	}, d.serverArgs()...)
}

// serverArgs builds the k3s server argv passed to the container. It is
// the same cluster-disabling flag set the Linux child-process launcher
// builds, minus the host-process-specific --bind-address and --rootless
// flags. The API server listens on the configured port inside the
// container; that port is published to the matching host port by runArgs.
//
// spec: §17.4 (identical cluster-disabling flag set across launchers).
func (d *dockerLauncher) serverArgs() []string {
	return []string{
		"server",
		"--https-listen-port", fmt.Sprintf("%d", d.cfg.APIPort),
		"--disable", "traefik",
		"--disable", "servicelb",
		"--disable-cloud-controller",
		"--disable-network-policy",
		"--flannel-backend", "host-gw",
	}
}

// waitReady blocks until the in-container k3s has written its admin
// kubeconfig or ReadyTimeout elapses. k3s writes the kubeconfig once the
// API server is serving, so the file's presence marks readiness. The
// check reads the in-container path with `docker exec cat`; it tolerates
// the transient errors while k3s is still bootstrapping and the file is
// absent.
func (d *dockerLauncher) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(d.cfg.ReadyTimeout)
	for {
		out, err := d.runDocker(ctx, "exec", d.name, "cat", containerKubeconfigPath)
		if err == nil && len(bytes.TrimSpace(out)) > 0 {
			return nil
		}
		if !d.Running() {
			return fmt.Errorf("embedded k3s: container %s exited before becoming ready", d.name)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("embedded k3s: API server not ready within %s (container %s)", d.cfg.ReadyTimeout, d.name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// extractKubeconfig reads the in-container admin kubeconfig, rewrites its
// server URL to the published host port, and writes it to KubeconfigPath
// with 0600 permissions. Host-process controllers and the gateway resolve
// their cluster connection from the rewritten file.
func (d *dockerLauncher) extractKubeconfig(ctx context.Context) error {
	out, err := d.runDocker(ctx, "exec", d.name, "cat", containerKubeconfigPath)
	if err != nil {
		return fmt.Errorf("embedded k3s: read in-container kubeconfig: %w: %s", err, bytes.TrimSpace(out))
	}
	rewritten := rewriteKubeconfigServer(string(out), d.cfg.APIPort)
	if err := os.WriteFile(d.KubeconfigPath(), []byte(rewritten), 0o600); err != nil {
		return fmt.Errorf("embedded k3s: write kubeconfig %s: %w", d.KubeconfigPath(), err)
	}
	return nil
}

// Stop removes the k3s container. Stop is idempotent and safe to call
// before Start: a docker rm of an absent container is benign, so the
// error is ignored.
func (d *dockerLauncher) Stop() error {
	_, _ = d.runDocker(context.Background(), "rm", "-f", d.name)
	return nil
}

// Running reports whether the k3s container is alive. It inspects the
// container's running state through docker; any docker error (absent
// container, docker unavailable) reports not-running.
func (d *dockerLauncher) Running() bool {
	out, err := d.runDocker(context.Background(), "inspect", "-f", "{{.State.Running}}", d.name)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// PID returns zero: the Docker-backed k3s runs inside the Docker VM
// rather than as a host process, so there is no host PID for it. The
// container is addressed by name (see Stop and Running) rather than by
// host PID.
func (d *dockerLauncher) PID() int {
	return 0
}

// containerNameUnsafe matches characters docker rejects in a container
// name. A container name must match [a-zA-Z0-9][a-zA-Z0-9_.-]*, so the
// state-directory-derived name is sanitized through this pattern.
var containerNameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// containerName derives a deterministic, docker-valid container name from
// the launcher's state directory so a lenny up and a later lenny down or
// lenny status address the same container. Path separators and other
// characters docker rejects are replaced with a hyphen.
func containerName(dir string) string {
	base := filepath.Base(filepath.Clean(dir))
	base = containerNameUnsafe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-_.")
	if base == "" {
		base = "k3s"
	}
	return "lenny-embedded-k3s-" + base
}

// serverURLPattern matches the `server:` line of a kubeconfig cluster
// entry and captures the leading whitespace so the rewrite preserves
// indentation. k3s writes the in-container server URL as
// https://127.0.0.1:<in-container-port> (or https://<host>:<port>); the
// rewrite replaces the whole URL with the published host-port URL.
var serverURLPattern = regexp.MustCompile(`(?m)^(\s*server:\s*).*$`)

// rewriteKubeconfigServer rewrites every `server:` line of a kubeconfig
// to point at https://127.0.0.1:<hostPort>. The in-container k3s
// kubeconfig names the API server at its in-container address, which a
// host process cannot reach; the published host port is reachable at
// loopback. The function is pure so it is unit-tested directly.
//
// spec: §17.4 (the in-container kubeconfig server URL is rewritten so
// host-process controllers and the gateway reach the API server via the
// published host port).
func rewriteKubeconfigServer(kubeconfig string, hostPort int) string {
	replacement := fmt.Sprintf("${1}https://127.0.0.1:%d", hostPort)
	return serverURLPattern.ReplaceAllString(kubeconfig, replacement)
}

// runDocker runs the docker CLI with the given arguments and returns its
// combined stdout and stderr. It shells out to the `docker` binary,
// matching pkg/embedded/localcli/image.go; the combined output lets a
// caller surface docker's diagnostic on failure. The context bounds the
// invocation so a hung docker call honors cancellation and deadlines.
func runDocker(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd.CombinedOutput()
}
