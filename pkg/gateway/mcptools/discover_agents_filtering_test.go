// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §10.6 transparent filtering on lenny/discover_agents.

func newMCPFiltered(t *testing.T) (*mcp.Server, runtimestore.Store, environmentstore.Store, tenantstore.Store) {
	t.Helper()
	runtimes := runtimestore.NewMemory()
	envs := environmentstore.NewMemory()
	tenants := tenantstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        memstore.New(),
		Executor:     executor.NewEchoExecutor(),
		Runtimes:     runtimes,
		Environments: envs,
		Tenants:      tenants,
		Clock:        func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:       func() string { return "sess_mcp" },
		TenantID:     "acme",
	})
	return srv, runtimes, envs, tenants
}

// callAs invokes an MCP tool with an authenticated principal on the
// request context, so §10.6 transparent filtering can resolve the
// caller's environment membership.
func callAs(t *testing.T, h http.Handler, principal authmw.Principal, tool, args string) map[string]any {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), principal))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	return resp
}

func securityEnv(selector environment.Selector) environmentstore.Environment {
	return environmentstore.Environment{
		Name: "security-team", TenantID: "acme",
		Members: []environmentstore.Member{{
			Identity: environmentstore.Identity{Type: "oidc-group", Value: "security-engineers"},
			Role:     environment.RoleCreator,
		}},
		RuntimeSelector: selector,
	}
}

func TestDiscoverAgentsTransparentFiltering(t *testing.T) {
	srv, runtimes, envs, tenants := newMCPFiltered(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "security"},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "research-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "research"},
	})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	_ = envs.Create(context.Background(), securityEnv(
		environment.Selector{MatchLabels: map[string]string{"team": "security"}}))

	// A caller in the security-engineers group sees only the runtime
	// the environment's runtimeSelector admits.
	caller := authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"security-engineers"}}
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/discover_agents", `{}`))
	if !strings.Contains(text, "sec-agent") {
		t.Errorf("member should see sec-agent: %q", text)
	}
	if strings.Contains(text, "research-agent") {
		t.Errorf("member must not see research-agent — out of environment scope: %q", text)
	}
}

func TestDiscoverAgentsDenyAllNoMembership(t *testing.T) {
	srv, runtimes, envs, tenants := newMCPFiltered(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-agent", Type: runtimestore.TypeAgent,
	})
	// noEnvironmentPolicy unset — the §10.6 platform default deny-all.
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	_ = envs.Create(context.Background(), securityEnv(environment.Selector{}))

	// A caller in no environment under deny-all reaches no agent.
	caller := authmw.Principal{Subject: "bob", TenantID: "acme", Groups: []string{"outsiders"}}
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/discover_agents", `{}`))
	if strings.Contains(text, "sec-agent") {
		t.Errorf("a deny-all non-member must reach no agents: %q", text)
	}
}

func TestDiscoverAgentsAllowAllNoMembership(t *testing.T) {
	srv, runtimes, envs, tenants := newMCPFiltered(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-agent", Type: runtimestore.TypeAgent,
	})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyAllowAll,
	})
	_ = envs.Create(context.Background(), securityEnv(environment.Selector{}))

	// Under allow-all a caller in no environment reaches every agent.
	caller := authmw.Principal{Subject: "bob", TenantID: "acme", Groups: []string{"outsiders"}}
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/discover_agents", `{}`))
	if !strings.Contains(text, "sec-agent") {
		t.Errorf("an allow-all non-member should reach all agents: %q", text)
	}
}

// buildFilteredMCP builds an MCP server with the §10.6 filtering deps
// and a platform-wide DefaultNoEnvironmentPolicy.
func buildFilteredMCP(t *testing.T, defaultPolicy string) (*mcp.Server, runtimestore.Store, environmentstore.Store, tenantstore.Store) {
	t.Helper()
	runtimes := runtimestore.NewMemory()
	envs := environmentstore.NewMemory()
	tenants := tenantstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:                      memstore.New(),
		Executor:                   executor.NewEchoExecutor(),
		Runtimes:                   runtimes,
		Environments:               envs,
		Tenants:                    tenants,
		DefaultNoEnvironmentPolicy: defaultPolicy,
		Clock:                      func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:                     func() string { return "sess_mcp" },
		TenantID:                   "acme",
	})
	return srv, runtimes, envs, tenants
}

func TestDiscoverAgentsPlatformDefaultFallback(t *testing.T) {
	// The caller carries a realistic multi-group OIDC membership, none
	// of which is the environment's member group — so the caller is in
	// no environment and the §10.6 noEnvironmentPolicy governs access.
	caller := authmw.Principal{
		Subject: "alice", TenantID: "acme",
		Groups: []string{
			"infra", "leads", "oncall", "verifiers", "engineers", "billing",
			"hardware", "auditors", "researchers", "analysts", "triage",
		},
	}

	// A tenant that has set no noEnvironmentPolicy inherits the
	// platform-wide default: allow-all reaches every agent.
	srv, runtimes, envs, tenants := buildFilteredMCP(t, tenantstore.NoEnvPolicyAllowAll)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "sec-agent", Type: runtimestore.TypeAgent})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	_ = envs.Create(context.Background(), securityEnv(environment.Selector{}))
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/discover_agents", `{}`))
	if !strings.Contains(text, "sec-agent") {
		t.Errorf("allow-all platform default should reach all agents: %q", text)
	}

	// Under a deny-all platform default the same caller reaches none.
	srv, runtimes, envs, tenants = buildFilteredMCP(t, tenantstore.NoEnvPolicyDenyAll)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "sec-agent", Type: runtimestore.TypeAgent})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	_ = envs.Create(context.Background(), securityEnv(environment.Selector{}))
	text = resultText(t, callAs(t, srv.Handler(), caller, "lenny/discover_agents", `{}`))
	if strings.Contains(text, "sec-agent") {
		t.Errorf("deny-all platform default should reach no agents: %q", text)
	}

	// §10.6: a per-tenant noEnvironmentPolicy overrides the platform
	// default — allow-all on the tenant beats a deny-all default.
	srv, runtimes, envs, tenants = buildFilteredMCP(t, tenantstore.NoEnvPolicyDenyAll)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "sec-agent", Type: runtimestore.TypeAgent})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyAllowAll,
	})
	_ = envs.Create(context.Background(), securityEnv(environment.Selector{}))
	text = resultText(t, callAs(t, srv.Handler(), caller, "lenny/discover_agents", `{}`))
	if !strings.Contains(text, "sec-agent") {
		t.Errorf("per-tenant allow-all should override the deny-all platform default: %q", text)
	}
}
