// SPDX-License-Identifier: MIT

package environmentstore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
)

// spec: §12.9 line 1033 — an environment-level workspaceTier override
// must be a tenant-settable §12.9 tier (T3 or T4); empty inherits. An
// out-of-enum value is rejected at the store admission boundary.
func TestEnvironmentRejectsInvalidWorkspaceTier(t *testing.T) {
	env := validEnvironment("acme", "phi-preprod")
	env.WorkspaceTier = "T2"
	if err := env.Validate(); err == nil || !strings.Contains(err.Error(), "workspaceTier") {
		t.Fatalf("Validate err = %v, want a workspaceTier enum rejection", err)
	}
}

// A valid override (T4) and the empty inherit value both pass validation
// and round-trip through the store body.
func TestEnvironmentWorkspaceTierRoundTrip(t *testing.T) {
	for _, tier := range []string{"", "T3", "T4"} {
		env := validEnvironment("acme", "phi-preprod")
		env.WorkspaceTier = tier
		if err := env.Validate(); err != nil {
			t.Fatalf("tier %q: Validate: %v", tier, err)
		}
	}

	m := environmentstore.NewMemory()
	env := validEnvironment("acme", "phi-preprod")
	env.WorkspaceTier = "T4"
	if err := m.Create(context.Background(), env); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := m.Get(context.Background(), "acme", "phi-preprod")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.WorkspaceTier != "T4" {
		t.Errorf("WorkspaceTier = %q, want T4", got.WorkspaceTier)
	}
}
