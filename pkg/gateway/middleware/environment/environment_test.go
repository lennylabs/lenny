// SPDX-License-Identifier: MIT

package environment_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/envaccess"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	environmentmw "github.com/lennylabs/lenny/pkg/gateway/middleware/environment"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §10.6 transparent-filtering environment-resolver middleware.

// --- fixtures -------------------------------------------------------

func runtimeWithTeam(name, team string) runtimestore.Runtime {
	return runtimestore.Runtime{
		Name: name, Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": team},
	}
}

func connectorWithTeam(id, team string) connectorstore.Connector {
	return connectorstore.Connector{ID: id, Labels: map[string]string{"team": team}}
}

func groupMember(value string, role environment.Role) environmentstore.Member {
	return environmentstore.Member{
		Identity: environmentstore.Identity{Type: "oidc-group", Value: value},
		Role:     role,
	}
}

// teamEnv is an environment whose runtimeSelector and connectorSelector
// both admit resources tagged for one team.
func teamEnv(name, team string, members ...environmentstore.Member) environmentstore.Environment {
	return environmentstore.Environment{
		Name: name, TenantID: "acme", Members: members,
		RuntimeSelector:   environment.Selector{MatchLabels: map[string]string{"team": team}},
		ConnectorSelector: environmentstore.ConnectorSelector{Selector: environment.Selector{MatchLabels: map[string]string{"team": team}}},
	}
}

func runtimeNames(rts []runtimestore.Runtime) []string {
	out := make([]string, len(rts))
	for i, rt := range rts {
		out[i] = rt.Name
	}
	return out
}

func connectorIDs(cs []connectorstore.Connector) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
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

// captureHandler records the Resolution the middleware attached to the
// request context and reports 200.
type captureHandler struct {
	res    environmentmw.Resolution
	served bool
}

func (h *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.res = environmentmw.FromContext(r.Context())
	h.served = true
	w.WriteHeader(http.StatusOK)
}

// errEnvStore is an environmentstore.Store whose List always fails, so
// a test can drive the middleware's fail-closed path.
type errEnvStore struct{ environmentstore.Store }

func (errEnvStore) List(context.Context, string) ([]environmentstore.Environment, error) {
	return nil, errors.New("registry unavailable")
}

// serve drives one request through the middleware with the given
// principal already on the context (the auth middleware's job in
// production), returning the captured Resolution and the status code.
func serve(t *testing.T, opts environmentmw.Options, p authmw.Principal) (environmentmw.Resolution, int) {
	t.Helper()
	cap := &captureHandler{}
	h := environmentmw.Wrap(cap, opts)
	req := httptest.NewRequest(http.MethodGet, "/v1/runtimes", nil)
	req = req.WithContext(authmw.WithPrincipal(req.Context(), p))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return cap.res, rr.Code
}

// seed builds an environment + tenant + runtime registry for a test.
func seed(t *testing.T, envs []environmentstore.Environment, tenant tenantstore.Tenant) (environmentstore.Store, tenantstore.Store) {
	t.Helper()
	es := environmentstore.NewMemory()
	for _, e := range envs {
		if err := es.Create(context.Background(), e); err != nil {
			t.Fatalf("seed environment %q: %v", e.Name, err)
		}
	}
	ts := tenantstore.NewMemory()
	if err := ts.Create(context.Background(), tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return es, ts
}

// --- tests ----------------------------------------------------------

// A caller in environments A and B sees the union of the runtimes (and
// connectors) the two environments' selectors authorize.
func TestResolverUnionAcrossEnvironments(t *testing.T) {
	es, ts := seed(
		t,
		[]environmentstore.Environment{
			teamEnv("env-a", "security", groupMember("eng", environment.RoleCreator)),
			teamEnv("env-b", "research", groupMember("eng", environment.RoleViewer)),
		},
		tenantstore.Tenant{ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyDenyAll},
	)
	res, code := serve(t, environmentmw.Options{Environments: es, Tenants: ts},
		authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"eng"}})
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", code)
	}
	if !res.Configured {
		t.Fatal("resolution must be configured when the registries are wired")
	}
	if len(res.MemberEnvironments) != 2 {
		t.Errorf("member environments: got %d, want 2 (env-a, env-b)", len(res.MemberEnvironments))
	}
	runtimes := []runtimestore.Runtime{
		runtimeWithTeam("sec-agent", "security"),
		runtimeWithTeam("research-agent", "research"),
		runtimeWithTeam("ops-agent", "operations"),
	}
	got := runtimeNames(res.FilterRuntimes(runtimes))
	if !equalStrings(got, []string{"research-agent", "sec-agent"}) {
		t.Errorf("runtime union: got %v, want [research-agent sec-agent]", got)
	}
	connectors := []connectorstore.Connector{
		connectorWithTeam("sec-vault", "security"),
		connectorWithTeam("research-index", "research"),
		connectorWithTeam("ops-store", "operations"),
	}
	gotC := connectorIDs(res.FilterConnectors(connectors))
	if !equalStrings(gotC, []string{"research-index", "sec-vault"}) {
		t.Errorf("connector union: got %v, want [research-index sec-vault]", gotC)
	}
}

// A caller in no environment is governed by the §10.6
// noEnvironmentPolicy: deny-all yields an empty view, allow-all yields
// every tenant runtime.
func TestResolverNoEnvironmentPolicy(t *testing.T) {
	runtimes := []runtimestore.Runtime{
		runtimeWithTeam("sec-agent", "security"),
		runtimeWithTeam("research-agent", "research"),
	}

	t.Run("deny-all", func(t *testing.T) {
		es, ts := seed(
			t,
			[]environmentstore.Environment{teamEnv("env-a", "security", groupMember("eng", environment.RoleCreator))},
			tenantstore.Tenant{ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyDenyAll},
		)
		// bob is in no environment — the "outsiders" group matches no member.
		res, _ := serve(t, environmentmw.Options{Environments: es, Tenants: ts},
			authmw.Principal{Subject: "bob", TenantID: "acme", Groups: []string{"outsiders"}})
		if got := res.FilterRuntimes(runtimes); len(got) != 0 {
			t.Errorf("deny-all no-environment caller: got %v, want empty", runtimeNames(got))
		}
	})

	t.Run("allow-all", func(t *testing.T) {
		es, ts := seed(
			t,
			[]environmentstore.Environment{teamEnv("env-a", "security", groupMember("eng", environment.RoleCreator))},
			tenantstore.Tenant{ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyAllowAll},
		)
		res, _ := serve(t, environmentmw.Options{Environments: es, Tenants: ts},
			authmw.Principal{Subject: "bob", TenantID: "acme", Groups: []string{"outsiders"}})
		got := runtimeNames(res.FilterRuntimes(runtimes))
		if !equalStrings(got, []string{"research-agent", "sec-agent"}) {
			t.Errorf("allow-all no-environment caller: got %v, want every runtime", got)
		}
	})

	t.Run("tenant value overrides the platform default", func(t *testing.T) {
		// The tenant explicitly sets deny-all; the platform default is
		// allow-all. §10.6: the per-tenant value takes precedence.
		es, ts := seed(
			t,
			[]environmentstore.Environment{teamEnv("env-a", "security", groupMember("eng", environment.RoleCreator))},
			tenantstore.Tenant{ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyDenyAll},
		)
		res, _ := serve(t, environmentmw.Options{
			Environments:               es,
			Tenants:                    ts,
			DefaultNoEnvironmentPolicy: tenantstore.NoEnvPolicyAllowAll,
		}, authmw.Principal{Subject: "bob", TenantID: "acme", Groups: []string{"outsiders"}})
		if res.Policy != tenantstore.NoEnvPolicyDenyAll {
			t.Errorf("policy: got %q, want the per-tenant deny-all", res.Policy)
		}
		if got := res.FilterRuntimes(runtimes); len(got) != 0 {
			t.Errorf("per-tenant deny-all must override platform allow-all: got %v", runtimeNames(got))
		}
	})

	t.Run("platform default applies when the tenant set none", func(t *testing.T) {
		// The tenant leaves NoEnvironmentPolicy empty; the platform
		// default (allow-all) reaches the resolver.
		es, ts := seed(
			t,
			[]environmentstore.Environment{teamEnv("env-a", "security", groupMember("eng", environment.RoleCreator))},
			tenantstore.Tenant{ID: "acme"},
		)
		res, _ := serve(t, environmentmw.Options{
			Environments:               es,
			Tenants:                    ts,
			DefaultNoEnvironmentPolicy: tenantstore.NoEnvPolicyAllowAll,
		}, authmw.Principal{Subject: "bob", TenantID: "acme", Groups: []string{"outsiders"}})
		if res.Policy != tenantstore.NoEnvPolicyAllowAll {
			t.Errorf("policy: got %q, want the platform-default allow-all", res.Policy)
		}
		got := runtimeNames(res.FilterRuntimes(runtimes))
		if !equalStrings(got, []string{"research-agent", "sec-agent"}) {
			t.Errorf("platform-default allow-all: got %v, want every runtime", got)
		}
	})
}

// §10.6 environment membership resolves through the caller's OIDC
// groups: a caller whose JWT carries the group named by an `oidc-group`
// member entry is a member; an unrelated group is not.
func TestResolverOIDCGroupDerivedMembership(t *testing.T) {
	es, ts := seed(
		t,
		[]environmentstore.Environment{
			teamEnv("security-team", "security", groupMember("security-engineers", environment.RoleCreator)),
		},
		tenantstore.Tenant{ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyDenyAll},
	)
	runtimes := []runtimestore.Runtime{runtimeWithTeam("sec-agent", "security")}

	// alice carries the security-engineers group → member of security-team.
	member, _ := serve(t, environmentmw.Options{Environments: es, Tenants: ts},
		authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"security-engineers"}})
	if len(member.MemberEnvironments) != 1 || member.MemberEnvironments[0].Name != "security-team" {
		t.Errorf("OIDC-group member: got %d environments, want only security-team", len(member.MemberEnvironments))
	}
	if got := runtimeNames(member.FilterRuntimes(runtimes)); !equalStrings(got, []string{"sec-agent"}) {
		t.Errorf("OIDC-group member view: got %v, want [sec-agent]", got)
	}

	// carol carries an unrelated group → no membership, deny-all view.
	nonMember, _ := serve(t, environmentmw.Options{Environments: es, Tenants: ts},
		authmw.Principal{Subject: "carol", TenantID: "acme", Groups: []string{"marketing"}})
	if len(nonMember.MemberEnvironments) != 0 {
		t.Errorf("unrelated-group caller: got %d environments, want none", len(nonMember.MemberEnvironments))
	}
	if got := nonMember.FilterRuntimes(runtimes); len(got) != 0 {
		t.Errorf("unrelated-group caller view: got %v, want empty under deny-all", runtimeNames(got))
	}
}

// The Resolution carries every tenant environment in AllEnvironments,
// so the delegation path can honor a §10.6 bilateral cross-environment
// declaration through envaccess.CrossEnvironmentReachable.
func TestResolverCrossEnvironmentDeclarationHonored(t *testing.T) {
	sharedSel := environment.Selector{MatchLabels: map[string]string{"shared": "true"}}
	// team-a: the caller's environment. Its own runtimeSelector admits
	// only team-a-tagged runtimes — the shared runtime is not in its
	// transparent-filter view. It declares an outbound rule to team-b.
	// team-b: a peer that admits the shared runtime and declares the
	// reciprocal inbound from team-a.
	teamA := environmentstore.Environment{
		Name: "team-a", TenantID: "acme",
		Members:         []environmentstore.Member{groupMember("eng", environment.RoleCreator)},
		RuntimeSelector: environment.Selector{MatchLabels: map[string]string{"team": "a"}},
		CrossEnvOutbound: []environmentstore.CrossEnvRule{
			{Environment: "team-b", Runtimes: sharedSel},
		},
	}
	teamB := environmentstore.Environment{
		Name: "team-b", TenantID: "acme", RuntimeSelector: sharedSel,
		CrossEnvInbound: []environmentstore.CrossEnvRule{
			{Environment: "team-a", Runtimes: sharedSel},
		},
	}
	es, ts := seed(t, []environmentstore.Environment{teamA, teamB},
		tenantstore.Tenant{ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyDenyAll})

	res, _ := serve(t, environmentmw.Options{Environments: es, Tenants: ts},
		authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"eng"}})
	if len(res.AllEnvironments) != 2 {
		t.Fatalf("AllEnvironments: got %d, want 2", len(res.AllEnvironments))
	}

	sharedRuntime := runtimestore.Runtime{
		Name: "shared-tool", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"shared": "true"},
	}
	// The shared runtime is not in team-a's own runtimeSelector, so the
	// transparent-filter view excludes it.
	if got := res.FilterRuntimes([]runtimestore.Runtime{sharedRuntime}); len(got) != 0 {
		t.Errorf("transparent filter must not surface a cross-environment runtime: got %v", runtimeNames(got))
	}
	// The delegation path consults the bilateral declaration over the
	// Resolution's AllEnvironments and finds the runtime reachable.
	if !envaccess.CrossEnvironmentReachable("team-a", sharedRuntime, res.AllEnvironments) {
		t.Error("a bilateral team-a <-> team-b declaration must make the shared runtime reachable")
	}
	// Removing team-b's inbound declaration breaks the bilateral link.
	noInbound := []environmentstore.Environment{
		teamA,
		{Name: "team-b", TenantID: "acme", RuntimeSelector: sharedSel},
	}
	if envaccess.CrossEnvironmentReachable("team-a", sharedRuntime, noInbound) {
		t.Error("an outbound declaration alone must not grant cross-environment reach")
	}
}

// The middleware is a no-op when the environment registry is not wired:
// the request passes through with an unconfigured Resolution and
// FilterRuntimes returns its input unchanged.
func TestResolverNoOpWhenRegistryUnwired(t *testing.T) {
	runtimes := []runtimestore.Runtime{
		runtimeWithTeam("sec-agent", "security"),
		runtimeWithTeam("research-agent", "research"),
	}
	res, code := serve(t, environmentmw.Options{Tenants: tenantstore.NewMemory()},
		authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"eng"}})
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", code)
	}
	if res.Configured {
		t.Error("resolution must not be configured when the environment registry is nil")
	}
	if got := res.FilterRuntimes(runtimes); !equalStrings(runtimeNames(got), runtimeNames(runtimes)) {
		t.Errorf("unconfigured FilterRuntimes must pass the list through: got %v", runtimeNames(got))
	}
}

// The middleware is a no-op for a request that carries no authenticated
// principal — only routes that do not require auth can reach this state.
func TestResolverNoOpWhenUnauthenticated(t *testing.T) {
	es, ts := seed(
		t,
		[]environmentstore.Environment{teamEnv("env-a", "security", groupMember("eng", environment.RoleCreator))},
		tenantstore.Tenant{ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyDenyAll},
	)
	cap := &captureHandler{}
	h := environmentmw.Wrap(cap, environmentmw.Options{Environments: es, Tenants: ts})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil) // no principal on context
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !cap.served {
		t.Fatal("an unauthenticated request must still reach the inner handler")
	}
	if cap.res.Configured {
		t.Error("resolution must not be configured for an unauthenticated request")
	}
}

// §10.6 transparent filtering is an authorization boundary: a registry
// read failure fails the request closed with 500 rather than serving an
// unfiltered view.
func TestResolverFailsClosedOnStoreError(t *testing.T) {
	cap := &captureHandler{}
	h := environmentmw.Wrap(cap, environmentmw.Options{
		Environments: errEnvStore{environmentstore.NewMemory()},
		Tenants:      tenantstore.NewMemory(),
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/runtimes", nil)
	req = req.WithContext(authmw.WithPrincipal(req.Context(),
		authmw.Principal{Subject: "alice", TenantID: "acme"}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("store error: got status %d, want 500", rr.Code)
	}
	if cap.served {
		t.Error("the inner handler must not run when the §10.6 resolver fails closed")
	}
}

// Resolve produces the same Resolution as the HTTP middleware, so a
// handler reached outside the middleware chain (an MCP tool in a unit
// test) filters identically.
func TestResolveMatchesMiddleware(t *testing.T) {
	es, ts := seed(
		t,
		[]environmentstore.Environment{teamEnv("env-a", "security", groupMember("eng", environment.RoleCreator))},
		tenantstore.Tenant{ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyDenyAll},
	)
	caller := envaccess.Caller{Subject: "alice", Groups: []string{"eng"}}
	res, err := environmentmw.Resolve(context.Background(), es, ts, "", caller, "acme")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !res.Configured || len(res.MemberEnvironments) != 1 {
		t.Fatalf("Resolve resolution: configured=%v members=%d", res.Configured, len(res.MemberEnvironments))
	}
	runtimes := []runtimestore.Runtime{
		runtimeWithTeam("sec-agent", "security"),
		runtimeWithTeam("research-agent", "research"),
	}
	if got := runtimeNames(res.FilterRuntimes(runtimes)); !equalStrings(got, []string{"sec-agent"}) {
		t.Errorf("Resolve FilterRuntimes: got %v, want [sec-agent]", got)
	}

	// Resolve with a nil registry returns an unconfigured Resolution.
	unconfigured, err := environmentmw.Resolve(context.Background(), nil, ts, "", caller, "acme")
	if err != nil {
		t.Fatalf("Resolve nil registry: %v", err)
	}
	if unconfigured.Configured {
		t.Error("Resolve with a nil environment registry must be unconfigured")
	}
}

// The Resolution survives a context round-trip via WithResolution /
// FromContext.
func TestResolutionContextRoundTrip(t *testing.T) {
	want := environmentmw.Resolution{Configured: true, TenantID: "acme", Policy: tenantstore.NoEnvPolicyAllowAll}
	ctx := environmentmw.WithResolution(context.Background(), want)
	got := environmentmw.FromContext(ctx)
	if got.TenantID != "acme" || got.Policy != tenantstore.NoEnvPolicyAllowAll || !got.Configured {
		t.Errorf("context round-trip: got %+v, want %+v", got, want)
	}
	// A context with no Resolution yields the unconfigured zero value.
	if environmentmw.FromContext(context.Background()).Configured {
		t.Error("FromContext on a bare context must return an unconfigured Resolution")
	}
}
