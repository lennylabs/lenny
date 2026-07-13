// SPDX-License-Identifier: MIT

package mcp

import (
	"sort"

	"github.com/lennylabs/lenny/pkg/common/scopes"
)

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
	// tools to that domain. The v1 management server exposes only
	// operability tools, so "admin" yields an empty list.
	Scope string
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
		// §25.12: the v1 management server exposes operability tools;
		// the "admin" scope filter (platform-management tools) yields an
		// empty list until the admin-API tools are generated.
		if caps.Scope == "admin" {
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
