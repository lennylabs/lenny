// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
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

// TestCmdImageListUnavailableStack covers cmdImageList's early return: with
// no running stack and no host k3s on disk, stack.CtrCommand reports
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
// valid reference passes validation: stack.CtrCommand reports K3S_UNAVAILABLE
// and cmdImageRm propagates the exit without invoking ctr.
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
