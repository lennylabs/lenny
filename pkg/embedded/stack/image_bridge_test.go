// SPDX-License-Identifier: MIT

package stack

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spec: §24.19.1 (the image bridge reaches the embedded containerd image
// store), §17.4 (the substrate is provisioned per host operating system) —
// on the Linux managed-child-process launcher the bridge runs the host k3s
// binary against the host containerd socket, so the argv is a bare `ctr`
// invocation with no docker-exec prefix.
func TestCtrInvocationArgsHostPath(t *testing.T) {
	c := CtrInvocation{Binary: "/k3s", socket: "/sock"}
	got := c.Args("k8s.io", false, "images", "ls")
	want := []string{"ctr", "--address", "/sock", "--namespace", "k8s.io", "images", "ls"}
	assertArgs(t, "host ls", got, want)

	// A stdin-requesting subcommand on the host path does not add `exec -i`;
	// `-i` is a docker-exec concern only.
	gotImport := c.Args("k8s.io", true, "images", "import", "-")
	wantImport := []string{"ctr", "--address", "/sock", "--namespace", "k8s.io", "images", "import", "-"}
	assertArgs(t, "host import", gotImport, wantImport)
}

// spec: §24.19.1, §17.4 — on the Docker-backed launcher (macOS and
// Windows) the bridge runs the bundled `ctr` inside the k3s container via
// `docker exec`, addressing the in-container containerd socket. A
// tarball-streaming subcommand requests an interactive stdin (`exec -i`).
func TestCtrInvocationArgsDockerPath(t *testing.T) {
	c := CtrInvocation{Binary: "docker", socket: containerCtrSocket, container: "lenny-embedded-k3s-x"}

	// A non-streaming subcommand: no `-i`.
	got := c.Args("k8s.io", false, "images", "ls")
	want := []string{
		"exec", "lenny-embedded-k3s-x",
		"ctr", "--address", containerCtrSocket, "--namespace", "k8s.io", "images", "ls",
	}
	assertArgs(t, "docker ls", got, want)

	// A streaming import requests `-i` so the host tarball pipes into the
	// in-container ctr stdin.
	gotImport := c.Args("custom", true, "images", "import", "-")
	wantImport := []string{
		"exec", "-i", "lenny-embedded-k3s-x",
		"ctr", "--address", containerCtrSocket, "--namespace", "custom", "images", "import", "-",
	}
	assertArgs(t, "docker import", gotImport, wantImport)
}

// TestImportFromFileMissingTarballOnDockerPath covers the open-error branch
// of ImportFromFile on the Docker-backed substrate: the host tarball is
// streamed into the in-container ctr via stdin, so a missing tarball is
// reported as an open error before any docker exec runs. The host-binary
// branch (ctr.container == "") and the success path shell out to ctr, so
// they belong to the tier-2 bring-up rather than this unit; this test pins
// the fail-closed open-error path without a real container.
//
// diagnosis: a failure means the --file import path on the Docker-backed
// substrate no longer fails closed when the host tarball is absent, so a
// missing image silently no-ops instead of surfacing an open error.
//
// spec: §24.19.1 line 275 (the `--file <tar>` import path), §17.4 (the
// Docker-backed substrate streams the host tarball through `docker exec -i`).
func TestImportFromFileMissingTarballOnDockerPath_spec_24_19_1(t *testing.T) {
	ctr := CtrInvocation{Binary: "docker", socket: containerCtrSocket, container: "lenny-embedded-k3s-x"}
	missing := filepath.Join(t.TempDir(), "no-such-image.tar")
	var stdout, stderr bytes.Buffer
	code := ImportFromFile(ctr, "k8s.io", "acme/chat:v1", missing, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("ImportFromFile on a missing tarball = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "open") {
		t.Errorf("stderr = %q, want it to name the open failure", stderr.String())
	}
}

// TestImportFromFileHostPathRunError covers the host-binary file-import
// error branch of ImportFromFile (ctr.container == ""): when the ctr
// invocation fails (here because the binary path does not exist), the import
// reports a non-zero exit with a diagnostic rather than claiming success.
// This pins the host-path error handling without a real k3s ctr; the
// success path shells out to a live ctr and belongs to the tier-2 bring-up.
//
// diagnosis: a failure means the host-substrate --file import path no longer
// reports a non-zero exit when the ctr invocation fails, so a broken import
// is mistaken for success.
//
// spec: §24.19.1 line 275 (the `--file <tar>` import path on the host
// substrate).
func TestImportFromFileHostPathRunError_spec_24_19_1(t *testing.T) {
	tar := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(tar, []byte("not-a-real-tar"), 0o644); err != nil {
		t.Fatalf("seed tarball: %v", err)
	}
	// A host-path ctr invocation (empty container) whose binary does not
	// exist, so RunStreamed returns an exec error and the error branch runs.
	ctr := CtrInvocation{Binary: filepath.Join(t.TempDir(), "no-such-ctr"), socket: "/sock"}
	var stdout, stderr bytes.Buffer
	if code := ImportFromFile(ctr, "k8s.io", "acme/chat:v1", tar, &stdout, &stderr); code != 1 {
		t.Fatalf("ImportFromFile with an unrunnable ctr = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "lenny image import:") {
		t.Errorf("stderr = %q, want a lenny image import diagnostic", stderr.String())
	}
}

// TestImportFromHostDaemonSuggestsTarFallbackWhenDockerMissing covers the
// host-daemon import path: when `docker` is not on PATH, ImportFromHostDaemon
// surfaces a diagnostic pointing at the `--file <tar>` fallback rather than
// propagating the raw os/exec "executable file not found" message.
//
// diagnosis: a failure means the host-daemon import path no longer points an
// operator without Docker at the tarball fallback, leaving them with the raw
// os/exec not-found error.
//
// spec: §17.4 line 290, §24.19.1 line 274.
func TestImportFromHostDaemonSuggestsTarFallbackWhenDockerMissing_spec_24_19_1(t *testing.T) {
	origLookPath := lookPathDocker
	t.Cleanup(func() { lookPathDocker = origLookPath })
	lookPathDocker = func() (string, error) {
		return "", errors.New("exec: \"docker\": executable file not found in $PATH")
	}

	// A resolved host-path ctr invocation; the docker-missing branch fires
	// before the invocation runs, so its concrete fields do not matter.
	ctr := CtrInvocation{Binary: "/k3s", socket: "/sock"}
	var out, errb bytes.Buffer
	code := ImportFromHostDaemon(ctr, "k8s.io", "my-agent:dev", &out, &errb)
	if code != 1 {
		t.Errorf("docker-missing exit = %d, want 1", code)
	}
	got := errb.String()
	if !strings.Contains(got, "the `docker` binary is required") {
		t.Errorf("stderr = %q, want guidance about docker binary", got)
	}
	if !strings.Contains(got, "--file image.tar") {
		t.Errorf("stderr = %q, want --file fallback suggestion", got)
	}
	if !strings.Contains(got, "podman save") || !strings.Contains(got, "skopeo copy") {
		t.Errorf("stderr = %q, want podman/skopeo suggestions", got)
	}
}

// spec: §24.19.1 line 282 (K3S_UNAVAILABLE), §17.4 (Docker-backed
// substrate) — when the running stack records a Docker-backed k3s
// container but the `docker` binary is absent, the image bridge fails
// closed with K3S_UNAVAILABLE and points the operator at Docker Desktop.
//
// diagnosis: a failure means the image bridge no longer fails closed on the
// Docker-backed substrate when Docker is missing, so an unreachable
// containerd is misreported as a recoverable state.
func TestCtrCommandDockerSubstrateMissingDocker_spec_24_19_1(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	home, _ := DefaultRoot()
	seedStackState(t, home, "lenny-embedded-k3s-x")

	origLook := lookPathDocker
	t.Cleanup(func() { lookPathDocker = origLook })
	lookPathDocker = func() (string, error) {
		return "", errors.New("exec: \"docker\": executable file not found in $PATH")
	}

	var errb bytes.Buffer
	_, code := CtrCommand(&errb)
	if code != ExitK3sUnavailable {
		t.Fatalf("docker-missing exit = %d, want %d", code, ExitK3sUnavailable)
	}
	if got := errb.String(); !strings.Contains(got, "K3S_UNAVAILABLE") || !strings.Contains(got, "Docker Desktop") {
		t.Errorf("stderr = %q, want K3S_UNAVAILABLE + Docker Desktop guidance", got)
	}
}

// spec: §24.19.1 line 282 (K3S_UNAVAILABLE), §17.4 — when the recorded
// Docker-backed k3s container is not running, the bridge reports
// K3S_UNAVAILABLE rather than running `docker exec` against a dead
// container.
//
// diagnosis: a failure means the image bridge runs `docker exec` against a
// stopped k3s container instead of reporting K3S_UNAVAILABLE.
func TestCtrCommandDockerSubstrateContainerDown_spec_24_19_1(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	home, _ := DefaultRoot()
	seedStackState(t, home, "lenny-embedded-k3s-x")

	origLook := lookPathDocker
	origRunning := containerRunning
	t.Cleanup(func() {
		lookPathDocker = origLook
		containerRunning = origRunning
	})
	lookPathDocker = func() (string, error) { return "/usr/bin/docker", nil }
	var probed string
	containerRunning = func(name string) bool {
		probed = name
		return false
	}

	var errb bytes.Buffer
	_, code := CtrCommand(&errb)
	if code != ExitK3sUnavailable {
		t.Fatalf("container-down exit = %d, want %d", code, ExitK3sUnavailable)
	}
	if probed != "lenny-embedded-k3s-x" {
		t.Errorf("probed container = %q, want lenny-embedded-k3s-x", probed)
	}
	if got := errb.String(); !strings.Contains(got, "K3S_UNAVAILABLE") || !strings.Contains(got, "container is not running") {
		t.Errorf("stderr = %q, want K3S_UNAVAILABLE + container-not-running guidance", got)
	}
}

// spec: §24.19.1 line 282 (K3S_UNAVAILABLE), §17.4 — when the recorded
// Docker-backed k3s container is up, CtrCommand returns a `docker exec`
// invocation addressing the in-container containerd socket, so the bridge
// reaches containerd inside the container rather than via absent host paths.
//
// diagnosis: a failure means the image bridge resolves the wrong invocation
// on a running Docker-backed substrate, so the bring-up import targets the
// host paths that do not exist for a containerized k3s.
func TestCtrCommandDockerSubstrateRunning_spec_24_19_1(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	home, _ := DefaultRoot()
	seedStackState(t, home, "lenny-embedded-k3s-x")

	origLook := lookPathDocker
	origRunning := containerRunning
	t.Cleanup(func() {
		lookPathDocker = origLook
		containerRunning = origRunning
	})
	lookPathDocker = func() (string, error) { return "/usr/bin/docker", nil }
	containerRunning = func(string) bool { return true }

	var errb bytes.Buffer
	ctr, code := CtrCommand(&errb)
	if code != 0 {
		t.Fatalf("running-container exit = %d (stderr %q), want 0", code, errb.String())
	}
	if ctr.Binary != "docker" || ctr.container != "lenny-embedded-k3s-x" || ctr.socket != containerCtrSocket {
		t.Errorf("invocation = %+v, want docker exec into lenny-embedded-k3s-x at %s", ctr, containerCtrSocket)
	}
}

// spec: §24.19.1 line 282 (K3S_UNAVAILABLE), §17.4 — the host
// child-process substrate (Linux) reports K3S_UNAVAILABLE when the host
// k3s binary and containerd socket are absent, with no stack state file
// recorded. This pins the host path unchanged: it depends on the on-disk
// host artifacts rather than the recorded substrate.
//
// diagnosis: a failure means the host-substrate image bridge no longer
// reports K3S_UNAVAILABLE when the embedded k3s is not present.
func TestCtrCommandHostSubstrateUnavailable_spec_24_19_1(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())

	var errb bytes.Buffer
	_, code := CtrCommand(&errb)
	if code != ExitK3sUnavailable {
		t.Fatalf("host-absent exit = %d, want %d", code, ExitK3sUnavailable)
	}
	if got := errb.String(); !strings.Contains(got, "K3S_UNAVAILABLE") {
		t.Errorf("stderr = %q, want K3S_UNAVAILABLE", got)
	}
}

// spec: §24.19.1 — a corrupt stack state file makes RunningSubstrate
// return a non-ErrNoRunningStack error. CtrCommand surfaces it as a
// generic failure (exit 1) rather than misclassifying it as a clean
// no-stack K3S_UNAVAILABLE, so the operator sees the real cause.
//
// diagnosis: a failure means a corrupt state file is misclassified as a
// clean no-stack K3S_UNAVAILABLE, masking the real cause from the operator.
func TestCtrCommandCorruptState(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	home, _ := DefaultRoot()
	paths := NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := os.WriteFile(paths.StateFile(), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt state: %v", err)
	}
	var errb bytes.Buffer
	_, code := CtrCommand(&errb)
	if code != 1 {
		t.Fatalf("corrupt-state exit = %d, want 1", code)
	}
	if got := errb.String(); !strings.Contains(got, "lenny image:") {
		t.Errorf("stderr = %q, want a lenny image diagnostic", got)
	}
}

func assertArgs(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: args = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: args[%d] = %q, want %q", label, i, got[i], want[i])
		}
	}
}

// seedStackState writes a minimal Embedded Mode stack state file under the
// given home directory recording the k3s container handle. A non-empty
// container selects the Docker-backed substrate; an empty container leaves
// the host child-process substrate (no recorded container). The state file
// JSON layout is stack.State at <home>/run/stack.json.
func seedStackState(t *testing.T, home, container string) {
	t.Helper()
	paths := NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("seedStackState: EnsureDirs: %v", err)
	}
	body := `{"k3sEnabled":true}`
	if container != "" {
		body = `{"k3sEnabled":true,"k3sContainer":"` + container + `"}`
	}
	if err := os.WriteFile(paths.StateFile(), []byte(body), 0o600); err != nil {
		t.Fatalf("seedStackState: write state: %v", err)
	}
}
