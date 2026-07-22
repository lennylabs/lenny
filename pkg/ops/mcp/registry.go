// SPDX-License-Identifier: MIT

package mcp

import (
	"sort"
	"strings"

	"github.com/lennylabs/lenny/pkg/common/scopes"
)

// operabilityDomains is the §25.12 operability-domain set: the closed set
// of scope-claim domain prefixes that classify a tool as an operability
// tool for the tools/list capability filter. The operability and admin
// sets partition the full tool inventory by the tool's x-lenny-scope
// domain prefix, decoupled from the ops-vs-gateway transport-routing
// predicate: a domain in this set is operability regardless of whether the
// tool is served by lenny-ops or proxied to the gateway admin API (for
// example health and audit are gateway-owned but operability-domain).
//
// spec: §25.12 (Capability Negotiation) — the operability domains are
// health, diagnostics, runbooks, events, audit, drift, backup, restore,
// upgrade, locks, escalation, logs, me, operations, recommendations, and
// the introspection and control domains config, schema, sessions, tree,
// platform, and preflight.
var operabilityDomains = map[string]bool{
	"health":          true,
	"diagnostics":     true,
	"runbooks":        true,
	"events":          true,
	"audit":           true,
	"drift":           true,
	"backup":          true,
	"restore":         true,
	"upgrade":         true,
	"locks":           true,
	"escalation":      true,
	"logs":            true,
	"me":              true,
	"operations":      true,
	"recommendations": true,
	"config":          true,
	"schema":          true,
	"sessions":        true,
	"tree":            true,
	"platform":        true,
	"preflight":       true,
}

// domainOf extracts the {domain} segment from a tools:{domain}:{action}
// scope claim (the tool's x-lenny-scope). It returns the empty string for
// a scope that does not carry a domain segment, which the operability set
// never contains, so such a tool classifies as admin.
//
// spec: §25.12 (Capability Negotiation — classification by the tool's
// x-lenny-scope domain prefix).
func domainOf(scope string) string {
	parts := strings.Split(scope, ":")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// Registry is the §25.12 management-tool inventory: the set of MCP
// tools the management server exposes via tools/list and dispatches via
// tools/call. It is the ManagementToolRegistry in §25.12's architecture.
type Registry struct {
	byName map[string]Tool
	order  []string
}

// NewRegistry returns the §25.12 v1 management-tool registry: the
// operability tool inventory the build-time openapi-to-mcp generator derives
// from the served OpenAPI document (generatedToolset, generated_tools.go).
func NewRegistry() *Registry {
	reg := &Registry{byName: make(map[string]Tool)}
	for _, t := range generatedToolset() {
		reg.byName[t.Name] = t
		reg.order = append(reg.order, t.Name)
	}
	sort.Strings(reg.order)
	return reg
}

// Lookup returns the tool of the given name. ok is false when no tool
// of that name is registered.
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// All returns every registered tool ordered by name.
func (r *Registry) All() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.byName[name])
	}
	return out
}

// Capabilities is the §25.12 MCP-initialization capability declaration:
// the agent's declared filters that curate the tools/list view. §25.12
// makes capability filtering a UX convenience — the scope and RBAC
// layers are what actually prevent unauthorized calls.
type Capabilities struct {
	// ReadOnly, when true, restricts the listed tools to the observation
	// category regardless of domain.
	ReadOnly bool
	// NonDestructive, when true, excludes tools in the destructive
	// category.
	NonDestructive bool
	// Scope, when set to "operability" or "admin", restricts the listed
	// tools to that category. Classification is by the tool's x-lenny-scope
	// domain prefix against the operability-domain set: "operability" keeps
	// only operability-domain tools and "admin" keeps the complement. The
	// two categories partition the full inventory.
	Scope string
	// TenantScoped, when true, restricts the listed tools to those a
	// tenant-admin can invoke, dropping every tool whose RequiredRole is
	// platform-admin.
	TenantScoped bool
}

// FilterForList returns the tools to show in a §25.12 tools/list
// response under the given capability declaration intersected with the
// caller's scope claim. callerScopes is the caller's §25.1 scope Set;
// filtering is always intersected with it, so a tool the claim does not
// permit is dropped regardless of the capability declaration. An absent
// claim (callerScopes.Present()==false) permits every scope per the
// §25.1 absent-claim semantics, so no scope narrowing applies.
//
// spec: §25.12 (Capability Negotiation) — "Filtering is ALWAYS
// intersected with the caller's scope claim (tools not permitted by
// scope are filtered out regardless of capability declaration)."
func (r *Registry) FilterForList(caps Capabilities, callerScopes scopes.Set) []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		t := r.byName[name]
		if caps.ReadOnly && !t.ReadOnly {
			continue
		}
		if caps.NonDestructive && t.Category == CategoryDestructive {
			continue
		}
		// §25.12: the operability/admin category filter classifies each tool
		// by its x-lenny-scope domain prefix against the operability-domain
		// set. "operability" lists only operability-domain tools; "admin"
		// lists the complement (the platform-management tools). The two
		// categories partition the inventory, so "admin" returns the
		// platform-management tools rather than an empty list.
		if caps.Scope == "operability" && !operabilityDomains[domainOf(t.Scope)] {
			continue
		}
		if caps.Scope == "admin" && operabilityDomains[domainOf(t.Scope)] {
			continue
		}
		// §25.12: tenantScoped lists only the tools a tenant-admin can
		// invoke, dropping every platform-admin-only tool.
		if caps.TenantScoped && t.RequiredRole == RolePlatformAdmin {
			continue
		}
		// §25.12: capability filtering is always intersected with the
		// caller's scope claim. Matches honors the §25.1 wildcard rules
		// and returns true for every scope when the claim is absent.
		if !callerScopes.Matches(t.Scope) {
			continue
		}
		out = append(out, t)
	}
	return out
}
