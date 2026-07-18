// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// newCrossEnvInheritMCP builds an MCP server with a §10.6 bilateral
// cross-environment declaration team-a -> team-b, a running parent
// session in team-a whose origin runtime supports {anthropic_direct,
// aws_bedrock}, and a cross-environment-reachable child runtime in team-b
// whose supportedProviders the caller chooses. The tenant credentialPolicy
// spans all three providers so the origin pool's provider set is bounded
// only by the origin runtime, isolating the §8.3 cross-environment
// inherit provider-compatibility check.
func newCrossEnvInheritMCP(t *testing.T, childProviders []string) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	envs := environmentstore.NewMemory()
	tenants := tenantstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        store,
		Executor:     executor.NewEchoExecutor(),
		Runtimes:     runtimes,
		Environments: envs,
		Tenants:      tenants,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})

	ctxbg := context.Background()
	sharedSel := environment.Selector{MatchLabels: map[string]string{"shared": "true"}}
	// The parent's origin runtime, resolved live at the hop, supports
	// anthropic_direct + aws_bedrock.
	_ = runtimes.Create(ctxbg, runtimestore.Runtime{
		Name: "team-a-agent", Type: runtimestore.TypeAgent,
		Labels:             map[string]string{"team": "a"},
		SupportedProviders: []string{"anthropic_direct", "aws_bedrock"},
	})
	// The cross-environment-reachable child runtime in team-b, with a
	// caller-chosen supportedProviders list.
	_ = runtimes.Create(ctxbg, runtimestore.Runtime{
		Name: "shared-tool", Type: runtimestore.TypeAgent,
		Labels:             map[string]string{"shared": "true"},
		SupportedProviders: childProviders,
	})
	// The tenant credentialPolicy spans every provider so the origin
	// pool's provider set equals the origin runtime's supportedProviders.
	_ = tenants.Create(ctxbg, tenantstore.Tenant{
		ID: "acme",
		CredentialPolicy: credential.CredentialPolicy{
			ProviderPools: map[string]credential.ProviderPool{
				"anthropic_direct": {DefaultPool: "claude-prod"},
				"aws_bedrock":      {DefaultPool: "bedrock-prod"},
				"openai_direct":    {DefaultPool: "openai-prod"},
			},
		},
	})
	// team-a admits its own team=a runtimes and declares outbound
	// delegation to team-b; team-b admits the shared tool and accepts
	// inbound delegation from team-a.
	_ = envs.Create(ctxbg, environmentstore.Environment{
		Name: "team-a", TenantID: "acme",
		Members: []environmentstore.Member{
			{
				Identity: environmentstore.Identity{Type: "oidc-group", Value: "team-a-members"},
				Role:     environment.RoleCreator,
			},
		},
		RuntimeSelector: environment.Selector{MatchLabels: map[string]string{"team": "a"}},
		CrossEnvOutbound: []environmentstore.CrossEnvRule{
			{Environment: "team-b", Runtimes: sharedSel},
		},
	})
	_ = envs.Create(ctxbg, environmentstore.Environment{
		Name: "team-b", TenantID: "acme", RuntimeSelector: sharedSel,
		CrossEnvInbound: []environmentstore.CrossEnvRule{
			{Environment: "team-a", Runtimes: sharedSel},
		},
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Create(ctxbg, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "team-a-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		Environment: "team-a", CreatedAt: now, UpdatedAt: now,
	})
	return srv, store
}

// teamAMember is a caller that genuinely belongs to team-a (the parent
// session's environment), so the §10.6 cross-environment reach is trusted.
var teamAMember = authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"team-a-members"}}

// spec: §8.3 — a cross-environment `credentialPropagation: inherit`
// delegation whose origin credential pool shares no provider with the
// child runtime's supportedProviders is rejected with
// CREDENTIAL_PROVIDER_MISMATCH before any warm pod is claimed and before
// any child session row is created. This pins the core §8.3 behavior:
// against the pre-check code the disjoint hop would proceed and commit a
// child.
func TestDelegateTaskCrossEnvInheritProviderMismatchRejects_spec_8_3(t *testing.T) {
	// The child supports only openai_direct; the origin pool is
	// {anthropic_direct, aws_bedrock}, so the intersection is empty.
	srv, store := newCrossEnvInheritMCP(t, []string{"openai_direct"})

	resp := callAs(t, srv.Handler(), teamAMember, "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"shared-tool","poolRef":"pool-b","credentialPropagation":"inherit","task":{"input":[{"type":"text","inline":"do work"}]}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a disjoint cross-environment inherit delegation must be a tool error: %+v", resp)
	}
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "CREDENTIAL_PROVIDER_MISMATCH" {
		t.Errorf("code = %v, want CREDENTIAL_PROVIDER_MISMATCH", env["code"])
	}
	// spec: §15 — CREDENTIAL_PROVIDER_MISMATCH is POLICY / 422,
	// non-retryable; the *mcp.ToolError carries that classification onto
	// the MCP envelope.
	if env["category"] != "POLICY" {
		t.Errorf("category = %v, want POLICY", env["category"])
	}
	if env["retryable"] != false {
		t.Errorf("retryable = %v, want false", env["retryable"])
	}
	msg, _ := env["message"].(string)
	if !strings.Contains(msg, "parent credential pool providers do not intersect with child runtime supportedProviders") {
		t.Errorf("message = %q, want the verbatim §8.3 mismatch message", msg)
	}
	details, _ := env["details"].(map[string]any)
	if details["originRuntime"] != "team-a-agent" {
		t.Errorf("details.originRuntime = %v, want team-a-agent", details["originRuntime"])
	}
	if details["childRuntime"] != "shared-tool" {
		t.Errorf("details.childRuntime = %v, want shared-tool", details["childRuntime"])
	}
	// The rejection is pre-claim: no child session row exists.
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("a CREDENTIAL_PROVIDER_MISMATCH rejection must not create a child session")
	}
}

// spec: §8.3 — a cross-environment `inherit` delegation whose origin
// credential pool shares at least one provider with the child runtime
// proceeds past the compatibility check and commits a child session.
func TestDelegateTaskCrossEnvInheritProviderOverlapAdmits_spec_8_3(t *testing.T) {
	// The child supports anthropic_direct (shared with the origin pool)
	// plus openai_direct, so the intersection is non-empty.
	srv, store := newCrossEnvInheritMCP(t, []string{"anthropic_direct", "openai_direct"})

	resp := callAs(t, srv.Handler(), teamAMember, "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"shared-tool","poolRef":"pool-b","credentialPropagation":"inherit","task":{"input":[{"type":"text","inline":"do work"}]}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("a compatible cross-environment inherit delegation must proceed: %+v", resp)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
		t.Fatalf("a compatible cross-environment inherit delegation must commit a child session: %v", err)
	}
}

// spec: §8.3 — the provider-compatibility gate fires only on the
// `inherit` hop. A cross-environment `independent` delegation with the
// same disjoint providers is not subjected to the check and proceeds.
func TestDelegateTaskCrossEnvIndependentSkipsProviderCheck_spec_8_3(t *testing.T) {
	srv, store := newCrossEnvInheritMCP(t, []string{"openai_direct"})

	resp := callAs(t, srv.Handler(), teamAMember, "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"shared-tool","poolRef":"pool-b","credentialPropagation":"independent","task":{"input":[{"type":"text","inline":"do work"}]}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("a cross-environment independent delegation must not be gated by the inherit check: %+v", resp)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
		t.Fatalf("a cross-environment independent delegation must commit a child session: %v", err)
	}
}

// spec: §8.3 — the compatibility gate is scoped to cross-environment
// hops. A same-environment `inherit` delegation is never subjected to the
// check, even when no environment registry is wired (viaCrossEnv false),
// and proceeds to commit a child session.
func TestDelegateTaskSameEnvInheritSkipsProviderCheck_spec_8_3(t *testing.T) {
	srv, store := newDelegateMCPWithChain(t, nil)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","credentialPropagation":"inherit","task":{"input":[{"type":"text","inline":"do work"}]}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("a same-environment inherit delegation must not be gated by the cross-environment check: %+v", resp)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
		t.Fatalf("a same-environment inherit delegation must commit a child session: %v", err)
	}
}
