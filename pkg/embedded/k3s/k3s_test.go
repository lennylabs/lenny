// SPDX-License-Identifier: MIT

package k3s

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

// withLookPathDocker swaps the package's docker-resolution hook for the
// duration of a test so the Docker-present and Docker-absent platform
// paths can be exercised without touching the real PATH, then restores
// it. found=false simulates an absent docker binary.
func withLookPathDocker(t *testing.T, found bool) {
	t.Helper()
	prev := lookPathDocker
	t.Cleanup(func() { lookPathDocker = prev })
	if found {
		lookPathDocker = func() (string, error) { return "/usr/local/bin/docker", nil }
	} else {
		lookPathDocker = func() (string, error) { return "", errors.New("exec: \"docker\": not found in PATH") }
	}
}

// spec: §17.4 (the embedded k3s substrate is provisioned per host
// operating system: Linux unconditionally, macOS and Windows when Docker
// supplies the Linux VM). SupportedPlatform gates the launcher selection.
func TestSupportedPlatform(t *testing.T) {
	if runtime.GOOS == "linux" {
		// Linux is supported unconditionally; Docker availability is
		// irrelevant on Linux.
		withLookPathDocker(t, false)
		if !SupportedPlatform() {
			t.Errorf("SupportedPlatform() = false on linux, want true unconditionally")
		}
		return
	}
	// On a non-Linux host the platform is supported exactly when the
	// docker CLI is resolvable: Docker Desktop supplies the Linux VM the
	// embedded k3s runs in.
	withLookPathDocker(t, true)
	if !SupportedPlatform() {
		t.Errorf("SupportedPlatform() = false on %s with docker present, want true", runtime.GOOS)
	}
	withLookPathDocker(t, false)
	if SupportedPlatform() {
		t.Errorf("SupportedPlatform() = true on %s with docker absent, want false (fail closed)", runtime.GOOS)
	}
}

// spec: §17.4 (New selects the launcher by host operating system: a
// managed child process on Linux, a Docker-backed container on macOS and
// Windows). The non-Linux Docker launcher selection is asserted here; the
// Linux child-process launcher selection is asserted in the unix test
// file, where the concrete *childSupervisor type is in the build.
func TestNewSelectsDockerLauncherOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux selects the child-process launcher; covered by the unix test file")
	}
	// New always returns a usable Launcher regardless of Docker
	// availability; SupportedPlatform, not New, is the gate.
	withLookPathDocker(t, true)
	l := New(Config{Dir: t.TempDir()})
	if l == nil {
		t.Fatal("New returned nil launcher")
	}
	if _, ok := l.(*dockerLauncher); !ok {
		t.Errorf("New on %s returned %T, want *dockerLauncher", runtime.GOOS, l)
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

// TestConfigWithDefaults covers the shared Config defaults both launchers
// apply: the k3s-convention API port and the download/ready timeouts.
func TestConfigWithDefaults(t *testing.T) {
	got := Config{Dir: "/x"}.withDefaults()
	if got.APIPort != 6443 {
		t.Errorf("default APIPort = %d, want 6443", got.APIPort)
	}
	if got.DownloadTimeout <= 0 {
		t.Errorf("default DownloadTimeout = %v, want > 0", got.DownloadTimeout)
	}
	if got.ReadyTimeout <= 0 {
		t.Errorf("default ReadyTimeout = %v, want > 0", got.ReadyTimeout)
	}
	// An explicit value is preserved.
	explicit := Config{Dir: "/x", APIPort: 7000}.withDefaults()
	if explicit.APIPort != 7000 {
		t.Errorf("explicit APIPort overwritten: got %d, want 7000", explicit.APIPort)
	}
}

// spec: §17.4 (an unsupported host fails closed). On a non-Linux host
// without Docker, Start rejects the launch with a clear diagnostic that
// names the Docker prerequisite, rather than attempting a launch that
// cannot succeed. On Linux the platform is always supported, so this
// path is skipped.
func TestStartRejectsUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux is supported unconditionally; this test covers the Docker-absent non-Linux path")
	}
	withLookPathDocker(t, false)
	l := New(Config{Dir: t.TempDir()})
	err := l.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to fail on an unsupported (Docker-absent) non-Linux host")
	}
	if !strings.Contains(err.Error(), "Docker") {
		t.Errorf("error %q does not name the Docker prerequisite", err.Error())
	}
}

func TestStopBeforeStartIsNoOp(t *testing.T) {
	l := New(Config{Dir: t.TempDir()})
	if err := l.Stop(); err != nil {
		t.Errorf("Stop before Start errored: %v", err)
	}
	if l.Running() {
		t.Error("Running reported true before Start")
	}
	if l.PID() != 0 {
		t.Errorf("PID = %d before Start, want 0", l.PID())
	}
}

// TestKubeconfigPathConvention covers the shared kubeconfig-path
// convention: every launcher writes the admin kubeconfig to
// <Dir>/kubeconfig.yaml so the host-process controllers resolve their
// cluster connection the same way regardless of substrate.
func TestKubeconfigPathConvention(t *testing.T) {
	dir := t.TempDir()
	l := New(Config{Dir: dir})
	want := dir + "/kubeconfig.yaml"
	if l.KubeconfigPath() != want {
		t.Errorf("KubeconfigPath = %q, want %q", l.KubeconfigPath(), want)
	}
}
