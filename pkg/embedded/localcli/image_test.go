// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
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

// TestCmdImageDispatchesSubcommands exercises the cmdImage switch so each
// subcommand arm routes to its handler. With no embedded stack on disk,
// import is rejected at the embedded-mode guard (exit 3) and list and rm
// reach the CtrCommand probe and report K3S_UNAVAILABLE (exit 4); both are
// the documented §24.19.1 exits for the dispatch, confirming cmdImage
// routes each subcommand to the right handler rather than mis-dispatching.
//
// spec: §24.19.1 (image subcommands import, list, rm).
func TestCmdImageDispatchesSubcommands(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"import routes to the embedded-mode guard", []string{"import", "acme/chat:v1"}, exitEmbeddedModeRequired},
		{"list routes to the ctr probe", []string{"list"}, exitK3sUnavailable},
		{"rm routes to the ctr probe", []string{"rm", "acme/chat:v1"}, exitK3sUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := cmdImage(tc.args, &out, &errb); code != tc.want {
				t.Errorf("cmdImage(%v) = %d, want %d", tc.args, code, tc.want)
			}
		})
	}
}

// TestCmdImageImportParsesFlags drives the --file and --namespace flag
// parsing in cmdImageImport. The flags are parsed before the embedded-mode
// guard, so with no stack on disk the call still exits 3
// EMBEDDED_MODE_REQUIRED; this exercises the flag-consumption branches
// (the i++ advance past each flag's value) and confirms a flagged
// invocation reaches the same guard as a bare one rather than mis-parsing
// the reference.
//
// spec: §24.19.1 (image import --file and --namespace flags; embedded-mode
// guard, exit 3).
func TestCmdImageImportParsesFlags(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var out, errb bytes.Buffer
	code := cmdImageImport([]string{"--namespace", "custom.io", "--file", "/tmp/img.tar", "acme/chat:v1"}, &out, &errb)
	if code != exitEmbeddedModeRequired {
		t.Fatalf("cmdImageImport with --file/--namespace = %d, want %d (flags parsed, then embedded-mode guard)", code, exitEmbeddedModeRequired)
	}
	if !strings.Contains(errb.String(), "EMBEDDED_MODE_REQUIRED") {
		t.Errorf("stderr = %q, want EMBEDDED_MODE_REQUIRED", errb.String())
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

// TestCmdImageImportRequiresEmbeddedStack pins the F-CTL-7 fix: a valid
// `lenny image import` invocation against no running Embedded Mode stack
// exits 3 EMBEDDED_MODE_REQUIRED, the same guard `lenny token print`
// performs, rather than the 4 K3S_UNAVAILABLE that reaching
// stack.CtrCommand would otherwise surface. The reference passes the OCI
// validation, so without the embedded-stack probe execution would fall
// through to CtrCommand and return exitK3sUnavailable (4); this asserts
// the corrected exit 3 and would fail against the pre-fix code.
//
// spec: §24.19.1 line 282,291 (image import requires embedded mode, exit
// 3 EMBEDDED_MODE_REQUIRED; same guard as lenny token print). (F-CTL-7)
func TestCmdImageImportRequiresEmbeddedStack_spec_24_19_1(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := cmdImageImport([]string{"acme/chat:v1"}, &stdout, &stderr)
	if code != exitEmbeddedModeRequired {
		t.Fatalf("cmdImageImport against no embedded stack = %d, want %d (EMBEDDED_MODE_REQUIRED), not %d (K3S_UNAVAILABLE)",
			code, exitEmbeddedModeRequired, exitK3sUnavailable)
	}
	if !strings.Contains(stderr.String(), "EMBEDDED_MODE_REQUIRED") {
		t.Errorf("stderr = %q, want the EMBEDDED_MODE_REQUIRED marker", stderr.String())
	}
	if !strings.Contains(stderr.String(), "lenny up") {
		t.Errorf("stderr = %q, want guidance to run lenny up", stderr.String())
	}
}

// TestRequireEmbeddedStackProbeError covers the requireEmbeddedStack
// branch where stat-ing the OIDC key returns an error other than
// ErrNotExist: the probe must surface that as exit 1 (a genuine probe
// failure) rather than the EMBEDDED_MODE_REQUIRED skip, so a transient
// filesystem fault is not mistaken for an absent stack. A regular file
// planted at the `oidc` path makes the `oidc/signing.key` stat fail with
// ENOTDIR, which is not ErrNotExist.
//
// spec: §24.19.1 line 282,291 (embedded-mode probe; a probe fault is exit
// 1, distinct from the EMBEDDED_MODE_REQUIRED skip).
func TestRequireEmbeddedStackProbeError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LENNY_HOME", home)
	// Plant a regular file where the `oidc` directory would be, so that
	// stat-ing oidc/signing.key returns ENOTDIR rather than ErrNotExist.
	if err := os.WriteFile(filepath.Join(home, "oidc"), []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("plant oidc file: %v", err)
	}
	var errb bytes.Buffer
	if code := requireEmbeddedStack(&errb); code != 1 {
		t.Fatalf("requireEmbeddedStack with a non-ErrNotExist probe error = %d, want 1", code)
	}
	if strings.Contains(errb.String(), "EMBEDDED_MODE_REQUIRED") {
		t.Errorf("stderr = %q, must not report EMBEDDED_MODE_REQUIRED on a probe fault", errb.String())
	}
}

// TestCmdImageImportReachesCtrWithEmbeddedStack confirms the guard does
// not over-reject: once `lenny up` has written the persisted OIDC key,
// the embedded-stack probe passes and import proceeds to the containerd
// invocation. With a seeded key but no real k3s on disk, stack.CtrCommand
// then reports K3S_UNAVAILABLE (4), demonstrating the guard fired only on
// the missing-stack path and not on a present stack.
//
// spec: §24.19.1 line 282 (the guard passes once embedded mode is up;
// K3S_UNAVAILABLE then surfaces when the containerd socket is unreachable).
func TestCmdImageImportReachesCtrWithEmbeddedStack_spec_24_19_1(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LENNY_HOME", home)
	seedOIDCKey(t, home)
	var stdout, stderr bytes.Buffer
	code := cmdImageImport([]string{"acme/chat:v1"}, &stdout, &stderr)
	if code != exitK3sUnavailable {
		t.Fatalf("cmdImageImport with a seeded embedded stack but no k3s = %d, want %d (K3S_UNAVAILABLE)",
			code, exitK3sUnavailable)
	}
	if strings.Contains(stderr.String(), "EMBEDDED_MODE_REQUIRED") {
		t.Errorf("stderr = %q, must not reject a present embedded stack as EMBEDDED_MODE_REQUIRED", stderr.String())
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
