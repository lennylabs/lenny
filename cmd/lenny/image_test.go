// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestCtrInvocationBaseArgs(t *testing.T) {
	c := ctrInvocation{binary: "/k3s", socket: "/sock"}
	got := c.baseArgs("k8s.io")
	want := []string{"ctr", "--address", "/sock", "--namespace", "k8s.io"}
	if len(got) != len(want) {
		t.Fatalf("baseArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("baseArgs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
