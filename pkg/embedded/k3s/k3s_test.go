// SPDX-License-Identifier: MIT

package k3s

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSupportedPlatform(t *testing.T) {
	// k3s requires Linux: the supervisor must report support only on
	// Linux hosts.
	want := runtime.GOOS == "linux"
	if SupportedPlatform() != want {
		t.Errorf("SupportedPlatform() = %v, want %v on %s", SupportedPlatform(), want, runtime.GOOS)
	}
}

func TestDownloadURLByArch(t *testing.T) {
	url := downloadURL()
	if !strings.Contains(url, Version) {
		t.Errorf("download URL %q does not pin the k3s version", url)
	}
	// k3s publishes a bare k3s binary for amd64 and k3s-arm64 for
	// arm64.
	switch runtime.GOARCH {
	case "arm64":
		if !strings.HasSuffix(url, "k3s-arm64") {
			t.Errorf("arm64 download URL %q should end in k3s-arm64", url)
		}
	case "amd64":
		if !strings.HasSuffix(url, "/k3s") {
			t.Errorf("amd64 download URL %q should end in /k3s", url)
		}
	}
}

func TestSupervisorPaths(t *testing.T) {
	dir := t.TempDir()
	s := New(Config{Dir: dir})
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

func TestSupervisorDefaultsAPIPort(t *testing.T) {
	s := New(Config{Dir: t.TempDir()})
	// The default API port is the k3s convention, 6443.
	if s.cfg.APIPort != 6443 {
		t.Errorf("default APIPort = %d, want 6443", s.cfg.APIPort)
	}
}

func TestStartRejectsUnsupportedPlatform(t *testing.T) {
	if SupportedPlatform() {
		t.Skip("host supports k3s; this test covers the unsupported-platform path")
	}
	s := New(Config{Dir: t.TempDir()})
	// On a non-Linux host Start must fail fast with a clear message
	// rather than attempting a download.
	err := s.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to fail on an unsupported platform")
	}
	if !strings.Contains(err.Error(), "requires Linux") {
		t.Errorf("error %q does not explain the Linux requirement", err.Error())
	}
}

func TestStopBeforeStartIsNoOp(t *testing.T) {
	s := New(Config{Dir: t.TempDir()})
	if err := s.Stop(); err != nil {
		t.Errorf("Stop before Start errored: %v", err)
	}
	if s.Running() {
		t.Error("Running reported true before Start")
	}
	if s.PID() != 0 {
		t.Errorf("PID = %d before Start, want 0", s.PID())
	}
}

func TestEnsureBinaryRejectsUnsupportedPlatform(t *testing.T) {
	if SupportedPlatform() {
		t.Skip("host supports k3s; this test covers the unsupported-platform path")
	}
	s := New(Config{Dir: t.TempDir()})
	if err := s.EnsureBinary(context.Background()); err == nil {
		t.Error("expected EnsureBinary to fail on an unsupported platform")
	}
}

// spec: §17.4 line 160 — "k3s (single-node, rootless where supported)".
// When running as non-root the supervisor passes --rootless so k3s
// picks its rootless mode on supported hosts; when running as root it
// omits the flag so root-mode k3s starts normally.
func TestServerArgsRootlessGating_spec_17_4_160(t *testing.T) {
	s := New(Config{Dir: t.TempDir()})

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
