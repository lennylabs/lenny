// SPDX-License-Identifier: MIT

//go:build unix

package k3s

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
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
