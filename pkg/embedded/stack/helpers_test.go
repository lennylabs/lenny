// SPDX-License-Identifier: MIT

package stack

import (
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

// TestKnownLogComponent covers the §24.19 line 263 component allow-list after
// the in-cluster topology removes the host-process components: the pod-backed
// sources gateway, controller, ops, and the k3s substrate are accepted, while
// the removed host-process components postgres, redis, kms, oidc, and
// supervisor are rejected.
//
// spec: §17.4 line 179, §24.19 line 263 (the pod-backed log component set).
func TestKnownLogComponent(t *testing.T) {
	for _, c := range []string{"gateway", "controller", "ops", "k3s"} {
		if !knownLogComponent(c) {
			t.Errorf("knownLogComponent(%q) = false, want true", c)
		}
	}
	for _, c := range []string{"postgres", "redis", "kms", "oidc", "supervisor", "not-a-component"} {
		if knownLogComponent(c) {
			t.Errorf("knownLogComponent(%q) = true, want false (the host-process components are removed)", c)
		}
	}
}
