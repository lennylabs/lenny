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
	"github.com/lennylabs/lenny/pkg/gateway/adapter"
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
		environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	))

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

func TestListRuntimesToolCoversAllTypesAndFilters(t *testing.T) {
	srv, runtimes, envs, tenants := newMCPFiltered(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "security"},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-mcp", Type: runtimestore.TypeMCP,
		Labels: map[string]string{"team": "security"},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "research-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "research"},
	})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	_ = envs.Create(context.Background(), securityEnv(
		environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	))

	caller := authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"security-engineers"}}
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/list_runtimes", `{}`))
	// §9.1 discovery covers every runtime type — unlike discover_agents
	// it includes type:mcp runtimes — and is environment-filtered.
	for _, want := range []string{"sec-agent", "sec-mcp"} {
		if !strings.Contains(text, want) {
			t.Errorf("list_runtimes should include %q: %q", want, text)
		}
	}
	if strings.Contains(text, "research-agent") {
		t.Errorf("list_runtimes must exclude an out-of-environment runtime: %q", text)
	}
}

func TestListRuntimesToolSurfacesAgentInterface(t *testing.T) {
	srv, runtimes, envs, tenants := newMCPFiltered(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "security"},
		AgentInterface: &runtimestore.AgentInterface{
			Description: "Security review agent",
			Skills:      []runtimestore.AgentInterfaceSkill{{ID: "scan"}},
		},
	})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	_ = envs.Create(context.Background(), securityEnv(
		environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	))

	caller := authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"security-engineers"}}
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/list_runtimes", `{}`))

	var resp struct {
		Runtimes []struct {
			Name           string                       `json:"name"`
			AgentInterface *runtimestore.AgentInterface `json:"agentInterface"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("decode: %v (%q)", err, text)
	}
	if len(resp.Runtimes) != 1 {
		t.Fatalf("runtimes: got %d, want 1 (%q)", len(resp.Runtimes), text)
	}
	// §9.1: list_runtimes surfaces the per-runtime agentInterface.
	ai := resp.Runtimes[0].AgentInterface
	if ai == nil || ai.Description != "Security review agent" || len(ai.Skills) != 1 {
		t.Errorf("list_runtimes must surface the agentInterface descriptor: %+v", ai)
	}
}

func TestListRuntimesToolSurfacesPublicMetadataRefs(t *testing.T) {
	srv, runtimes, envs, tenants := newMCPFiltered(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "security"},
		PublishedMetadata: []runtimestore.PublishedMetadataEntry{
			{Key: "agent-card", ContentType: "application/json", Visibility: runtimestore.VisibilityPublic, Content: `{"k":"payloadbytes"}`},
			{Key: "spec", Visibility: runtimestore.VisibilityInternal, Content: "internalonly"},
		},
	})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	_ = envs.Create(context.Background(), securityEnv(
		environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	))

	caller := authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"security-engineers"}}
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/list_runtimes", `{}`))

	var resp struct {
		Runtimes []struct {
			PublishedMetadata []runtimestore.PublishedMetadataRef `json:"publishedMetadata"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("decode: %v (%q)", err, text)
	}
	if len(resp.Runtimes) != 1 {
		t.Fatalf("runtimes: got %d, want 1 (%q)", len(resp.Runtimes), text)
	}
	// §15: list_runtimes carries only the public refs, never content.
	refs := resp.Runtimes[0].PublishedMetadata
	if len(refs) != 1 || refs[0].Key != "agent-card" {
		t.Errorf("list_runtimes must surface only the public metadata ref: %+v", refs)
	}
	if strings.Contains(text, "payloadbytes") || strings.Contains(text, "internalonly") {
		t.Errorf("list_runtimes refs must not carry entry content: %q", text)
	}
}

func TestListRuntimesToolResolvesDerivedRuntime(t *testing.T) {
	// §5.1: list_runtimes reports a derived runtime as its effective
	// merged definition — it inherits the base's labels (so it passes
	// the §10.6 environment filter) and the base's agentInterface.
	srv, runtimes, envs, tenants := newMCPFiltered(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-base", Type: runtimestore.TypeAgent,
		Labels:         map[string]string{"team": "security"},
		AgentInterface: &runtimestore.AgentInterface{Description: "base iface"},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-derived", BaseRuntime: "sec-base",
	})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	_ = envs.Create(context.Background(), securityEnv(
		environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	))

	caller := authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"security-engineers"}}
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/list_runtimes", `{}`))

	var resp struct {
		Runtimes []struct {
			Name           string                       `json:"name"`
			AgentInterface *runtimestore.AgentInterface `json:"agentInterface"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("decode: %v (%q)", err, text)
	}
	var derived *runtimestore.AgentInterface
	found := false
	for _, rt := range resp.Runtimes {
		if rt.Name == "sec-derived" {
			found = true
			derived = rt.AgentInterface
		}
	}
	if !found {
		t.Fatalf("derived runtime missing — it must inherit the base's labels to pass the filter: %q", text)
	}
	if derived == nil || derived.Description != "base iface" {
		t.Errorf("derived runtime must surface the effective agentInterface: %+v", derived)
	}
}

func TestListRuntimesToolEmbedsAdapterCapabilities(t *testing.T) {
	srv, runtimes, envs, tenants := newMCPFiltered(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "security"},
	})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	_ = envs.Create(context.Background(), securityEnv(
		environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	))

	caller := authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"security-engineers"}}
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/list_runtimes", `{}`))

	// §9.1: the list_runtimes response embeds a top-level
	// adapterCapabilities object describing the MCP adapter.
	var resp struct {
		AdapterCapabilities adapter.Capabilities `json:"adapterCapabilities"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("decode: %v (%q)", err, text)
	}
	caps := resp.AdapterCapabilities
	if caps.Protocol != "mcp" || caps.PathPrefix != "/mcp" {
		t.Errorf("adapterCapabilities routing fields: %+v", caps)
	}
	// The MCP transport carries the §9.2 elicitation chain, the
	// delegate_task tool, and interrupt signalling.
	if !caps.SupportsElicitation || !caps.SupportsDelegation || !caps.SupportsInterrupt {
		t.Errorf("MCP adapter must report elicitation, delegation, interrupt: %+v", caps)
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

// TestListRuntimesStampsMcpEndpointForMcpTypes_spec_9_1_38 pins the
// §9.1 line 38 / §15.1 line 698 contract on the MCP discovery surface:
// `lenny/list_runtimes` reports `mcpEndpoint: /mcp/runtimes/{name}` on
// every type:mcp runtime; type:agent runtimes carry an empty value
// (omitted from the JSON envelope). F-9.1.4 / coordinated with F-9.1.3.
func TestListRuntimesStampsMcpEndpointForMcpTypes_spec_9_1_38(t *testing.T) {
	srv, runtimes, envs, tenants := newMCPFiltered(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-mcp", Type: runtimestore.TypeMCP,
		Labels: map[string]string{"team": "security"},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "security"},
	})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	_ = envs.Create(context.Background(), securityEnv(
		environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	))

	caller := authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"security-engineers"}}
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/list_runtimes", `{}`))
	var resp struct {
		Runtimes []struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			McpEndpoint string `json:"mcpEndpoint"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("decode: %v (%q)", err, text)
	}
	got := map[string]string{}
	for _, r := range resp.Runtimes {
		got[r.Name] = r.McpEndpoint
	}
	if got["sec-mcp"] != "/mcp/runtimes/sec-mcp" {
		t.Errorf("sec-mcp McpEndpoint = %q, want %q", got["sec-mcp"], "/mcp/runtimes/sec-mcp")
	}
	if got["sec-agent"] != "" {
		t.Errorf("sec-agent McpEndpoint = %q, want empty (type:agent has no per-runtime MCP endpoint)", got["sec-agent"])
	}
	if !strings.Contains(text, `"mcpEndpoint":"/mcp/runtimes/sec-mcp"`) {
		t.Errorf("list_runtimes response missing mcpEndpoint for type:mcp: %q", text)
	}
}
