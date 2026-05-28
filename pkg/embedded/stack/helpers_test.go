// SPDX-License-Identifier: MIT

package stack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"localhost": true,
		"":          true,
		"127.0.0.1": true,
		"::1":       true,
		"0.0.0.0":   false,
		"10.0.0.5":  false,
		"example":   false,
	}
	for host, want := range cases {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestPortSuffix(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:8443": ":8443",
		"localhost:8080": ":8080",
		"noport":         "noport",
	}
	for addr, want := range cases {
		if got := portSuffix(addr); got != want {
			t.Errorf("portSuffix(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestResolveRoot(t *testing.T) {
	if got, err := resolveRoot("/explicit"); err != nil || got != "/explicit" {
		t.Errorf("resolveRoot(explicit) = %q, %v", got, err)
	}
	t.Setenv("LENNY_HOME", "/from-env")
	got, err := resolveRoot("")
	if err != nil {
		t.Fatalf("resolveRoot(empty): %v", err)
	}
	if got != "/from-env" {
		t.Errorf("resolveRoot(empty) = %q, want the LENNY_HOME value", got)
	}
}

// TestKnownLogComponent covers the §24.19 line 263 component allow-list:
// gateway, controller, ops, postgres, redis, kms, oidc, k3s, supervisor.
//
// spec: §24.19 line 263.
func TestKnownLogComponent(t *testing.T) {
	for _, c := range []string{
		"gateway", "controller", "ops", "postgres", "redis",
		"kms", "oidc", "k3s", "supervisor",
	} {
		if !knownLogComponent(c) {
			t.Errorf("knownLogComponent(%q) = false, want true", c)
		}
	}
	if knownLogComponent("not-a-component") {
		t.Error("knownLogComponent rejected expected to reject bogus name")
	}
}

func TestResolveBinExplicit(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "lenny-gateway")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("seed binary: %v", err)
	}
	got, err := resolveBin(bin, "lenny-gateway")
	if err != nil {
		t.Fatalf("resolveBin: %v", err)
	}
	if got != bin {
		t.Errorf("resolveBin = %q, want %q", got, bin)
	}
	// An explicit path that does not exist is an error.
	if _, err := resolveBin(filepath.Join(dir, "absent"), "lenny-gateway"); err == nil {
		t.Error("expected resolveBin to error on a missing explicit path")
	}
}

func TestResolveBinDiscoversInWorkingDir(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "lenny-controller")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("seed binary: %v", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	got, err := resolveBin("", "lenny-controller")
	if err != nil {
		t.Fatalf("resolveBin: %v", err)
	}
	if filepath.Base(got) != "lenny-controller" {
		t.Errorf("resolveBin = %q, want a lenny-controller path", got)
	}
}
