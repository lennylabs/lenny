// SPDX-License-Identifier: MIT

// Package k3s supervises the embedded Kubernetes layer for §17.4
// Embedded Mode. The single-node k3s distribution bundles a Linux
// container runtime and an embedded etcd-compatible datastore, so it
// runs only on Linux. On a first lenny up the k3s binary is downloaded
// into the Embedded Mode state directory and started as a managed
// child process; lenny down terminates it.
//
// In-process embedding of k3s is not feasible: k3s links a container
// runtime that requires Linux kernel namespaces and a root or
// rootless-capable environment. The child-process model keeps the
// production controllers and gateway pointed at a real Kubernetes API
// server through a generated kubeconfig.
//
// On a non-Linux host the Supervisor reports an unsupported platform
// rather than failing the whole stack: the embedded Postgres, Redis,
// OIDC, TLS, and gateway components still come up so a developer can
// exercise the storage and identity paths.
package k3s

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// Version is the k3s release the embedded stack downloads. It is
// pinned so a lenny up is reproducible.
const Version = "v1.31.4+k3s1"

// SupportedPlatform reports whether the host operating system can run
// the embedded k3s server. k3s requires Linux.
func SupportedPlatform() bool {
	return runtime.GOOS == "linux"
}

// downloadURL returns the k3s release binary URL for the host
// architecture. k3s publishes a bare k3s binary for amd64 and a
// k3s-arm64 binary for arm64.
func downloadURL() string {
	asset := "k3s"
	if runtime.GOARCH == "arm64" {
		asset = "k3s-arm64"
	}
	return fmt.Sprintf("https://github.com/k3s-io/k3s/releases/download/%s/%s", Version, asset)
}

// Config configures the embedded k3s supervisor.
type Config struct {
	// Dir holds the downloaded k3s binary, the data directory, and the
	// generated kubeconfig. §17.4 places it at ~/.lenny/k3s/.
	Dir string
	// APIPort is the loopback port the k3s API server listens on.
	APIPort int
	// DownloadTimeout bounds the one-time binary download. Zero
	// defaults to 5 minutes.
	DownloadTimeout time.Duration
	// ReadyTimeout bounds the wait for the API server to become
	// reachable after the process starts. Zero defaults to 2 minutes.
	ReadyTimeout time.Duration
}

// Supervisor manages the k3s child process.
type Supervisor struct {
	cfg     Config
	cmd     *exec.Cmd
	logFile *os.File
}

// New constructs a Supervisor. The process is not started until Start
// is called.
func New(cfg Config) *Supervisor {
	if cfg.APIPort == 0 {
		cfg.APIPort = 6443
	}
	if cfg.DownloadTimeout <= 0 {
		cfg.DownloadTimeout = 5 * time.Minute
	}
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = 2 * time.Minute
	}
	return &Supervisor{cfg: cfg}
}

// BinaryPath returns the path the k3s binary is stored at.
func (s *Supervisor) BinaryPath() string {
	return filepath.Join(s.cfg.Dir, "k3s")
}

// KubeconfigPath returns the path k3s writes its admin kubeconfig to.
// The controllers and gateway resolve their cluster connection from
// this file through the KUBECONFIG environment variable.
func (s *Supervisor) KubeconfigPath() string {
	return filepath.Join(s.cfg.Dir, "kubeconfig.yaml")
}

// LogPath returns the path the supervisor tees the k3s process output
// to. lenny logs k3s reads this file.
func (s *Supervisor) LogPath() string {
	return filepath.Join(s.cfg.Dir, "k3s.log")
}

// EnsureBinary downloads the pinned k3s binary into the state
// directory when it is absent. It is a no-op when the binary is
// already present.
func (s *Supervisor) EnsureBinary(ctx context.Context) error {
	if !SupportedPlatform() {
		return fmt.Errorf("embedded k3s: unsupported platform %s/%s (k3s requires Linux)", runtime.GOOS, runtime.GOARCH)
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
	resp, err := http.DefaultClient.Do(req)
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
func (s *Supervisor) Start(ctx context.Context) error {
	if !SupportedPlatform() {
		return fmt.Errorf("embedded k3s: unsupported platform %s/%s (k3s requires Linux)", runtime.GOOS, runtime.GOARCH)
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
	// Put k3s in its own process group so the supervisor can signal
	// the whole tree on Stop.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
func (s *Supervisor) serverArgs(asRoot bool) []string {
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
	}
	if !asRoot {
		args = append(args, "--rootless")
	}
	return args
}

// runningAsRoot reports whether the current process is root. k3s
// rootless mode is only added when the supervisor itself is non-root.
func runningAsRoot() bool {
	return os.Geteuid() == 0
}

// waitReady blocks until the kubeconfig file exists or ReadyTimeout
// elapses. k3s writes the kubeconfig once the API server is serving.
func (s *Supervisor) waitReady(ctx context.Context) error {
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

// Stop terminates the k3s process group. Stop is idempotent.
func (s *Supervisor) Stop() error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	pid := s.cmd.Process.Pid
	// Signal the whole process group: k3s spawns containerd and
	// kubelet children.
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-done
	}
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
	s.cmd = nil
	return nil
}

// Running reports whether the supervised process is still alive.
func (s *Supervisor) Running() bool {
	return s.cmd != nil && s.cmd.Process != nil &&
		(s.cmd.ProcessState == nil || !s.cmd.ProcessState.Exited())
}

// PID returns the process identifier of the supervised k3s server, or
// zero when it is not running.
func (s *Supervisor) PID() int {
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}
