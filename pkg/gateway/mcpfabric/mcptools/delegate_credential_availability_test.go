// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"errors"
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
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// fakeCredChecker is a test double for mcptools.CredentialAvailabilityChecker.
// It records every DelegationCredentialQuery the delegate handler builds and
// returns a configured result, so a test can assert both the error mapping the
// §8.3 gate applies and the exact query contents (tenant, user, child runtime,
// resolved credential origin) the gate derives.
type fakeCredChecker struct {
	err   error
	calls []sessionserver.DelegationCredentialQuery
}

func (f *fakeCredChecker) CheckDelegationCredentialAvailability(_ context.Context, q sessionserver.DelegationCredentialQuery) error {
	f.calls = append(f.calls, q)
	return f.err
}

// getErrStore wraps a session store and forces Get to fail for one session id,
// so a test can drive the §8.3 gate's parent-lookup-error fail-closed branch
// deterministically. Every other method delegates to the embedded store.
type getErrStore struct {
	sessionstore.Store
	errID string
	err   error
}

func (s getErrStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	if id == s.errID {
		return sessionstore.Session{}, s.err
	}
	return s.Store.Get(ctx, tenantID, id)
}

// newDelegateCredAvailMCP builds a same-environment delegation MCP server with
// the §8.3 credential-availability checker wired to the supplied double and a
// running parent session whose credential origin is parentOrigin (empty leaves
// the parent as its own origin). The delegation service issues child ids of
// "sess_child", so a committed child row proves the handler reached Delegate.
func newDelegateCredAvailMCP(t *testing.T, checker mcptools.CredentialAvailabilityChecker, parentOrigin string) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:            store,
		Executor:         executor.NewEchoExecutor(),
		Runtimes:         runtimes,
		CredAvailability: checker,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	ctxbg := context.Background()
	_ = runtimes.Create(ctxbg, runtimestore.Runtime{Name: "child-agent", Type: runtimestore.TypeAgent})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Create(ctxbg, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:                     session.StateRunning,
		RuntimeRef:                "child-agent",
		PoolRef:                   "pool-a",
		IsolationProfile:          isolation.ProfileSandboxed,
		CredentialOriginSessionID: parentOrigin,
		CreatedAt:                 now, UpdatedAt: now,
	})
	return srv, store
}

func childRowExists(t *testing.T, store sessionstore.Store) bool {
	t.Helper()
	_, err := store.Get(context.Background(), "acme", "sess_child")
	return err == nil
}

// spec: 8.3 (delegation-time credential-availability pre-check,
// CREDENTIAL_POOL_EXHAUSTED) — an inherit or independent delegation whose
// origin credential pool is exhausted is rejected with
// CREDENTIAL_POOL_EXHAUSTED (POLICY / 503, retryable) before the child row is
// created. Against the pre-gate code the exhausted hop would proceed and commit
// a child.
func TestDelegateTaskCredentialPoolExhaustedRejects_spec_8_3(t *testing.T) {
	for _, mode := range []string{"inherit", "independent"} {
		t.Run(mode, func(t *testing.T) {
			checker := &fakeCredChecker{err: sessionserver.ErrDelegationCredentialUnavailable}
			srv, store := newDelegateCredAvailMCP(t, checker, "")

			resp := call(t, srv.Handler(), "lenny/delegate_task",
				`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","credentialPropagation":"`+mode+`","task":{"input":[{"type":"text","inline":"do work"}]}}`)
			result, _ := resp["result"].(map[string]any)
			env := readLennyErrorEnvelope(t, result)
			if env["code"] != "CREDENTIAL_POOL_EXHAUSTED" {
				t.Fatalf("code = %v, want CREDENTIAL_POOL_EXHAUSTED", env["code"])
			}
			if env["category"] != "POLICY" {
				t.Errorf("category = %v, want POLICY", env["category"])
			}
			if env["retryable"] != true {
				t.Errorf("retryable = %v, want true", env["retryable"])
			}
			details, _ := env["details"].(map[string]any)
			if details["childRuntime"] != "child-agent" {
				t.Errorf("details.childRuntime = %v, want child-agent", details["childRuntime"])
			}
			if details["credentialPropagation"] != mode {
				t.Errorf("details.credentialPropagation = %v, want %s", details["credentialPropagation"], mode)
			}
			if len(checker.calls) != 1 {
				t.Errorf("checker calls = %d, want 1", len(checker.calls))
			}
			if childRowExists(t, store) {
				t.Error("an exhausted-pool rejection must not create a child session")
			}
		})
	}
}

// spec: 8.3 — an inherit delegation whose credential pool has an assignable
// slot passes the pre-check and proceeds to commit a child session.
func TestDelegateTaskCredentialAvailableProceeds_spec_8_3(t *testing.T) {
	checker := &fakeCredChecker{err: nil}
	srv, store := newDelegateCredAvailMCP(t, checker, "")

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","credentialPropagation":"inherit","task":{"input":[{"type":"text","inline":"do work"}]}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("an available delegation must proceed: %+v", resp)
	}
	if len(checker.calls) != 1 {
		t.Errorf("checker calls = %d, want 1", len(checker.calls))
	}
	if !childRowExists(t, store) {
		t.Error("an available delegation must commit a child session")
	}
}

// spec: 8.3 — a deny hop needs no credential, so the §8.3 pre-check is skipped
// entirely (the checker is never invoked) and the delegation proceeds even when
// the checker is configured to reject.
func TestDelegateTaskDenyModeSkipsCredentialCheck_spec_8_3(t *testing.T) {
	checker := &fakeCredChecker{err: sessionserver.ErrDelegationCredentialUnavailable}
	srv, store := newDelegateCredAvailMCP(t, checker, "")

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","credentialPropagation":"deny","task":{"input":[{"type":"text","inline":"do work"}]}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("a deny hop must not be gated by the credential pre-check: %+v", resp)
	}
	if len(checker.calls) != 0 {
		t.Errorf("a deny hop must not invoke the checker, got %d calls", len(checker.calls))
	}
	if !childRowExists(t, store) {
		t.Error("a deny delegation must commit a child session")
	}
}

// spec: 8.3 — a nil checker (the minimal in-process gateway wires none) skips
// the gate, so the delegation proceeds without any availability check.
func TestDelegateTaskNilCheckerSkipsGate_spec_8_3(t *testing.T) {
	srv, store := newDelegateMCPWithChain(t, nil)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","credentialPropagation":"independent","task":{"input":[{"type":"text","inline":"do work"}]}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("a nil checker must skip the gate and admit the delegation: %+v", resp)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
		t.Errorf("a nil-checker delegation must commit a child session: %v", err)
	}
}

// newCrossEnvInheritCredAvailMCP mirrors the §10.6 cross-environment inherit
// setup but wires the §8.3 credential-availability checker, so a test can prove
// the cross-environment inherit CREDENTIAL_PROVIDER_MISMATCH provider-compatibility
// gate is evaluated before the §8.3 availability gate on a cross-environment
// inherit hop.
func newCrossEnvInheritCredAvailMCP(t *testing.T, checker mcptools.CredentialAvailabilityChecker, childProviders []string) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	envs := environmentstore.NewMemory()
	tenants := tenantstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:            store,
		Executor:         executor.NewEchoExecutor(),
		Runtimes:         runtimes,
		Environments:     envs,
		Tenants:          tenants,
		CredAvailability: checker,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	ctxbg := context.Background()
	sharedSel := environment.Selector{MatchLabels: map[string]string{"shared": "true"}}
	_ = runtimes.Create(ctxbg, runtimestore.Runtime{
		Name: "team-a-agent", Type: runtimestore.TypeAgent,
		Labels:             map[string]string{"team": "a"},
		SupportedProviders: []string{"anthropic_direct", "aws_bedrock"},
	})
	_ = runtimes.Create(ctxbg, runtimestore.Runtime{
		Name: "shared-tool", Type: runtimestore.TypeAgent,
		Labels:             map[string]string{"shared": "true"},
		SupportedProviders: childProviders,
	})
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

// spec: 8.3 — on a cross-environment inherit hop the more specific
// CREDENTIAL_PROVIDER_MISMATCH provider-compatibility gate is evaluated before
// the §8.3 availability gate. With disjoint providers and a checker configured to
// report exhaustion, the handler returns CREDENTIAL_PROVIDER_MISMATCH, proving the
// availability gate does not preempt the provider-compatibility gate.
func TestDelegateTaskCrossEnvInheritMismatchBeforeAvailability_spec_8_3(t *testing.T) {
	checker := &fakeCredChecker{err: sessionserver.ErrDelegationCredentialUnavailable}
	srv, store := newCrossEnvInheritCredAvailMCP(t, checker, []string{"openai_direct"})

	resp := callAs(t, srv.Handler(), teamAMember, "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"shared-tool","poolRef":"pool-b","credentialPropagation":"inherit","task":{"input":[{"type":"text","inline":"do work"}]}}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "CREDENTIAL_PROVIDER_MISMATCH" {
		t.Fatalf("code = %v, want CREDENTIAL_PROVIDER_MISMATCH (provider-compatibility gate must run first)", env["code"])
	}
	if len(checker.calls) != 0 {
		t.Errorf("the availability checker must not run when the provider-compatibility gate rejects, got %d calls", len(checker.calls))
	}
	if childRowExists(t, store) {
		t.Error("a rejected cross-environment inherit hop must not create a child session")
	}
}

// spec: 8.3 (line 445 omitted default) — an omitted credentialPropagation
// resolves to independent, so the pre-check still runs: an exhausted pool
// rejects with CREDENTIAL_POOL_EXHAUSTED and never reaches Delegate, and an
// available pool proceeds. A regression dropping the empty-mode clause of the
// gate condition would silently disable the pre-check for the common
// default-mode delegation.
func TestDelegateTaskOmittedModeRunsIndependentCheck_spec_8_3(t *testing.T) {
	t.Run("exhausted", func(t *testing.T) {
		checker := &fakeCredChecker{err: sessionserver.ErrDelegationCredentialUnavailable}
		srv, store := newDelegateCredAvailMCP(t, checker, "")

		resp := call(t, srv.Handler(), "lenny/delegate_task",
			`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"do work"}]}}`)
		result, _ := resp["result"].(map[string]any)
		env := readLennyErrorEnvelope(t, result)
		if env["code"] != "CREDENTIAL_POOL_EXHAUSTED" {
			t.Fatalf("code = %v, want CREDENTIAL_POOL_EXHAUSTED for the omitted default mode", env["code"])
		}
		if len(checker.calls) != 1 {
			t.Fatalf("checker calls = %d, want 1 (the omitted default must be checked)", len(checker.calls))
		}
		if checker.calls[0].CredentialOriginSessionID != "" {
			t.Errorf("omitted-default origin = %q, want empty (independent)", checker.calls[0].CredentialOriginSessionID)
		}
		if childRowExists(t, store) {
			t.Error("an exhausted omitted-default delegation must not create a child session")
		}
	})
	t.Run("available", func(t *testing.T) {
		checker := &fakeCredChecker{err: nil}
		srv, store := newDelegateCredAvailMCP(t, checker, "")

		resp := call(t, srv.Handler(), "lenny/delegate_task",
			`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"do work"}]}}`)
		result, _ := resp["result"].(map[string]any)
		if result["isError"] == true {
			t.Fatalf("an available omitted-default delegation must proceed: %+v", resp)
		}
		if !childRowExists(t, store) {
			t.Error("an available omitted-default delegation must commit a child session")
		}
	})
}

// spec: 8.3 (line 470); 4.9 (line 1364) — when the eligible providers are all
// user-only and the user has no registered credential, the §4.9 engine returns
// the distinct user-credential outcome, which the delegate path must surface as
// USER_CREDENTIAL_NOT_FOUND (PERMANENT / 404) rather than the generic
// INTERNAL_ERROR a bare error would produce.
func TestDelegateTaskUserCredentialNotFoundRejects_spec_8_3(t *testing.T) {
	checker := &fakeCredChecker{err: sessionserver.ErrDelegationUserCredentialNotFound}
	srv, store := newDelegateCredAvailMCP(t, checker, "")

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","credentialPropagation":"independent","task":{"input":[{"type":"text","inline":"do work"}]}}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "USER_CREDENTIAL_NOT_FOUND" {
		t.Fatalf("code = %v, want USER_CREDENTIAL_NOT_FOUND", env["code"])
	}
	if env["category"] != "PERMANENT" {
		t.Errorf("category = %v, want PERMANENT", env["category"])
	}
	if env["retryable"] != false {
		t.Errorf("retryable = %v, want false", env["retryable"])
	}
	if childRowExists(t, store) {
		t.Error("a USER_CREDENTIAL_NOT_FOUND rejection must not create a child session")
	}
}

// spec: 8.3 — the query the gate builds carries the delegating tenant, the
// parent session's user, the resolved child runtime, and the credential origin
// the mode implies: for inherit, the parent's stored ancestor origin when
// present; for independent and the omitted default, an empty origin. These pin
// the mode-distinguishing origin derivation the field-mapping wrapper test does
// not exercise.
func TestDelegateTaskGateQueryContents_spec_8_3(t *testing.T) {
	t.Run("inherit passes parent stored origin", func(t *testing.T) {
		checker := &fakeCredChecker{err: nil}
		srv, _ := newDelegateCredAvailMCP(t, checker, "origin_sess")

		_ = call(t, srv.Handler(), "lenny/delegate_task",
			`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","credentialPropagation":"inherit","task":{"input":[{"type":"text","inline":"do work"}]}}`)
		if len(checker.calls) != 1 {
			t.Fatalf("checker calls = %d, want 1", len(checker.calls))
		}
		q := checker.calls[0]
		if q.TenantID != "acme" {
			t.Errorf("q.TenantID = %q, want acme", q.TenantID)
		}
		if q.UserID != "user_alice" {
			t.Errorf("q.UserID = %q, want user_alice", q.UserID)
		}
		if q.ChildRuntimeRef != "child-agent" {
			t.Errorf("q.ChildRuntimeRef = %q, want child-agent", q.ChildRuntimeRef)
		}
		if q.CredentialOriginSessionID != "origin_sess" {
			t.Errorf("q.CredentialOriginSessionID = %q, want origin_sess", q.CredentialOriginSessionID)
		}
	})
	t.Run("inherit without stored origin falls back to parent id", func(t *testing.T) {
		checker := &fakeCredChecker{err: nil}
		srv, _ := newDelegateCredAvailMCP(t, checker, "")

		_ = call(t, srv.Handler(), "lenny/delegate_task",
			`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","credentialPropagation":"inherit","task":{"input":[{"type":"text","inline":"do work"}]}}`)
		if len(checker.calls) != 1 {
			t.Fatalf("checker calls = %d, want 1", len(checker.calls))
		}
		if got := checker.calls[0].CredentialOriginSessionID; got != "sess_parent" {
			t.Errorf("q.CredentialOriginSessionID = %q, want sess_parent (parent id fallback)", got)
		}
	})
	t.Run("independent passes empty origin", func(t *testing.T) {
		checker := &fakeCredChecker{err: nil}
		srv, _ := newDelegateCredAvailMCP(t, checker, "origin_sess")

		_ = call(t, srv.Handler(), "lenny/delegate_task",
			`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","credentialPropagation":"independent","task":{"input":[{"type":"text","inline":"do work"}]}}`)
		if len(checker.calls) != 1 {
			t.Fatalf("checker calls = %d, want 1", len(checker.calls))
		}
		if got := checker.calls[0].CredentialOriginSessionID; got != "" {
			t.Errorf("independent origin = %q, want empty", got)
		}
	})
}

// spec: 8.3 (line 470) — an inherit hop whose parent row cannot be read fails
// closed: the handler propagates the lookup error, and it neither invokes the
// checker with an empty (unconstrained) origin nor commits a child. This pins
// the deny-on-doubt credential-handling discipline; without it an unreadable
// parent would downgrade an inherit hop to an unconstrained availability check.
func TestDelegateTaskParentLookupErrorFailsClosed_spec_8_3(t *testing.T) {
	checker := &fakeCredChecker{err: nil}
	base := memstore.New()
	runtimes := runtimestore.NewMemory()
	wrapped := getErrStore{Store: base, errID: "sess_parent", err: errors.New("store unavailable")}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:            wrapped,
		Executor:         executor.NewEchoExecutor(),
		Runtimes:         runtimes,
		CredAvailability: checker,
		Delegation: delegation.NewService(wrapped, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	ctxbg := context.Background()
	_ = runtimes.Create(ctxbg, runtimestore.Runtime{Name: "child-agent", Type: runtimestore.TypeAgent})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = base.Create(ctxbg, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "child-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","credentialPropagation":"inherit","task":{"input":[{"type":"text","inline":"do work"}]}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("an unreadable parent must fail the inherit hop closed: %+v", resp)
	}
	if len(checker.calls) != 0 {
		t.Errorf("the checker must not run when the parent lookup fails, got %d calls", len(checker.calls))
	}
	if _, err := base.Get(ctxbg, "acme", "sess_child"); err == nil {
		t.Error("a fail-closed inherit hop must not create a child session")
	}
}
