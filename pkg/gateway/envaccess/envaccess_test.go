// SPDX-License-Identifier: MIT

package envaccess_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/envaccess"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// spec: §10.6 environment access — transparent filtering.

func groupMember(value string, role environment.Role) environmentstore.Member {
	return environmentstore.Member{
		Identity: environmentstore.Identity{Type: "oidc-group", Value: value},
		Role:     role,
	}
}

func userMember(subject string, role environment.Role) environmentstore.Member {
	return environmentstore.Member{
		Identity: environmentstore.Identity{Type: "oidc-user", Value: subject},
		Role:     role,
	}
}

func runtimeWithTeam(name, team string) runtimestore.Runtime {
	return runtimestore.Runtime{
		Name: name, Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": team},
	}
}

func teamEnv(name, team string, members ...environmentstore.Member) environmentstore.Environment {
	return environmentstore.Environment{
		Name: name, TenantID: "acme", Members: members,
		RuntimeSelector: environment.Selector{MatchLabels: map[string]string{"team": team}},
	}
}

func names(rts []runtimestore.Runtime) []string {
	out := make([]string, len(rts))
	for i, rt := range rts {
		out[i] = rt.Name
	}
	return out
}

func TestMembershipGroupMatch(t *testing.T) {
	env := teamEnv("security-team", "security",
		groupMember("security-engineers", environment.RoleCreator))
	role, ok := envaccess.Membership(
		envaccess.Caller{Subject: "alice", Groups: []string{"security-engineers"}}, env,
	)
	if !ok || role != environment.RoleCreator {
		t.Errorf("group match: role=%q ok=%v, want creator true", role, ok)
	}
}

func TestMembershipSubjectMatch(t *testing.T) {
	env := teamEnv("security-team", "security", userMember("alice", environment.RoleAdmin))
	role, ok := envaccess.Membership(envaccess.Caller{Subject: "alice"}, env)
	if !ok || role != environment.RoleAdmin {
		t.Errorf("subject match: role=%q ok=%v, want admin true", role, ok)
	}
}

func TestMembershipHighestRoleWins(t *testing.T) {
	// The caller is in two groups, each a member at a different role.
	env := teamEnv("security-team", "security",
		groupMember("engineers", environment.RoleViewer),
		groupMember("leads", environment.RoleAdmin))
	role, ok := envaccess.Membership(
		envaccess.Caller{Subject: "alice", Groups: []string{"engineers", "leads"}}, env,
	)
	if !ok || role != environment.RoleAdmin {
		t.Errorf("highest role: role=%q ok=%v, want admin true", role, ok)
	}
}

func TestMembershipNonMember(t *testing.T) {
	env := teamEnv("security-team", "security",
		groupMember("security-engineers", environment.RoleCreator))
	if _, ok := envaccess.Membership(
		envaccess.Caller{Subject: "bob", Groups: []string{"research-team"}}, env,
	); ok {
		t.Error("a caller in no matching group must not be a member")
	}
}

func TestMemberEnvironments(t *testing.T) {
	envs := []environmentstore.Environment{
		teamEnv("security-team", "security", groupMember("eng", environment.RoleCreator)),
		teamEnv("research-team", "research", groupMember("scientists", environment.RoleViewer)),
	}
	got := envaccess.MemberEnvironments(
		envaccess.Caller{Subject: "alice", Groups: []string{"eng"}}, envs,
	)
	if len(got) != 1 || got[0].Name != "security-team" {
		t.Errorf("member environments: got %d, want only security-team", len(got))
	}
}

func TestAuthorizedRuntimesUnionAndDedup(t *testing.T) {
	runtimes := []runtimestore.Runtime{
		runtimeWithTeam("sec-scanner", "security"),
		runtimeWithTeam("research-agent", "research"),
		runtimeWithTeam("shared-tool", "security"),
	}
	envs := []environmentstore.Environment{
		teamEnv("security-team", "security", groupMember("eng", environment.RoleCreator)),
		teamEnv("research-team", "research", groupMember("eng", environment.RoleViewer)),
	}
	// The caller belongs to both environments via the "eng" group, so
	// the result unions both selectors.
	got := envaccess.AuthorizedRuntimes(
		envaccess.Caller{Subject: "alice", Groups: []string{"eng"}},
		envs, runtimes, envaccess.PolicyDenyAll,
	)
	want := []string{"research-agent", "sec-scanner", "shared-tool"}
	if got2 := names(got); !equalStrings(got2, want) {
		t.Errorf("union: got %v, want %v", got2, want)
	}
}

func TestAuthorizedRuntimesNoMembershipDenyAll(t *testing.T) {
	runtimes := []runtimestore.Runtime{runtimeWithTeam("sec-scanner", "security")}
	envs := []environmentstore.Environment{
		teamEnv("security-team", "security", groupMember("eng", environment.RoleCreator)),
	}
	got := envaccess.AuthorizedRuntimes(
		envaccess.Caller{Subject: "bob", Groups: []string{"outsiders"}},
		envs, runtimes, envaccess.PolicyDenyAll,
	)
	if len(got) != 0 {
		t.Errorf("deny-all with no membership: got %v, want empty", names(got))
	}
}

func TestAuthorizedRuntimesNoMembershipAllowAll(t *testing.T) {
	runtimes := []runtimestore.Runtime{
		runtimeWithTeam("b-runtime", "x"), runtimeWithTeam("a-runtime", "y"),
	}
	got := envaccess.AuthorizedRuntimes(
		envaccess.Caller{Subject: "bob"}, nil, runtimes, envaccess.PolicyAllowAll,
	)
	if got2 := names(got); !equalStrings(got2, []string{"a-runtime", "b-runtime"}) {
		t.Errorf("allow-all with no membership: got %v, want all runtimes name-sorted", got2)
	}
}

func TestAuthorizedRuntimesMemberScopedBySelector(t *testing.T) {
	// A member sees only what their environment's runtimeSelector
	// admits — noEnvironmentPolicy does not widen a member's view.
	runtimes := []runtimestore.Runtime{
		runtimeWithTeam("sec-scanner", "security"),
		runtimeWithTeam("research-agent", "research"),
	}
	envs := []environmentstore.Environment{
		teamEnv("security-team", "security", groupMember("eng", environment.RoleCreator)),
	}
	got := envaccess.AuthorizedRuntimes(
		envaccess.Caller{Subject: "alice", Groups: []string{"eng"}},
		envs, runtimes, envaccess.PolicyAllowAll,
	)
	if got2 := names(got); !equalStrings(got2, []string{"sec-scanner"}) {
		t.Errorf("member scoped by selector: got %v, want only sec-scanner", got2)
	}
}

// sharedTool is the cross-environment delegation target used below.
var sharedTool = runtimestore.Runtime{
	Name: "shared-tool", Type: runtimestore.TypeAgent,
	Labels: map[string]string{"shared": "true"},
}

func crossEnvRule(peer string) environmentstore.CrossEnvRule {
	return environmentstore.CrossEnvRule{
		Environment: peer,
		Runtimes:    environment.Selector{MatchLabels: map[string]string{"shared": "true"}},
	}
}

func TestCrossEnvironmentReachableBilateral(t *testing.T) {
	sharedSel := environment.Selector{MatchLabels: map[string]string{"shared": "true"}}
	envs := []environmentstore.Environment{
		{
			Name: "team-a", TenantID: "acme",
			CrossEnvOutbound: []environmentstore.CrossEnvRule{crossEnvRule("team-b")},
		},
		{
			Name: "team-b", TenantID: "acme", RuntimeSelector: sharedSel,
			CrossEnvInbound: []environmentstore.CrossEnvRule{crossEnvRule("team-a")},
		},
	}
	if !envaccess.CrossEnvironmentReachable("team-a", sharedTool, envs) {
		t.Error("a bilateral team-a <-> team-b declaration should make the shared tool reachable")
	}
}

func TestCrossEnvironmentReachableWildcardInbound(t *testing.T) {
	sharedSel := environment.Selector{MatchLabels: map[string]string{"shared": "true"}}
	envs := []environmentstore.Environment{
		{
			Name: "team-a", TenantID: "acme",
			CrossEnvOutbound: []environmentstore.CrossEnvRule{crossEnvRule("team-b")},
		},
		{
			Name: "team-b", TenantID: "acme", RuntimeSelector: sharedSel,
			CrossEnvInbound: []environmentstore.CrossEnvRule{crossEnvRule("*")},
		},
	}
	if !envaccess.CrossEnvironmentReachable("team-a", sharedTool, envs) {
		t.Error("a wildcard inbound source should admit team-a")
	}
}

func TestCrossEnvironmentUnreachableWithoutBothSides(t *testing.T) {
	sharedSel := environment.Selector{MatchLabels: map[string]string{"shared": "true"}}
	// team-a declares outbound but team-b has no reciprocal inbound.
	noInbound := []environmentstore.Environment{
		{
			Name: "team-a", TenantID: "acme",
			CrossEnvOutbound: []environmentstore.CrossEnvRule{crossEnvRule("team-b")},
		},
		{Name: "team-b", TenantID: "acme", RuntimeSelector: sharedSel},
	}
	if envaccess.CrossEnvironmentReachable("team-a", sharedTool, noInbound) {
		t.Error("an outbound declaration alone must not grant cross-environment access")
	}
	// team-b declares inbound but team-a has no outbound.
	noOutbound := []environmentstore.Environment{
		{Name: "team-a", TenantID: "acme"},
		{
			Name: "team-b", TenantID: "acme", RuntimeSelector: sharedSel,
			CrossEnvInbound: []environmentstore.CrossEnvRule{crossEnvRule("team-a")},
		},
	}
	if envaccess.CrossEnvironmentReachable("team-a", sharedTool, noOutbound) {
		t.Error("an inbound declaration alone must not grant cross-environment access")
	}
}

func TestCrossEnvironmentUnreachableWhenTargetNotInPeer(t *testing.T) {
	// The bilateral declaration exists, but team-b's runtimeSelector
	// does not admit the target — team-b is not a home of the runtime.
	envs := []environmentstore.Environment{
		{
			Name: "team-a", TenantID: "acme",
			CrossEnvOutbound: []environmentstore.CrossEnvRule{crossEnvRule("team-b")},
		},
		{
			Name: "team-b", TenantID: "acme",
			RuntimeSelector: environment.Selector{MatchLabels: map[string]string{"team": "other"}},
			CrossEnvInbound: []environmentstore.CrossEnvRule{crossEnvRule("team-a")},
		},
	}
	if envaccess.CrossEnvironmentReachable("team-a", sharedTool, envs) {
		t.Error("a target the peer environment does not admit must not be reachable")
	}
}

func TestCrossEnvironmentReachableUnknownCallerEnv(t *testing.T) {
	if envaccess.CrossEnvironmentReachable("ghost", sharedTool, nil) {
		t.Error("an unknown caller environment must not be cross-environment reachable")
	}
}

// connectorWithTeam is a §9.3 connector tagged for one team. The §10.6
// connectorSelector matches on the connector's label map.
func connectorWithTeam(id, team string) connectorstore.Connector {
	return connectorstore.Connector{
		ID: id, Labels: map[string]string{"team": team},
	}
}

// teamConnectorEnv is an environment whose connectorSelector admits
// connectors tagged for one team.
func teamConnectorEnv(name, team string, members ...environmentstore.Member) environmentstore.Environment {
	return environmentstore.Environment{
		Name: name, TenantID: "acme", Members: members,
		ConnectorSelector: environmentstore.ConnectorSelector{Selector: environment.Selector{MatchLabels: map[string]string{"team": team}}},
	}
}

func connectorIDs(cs []connectorstore.Connector) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func TestAuthorizedConnectorsUnionAndDedup(t *testing.T) {
	connectors := []connectorstore.Connector{
		connectorWithTeam("sec-vault", "security"),
		connectorWithTeam("research-index", "research"),
		connectorWithTeam("shared-store", "security"),
	}
	envs := []environmentstore.Environment{
		teamConnectorEnv("security-team", "security", groupMember("eng", environment.RoleCreator)),
		teamConnectorEnv("research-team", "research", groupMember("eng", environment.RoleViewer)),
	}
	// The caller belongs to both environments via the "eng" group, so
	// the result unions both connectorSelectors.
	got := envaccess.AuthorizedConnectors(
		envaccess.Caller{Subject: "alice", Groups: []string{"eng"}},
		envs, connectors, envaccess.PolicyDenyAll,
	)
	want := []string{"research-index", "sec-vault", "shared-store"}
	if got2 := connectorIDs(got); !equalStrings(got2, want) {
		t.Errorf("connector union: got %v, want %v", got2, want)
	}
}

func TestAuthorizedConnectorsNoMembershipDenyAll(t *testing.T) {
	connectors := []connectorstore.Connector{connectorWithTeam("sec-vault", "security")}
	got := envaccess.AuthorizedConnectors(
		envaccess.Caller{Subject: "bob", Groups: []string{"outsiders"}},
		nil, connectors, envaccess.PolicyDenyAll,
	)
	if len(got) != 0 {
		t.Errorf("deny-all with no membership: got %v, want empty", connectorIDs(got))
	}
}

func TestAuthorizedConnectorsNoMembershipAllowAll(t *testing.T) {
	connectors := []connectorstore.Connector{
		connectorWithTeam("b-conn", "x"), connectorWithTeam("a-conn", "y"),
	}
	got := envaccess.AuthorizedConnectors(
		envaccess.Caller{Subject: "bob"}, nil, connectors, envaccess.PolicyAllowAll,
	)
	if got2 := connectorIDs(got); !equalStrings(got2, []string{"a-conn", "b-conn"}) {
		t.Errorf("allow-all with no membership: got %v, want all connectors id-sorted", got2)
	}
}

func TestAuthorizedConnectorsMemberScopedBySelector(t *testing.T) {
	// A member sees only what their environment's connectorSelector
	// admits — noEnvironmentPolicy does not widen a member's view.
	connectors := []connectorstore.Connector{
		connectorWithTeam("sec-vault", "security"),
		connectorWithTeam("research-index", "research"),
	}
	envs := []environmentstore.Environment{
		teamConnectorEnv("security-team", "security", groupMember("eng", environment.RoleCreator)),
	}
	got := envaccess.AuthorizedConnectors(
		envaccess.Caller{Subject: "alice", Groups: []string{"eng"}},
		envs, connectors, envaccess.PolicyAllowAll,
	)
	if got2 := connectorIDs(got); !equalStrings(got2, []string{"sec-vault"}) {
		t.Errorf("member scoped by selector: got %v, want only sec-vault", got2)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
