// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

const delegateArgs = `{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"do work"}]}}`

// failingUsers is a user registry whose every read fails.
type failingUsers struct{ userstore.Store }

func (failingUsers) Get(context.Context, string, string) (userstore.User, error) {
	return userstore.User{}, errors.New("user registry unavailable")
}

// newFencedDelegateMCP builds a delegation-capable MCP server over a
// fenced session store, with the tenant and user registries the gate and
// the fence read. The parent session sess_parent is owned by user_alice.
func newFencedDelegateMCP(t *testing.T, tenants *tenantstore.Memory, users userstore.Store, fenceUsers *userstore.Memory) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New(memstore.WithCreationMarkers(sessionstore.StoreMarkers(tenants, fenceUsers)))
	runtimes := runtimestore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store,
		Executor:          executor.NewEchoExecutor(),
		Runtimes:          runtimes,
		Users:             users,
		Tenants:           tenants,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	ctx := context.Background()
	_ = runtimes.Create(ctx, runtimestore.Runtime{Name: "child-agent", Type: runtimestore.TypeAgent})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// The parent row is seeded before any marker is set.
	if err := store.Create(ctx, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice", State: session.StateRunning,
		RuntimeRef: "child-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	return srv, store
}

func seededRegistries(t *testing.T) (*tenantstore.Memory, *userstore.Memory) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	users := userstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := users.Create(context.Background(), userstore.User{Subject: "user_alice", TenantID: "acme"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return tenants, users
}

// TestDelegateGateTenantAndRestrictionMarkers_spec_12_8 pins the early
// gate of a delegated child against the fence predicate: each tenant
// marker and the owner's restriction refuse with the predicate's code
// and details before any child row, while a soft-disabled owner is
// admitted. The gate read no tenant marker and no restriction before.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 11.4 (User Invalidation)
func TestDelegateGateTenantAndRestrictionMarkers_spec_12_8(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, *tenantstore.Memory, *userstore.Memory)
		code   string
	}{
		{"tenant suspended", func(t *testing.T, ts *tenantstore.Memory, _ *userstore.Memory) {
			_, _ = ts.Update(context.Background(), "acme", func(tn *tenantstore.Tenant) error { tn.Suspended = true; return nil })
		}, "TENANT_SUSPENDED"},
		{"tenant deleting", func(t *testing.T, ts *tenantstore.Memory, _ *userstore.Memory) {
			_, _ = ts.Update(context.Background(), "acme", func(tn *tenantstore.Tenant) error {
				tn.State = tenantstore.TenantStateDeleting
				return nil
			})
		}, "TENANT_NOT_ACTIVE"},
		{"owner restricted", func(t *testing.T, _ *tenantstore.Memory, us *userstore.Memory) {
			_, _ = us.Update(context.Background(), "acme", "user_alice", func(u *userstore.User) error {
				u.ProcessingRestricted, u.ErasureJobID = true, "job-8"
				return nil
			})
		}, "ERASURE_IN_PROGRESS"},
		{"owner soft-disabled", func(t *testing.T, _ *tenantstore.Memory, us *userstore.Memory) {
			_, _ = us.Update(context.Background(), "acme", "user_alice", func(u *userstore.User) error { u.Disabled = true; return nil })
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenants, users := seededRegistries(t)
			srv, store := newFencedDelegateMCP(t, tenants, users, users)
			tc.mutate(t, tenants, users)
			resp := call(t, srv.Handler(), "lenny/delegate_task", delegateArgs)
			result, _ := resp["result"].(map[string]any)
			_, childErr := store.Get(context.Background(), "acme", "sess_child")
			if tc.code == "" {
				if result["isError"] == true {
					t.Fatalf("admitted state refused: %+v", resp)
				}
				if childErr != nil {
					t.Errorf("the admitted delegation committed no child: %v", childErr)
				}
				return
			}
			env := readLennyErrorEnvelope(t, result)
			if env["code"] != tc.code {
				t.Fatalf("code = %v, want %s", env["code"], tc.code)
			}
			if tc.code == "ERASURE_IN_PROGRESS" {
				details, _ := env["details"].(map[string]any)
				if details["userId"] != "user_alice" || details["jobId"] != "job-8" {
					t.Errorf("details = %v, want the owner and the job", details)
				}
			}
			if childErr == nil {
				t.Error("a refused delegation committed a child session")
			}
		})
	}
}

// TestDelegateGateReadErrorAnswersInternal_spec_12_8 pins the Gates column
// rule on the delegated-child gate: a marker read error answers the
// INTERNAL_ERROR tool error and commits no child.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDelegateGateReadErrorAnswersInternal_spec_12_8(t *testing.T) {
	tenants, users := seededRegistries(t)
	srv, store := newFencedDelegateMCP(t, tenants, failingUsers{users}, users)
	resp := call(t, srv.Handler(), "lenny/delegate_task", delegateArgs)
	result, _ := resp["result"].(map[string]any)
	if env := readLennyErrorEnvelope(t, result); env["code"] != "INTERNAL_ERROR" {
		t.Fatalf("code = %v, want INTERNAL_ERROR", env["code"])
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("a failed gate committed a child session")
	}
}

// TestDelegateTaskMapsFenceRefusal_spec_12_8 pins the delegate_task error
// mapping: a marker set after the early gate admitted the delegation is
// refused by the fence in the child's insert, and the tool answers the
// refusal's code rather than the generic fallback.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.2.1 (REST/MCP Consistency Contract)
func TestDelegateTaskMapsFenceRefusal_spec_12_8(t *testing.T) {
	tenants, users := seededRegistries(t)
	// The gate reads a registry with no markers; the fence reads the
	// suspended tenant, as when the marker commits between the two.
	gateTenants := tenantstore.NewMemory()
	_ = gateTenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	store := memstore.New(memstore.WithCreationMarkers(sessionstore.StoreMarkers(tenants, users)))
	runtimes := runtimestore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store, Executor: executor.NewEchoExecutor(), Runtimes: runtimes,
		Users: users, Tenants: gateTenants,
		Delegation: delegation.NewService(store, delegation.Options{IDFunc: func() string { return "sess_child" }}),
		IDFunc:     func() string { return "sess_mcp" }, TenantID: "acme",
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "child-agent", Type: runtimestore.TypeAgent})
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice", State: session.StateRunning,
		RuntimeRef: "child-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	_, _ = tenants.Update(context.Background(), "acme", func(tn *tenantstore.Tenant) error { tn.Suspended = true; return nil })

	resp := call(t, srv.Handler(), "lenny/delegate_task", delegateArgs)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "TENANT_SUSPENDED" {
		t.Fatalf("code = %v, want TENANT_SUSPENDED", env["code"])
	}
	if env["retryable"] != false {
		t.Errorf("retryable = %v, want false", env["retryable"])
	}
}

// TestMinimalCreateSessionMapsFenceRefusal_spec_12_8 pins the fallback
// create_session of a gateway that wires no session server: the fence's
// refusal answers a tool error with its code and details rather than the
// raw store error.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestMinimalCreateSessionMapsFenceRefusal_spec_12_8(t *testing.T) {
	tenants, users := seededRegistries(t)
	_, _ = users.Update(context.Background(), "acme", "user_alice", func(u *userstore.User) error {
		u.ProcessingRestricted, u.ErasureJobID = true, "job-6"
		return nil
	})
	store := memstore.New(memstore.WithCreationMarkers(sessionstore.StoreMarkers(tenants, users)))
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store, Executor: executor.NewEchoExecutor(), Runtimes: runtimestore.NewMemory(),
		IDFunc: func() string { return "sess_new" }, TenantID: "acme",
	})
	resp := call(t, srv.Handler(), "lenny/create_session", `{"runtimeRef":"claude-code","userId":"user_alice"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "ERASURE_IN_PROGRESS" {
		t.Fatalf("code = %v, want ERASURE_IN_PROGRESS", env["code"])
	}
	details, _ := env["details"].(map[string]any)
	if details["userId"] != "user_alice" || details["jobId"] != "job-6" {
		t.Errorf("details = %v", details)
	}
}

// newResolutionMCP builds the client MCP surface with the interaction
// tools and a user registry, and seeds alice's session with a pending
// elicitation.
func newResolutionMCP(t *testing.T, users userstore.Store) (*mcp.Server, interactionstore.Store) {
	t.Helper()
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store, Interactions: interactions, Users: users,
		ElicitationTimeout: 5 * time.Second, IDFunc: func() string { return "elic_gen" }, TenantID: "acme",
	})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_bob", "bob", "")
	if err := interactions.Put(context.Background(), interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice", Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	return srv, interactions
}

// TestMCPElicitationResolutionCallerCheck_spec_12_8 pins the caller check
// of the MCP elicitation-resolution tools for each principal state, the
// check's position before the session lookup, and the principal as the
// triple's user. These cases ran no user check and resolved under the
// session row's user before.
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18)), 11.4 (User Invalidation), 9.2 (Elicitation Chain)
func TestMCPElicitationResolutionCallerCheck_spec_12_8(t *testing.T) {
	tools := []struct{ name, args string }{
		{"lenny/respond_to_elicitation", `{"sessionId":"%s","elicitationId":"elic_x","response":"ok"}`},
		{"lenny/dismiss_elicitation", `{"sessionId":"%s","elicitationId":"elic_x"}`},
	}
	states := []struct {
		name    string
		seed    *userstore.User
		failing bool
		code    string
	}{
		{name: "restricted", seed: &userstore.User{Subject: "alice", TenantID: "acme", ProcessingRestricted: true, ErasureJobID: "job-1"}, code: "ERASURE_IN_PROGRESS"},
		{name: "invalidated", seed: &userstore.User{Subject: "alice", TenantID: "acme", Disabled: true}, code: "USER_INVALIDATED"},
		{name: "both", seed: &userstore.User{Subject: "alice", TenantID: "acme", Disabled: true, ProcessingRestricted: true}, code: "USER_INVALIDATED"},
		{name: "no registry row"},
		{name: "read error", failing: true, code: "INTERNAL_ERROR"},
	}
	for _, tool := range tools {
		for _, st := range states {
			for _, sessionID := range []string{"sess_root", "sess_missing"} {
				t.Run(tool.name+"/"+st.name+"/"+sessionID, func(t *testing.T) {
					mem := userstore.NewMemory()
					if st.seed != nil {
						_ = mem.Create(context.Background(), *st.seed)
					}
					var us userstore.Store = mem
					if st.failing {
						us = failingUsers{mem}
					}
					srv, interactions := newResolutionMCP(t, us)
					args := strings.Replace(tool.args, "%s", sessionID, 1)
					resp := callAs(t, srv.Handler(), authmw.Principal{Subject: "alice", TenantID: "acme"}, tool.name, args)
					result, _ := resp["result"].(map[string]any)
					want := st.code
					if want == "" && sessionID == "sess_missing" {
						want = "ELICITATION_NOT_FOUND"
					}
					if want == "" {
						if result["isError"] == true {
							t.Fatalf("admitted caller refused: %+v", resp)
						}
						return
					}
					env := readLennyErrorEnvelope(t, result)
					if env["code"] != want {
						t.Fatalf("code = %v, want %s", env["code"], want)
					}
					if want == "ERASURE_IN_PROGRESS" || want == "USER_INVALIDATED" {
						if msg, _ := env["message"].(string); !strings.Contains(msg, "resolve interactions") {
							t.Errorf("message %q does not name interaction resolution", msg)
						}
					}
					if want == "ERASURE_IN_PROGRESS" {
						details, _ := env["details"].(map[string]any)
						if details["userId"] != "alice" || details["jobId"] != "job-1" {
							t.Errorf("details = %v", details)
						}
					}
					cur, _ := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x")
					if cur.Phase != interactionstore.PhasePending {
						t.Errorf("a refused resolution changed the phase to %q", cur.Phase)
					}
				})
			}
		}
	}
}

// TestMCPElicitationResolutionKeysOnPrincipal_spec_9_2 pins the principal
// as the user both the caller check and the triple read: a restricted
// principal resolving another user's elicitation answers
// ERASURE_IN_PROGRESS, and an active principal resolving another user's
// elicitation through that user's session answers ELICITATION_NOT_FOUND.
// spec: 9.2 (Elicitation Chain), 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18))
func TestMCPElicitationResolutionKeysOnPrincipal_spec_9_2(t *testing.T) {
	mem := userstore.NewMemory()
	_ = mem.Create(context.Background(), userstore.User{Subject: "bob", TenantID: "acme", ProcessingRestricted: true, ErasureJobID: "job-2"})
	_ = mem.Create(context.Background(), userstore.User{Subject: "carol", TenantID: "acme"})
	srv, interactions := newResolutionMCP(t, mem)
	args := `{"sessionId":"sess_root","elicitationId":"elic_x","response":"ok"}`

	resp := callAs(t, srv.Handler(), authmw.Principal{Subject: "bob", TenantID: "acme"}, "lenny/respond_to_elicitation", args)
	result, _ := resp["result"].(map[string]any)
	if env := readLennyErrorEnvelope(t, result); env["code"] != "ERASURE_IN_PROGRESS" {
		t.Fatalf("restricted principal: code = %v, want ERASURE_IN_PROGRESS", env["code"])
	}

	resp = callAs(t, srv.Handler(), authmw.Principal{Subject: "carol", TenantID: "acme"}, "lenny/respond_to_elicitation", args)
	result, _ = resp["result"].(map[string]any)
	if env := readLennyErrorEnvelope(t, result); env["code"] != "ELICITATION_NOT_FOUND" {
		t.Fatalf("another user's elicitation: code = %v, want ELICITATION_NOT_FOUND", env["code"])
	}
	cur, _ := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x")
	if cur.Phase != interactionstore.PhasePending {
		t.Errorf("another principal resolved alice's elicitation: phase %q", cur.Phase)
	}
}
