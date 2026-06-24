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
