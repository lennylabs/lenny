// SPDX-License-Identifier: MIT

//go:build unix

package k3s

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// childk3s_unix.go is the Linux launcher: it supervises k3s as a managed
// child process. The single-node k3s distribution bundles a Linux
// container runtime and an embedded datastore, so this launcher runs
// only where a Linux kernel is present (the unix build tag; macOS and
// Windows provision the substrate through the Docker-backed launcher
// instead). On a first lenny up the pinned k3s binary is downloaded into
// the Embedded Mode state directory and started as a child process;
// Stop terminates it.
//
// In-process embedding of k3s is not feasible: k3s links a container
// runtime that requires Linux kernel namespaces and a root or
// rootless-capable environment. The child-process model keeps the
// production controllers and gateway pointed at a real Kubernetes API
// server through a generated kubeconfig. The OS-specific process-group
// start and tree termination live in the build-tagged process-control
// substrate (process_unix.go), so this body carries no raw syscall.
//
// spec: §17.4 (the embedded substrate is a managed k3s child process on
// Linux), §24.19 (lenny up/down manage the supervised child process).

// childSupervisor manages the k3s child process. It implements Launcher
// for the Linux host.
type childSupervisor struct {
	cfg     Config
	cmd     *exec.Cmd
	logFile *os.File
	// httpGet fetches the pinned k3s binary. It is a field so a unit test
	// can substitute a fake server without reaching GitHub releases,
	// mirroring the runDocker injection on the Docker-backed launcher.
	httpGet func(req *http.Request) (*http.Response, error)
}

// newChildLauncher constructs the Linux child-process launcher. The
// caller has already applied Config defaults via withDefaults.
func newChildLauncher(cfg Config) Launcher {
	return &childSupervisor{cfg: cfg, httpGet: http.DefaultClient.Do}
}

// BinaryPath returns the path the k3s binary is stored at.
func (s *childSupervisor) BinaryPath() string {
	return filepath.Join(s.cfg.Dir, "k3s")
}

// KubeconfigPath returns the path k3s writes its admin kubeconfig to.
// The controllers and gateway resolve their cluster connection from
// this file through the KUBECONFIG environment variable.
func (s *childSupervisor) KubeconfigPath() string {
	return filepath.Join(s.cfg.Dir, "kubeconfig.yaml")
}

// LogPath returns the path the supervisor tees the k3s process output
// to. lenny logs k3s reads this file.
func (s *childSupervisor) LogPath() string {
	return filepath.Join(s.cfg.Dir, "k3s.log")
}

// GatewayHost returns the host an in-cluster pod uses to reach the host
// gateway. The Linux launcher runs k3s on the host, so a pod reaches the
// host gateway at loopback. spec: §4.7, §17.4.
func (s *childSupervisor) GatewayHost() string {
	return loopbackHost
}

// EnsureBinary downloads the pinned k3s binary into the state
// directory when it is absent. It is a no-op when the binary is
// already present.
func (s *childSupervisor) EnsureBinary(ctx context.Context) error {
	if !SupportedPlatform() {
		return unsupportedPlatformError()
	}
	path := s.BinaryPath()
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
		return nil
	}
	if err := os.MkdirAll(s.cfg.Dir, 0o755); err != nil {
		return fmt.Errorf("embedded k3s: create %s: %w", s.cfg.Dir, err)
	}
	dlCtx, cancel := context.WithTimeout(ctx, s.cfg.DownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, downloadURL(), nil)
	if err != nil {
		return fmt.Errorf("embedded k3s: build download request: %w", err)
	}
	resp, err := s.httpGet(req)
	if err != nil {
		return fmt.Errorf("embedded k3s: download %s: %w", downloadURL(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("embedded k3s: download %s: HTTP %d", downloadURL(), resp.StatusCode)
	}
	// Write to a temp file and rename so a partial download is never
	// observed as a complete binary.
	tmp := path + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("embedded k3s: create %s: %w", tmp, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("embedded k3s: write binary: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("embedded k3s: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("embedded k3s: install binary: %w", err)
	}
	return nil
}

// Start downloads the k3s binary when absent and starts the k3s server
// as a child process. It waits for the kubeconfig to be written and
// for the API server to accept connections, up to ReadyTimeout.
func (s *childSupervisor) Start(ctx context.Context) error {
	if !SupportedPlatform() {
		return unsupportedPlatformError()
	}
	if err := s.EnsureBinary(ctx); err != nil {
		return err
	}
	logFile, err := os.OpenFile(s.LogPath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("embedded k3s: open log %s: %w", s.LogPath(), err)
	}
	args := s.serverArgs(runningAsRoot())
	cmd := exec.Command(s.BinaryPath(), args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Put k3s in its own process group so the supervisor can terminate
	// the whole tree on Stop. The attributes are OS-specific and come
	// from the build-tagged process-control substrate.
	cmd.SysProcAttr = processGroupSysProcAttr()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("embedded k3s: start server: %w", err)
	}
	s.cmd = cmd
	s.logFile = logFile

	if err := s.waitReady(ctx); err != nil {
		_ = s.Stop()
		return err
	}
	return nil
}

// serverArgs builds the argv k3s server is invoked with. asRoot=false
// adds --rootless so a non-root supervisor uses k3s' rootless mode on
// hosts that support it (spec §17.4 line 160: "rootless where
// supported"). On a host without rootless prerequisites k3s itself
// reports the error in the log file.
//
// spec: §17.4 line 160 ("rootless where supported")
func (s *childSupervisor) serverArgs(asRoot bool) []string {
	args := []string{
		"server",
		"--data-dir", filepath.Join(s.cfg.Dir, "data"),
		"--write-kubeconfig", s.KubeconfigPath(),
		"--write-kubeconfig-mode", "0600",
		"--https-listen-port", fmt.Sprintf("%d", s.cfg.APIPort),
		"--bind-address", "127.0.0.1",
		"--disable", "traefik",
		"--disable", "servicelb",
		"--disable-cloud-controller",
		"--disable-network-policy",
		"--flannel-backend", "host-gw",
		// Constrain the kube-proxy NodePort bind to loopback. --bind-address
		// scopes only the API server; without this the in-cluster gateway's
		// NodePort would bind 0.0.0.0 on the Linux host's own interfaces and
		// expose the gateway at <host-ip>:<nodePort>, bypassing the
		// loopback-only host-side forwarder and violating the §17.4
		// EMBEDDED_MODE_LOCAL_ONLY fail-closed invariant. Restricting the
		// NodePort to 127.0.0.1/32 matches the Docker launcher's containment,
		// where the in-VM NodePort is published only on host loopback (C4).
		// spec: §17.4 (EMBEDDED_MODE_LOCAL_ONLY: the gateway is reachable on
		// host loopback only).
		"--kube-proxy-arg=nodeport-addresses=127.0.0.1/32",
	}
	if !asRoot {
		args = append(args, "--rootless")
	}
	return args
}

// waitReady blocks until the kubeconfig file exists or ReadyTimeout
// elapses. k3s writes the kubeconfig once the API server is serving.
func (s *childSupervisor) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(s.cfg.ReadyTimeout)
	for {
		if fi, err := os.Stat(s.KubeconfigPath()); err == nil && fi.Size() > 0 {
			return nil
		}
		if s.cmd != nil && s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
			return fmt.Errorf("embedded k3s: server exited before becoming ready (see %s)", s.LogPath())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("embedded k3s: API server not ready within %s (see %s)", s.cfg.ReadyTimeout, s.LogPath())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Stop terminates the k3s process group while persisting the data
// directory on disk, so a warm lenny up restarts k3s against the existing
// data directory (which holds the embedded containerd image store) rather
// than re-provisioning. Stop is idempotent. spec: §17.4 (lenny down
// persists the substrate and the imported-image store).
func (s *childSupervisor) Stop() error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	// Terminate the whole process tree: k3s spawns containerd and
	// kubelet children. The OS-specific group/job termination, with the
	// 20s grace-then-force escalation, lives in the build-tagged
	// process-control substrate.
	terminateProcessGroup(s.cmd, 20*time.Second)
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
	s.cmd = nil
	return nil
}

// Remove terminates the k3s process group. On the Linux child-process
// launcher the persisted state is the on-disk data directory, which
// lenny down --purge removes through purgeRoot after Remove terminates the
// process; Remove itself only stops the process, so it is equivalent to
// Stop here. The destroy-versus-persist distinction is in purgeRoot, not in
// the process termination. Remove is idempotent. spec: §17.4 (--purge
// removes the persisted substrate; the data-directory removal is purgeRoot's).
func (s *childSupervisor) Remove() error {
	return s.Stop()
}

// Running reports whether the supervised process is still alive.
func (s *childSupervisor) Running() bool {
	return s.cmd != nil && s.cmd.Process != nil &&
		(s.cmd.ProcessState == nil || !s.cmd.ProcessState.Exited())
}

// PID returns the process identifier of the supervised k3s server, or
// zero when it is not running.
func (s *childSupervisor) PID() int {
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}
