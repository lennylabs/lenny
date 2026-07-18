// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §8.3 delegation-time credential-availability
// pre-check on lenny/delegate_task. A delegation with credentialPropagation:
// independent (or the omitted default, which resolves to independent) runs
// the §4.9 pre-claim credential-availability check before the child session
// row is committed. When no credential is assignable for the prospective
// child — here the child runtime's supportedProviders are disjoint from the
// tenant credentialPolicy.providerPools, an empty §4.9 intersection — the
// gateway rejects with CREDENTIAL_POOL_EXHAUSTED and commits no child row.
//
// The flow is driven end to end through the real lenny/delegate_task MCP
// handler over a real delegation.Service, with the real §4.9 engine on a
// *sessionserver.Server wired as the mcptools CredentialAvailabilityChecker,
// over the real tenant, runtime, and credential-pool registries. The pre-
// check is a point-in-time read that claims no warm pod (spec/08 line 470),
// so the load-bearing delegate-time invariant is the absence of a committed
// child session, which the tier-1 spy-based unit test cannot assert against
// a real session store.
//
// The crossEnvCall, crossEnvErrorEnvelope, childrenOf, childIDCounter, and
// mustCreateRuntime helpers live in cross_environment_delegation_test.go
// (same package), as do the provAnthropic/provOpenAI/poolAnthropic
// constants and the crossEnvCallerTeamA principal.
//
// The post-pod-claim assignment race, the pod release, and the one-winner/
// N-1 outcome of spec/08 line 470 depend on a delegate-path pod-claim and
// lease-assignment step that the delegate path does not perform (it commits
// the child in StateCreated with no PodAssignment). That coverage is tracked
// as an open TEST-GAPS finding against the future §8.2 delegate-path pod-
// claim build rather than exercised here.

package tier4_integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
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

// newCredPoolExhaustionFixture wires the real lenny/delegate_task MCP handler
// over a real delegation.Service, with a real *sessionserver.Server as the
// §8.3 delegation-time CredentialAvailabilityChecker. The acme tenant's
// credentialPolicy pools only the anthropic_direct provider, while the
// delegation target runtime isolated-worker supports only openai_direct, so
// the §4.9 intersection of the child runtime's supportedProviders and the
// tenant providerPools is empty. A fully usable anthropic pool exists but is
// never in the child's eligible set, so the empty intersection — not an
// at-capacity pool — is what drives the CREDENTIAL_POOL_EXHAUSTED rejection.
func newCredPoolExhaustionFixture(t *testing.T) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	ctx := context.Background()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	tenants := tenantstore.NewMemory()
	credPools := credentialpoolstore.NewMemory()

	// The parent runs planner; the delegation target isolated-worker
	// supports only openai_direct, disjoint from the tenant's
	// anthropic_direct-only credentialPolicy.
	mustCreateRuntime(t, runtimes, runtimestore.Runtime{
		Name: "planner", Type: runtimestore.TypeAgent,
		SupportedProviders: []string{provAnthropic},
	})
	mustCreateRuntime(t, runtimes, runtimestore.Runtime{
		Name: "isolated-worker", Type: runtimestore.TypeAgent,
		SupportedProviders: []string{provOpenAI},
	})

	if err := tenants.Create(ctx, tenantstore.Tenant{
		ID: "acme",
		CredentialPolicy: credential.CredentialPolicy{
			ProviderPools: map[string]credential.ProviderPool{
				provAnthropic: {DefaultPool: poolAnthropic},
			},
		},
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if err := credPools.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: poolAnthropic, Provider: provAnthropic, MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{
			{ID: poolAnthropic + "-cred", SecretRef: "secret-" + poolAnthropic, Status: credentialpoolstore.CredentialActive},
		},
	}); err != nil {
		t.Fatalf("create pool %s: %v", poolAnthropic, err)
	}

	// The real §4.9 engine (resolveCredentialPools → credrouter.PreClaim),
	// wired as the §8.3 delegation-time availability checker. It reads the
	// tenant policy, runtime registry, credential pools, and session store.
	credChecker := sessionserver.New(store, sessionserver.Options{
		Tenants:         tenants,
		Runtimes:        runtimes,
		CredentialPools: credPools,
	})

	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Runtimes: runtimes,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc:   childIDCounter(),
			Runtimes: runtimes,
		}),
		CredAvailability: credChecker,
		Clock:            func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:           func() string { return "sess_mcp" },
		TenantID:         "acme",
	})

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "alice@acme.com",
		State:      session.StateRunning,
		RuntimeRef: "planner", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create parent session: %v", err)
	}
	return srv, store
}

// spec: 8.3 (spec/08_recursive-delegation.md line 470: delegation-time
// pre-claim credential-availability check; CREDENTIAL_POOL_EXHAUSTED)
// diagnosis: the §8.3 delegation-time credential-availability pre-check
// diverged. A lenny/delegate_task delegation whose prospective child has no
// assignable credential (an empty §4.9 intersection of the child runtime
// supportedProviders and the tenant credentialPolicy.providerPools) was not
// rejected with CREDENTIAL_POOL_EXHAUSTED before the child row was committed,
// so an unbacked delegation was admitted and committed a child, or the gate
// rejected a delegation that should have proceeded.
func TestDelegateTaskCredentialPoolExhaustionPreCheck(t *testing.T) {
	// An independent delegation whose child runtime shares no provider with
	// the tenant credentialPolicy has an empty §4.9 intersection: no
	// credential is assignable, so the pre-check rejects with
	// CREDENTIAL_POOL_EXHAUSTED before delegation.Service.Delegate commits
	// the child row.
	t.Run("independent_disjoint_provider_intersection_rejects", func(t *testing.T) {
		srv, store := newCredPoolExhaustionFixture(t)
		resp := crossEnvCall(t, srv, crossEnvCallerTeamA, "sess_parent", "isolated-worker", "independent")
		assertPoolExhaustedNoChild(t, resp, store)
	})

	// The omitted credentialPropagation resolves to independent (spec/08
	// line 445), so the default-mode delegation runs the same pre-check.
	// This pins the empty-mode clause of the gate condition: a regression
	// dropping it would silently skip the pre-check for the common
	// default-mode delegation while the explicit-independent case still
	// passes.
	t.Run("default_mode_disjoint_provider_intersection_rejects", func(t *testing.T) {
		srv, store := newCredPoolExhaustionFixture(t)
		resp := crossEnvCall(t, srv, crossEnvCallerTeamA, "sess_parent", "isolated-worker", "")
		assertPoolExhaustedNoChild(t, resp, store)
	})
}

// assertPoolExhaustedNoChild asserts a delegate_task response is the §8.3
// CREDENTIAL_POOL_EXHAUSTED tool error (POLICY / 503, retryable) and that the
// rejection committed no child session under sess_parent, the integration-
// only pre-claim signal the tier-1 spy test cannot make.
func assertPoolExhaustedNoChild(t *testing.T, resp map[string]any, store sessionstore.Store) {
	t.Helper()
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a delegation with an empty credential intersection must be a tool error: %+v", resp)
	}
	env := crossEnvErrorEnvelope(t, result)
	if env["code"] != "CREDENTIAL_POOL_EXHAUSTED" {
		t.Errorf("code = %v, want CREDENTIAL_POOL_EXHAUSTED", env["code"])
	}
	// spec: §15 — CREDENTIAL_POOL_EXHAUSTED is POLICY / 503, retryable.
	if env["category"] != "POLICY" {
		t.Errorf("category = %v, want POLICY", env["category"])
	}
	if env["retryable"] != true {
		t.Errorf("retryable = %v, want true", env["retryable"])
	}
	// The rejection is pre-claim: the gate fires before
	// delegation.Service.Delegate, so no child session row is committed. No
	// committed child means no session that could ever reach finalize and
	// claim a warm pod, so this is the delegate-time observable of the
	// "before any pod allocation" guarantee.
	if childrenOf(t, store, "sess_parent") != 0 {
		t.Error("a CREDENTIAL_POOL_EXHAUSTED rejection must commit no child session (pre-claim)")
	}
}
