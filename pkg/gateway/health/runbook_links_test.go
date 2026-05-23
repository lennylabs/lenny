// SPDX-License-Identifier: MIT

package health_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/health"
)

// spec: §4.0 / §25.3 — the centralized runbook-link table resolves
// known components to their runbook references and returns the empty
// string for unknown components.
func TestRunbookFor_KnownComponents(t *testing.T) {
	cases := map[string]string{
		"postgres":              "postgres-failover",
		"redis":                 "redis-failure",
		"circuit_breaker_cache": "redis-failure",
		"warm_pool":             "warm-pool-exhausted",
		"unknown_component":     "",
	}
	for comp, want := range cases {
		if got := health.RunbookFor(comp); got != want {
			t.Errorf("RunbookFor(%q): want %q, got %q", comp, want, got)
		}
	}
}

// spec: §4.0 — RegisterRunbook installs a new (component → runbook)
// link so out-of-tree backends can publish their own runbook routing.
func TestRegisterRunbook(t *testing.T) {
	health.RegisterRunbook("custom_backend", "custom-runbook")
	if got := health.RunbookFor("custom_backend"); got != "custom-runbook" {
		t.Errorf("RunbookFor(custom_backend): want custom-runbook, got %q", got)
	}
}
