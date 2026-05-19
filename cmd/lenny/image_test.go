// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
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
