// SPDX-License-Identifier: MIT

// Package envaccess computes §10.6 environment access. It resolves
// which environments a caller belongs to and which runtimes are in
// scope for that caller under the transparent-filtering model: a
// caller sees the union of runtimes authorized across every
// environment where the caller's groups or subject hold a member
// role.
package envaccess

import (
	"sort"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// §10.6 noEnvironmentPolicy values. The platform default is deny-all.
const (
	PolicyDenyAll  = "deny-all"
	PolicyAllowAll = "allow-all"
)

// identityGroupType is the §10.6 member-identity type that names an
// OIDC group. Every other identity type names an individual caller and
// is matched against the caller's subject.
const identityGroupType = "oidc-group"

// Caller is the authenticated identity whose environment access is
// being resolved.
type Caller struct {
	// Subject is the caller's §10.2 subject (the JWT sub claim).
	Subject string
	// Groups is the caller's §10.6 OIDC group set.
	Groups []string
}

// matchesIdentity reports whether an environment member identity of the
// given type and value names this caller.
func (c Caller) matchesIdentity(typ, value string) bool {
	if value == "" {
		return false
	}
	if typ == identityGroupType {
		for _, g := range c.Groups {
			if g == value {
				return true
			}
		}
		return false
	}
	return value == c.Subject
}

// Membership reports the caller's §10.6 role in env and whether the
// caller is a member at all. When more than one member entry names the
// caller (for example through two of the caller's groups), the highest
// role wins under the viewer < creator < operator < admin escalation
// order.
func Membership(caller Caller, env environmentstore.Environment) (environment.Role, bool) {
	var best environment.Role
	found := false
	for _, m := range env.Members {
		if !caller.matchesIdentity(m.Identity.Type, m.Identity.Value) {
			continue
		}
		if !found || m.Role.AtLeast(best) {
			best = m.Role
		}
		found = true
	}
	return best, found
}

// MemberEnvironments returns the environments, in input order, that the
// caller is a member of.
func MemberEnvironments(caller Caller, envs []environmentstore.Environment) []environmentstore.Environment {
	out := make([]environmentstore.Environment, 0)
	for _, env := range envs {
		if _, ok := Membership(caller, env); ok {
			out = append(out, env)
		}
	}
	return out
}

// AuthorizedRuntimes returns the runtimes in scope for the caller under
// §10.6 transparent filtering: the union of runtimes admitted by the
// runtimeSelector of every environment the caller belongs to. A caller
// with no environment membership reaches every supplied runtime when
// noEnvPolicy is allow-all and none under deny-all (the platform
// default). The result is deduplicated by name and name-sorted.
//
// runtimes must already be scoped to the caller's tenant; this
// resolver does not perform tenant filtering.
func AuthorizedRuntimes(caller Caller, envs []environmentstore.Environment, runtimes []runtimestore.Runtime, noEnvPolicy string) []runtimestore.Runtime {
	memberEnvs := MemberEnvironments(caller, envs)
	if len(memberEnvs) == 0 {
		if noEnvPolicy == PolicyAllowAll {
			return sortedByName(runtimes)
		}
		return []runtimestore.Runtime{}
	}
	admitted := map[string]runtimestore.Runtime{}
	for _, env := range memberEnvs {
		for _, rt := range runtimes {
			if _, seen := admitted[rt.Name]; seen {
				continue
			}
			if env.RuntimeSelector.Matches(environment.Candidate{
				Name: rt.Name, Type: string(rt.Type), Labels: rt.Labels,
			}) {
				admitted[rt.Name] = rt
			}
		}
	}
	out := make([]runtimestore.Runtime, 0, len(admitted))
	for _, rt := range admitted {
		out = append(out, rt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// sortedByName returns a name-sorted copy of runtimes.
func sortedByName(runtimes []runtimestore.Runtime) []runtimestore.Runtime {
	out := append([]runtimestore.Runtime(nil), runtimes...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
