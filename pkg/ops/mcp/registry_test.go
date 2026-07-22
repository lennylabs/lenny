// SPDX-License-Identifier: MIT

package mcp

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/common/scopes"
)

// filteredNames returns the set of tool names FilterForList admits under
// caps with no caller scope narrowing (an absent claim permits every
// scope), so the assertions isolate the capability axis.
func filteredNames(caps Capabilities) map[string]bool {
	reg := NewRegistry()
	names := make(map[string]bool)
	for _, t := range reg.FilterForList(caps, scopes.Set{}) {
		names[t.Name] = true
	}
	return names
}

// TestFilterForListClassifiesByScopeDomain pins the §25.12 operability-vs-
// admin classification to the tool's x-lenny-scope domain prefix. The
// non-happy path is the gateway-owned-but-operability tool (health, audit)
// that a transport-ownership rule would misclassify into admin: both carry
// an operability-domain scope claim and must list under scope:operability.
//
// spec: §25.12 (Capability Negotiation, operability-vs-admin by scope-claim domain)
func TestFilterForListClassifiesByScopeDomain(t *testing.T) {
	operability := filteredNames(Capabilities{Scope: "operability"})
	admin := filteredNames(Capabilities{Scope: "admin"})

	// Every tool the registry holds lands in exactly one category, and the
	// side it lands on matches its scope-claim domain membership.
	reg := NewRegistry()
	for _, tool := range reg.All() {
		wantOperability := operabilityDomains[domainOf(tool.Scope)]
		if wantOperability && !operability[tool.Name] {
			t.Errorf("%s (%s) is operability-domain but absent from scope:operability", tool.Name, tool.Scope)
		}
		if !wantOperability && operability[tool.Name] {
			t.Errorf("%s (%s) is admin-domain but present in scope:operability", tool.Name, tool.Scope)
		}
		if wantOperability && admin[tool.Name] {
			t.Errorf("%s (%s) is operability-domain but present in scope:admin", tool.Name, tool.Scope)
		}
		if !wantOperability && !admin[tool.Name] {
			t.Errorf("%s (%s) is admin-domain but absent from scope:admin", tool.Name, tool.Scope)
		}
	}

	// Representative membership: health and audit are gateway-owned yet
	// operability-domain, so they classify operability; the platform-
	// management pool/tenant tools classify admin.
	cases := []struct {
		name          string
		wantInOpsView bool
	}{
		{"admin.health", true},           // tools:health:read
		{"admin.get_audit_event", true},  // tools:audit:read (gateway-owned, operability)
		{"lenny_diagnostics_pool", true}, // tools:diagnostics:read
		{"admin.create_pool", false},     // tools:pool:write (admin)
		{"admin.create_tenant", false},   // tools:tenant:write (admin)
	}
	for _, c := range cases {
		if _, registered := reg.Lookup(c.name); !registered {
			t.Fatalf("test premise stale: %s is not a registered tool", c.name)
		}
		if operability[c.name] != c.wantInOpsView {
			t.Errorf("scope:operability membership of %s = %v, want %v", c.name, operability[c.name], c.wantInOpsView)
		}
		if admin[c.name] != !c.wantInOpsView {
			t.Errorf("scope:admin membership of %s = %v, want %v", c.name, admin[c.name], !c.wantInOpsView)
		}
	}
}

// TestFilterForListAdminReturnsNonEmptyInventory pins the corrected
// behavior: scope:admin now returns the platform-management inventory
// rather than the pre-fix empty-list short-circuit. This assertion fails
// against the pre-fix code, which returned an empty list for scope:admin.
//
// spec: §25.12 (Capability Negotiation — the operability and admin sets partition the inventory)
func TestFilterForListAdminReturnsNonEmptyInventory(t *testing.T) {
	admin := filteredNames(Capabilities{Scope: "admin"})
	if len(admin) == 0 {
		t.Fatal("scope:admin returned an empty inventory; it must return the platform-management tools")
	}
	// A representative platform-management tool is present.
	if !admin["admin.create_pool"] {
		t.Error("scope:admin is missing the platform-management tool admin.create_pool")
	}
}

// TestFilterForListOperabilityReadOnlyIntersects pins that the combined
// scope:operability + readOnly filters intersect: a tool must be both an
// operability-domain tool and read-only to survive. The non-happy path is
// a mutating operability-domain tool (a lock acquire) that the operability
// guard alone would keep but the readOnly guard must drop.
//
// spec: §25.12 (Capability Negotiation — multiple filters combine)
func TestFilterForListOperabilityReadOnlyIntersects(t *testing.T) {
	combined := filteredNames(Capabilities{Scope: "operability", ReadOnly: true})

	reg := NewRegistry()
	for _, tool := range reg.All() {
		if !combined[tool.Name] {
			continue
		}
		if !operabilityDomains[domainOf(tool.Scope)] {
			t.Errorf("combined filter kept %s (%s), not an operability-domain tool", tool.Name, tool.Scope)
		}
		if !tool.ReadOnly {
			t.Errorf("combined filter kept %s, which is not read-only", tool.Name)
		}
	}
	// admin.health is read-only and operability-domain, so it survives.
	if !combined["admin.health"] {
		t.Error("combined operability+readOnly dropped the read-only operability tool admin.health")
	}
	// lenny_lock_acquire is operability-domain (locks) but mutating, so the
	// readOnly guard drops it.
	if combined["lenny_lock_acquire"] {
		t.Error("combined operability+readOnly kept the mutating lenny_lock_acquire")
	}
}

// TestFilterForListTenantScopedDropsPlatformAdminTools pins that
// tenantScoped drops every platform-admin-only tool and keeps the tenant-
// admin-accessible ones. The non-happy path is the platform-admin-only
// tool that must not surface to a tenant-scoped view.
//
// spec: §25.12 (tenantScoped filter)
func TestFilterForListTenantScopedDropsPlatformAdminTools(t *testing.T) {
	tenant := filteredNames(Capabilities{TenantScoped: true})
	if len(tenant) == 0 {
		t.Fatal("tenantScoped returned an empty inventory; the tenant-admin tools must survive")
	}
	reg := NewRegistry()
	sawPlatformAdmin := false
	for _, tool := range reg.All() {
		if tool.RequiredRole == RolePlatformAdmin {
			sawPlatformAdmin = true
			if tenant[tool.Name] {
				t.Errorf("tenantScoped kept the platform-admin-only tool %s", tool.Name)
			}
		}
	}
	if !sawPlatformAdmin {
		t.Fatal("test premise stale: the inventory has no platform-admin tool to drop")
	}
}

// TestCapabilitiesFromParamsParsesTenantScoped pins that tenantScoped is
// parsed from both the capabilities object and the clientInfo.capabilities
// initialize-handshake form.
//
// spec: §25.12 (tenantScoped filter)
func TestCapabilitiesFromParamsParsesTenantScoped(t *testing.T) {
	cases := []struct {
		name   string
		params string
	}{
		{"capabilities object", `{"capabilities":{"tenantScoped":true}}`},
		{"clientInfo.capabilities", `{"clientInfo":{"capabilities":{"tenantScoped":true}}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			caps := capabilitiesFromParams(json.RawMessage(c.params))
			if !caps.TenantScoped {
				t.Errorf("capabilitiesFromParams(%s).TenantScoped = false, want true", c.params)
			}
		})
	}
}

// TestDomainOfExtractsScopeDomain pins the domain extraction from a
// tools:{domain}:{action} scope claim, including the malformed claim that
// carries no domain segment and must classify as admin.
//
// spec: §25.12 (Capability Negotiation — classification by scope domain prefix)
func TestDomainOfExtractsScopeDomain(t *testing.T) {
	cases := []struct {
		scope string
		want  string
	}{
		{"tools:health:read", "health"},
		{"tools:pool:write", "pool"},
		{"tools:credential_pool:write", "credential_pool"},
		{"", ""},
		{"malformed", ""},
	}
	for _, c := range cases {
		if got := domainOf(c.scope); got != c.want {
			t.Errorf("domainOf(%q) = %q, want %q", c.scope, got, c.want)
		}
	}
}
