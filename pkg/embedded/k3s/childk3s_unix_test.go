// SPDX-License-Identifier: MIT

//go:build unix

package k3s

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// childk3s_unix_test.go exercises the Linux child-process launcher's
// concrete surface (childk3s_unix.go), which is build-tagged unix-only.
// The cross-platform launcher selection, SupportedPlatform gating, and
// Launcher-interface behavior are covered in k3s_test.go.
//
// spec: §17.4 (the embedded substrate is a managed k3s child process on
// Linux), §24.19 (lenny up/down manage the supervised child process).

// newChild constructs the child-process launcher directly so a test can
// reach the concrete BinaryPath/LogPath/serverArgs/EnsureBinary surface
// that is not part of the Launcher interface.
func newChild(t *testing.T) *childSupervisor {
	t.Helper()
	s, ok := newChildLauncher(Config{Dir: t.TempDir()}.withDefaults()).(*childSupervisor)
	if !ok {
		t.Fatalf("newChildLauncher returned %T, want *childSupervisor on a unix build", s)
	}
	return s
}

// spec: §17.4 (New selects the managed child-process launcher on Linux).
// On Linux New returns the concrete child supervisor; on a non-Linux unix
// host (such as darwin) it returns the Docker-backed launcher instead, so
// this assertion is gated to linux.
func TestNewSelectsChildLauncherOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only linux selects the managed child-process launcher")
	}
	l := New(Config{Dir: t.TempDir()})
	if _, ok := l.(*childSupervisor); !ok {
		t.Errorf("New on linux returned %T, want *childSupervisor", l)
	}
}

func TestChildSupervisorPaths(t *testing.T) {
	s := newChild(t)
	dir := s.cfg.Dir
	if s.BinaryPath() != filepath.Join(dir, "k3s") {
		t.Errorf("BinaryPath = %q", s.BinaryPath())
	}
	if s.KubeconfigPath() != filepath.Join(dir, "kubeconfig.yaml") {
		t.Errorf("KubeconfigPath = %q", s.KubeconfigPath())
	}
	if s.LogPath() != filepath.Join(dir, "k3s.log") {
		t.Errorf("LogPath = %q", s.LogPath())
	}
}

func TestChildSupervisorDefaultsAPIPort(t *testing.T) {
	s := newChild(t)
	// The default API port is the k3s convention, 6443.
	if s.cfg.APIPort != 6443 {
		t.Errorf("default APIPort = %d, want 6443", s.cfg.APIPort)
	}
}

// spec: §4.7 (the gateway↔adapter callback host), §17.4. The Linux
// child-process launcher runs k3s on the host, so an in-cluster pod
// reaches the host gateway at loopback rather than the
// host.docker.internal alias the Docker-backed launcher uses. GatewayHost
// returns the substrate-specific host the stack joins to the gateway gRPC
// port to compute the §4.7 callback address it stamps onto pods.
func TestChildSupervisorGatewayHostIsLoopback(t *testing.T) {
	s := newChild(t)
	if got := s.GatewayHost(); got != "127.0.0.1" {
		t.Errorf("childSupervisor.GatewayHost() = %q, want 127.0.0.1 (k3s runs on the host)", got)
	}
}

// TestChildEnsureBinaryRejectsUnsupportedPlatform covers the
// platform gate on the child launcher's EnsureBinary. On a non-Linux
// unix host (a unix build that is not linux, such as darwin) the child
// launcher cannot download and run k3s, so EnsureBinary must fail rather
// than fetch a binary it cannot execute. On linux the platform is
// supported, so the gate is skipped.
func TestChildEnsureBinaryRejectsUnsupportedPlatform(t *testing.T) {
	if SupportedPlatform() {
		t.Skip("host supports the embedded substrate; this test covers the unsupported-platform gate")
	}
	s := newChild(t)
	if err := s.EnsureBinary(context.Background()); err == nil {
		t.Error("expected EnsureBinary to fail on an unsupported platform")
	}
}

// spec: §17.4 line 160 — "k3s (single-node, rootless where supported)".
// When running as non-root the supervisor passes --rootless so k3s
// picks its rootless mode on supported hosts; when running as root it
// omits the flag so root-mode k3s starts normally.
func TestServerArgsRootlessGating_spec_17_4_160(t *testing.T) {
	s := newChild(t)

	rootArgs := s.serverArgs(true)
	for _, a := range rootArgs {
		if a == "--rootless" {
			t.Fatalf("root invocation must omit --rootless, got %v", rootArgs)
		}
	}

	nonRootArgs := s.serverArgs(false)
	found := false
	for _, a := range nonRootArgs {
		if a == "--rootless" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("non-root invocation must include --rootless, got %v", nonRootArgs)
	}
}

// TestChildSupervisorHandleBeforeStart covers the child launcher's handle
// methods on a supervisor that was never started: PID is zero, Running is
// false, and Stop is a no-op. lenny status and lenny down probe these before
// or after a launch, so they must not panic on a nil cmd.
//
// spec: §24.19 (lenny up/down/status manage the supervised child process).
func TestChildSupervisorHandleBeforeStart(t *testing.T) {
	s := newChild(t)
	if s.PID() != 0 {
		t.Errorf("PID before Start = %d, want 0", s.PID())
	}
	if s.Running() {
		t.Error("Running before Start = true, want false")
	}
	if err := s.Stop(); err != nil {
		t.Errorf("Stop before Start errored: %v", err)
	}
}

// TestChildStartSpawnsAndFailsReadiness covers the Start path up to the
// readiness wait: with an injected fetch installing a fake k3s that exits
// without writing a kubeconfig, Start downloads the binary, spawns the
// process, blocks in waitReady, times out, and tears the process down,
// returning the readiness error. This pins the download-spawn-cleanup
// sequence without a real k3s server or cluster; the success path (a real
// k3s writing a kubeconfig and serving the API) is exercised by the tier-4
// embedded smoke test on Linux.
//
// spec: §17.4 (Start downloads and supervises the k3s child process and
// waits for the API server to be reachable), §24.19.
func TestChildStartSpawnsAndFailsReadiness_spec_17_4(t *testing.T) {
	if !SupportedPlatform() {
		t.Skip("host does not support the child launcher; Start rejects on the platform gate")
	}
	s := newChild(t)
	// A fake k3s that exits 0 immediately and never writes a kubeconfig, so
	// waitReady observes the process exit and returns before the timeout.
	s.httpGet = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("#!/bin/sh\nexit 0\n")),
		}, nil
	}
	s.cfg.ReadyTimeout = 1 * time.Second
	err := s.Start(context.Background())
	if err == nil {
		t.Fatal("Start with a fake k3s that never serves = nil, want a readiness error")
	}
	// Start tore the spawned process down on failure, so the supervisor is
	// no longer running.
	if s.Running() {
		t.Error("Start left the k3s process running after a readiness failure")
	}
}

// TestChildSupervisorStopTerminatesRunningProcess covers the Stop, Running,
// and PID methods against a live child process: a running supervisor reports
// a positive PID and Running()=true, and Stop terminates the process tree so
// Running goes false. A sleeper stands in for the k3s process tree so the
// termination path is pinned without downloading and starting real k3s.
//
// spec: §24.19 (lenny down terminates the supervised k3s child process tree).
func TestChildSupervisorStopTerminatesRunningProcess(t *testing.T) {
	s := newChild(t)
	cmd := exec.Command("sleep", "120")
	cmd.SysProcAttr = processGroupSysProcAttr()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleeper: %v", err)
	}
	s.cmd = cmd
	pid := s.PID()
	if pid <= 0 {
		t.Fatalf("PID for a running child = %d, want positive", pid)
	}
	if !s.Running() {
		t.Fatal("Running for a running child = false, want true")
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if s.Running() {
		t.Errorf("Running after Stop = true (pid %d), want false", pid)
	}
	// Stop is idempotent.
	if err := s.Stop(); err != nil {
		t.Errorf("second Stop errored: %v", err)
	}
}

// TestChildEnsureBinaryNoOpWhenPresent covers the EnsureBinary fast path:
// when the pinned k3s binary already exists it is a no-op that performs no
// download. A pre-seeded binary file makes the host-platform gate
// irrelevant, so this runs on any unix host (including a Docker-absent
// darwin where SupportedPlatform is false), provided the gate passes; on a
// host where the platform is unsupported EnsureBinary returns the platform
// diagnostic before reaching the fast path, so the test is gated on
// SupportedPlatform.
//
// spec: §17.4 (the pinned k3s binary is downloaded on first lenny up and
// reused thereafter).
func TestChildEnsureBinaryNoOpWhenPresent(t *testing.T) {
	if !SupportedPlatform() {
		t.Skip("host does not support the child launcher; the platform gate precedes the fast path")
	}
	s := newChild(t)
	// Seed a regular file at the binary path so the present-binary branch is
	// taken and no network download is attempted.
	if err := os.WriteFile(s.BinaryPath(), []byte("k3s\n"), 0o755); err != nil {
		t.Fatalf("seed k3s binary: %v", err)
	}
	if err := s.EnsureBinary(context.Background()); err != nil {
		t.Errorf("EnsureBinary with the binary present = %v, want nil (no download)", err)
	}
}

// TestChildEnsureBinaryMkdirError covers the create-directory error branch
// of EnsureBinary: when the state directory cannot be created (its parent is
// a regular file), EnsureBinary returns a wrapped error rather than
// attempting a download into a path it cannot create.
//
// spec: §17.4 (the pinned k3s binary is downloaded into the state directory
// on first lenny up).
func TestChildEnsureBinaryMkdirError(t *testing.T) {
	if !SupportedPlatform() {
		t.Skip("host does not support the child launcher; the platform gate precedes the mkdir branch")
	}
	// A regular file standing where the state directory's parent must be a
	// directory makes MkdirAll fail.
	base := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(base, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	s, ok := newChildLauncher(Config{Dir: filepath.Join(base, "k3s")}.withDefaults()).(*childSupervisor)
	if !ok {
		t.Fatalf("newChildLauncher returned %T, want *childSupervisor", s)
	}
	if err := s.EnsureBinary(context.Background()); err == nil {
		t.Error("EnsureBinary into an uncreatable directory = nil, want an error")
	}
}

// TestChildEnsureBinaryDownloadError covers the download-failure branch of
// EnsureBinary: when the HTTP fetch of the pinned k3s binary fails,
// EnsureBinary returns a wrapped error and installs no partial binary. The
// fetch is injected so no real network request is attempted.
//
// spec: §17.4 (the pinned k3s binary download bounds the first lenny up).
func TestChildEnsureBinaryDownloadError(t *testing.T) {
	if !SupportedPlatform() {
		t.Skip("host does not support the child launcher; the platform gate precedes the download branch")
	}
	s := newChild(t)
	s.httpGet = func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	}
	if err := s.EnsureBinary(context.Background()); err == nil {
		t.Error("EnsureBinary with a failing fetch = nil, want an error")
	}
	// No partial binary is left at the target path.
	if _, err := os.Stat(s.BinaryPath()); err == nil {
		t.Error("EnsureBinary left a binary at the target path after a failed download")
	}
}

// TestChildEnsureBinaryDownloadsAndInstalls covers the EnsureBinary success
// path with an injected HTTP fetch: when the binary is absent, EnsureBinary
// streams the response body into the target path atomically (temp file then
// rename) and the installed binary is executable. The injected fetch returns
// a fake k3s payload so no GitHub release is reached, mirroring the
// runDocker injection on the Docker-backed launcher.
//
// spec: §17.4 (the pinned k3s binary is downloaded on first lenny up into
// the state directory).
func TestChildEnsureBinaryDownloadsAndInstalls_spec_17_4(t *testing.T) {
	if !SupportedPlatform() {
		t.Skip("host does not support the child launcher; the platform gate precedes the download")
	}
	s := newChild(t)
	const payload = "#!/bin/sh\n# fake k3s\n"
	s.httpGet = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
		}, nil
	}
	if err := s.EnsureBinary(context.Background()); err != nil {
		t.Fatalf("EnsureBinary with an injected fetch: %v", err)
	}
	got, err := os.ReadFile(s.BinaryPath())
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if string(got) != payload {
		t.Errorf("installed binary = %q, want the downloaded payload", string(got))
	}
	fi, err := os.Stat(s.BinaryPath())
	if err != nil {
		t.Fatalf("stat installed binary: %v", err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("installed binary mode = %v, want the owner-execute bit set", fi.Mode().Perm())
	}
	// No partial file lingers after a successful install.
	if _, err := os.Stat(s.BinaryPath() + ".partial"); err == nil {
		t.Error("EnsureBinary left a .partial file after a successful install")
	}
}

// TestChildEnsureBinaryRejectsNon200 covers the non-OK HTTP status branch:
// a download that answers a non-200 status returns an error and installs no
// binary, so a server-side failure does not leave a corrupt k3s in place.
//
// spec: §17.4 (the pinned k3s binary download).
func TestChildEnsureBinaryRejectsNon200(t *testing.T) {
	if !SupportedPlatform() {
		t.Skip("host does not support the child launcher; the platform gate precedes the download")
	}
	s := newChild(t)
	s.httpGet = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
		}, nil
	}
	if err := s.EnsureBinary(context.Background()); err == nil {
		t.Error("EnsureBinary on a 404 download = nil, want an error")
	}
	if _, err := os.Stat(s.BinaryPath()); err == nil {
		t.Error("EnsureBinary installed a binary despite a non-200 download")
	}
}

// TestChildWaitReady covers waitReady, the readiness loop Start blocks on:
// it returns nil once k3s writes a non-empty kubeconfig, and it returns a
// timeout error when the kubeconfig never appears. The kubeconfig file is
// written directly rather than by a real k3s server so the loop is pinned
// without a cluster.
//
// spec: §17.4 (lenny up waits for the API server to be reachable before
// reporting the stack ready), §24.19.
func TestChildWaitReady(t *testing.T) {
	t.Run("ready_when_kubeconfig_written", func(t *testing.T) {
		s := newChild(t)
		s.cfg.ReadyTimeout = 5 * time.Second
		if err := os.WriteFile(s.KubeconfigPath(), []byte("apiVersion: v1\n"), 0o600); err != nil {
			t.Fatalf("write kubeconfig: %v", err)
		}
		if err := s.waitReady(context.Background()); err != nil {
			t.Errorf("waitReady with a written kubeconfig = %v, want nil", err)
		}
	})
	t.Run("times_out_without_kubeconfig", func(t *testing.T) {
		s := newChild(t)
		s.cfg.ReadyTimeout = 200 * time.Millisecond
		err := s.waitReady(context.Background())
		if err == nil {
			t.Fatal("waitReady with no kubeconfig = nil, want a timeout error")
		}
	})
	t.Run("honors_context_cancel", func(t *testing.T) {
		s := newChild(t)
		s.cfg.ReadyTimeout = 10 * time.Second
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.waitReady(ctx); err == nil {
			t.Error("waitReady with a cancelled context = nil, want the context error")
		}
	})
}
