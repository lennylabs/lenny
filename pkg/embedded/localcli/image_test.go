// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

func TestCmdImageRequiresSubcommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := cmdImage(nil, &out, &errb); code != 2 {
		t.Errorf("no subcommand: exit = %d, want 2", code)
	}
}

func TestCmdImageRejectsUnknownSubcommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := cmdImage([]string{"frobnicate"}, &out, &errb); code != 2 {
		t.Errorf("unknown subcommand: exit = %d, want 2", code)
	}
}

func TestCmdImageImportRequiresReference(t *testing.T) {
	var out, errb bytes.Buffer
	if code := cmdImageImport(nil, &out, &errb); code != 2 {
		t.Errorf("missing reference: exit = %d, want 2", code)
	}
}

func TestCmdImageImportRejectsInvalidReference(t *testing.T) {
	var out, errb bytes.Buffer
	if code := cmdImageImport([]string{"not a ref"}, &out, &errb); code != exitInvalidImageRef {
		t.Errorf("invalid reference: exit = %d, want %d", code, exitInvalidImageRef)
	}
}

func TestCmdImageRmRejectsInvalidReference(t *testing.T) {
	var out, errb bytes.Buffer
	if code := cmdImageRm([]string{"bad ref"}, &out, &errb); code != exitInvalidImageRef {
		t.Errorf("invalid reference: exit = %d, want %d", code, exitInvalidImageRef)
	}
}

func TestImageRefPattern(t *testing.T) {
	valid := []string{
		"my-agent:dev",
		"ghcr.io/lennylabs/runtime-chat:1.0.0",
		"busybox",
		"localhost:5000/x@sha256:abc123",
	}
	for _, r := range valid {
		if !imageRefPattern.MatchString(r) {
			t.Errorf("%q should be a valid OCI reference", r)
		}
	}
	invalid := []string{"", "has space", "bad\tref", "-leadingdash"}
	for _, r := range invalid {
		if imageRefPattern.MatchString(r) {
			t.Errorf("%q should be rejected as an OCI reference", r)
		}
	}
}

func TestNamespaceFlag(t *testing.T) {
	if ns := namespaceFlag(nil, "k8s.io"); ns != "k8s.io" {
		t.Errorf("default namespace = %q, want k8s.io", ns)
	}
	if ns := namespaceFlag([]string{"--namespace", "custom"}, "k8s.io"); ns != "custom" {
		t.Errorf("override namespace = %q, want custom", ns)
	}
}

// spec: §24.19.1 line 278 — `lenny image rm` classifies the containerd
// "image is in use" failure (referenced by container or snapshot) so
// operators see an actionable diagnostic instead of the raw ctr error.
func TestImageInUseErrorClassifier(t *testing.T) {
	hits := []string{
		"ctr: image \"foo\" is referenced by snapshot \"abc\": failed precondition",
		"image is in use by container",
		"in use by container abcd",
		"failed precondition: in use:",
	}
	for _, raw := range hits {
		if !imageInUseError(raw) {
			t.Errorf("imageInUseError(%q) = false, want true", raw)
		}
	}
	misses := []string{
		"",
		"image not found",
		"unknown image",
		"unauthorized",
	}
	for _, raw := range misses {
		if imageInUseError(raw) {
			t.Errorf("imageInUseError(%q) = true, want false", raw)
		}
	}
}

// spec: §24.19.1 line 278 — when ctr names the consuming reference,
// the wrapped message points at it for faster operator triage.
func TestImageInUseReferenceExtraction(t *testing.T) {
	if got := imageInUseReference("ctr: image \"foo\" is referenced by snapshot \"abc\":"); got != "snapshot \"abc\"" {
		t.Errorf("reference extraction = %q, want %q", got, "snapshot \"abc\"")
	}
	if got := imageInUseReference("image is in use by pod kube-system/x"); got != "" {
		t.Errorf("non-referenced-by message extracted %q, want empty", got)
	}
	if got := imageInUseReference("trailer\nis referenced by container foo\nmore"); got != "container foo" {
		t.Errorf("multi-line extraction = %q, want %q", got, "container foo")
	}
}

// TestImageImportSuggestsTarFallbackWhenDockerMissing covers the
// host-daemon path: when `docker` is not on PATH, the command surfaces
// a diagnostic pointing at the `--file <tar>` fallback rather than
// propagating the raw os/exec "executable file not found" message.
//
// spec: §17.4 line 290, §24.19.1 line 274.
func TestImageImportSuggestsTarFallbackWhenDockerMissing_spec_24_19_1(t *testing.T) {
	origLookPath := lookPathDocker
	t.Cleanup(func() { lookPathDocker = origLookPath })

	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	// Seed a fake k3s binary + containerd socket so ctrCommand returns
	// successfully and the docker-missing branch is reached.
	k3sDir := filepath.Join(root, "k3s")
	if err := os.MkdirAll(filepath.Join(k3sDir, "data", "agent", "containerd"), 0o755); err != nil {
		t.Fatalf("seed k3s dirs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(k3sDir, "k3s"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("seed k3s binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(k3sDir, "data", "agent", "containerd", "containerd.sock"), nil, 0o600); err != nil {
		t.Fatalf("seed containerd sock: %v", err)
	}
	lookPathDocker = func() (string, error) {
		return "", errors.New("exec: \"docker\": executable file not found in $PATH")
	}
	var out, errb bytes.Buffer
	code := cmdImageImport([]string{"my-agent:dev"}, &out, &errb)
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

// spec: §24.19.1 (the image bridge reaches the embedded containerd image
// store), §17.4 (the substrate is provisioned per host operating system) —
// on the Linux managed-child-process launcher the bridge runs the host k3s
// binary against the host containerd socket, so the argv is a bare `ctr`
// invocation with no docker-exec prefix.
func TestCtrInvocationArgsHostPath(t *testing.T) {
	c := ctrInvocation{binary: "/k3s", socket: "/sock"}
	got := c.args("k8s.io", false, "images", "ls")
	want := []string{"ctr", "--address", "/sock", "--namespace", "k8s.io", "images", "ls"}
	assertArgs(t, "host ls", got, want)

	// A stdin-requesting subcommand on the host path does not add `exec -i`;
	// `-i` is a docker-exec concern only.
	gotImport := c.args("k8s.io", true, "images", "import", "-")
	wantImport := []string{"ctr", "--address", "/sock", "--namespace", "k8s.io", "images", "import", "-"}
	assertArgs(t, "host import", gotImport, wantImport)
}

// spec: §24.19.1, §17.4 — on the Docker-backed launcher (macOS and
// Windows) the bridge runs `k3s ctr` inside the k3s container via
// `docker exec`, addressing the in-container containerd socket. A
// tarball-streaming subcommand requests an interactive stdin (`exec -i`).
func TestCtrInvocationArgsDockerPath(t *testing.T) {
	c := ctrInvocation{binary: "docker", socket: containerCtrSocket, container: "lenny-embedded-k3s-x"}

	// A non-streaming subcommand: no `-i`.
	got := c.args("k8s.io", false, "images", "ls")
	want := []string{
		"exec", "lenny-embedded-k3s-x", "k3s",
		"ctr", "--address", containerCtrSocket, "--namespace", "k8s.io", "images", "ls",
	}
	assertArgs(t, "docker ls", got, want)

	// A streaming import requests `-i` so the host tarball pipes into the
	// in-container ctr stdin.
	gotImport := c.args("custom", true, "images", "import", "-")
	wantImport := []string{
		"exec", "-i", "lenny-embedded-k3s-x", "k3s",
		"ctr", "--address", containerCtrSocket, "--namespace", "custom", "images", "import", "-",
	}
	assertArgs(t, "docker import", gotImport, wantImport)
}

// TestImportFromFileMissingTarballOnDockerPath covers the open-error branch
// of importFromFile on the Docker-backed substrate: the host tarball is
// streamed into the in-container ctr via stdin, so a missing tarball is
// reported as an open error before any docker exec runs. The host-binary
// branch (ctr.container == "") and the success path shell out to ctr, so
// they belong to the tier-2 bring-up rather than this unit; this test pins
// the fail-closed open-error path without a real container.
//
// spec: §24.19.1 line 275 (the `--file <tar>` import path), §17.4 (the
// Docker-backed substrate streams the host tarball through `docker exec -i`).
func TestImportFromFileMissingTarballOnDockerPath_spec_24_19_1(t *testing.T) {
	ctr := ctrInvocation{binary: "docker", socket: containerCtrSocket, container: "lenny-embedded-k3s-x"}
	missing := filepath.Join(t.TempDir(), "no-such-image.tar")
	var stdout, stderr bytes.Buffer
	code := importFromFile(ctr, "k8s.io", "acme/chat:v1", missing, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("importFromFile on a missing tarball = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "open") {
		t.Errorf("stderr = %q, want it to name the open failure", stderr.String())
	}
}

// TestImportFromFileHostPathRunError covers the host-binary file-import
// error branch of importFromFile (ctr.container == ""): when the ctr
// invocation fails (here because the binary path does not exist), the import
// reports a non-zero exit with a diagnostic rather than claiming success.
// This pins the host-path error handling without a real k3s ctr; the
// success path shells out to a live ctr and belongs to the tier-2 bring-up.
//
// spec: §24.19.1 line 275 (the `--file <tar>` import path on the host
// substrate).
func TestImportFromFileHostPathRunError_spec_24_19_1(t *testing.T) {
	tar := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(tar, []byte("not-a-real-tar"), 0o644); err != nil {
		t.Fatalf("seed tarball: %v", err)
	}
	// A host-path ctr invocation (empty container) whose binary does not
	// exist, so runStreamed returns an exec error and the error branch runs.
	ctr := ctrInvocation{binary: filepath.Join(t.TempDir(), "no-such-ctr"), socket: "/sock"}
	var stdout, stderr bytes.Buffer
	if code := importFromFile(ctr, "k8s.io", "acme/chat:v1", tar, &stdout, &stderr); code != 1 {
		t.Fatalf("importFromFile with an unrunnable ctr = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "lenny image import:") {
		t.Errorf("stderr = %q, want a lenny image import diagnostic", stderr.String())
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
	paths := stack.NewPaths(home)
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

// spec: §24.19.1 line 282 (K3S_UNAVAILABLE), §17.4 (Docker-backed
// substrate) — when the running stack records a Docker-backed k3s
// container but the `docker` binary is absent, the image bridge fails
// closed with K3S_UNAVAILABLE and points the operator at Docker Desktop.
func TestCtrCommandDockerSubstrateMissingDocker_spec_24_19_1(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	home, _ := stack.DefaultRoot()
	seedStackState(t, home, "lenny-embedded-k3s-x")

	origLook := lookPathDocker
	t.Cleanup(func() { lookPathDocker = origLook })
	lookPathDocker = func() (string, error) {
		return "", errors.New("exec: \"docker\": executable file not found in $PATH")
	}

	var errb bytes.Buffer
	_, code := ctrCommand(&errb)
	if code != exitK3sUnavailable {
		t.Fatalf("docker-missing exit = %d, want %d", code, exitK3sUnavailable)
	}
	if got := errb.String(); !strings.Contains(got, "K3S_UNAVAILABLE") || !strings.Contains(got, "Docker Desktop") {
		t.Errorf("stderr = %q, want K3S_UNAVAILABLE + Docker Desktop guidance", got)
	}
}

// spec: §24.19.1 line 282 (K3S_UNAVAILABLE), §17.4 — when the recorded
// Docker-backed k3s container is not running, the bridge reports
// K3S_UNAVAILABLE rather than running `docker exec` against a dead
// container.
func TestCtrCommandDockerSubstrateContainerDown_spec_24_19_1(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	home, _ := stack.DefaultRoot()
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
	_, code := ctrCommand(&errb)
	if code != exitK3sUnavailable {
		t.Fatalf("container-down exit = %d, want %d", code, exitK3sUnavailable)
	}
	if probed != "lenny-embedded-k3s-x" {
		t.Errorf("probed container = %q, want lenny-embedded-k3s-x", probed)
	}
	if got := errb.String(); !strings.Contains(got, "K3S_UNAVAILABLE") || !strings.Contains(got, "container is not running") {
		t.Errorf("stderr = %q, want K3S_UNAVAILABLE + container-not-running guidance", got)
	}
}

// spec: §24.19.1 line 282 (K3S_UNAVAILABLE), §17.4 — when the recorded
// Docker-backed k3s container is up, ctrCommand returns a `docker exec`
// invocation addressing the in-container containerd socket, so the bridge
// reaches containerd inside the container rather than via absent host paths.
func TestCtrCommandDockerSubstrateRunning_spec_24_19_1(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	home, _ := stack.DefaultRoot()
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
	ctr, code := ctrCommand(&errb)
	if code != 0 {
		t.Fatalf("running-container exit = %d (stderr %q), want 0", code, errb.String())
	}
	if ctr.binary != "docker" || ctr.container != "lenny-embedded-k3s-x" || ctr.socket != containerCtrSocket {
		t.Errorf("invocation = %+v, want docker exec into lenny-embedded-k3s-x at %s", ctr, containerCtrSocket)
	}
}

// spec: §24.19.1 line 282 (K3S_UNAVAILABLE), §17.4 — the host
// child-process substrate (Linux) reports K3S_UNAVAILABLE when the host
// k3s binary and containerd socket are absent, with no stack state file
// recorded. This pins the host path unchanged: it depends on the on-disk
// host artifacts rather than the recorded substrate.
func TestCtrCommandHostSubstrateUnavailable_spec_24_19_1(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())

	var errb bytes.Buffer
	_, code := ctrCommand(&errb)
	if code != exitK3sUnavailable {
		t.Fatalf("host-absent exit = %d, want %d", code, exitK3sUnavailable)
	}
	if got := errb.String(); !strings.Contains(got, "K3S_UNAVAILABLE") {
		t.Errorf("stderr = %q, want K3S_UNAVAILABLE", got)
	}
}

// spec: §24.19.1 — a corrupt stack state file makes RunningSubstrate
// return a non-ErrNoRunningStack error. ctrCommand surfaces it as a
// generic failure (exit 1) rather than misclassifying it as a clean
// no-stack K3S_UNAVAILABLE, so the operator sees the real cause.
func TestCtrCommandCorruptState(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	home, _ := stack.DefaultRoot()
	paths := stack.NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := os.WriteFile(paths.StateFile(), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt state: %v", err)
	}
	var errb bytes.Buffer
	_, code := ctrCommand(&errb)
	if code != 1 {
		t.Fatalf("corrupt-state exit = %d, want 1", code)
	}
	if got := errb.String(); !strings.Contains(got, "lenny image:") {
		t.Errorf("stderr = %q, want a lenny image diagnostic", got)
	}
}

// TestCmdImageListUnavailableStack covers cmdImageList's early return: with
// no running stack and no host k3s on disk, ctrCommand reports
// K3S_UNAVAILABLE and cmdImageList propagates that exit without attempting a
// ctr invocation.
//
// spec: §24.19.1 line 282 (K3S_UNAVAILABLE when the embedded containerd is
// unreachable).
func TestCmdImageListUnavailableStack_spec_24_19_1(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := cmdImageList(nil, &stdout, &stderr); code != exitK3sUnavailable {
		t.Fatalf("cmdImageList against an unavailable stack = %d, want %d", code, exitK3sUnavailable)
	}
	if !strings.Contains(stderr.String(), "K3S_UNAVAILABLE") {
		t.Errorf("stderr = %q, want K3S_UNAVAILABLE", stderr.String())
	}
}

// TestCmdImageRmUnavailableStack covers cmdImageRm's early return after a
// valid reference passes validation: ctrCommand reports K3S_UNAVAILABLE and
// cmdImageRm propagates the exit without invoking ctr.
//
// spec: §24.19.1 line 282 (K3S_UNAVAILABLE).
func TestCmdImageRmUnavailableStack_spec_24_19_1(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := cmdImageRm([]string{"acme/chat:v1"}, &stdout, &stderr); code != exitK3sUnavailable {
		t.Fatalf("cmdImageRm against an unavailable stack = %d, want %d", code, exitK3sUnavailable)
	}
	if !strings.Contains(stderr.String(), "K3S_UNAVAILABLE") {
		t.Errorf("stderr = %q, want K3S_UNAVAILABLE", stderr.String())
	}
}
