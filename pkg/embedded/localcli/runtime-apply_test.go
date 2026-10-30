// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestParseRuntimeApplyFlags covers the verb's flag parsing: --file (and its
// -f alias) capture the path, a missing --file value and an unknown argument
// are rejected, and an absent --file is rejected so a typo fails fast rather
// than applying nothing silently.
func TestParseRuntimeApplyFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--file", "/tmp/runtime-crds.yaml"},
		{"-f", "/tmp/runtime-crds.yaml"},
	} {
		got, err := parseRuntimeApplyFlags(args)
		if err != nil {
			t.Fatalf("parseRuntimeApplyFlags(%v): %v", args, err)
		}
		if got != "/tmp/runtime-crds.yaml" {
			t.Errorf("parseRuntimeApplyFlags(%v) = %q, want the path", args, got)
		}
	}
	if _, err := parseRuntimeApplyFlags(nil); err == nil {
		t.Error("parseRuntimeApplyFlags(nil) accepted a missing --file, want an error")
	}
	if _, err := parseRuntimeApplyFlags([]string{"--file"}); err == nil {
		t.Error("parseRuntimeApplyFlags accepted --file with no value, want an error")
	}
	if _, err := parseRuntimeApplyFlags([]string{"--bogus", "x"}); err == nil {
		t.Error("parseRuntimeApplyFlags accepted an unknown argument, want an error")
	}
}

// TestRuntimeRequiresSubcommand covers the `lenny runtime` dispatch: no
// subcommand and an unknown subcommand each exit 2 with the usage text, so a
// bare or mistyped invocation surfaces the available subcommand.
func TestRuntimeRequiresSubcommand(t *testing.T) {
	for _, args := range [][]string{nil, {"frobnicate"}} {
		var stdout, stderr bytes.Buffer
		code := cmdRuntime(context.Background(), args, &stdout, &stderr)
		if code != 2 {
			t.Errorf("cmdRuntime(%v) = %d, want 2", args, code)
		}
		if !strings.Contains(stderr.String(), "runtime apply") {
			t.Errorf("cmdRuntime(%v) stderr = %q, want the usage text", args, stderr.String())
		}
	}
}

// TestRuntimeApplyWithoutStackRequiresEmbeddedMode covers the §24.19.1 exit
// convention: invoked with no running Embedded Mode stack, `lenny runtime
// apply` exits 3 EMBEDDED_MODE_REQUIRED with guidance to run lenny up, because
// the verb applies CRDs to the embedded cluster's kubeconfig, which only a
// running stack records.
//
// spec: §17.4 (the verb operates against a running embedded cluster), §24.19.1
// (EMBEDDED_MODE_REQUIRED).
func TestRuntimeApplyWithoutStackRequiresEmbeddedMode(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Main([]string{"runtime", "apply", "--file", "/tmp/runtime-crds.yaml"}, &stdout, &stderr, testVersion)
	if code != exitEmbeddedModeRequired {
		t.Errorf("exit code = %d, want %d (EMBEDDED_MODE_REQUIRED)", code, exitEmbeddedModeRequired)
	}
	if !strings.Contains(stderr.String(), "lenny up") {
		t.Errorf("stderr = %q, want guidance to run lenny up", stderr.String())
	}
	if !strings.Contains(stderr.String(), "EMBEDDED_MODE_REQUIRED") {
		t.Errorf("stderr = %q, want the EMBEDDED_MODE_REQUIRED marker", stderr.String())
	}
}

// TestRuntimeApplyMissingFileExitsTwo covers the §24.19.1 usage-error exit: a
// `lenny runtime apply` with no --file flag exits 2 before touching any stack
// state, so a malformed invocation fails fast.
func TestRuntimeApplyMissingFileExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Main([]string{"runtime", "apply"}, &stdout, &stderr, testVersion)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--file") {
		t.Errorf("stderr = %q, want a missing --file error", stderr.String())
	}
}

// TestRuntimeApplyThreadsFileToStack covers that cmdRuntimeApply resolves the
// running stack's kubeconfig and threads the --file path into the stack apply
// seam, so the verb's dispatch and flag plumbing reach the stack with the
// operator's file. The apply seam is substituted to capture the (kubeconfig,
// file) pair without reaching a real cluster.
func TestRuntimeApplyThreadsFileToStack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LENNY_HOME", home)
	// Record a running stack so RunningKubeconfig resolves; the recorded
	// kubeconfig path is what the verb must thread to the apply seam.
	const kubeconfig = "/tmp/embedded-kubeconfig.yaml"
	seedRunningStackKubeconfig(t, home, kubeconfig)

	var gotKubeconfig, gotFile string
	prev := applyRuntimeSetFn
	t.Cleanup(func() { applyRuntimeSetFn = prev })
	applyRuntimeSetFn = func(_ context.Context, kc, file string) error {
		gotKubeconfig = kc
		gotFile = file
		return nil
	}

	const wantFile = "/work/runtime-crds.yaml"
	var stdout, stderr bytes.Buffer
	code := Main([]string{"runtime", "apply", "--file", wantFile}, &stdout, &stderr, testVersion)
	if code != 0 {
		t.Fatalf("exit code = %d (stderr %q), want 0", code, stderr.String())
	}
	if gotFile != wantFile {
		t.Errorf("apply seam received file %q, want %q", gotFile, wantFile)
	}
	if gotKubeconfig != kubeconfig {
		t.Errorf("apply seam received kubeconfig %q, want the recorded %q", gotKubeconfig, kubeconfig)
	}
	if !strings.Contains(stdout.String(), wantFile) {
		t.Errorf("stdout = %q, want an applied-from confirmation naming the file", stdout.String())
	}
}
