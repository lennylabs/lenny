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
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
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

// AuthorizedConnectors returns the connectors in scope for the caller
// under §10.6 transparent filtering: the union of connectors admitted
// by the connectorSelector of every environment the caller belongs to.
// A caller with no environment membership reaches every supplied
// connector when noEnvPolicy is allow-all and none under deny-all (the
// platform default). The result is deduplicated by id and id-sorted.
//
// §10.6 scopes connectorSelector to the internal connector surface —
// "Connectors are never cross-environment", so unlike AuthorizedRuntimes
// there is no cross-environment widening. connectors must already be
// scoped to the caller's tenant; this resolver does not perform tenant
// filtering.
func AuthorizedConnectors(caller Caller, envs []environmentstore.Environment, connectors []connectorstore.Connector, noEnvPolicy string) []connectorstore.Connector {
	memberEnvs := MemberEnvironments(caller, envs)
	if len(memberEnvs) == 0 {
		if noEnvPolicy == PolicyAllowAll {
			return sortedConnectorsByID(connectors)
		}
		return []connectorstore.Connector{}
	}
	admitted := map[string]connectorstore.Connector{}
	for _, env := range memberEnvs {
		for _, c := range connectors {
			if _, seen := admitted[c.ID]; seen {
				continue
			}
			if env.ConnectorSelector.Selector.Matches(environment.Candidate{
				Name: c.ID, Labels: c.Labels,
			}) {
				admitted[c.ID] = c
			}
		}
	}
	out := make([]connectorstore.Connector, 0, len(admitted))
	for _, c := range admitted {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CrossEnvironmentReachable reports whether the target runtime is
// reachable from the caller's environment through a §10.6 bilateral
// cross-environment-delegation declaration. It is reachable when some
// other environment B admits the target via its runtimeSelector, the
// caller's environment has an outbound rule for B whose runtime
// selector admits the target, and B has an inbound rule for the
// caller's environment (or the "*" wildcard source) whose selector
// also admits the target. Both sides must permit the delegation;
// neither environment can grant it unilaterally.
func CrossEnvironmentReachable(callerEnvName string, target runtimestore.Runtime, envs []environmentstore.Environment) bool {
	callerEnv, ok := findEnvironment(callerEnvName, envs)
	if !ok {
		return false
	}
	cand := environment.Candidate{
		Name: target.Name, Type: string(target.Type), Labels: target.Labels,
	}
	for _, b := range envs {
		if b.Name == callerEnvName {
			continue
		}
		// b must be a home environment of the target — its
		// runtimeSelector admits the runtime.
		if !b.RuntimeSelector.Matches(cand) {
			continue
		}
		if outboundPermits(callerEnv, b.Name, cand) && inboundPermits(b, callerEnvName, cand) {
			return true
		}
	}
	return false
}

// findEnvironment returns the environment named name.
func findEnvironment(name string, envs []environmentstore.Environment) (environmentstore.Environment, bool) {
	for _, e := range envs {
		if e.Name == name {
			return e, true
		}
	}
	return environmentstore.Environment{}, false
}

// outboundPermits reports whether env has an outbound cross-environment
// rule that targets targetEnv and whose runtime selector admits cand.
func outboundPermits(env environmentstore.Environment, targetEnv string, cand environment.Candidate) bool {
	for _, rule := range env.CrossEnvOutbound {
		if rule.Environment == targetEnv && rule.Runtimes.Matches(cand) {
			return true
		}
	}
	return false
}

// inboundPermits reports whether env has an inbound cross-environment
// rule that admits sourceEnv — by exact name or the "*" wildcard — and
// whose runtime selector admits cand.
func inboundPermits(env environmentstore.Environment, sourceEnv string, cand environment.Candidate) bool {
	for _, rule := range env.CrossEnvInbound {
		if (rule.Environment == sourceEnv || rule.Environment == "*") && rule.Runtimes.Matches(cand) {
			return true
		}
	}
	return false
}

// sortedByName returns a name-sorted copy of runtimes.
func sortedByName(runtimes []runtimestore.Runtime) []runtimestore.Runtime {
	out := append([]runtimestore.Runtime(nil), runtimes...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// sortedConnectorsByID returns an id-sorted copy of connectors.
func sortedConnectorsByID(connectors []connectorstore.Connector) []connectorstore.Connector {
	out := append([]connectorstore.Connector(nil), connectors...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
