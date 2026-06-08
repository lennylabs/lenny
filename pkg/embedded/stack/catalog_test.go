// SPDX-License-Identifier: MIT

package stack

import (
	"strings"
	"testing"
)

func TestReferenceRuntimesCoversCatalog(t *testing.T) {
	rts := ReferenceRuntimes()
	// §26.1 lists nine reference runtimes. Spot-check the names rather
	// than the count so the test states which entries are expected.
	wantNames := []string{
		"claude-code", "gemini-cli", "codex", "cursor-cli", "chat",
		"langgraph", "mastra", "openai-assistants", "crewai",
	}
	got := map[string]ReferenceRuntime{}
	for _, rt := range rts {
		got[rt.Name] = rt
	}
	for _, name := range wantNames {
		if _, ok := got[name]; !ok {
			t.Errorf("reference catalog is missing %q", name)
		}
	}
	if len(rts) != len(wantNames) {
		t.Errorf("catalog has %d runtimes, want %d", len(rts), len(wantNames))
	}
}

func TestReferenceRuntimesIntegrationLevels(t *testing.T) {
	for _, rt := range ReferenceRuntimes() {
		// §26.1: chat is Standard; every other reference runtime is
		// Full.
		want := "full"
		if rt.Name == "chat" {
			want = "standard"
		}
		if rt.IntegrationLevel != want {
			t.Errorf("%s integrationLevel = %q, want %q", rt.Name, rt.IntegrationLevel, want)
		}
		if !strings.HasPrefix(rt.Image, "ghcr.io/lennylabs/runtime-") {
			t.Errorf("%s image %q is not a canonical Lenny registry path", rt.Name, rt.Image)
		}
		// §5.1 / §13.1: the gateway rejects a tag-only image reference.
		// Every catalog image must be digest-pinned.
		if !strings.Contains(rt.Image, "@sha256:") {
			t.Errorf("%s image %q is not digest-pinned", rt.Name, rt.Image)
		}
	}
}

func TestReferenceRuntimesReturnsCopy(t *testing.T) {
	first := ReferenceRuntimes()
	first[0].Name = "mutated"
	second := ReferenceRuntimes()
	if second[0].Name == "mutated" {
		t.Error("ReferenceRuntimes returned a slice sharing backing storage")
	}
}

func TestBuildBootstrapSeed(t *testing.T) {
	seed := buildBootstrapSeed()
	// §17.4: lenny up creates the default tenant.
	if len(seed.Tenants) != 1 || seed.Tenants[0].ID != defaultTenant {
		t.Errorf("seed tenants = %+v, want the default tenant", seed.Tenants)
	}
	// The built-in user is seeded as a platform-admin in the default
	// tenant.
	if len(seed.Users) != 1 {
		t.Fatalf("seed users = %+v, want one built-in user", seed.Users)
	}
	if seed.Users[0].TenantID != defaultTenant {
		t.Errorf("built-in user tenant = %q, want %q", seed.Users[0].TenantID, defaultTenant)
	}
	hasAdmin := false
	for _, r := range seed.Users[0].Roles {
		if r == "platform-admin" {
			hasAdmin = true
		}
	}
	if !hasAdmin {
		t.Errorf("built-in user roles = %v, want platform-admin", seed.Users[0].Roles)
	}
	// Every §26 reference runtime is seeded as a type:agent record.
	if len(seed.Runtimes) != len(referenceRuntimes) {
		t.Errorf("seed has %d runtimes, want %d", len(seed.Runtimes), len(referenceRuntimes))
	}
	for _, rt := range seed.Runtimes {
		if rt.Type != "agent" {
			t.Errorf("runtime %s seeded with type %q, want agent", rt.Name, rt.Type)
		}
		if rt.Image == "" {
			t.Errorf("runtime %s seeded without an image", rt.Name)
		}
		// §5.1 line 51: labels are required from v1. The gateway bootstrap
		// handler rejects a create without them, so every reference-runtime
		// seed must carry at least one label or `lenny up` fails to install.
		if len(rt.Labels) == 0 {
			t.Errorf("runtime %s seeded without labels (§5.1 line 51 requires them)", rt.Name)
		}
		if rt.Labels["lenny.dev/reference-runtime"] != "true" {
			t.Errorf("runtime %s missing the reference-runtime marker label, got %v", rt.Name, rt.Labels)
		}
	}
}
