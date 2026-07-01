// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// spec: §17.6 lines 403-411, 429, 438 — bootstrap seed of pools,
// delegationPolicies, and environments (F-17.6.2 / F-24.1.4).

type bootstrapStores struct {
	router   *admin.Router
	tenants  *tenantstore.Memory
	runtimes *runtimestore.Memory
	pools    *poolstore.Memory
	credPool *credentialpoolstore.Memory
	delpol   *delegationpolicystore.Memory
	envs     *environmentstore.Memory
}

func newFullBootstrapRouter(t *testing.T, siemConfigured bool) bootstrapStores {
	t.Helper()
	s := bootstrapStores{
		tenants:  tenantstore.NewMemory(),
		runtimes: runtimestore.NewMemory(),
		pools:    poolstore.NewMemory(),
		credPool: credentialpoolstore.NewMemory(),
		delpol:   delegationpolicystore.NewMemory(),
		envs:     environmentstore.NewMemory(),
	}
	s.router = admin.NewRouter(s.tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithRuntimes(s.runtimes).
		WithPools(s.pools).
		WithCredentialPools(s.credPool).
		WithDelegationPolicies(s.delpol).
		WithEnvironments(s.envs).
		WithSIEMConfigured(siemConfigured)
	return s
}

// TestBootstrapSeedsPoolForRegisteredRuntime_spec_17_6_408 covers the
// §17.6 line 429 "sessions require warm pods" requirement: a pool naming
// a seeded runtime is created in the same bootstrap run.
func TestBootstrapSeedsPoolForRegisteredRuntime_spec_17_6_408(t *testing.T) {
	s := newFullBootstrapRouter(t, false)
	rec, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		Runtimes: []admin.RuntimePayload{{Name: "echo", Image: "lenny/echo@sha256:abc", Type: "agent", Labels: map[string]string{"tier": "test"}}},
		Pools: []admin.PoolPayload{
			{Name: "echo-pool", RuntimeRef: "echo", IsolationProfile: "sandboxed", WarmCount: 2},
		},
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resp.Pools.CreatedCount != 1 {
		t.Fatalf("pool createdCount = %d, want 1; resp=%+v", resp.Pools.CreatedCount, resp.Pools)
	}
	row, err := s.pools.Get(context.Background(), "echo-pool")
	if err != nil {
		t.Fatalf("seeded pool not stored: %v", err)
	}
	if row.RuntimeRef != "echo" || row.WarmCount != 2 {
		t.Errorf("stored pool: %+v", row)
	}
}

// TestBootstrapPoolRejectsUnknownRuntime_spec_17_6_408 covers the
// cross-resource gate: a pool naming a runtime that was not seeded is a
// per-entry validation error, not a silent admission.
func TestBootstrapPoolRejectsUnknownRuntime_spec_17_6_408(t *testing.T) {
	s := newFullBootstrapRouter(t, false)
	rec, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		Pools: []admin.PoolPayload{{Name: "ghost-pool", RuntimeRef: "nonexistent"}},
	}, "")
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status: got %d, want 207 (partial)", rec.Code)
	}
	if len(resp.Pools.Errors) != 1 || resp.Pools.Errors[0].Code != "SEED_VALIDATION" {
		t.Fatalf("expected one SEED_VALIDATION error, got %+v", resp.Pools.Errors)
	}
	if _, err := s.pools.Get(context.Background(), "ghost-pool"); err == nil {
		t.Error("rejected pool must not be persisted")
	}
}

// TestBootstrapPoolRejectsUnacknowledgedNonceOnly_spec_4_7 covers the
// §4.7 / §5.3 nonce-only acknowledgment gate on the bootstrap seed path,
// the second gateway pool-admission write path. A seed pool bound to a
// runtime carrying requireSoPeercred: false is rejected unless it sets
// acknowledgeNonceOnlyAuth: true, so an unacknowledged nonce-only pool
// never enters the registry through POST /v1/admin/bootstrap. An
// acknowledged seed pool is admitted, ruling out a blanket rejection.
//
// diagnosis: the bootstrap seed path admitted a nonce-only pool without
// the acknowledgment, bypassing the gate handleCreatePool enforces and
// violating the §4.7 fail-closed invariant that an unacknowledged
// nonce-only pool never enters the registry.
// spec: 4.7, 5.3, 17.6
func TestBootstrapPoolRejectsUnacknowledgedNonceOnly_spec_4_7(t *testing.T) {
	s := newFullBootstrapRouter(t, false)
	nonceOnly := false
	if err := s.runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "nonce-rt", RequireSoPeercred: &nonceOnly,
	}); err != nil {
		t.Fatalf("seed nonce-only runtime: %v", err)
	}

	// Unacknowledged seed pool bound to the nonce-only runtime: rejected,
	// not persisted.
	rec, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		Pools: []admin.PoolPayload{
			{Name: "nonce-noack", RuntimeRef: "nonce-rt", IsolationProfile: "sandboxed"},
		},
	}, "")
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status: got %d, want 207 (partial); body=%s", rec.Code, rec.Body.String())
	}
	if len(resp.Pools.Errors) != 1 || resp.Pools.Errors[0].Code != "SEED_VALIDATION" {
		t.Fatalf("expected one SEED_VALIDATION error, got %+v", resp.Pools.Errors)
	}
	if _, err := s.pools.Get(context.Background(), "nonce-noack"); err == nil {
		t.Error("§4.7: an unacknowledged nonce-only seed pool must not be persisted")
	}

	// Acknowledged seed pool: admitted.
	rec, resp = postBootstrap(t, s.router, admin.BootstrapRequest{
		Pools: []admin.PoolPayload{
			{Name: "nonce-ack", RuntimeRef: "nonce-rt", IsolationProfile: "sandboxed", AcknowledgeNonceOnlyAuth: true},
		},
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resp.Pools.CreatedCount != 1 {
		t.Fatalf("acknowledged nonce-only pool createdCount = %d, want 1; resp=%+v", resp.Pools.CreatedCount, resp.Pools)
	}
	row, err := s.pools.Get(context.Background(), "nonce-ack")
	if err != nil {
		t.Fatalf("acknowledged nonce-only seed pool not stored: %v", err)
	}
	if !row.AcknowledgeNonceOnlyAuth {
		t.Errorf("§5.3: stored pool must carry the acknowledgment, got %+v", row)
	}
}

// TestBootstrapPoolBlocksIsolationDowngrade_spec_17_6_451 covers the
// security-critical field rule: a re-run that changes an existing pool's
// isolationProfile is blocked regardless of --force-update.
func TestBootstrapPoolBlocksIsolationDowngrade_spec_17_6_451(t *testing.T) {
	s := newFullBootstrapRouter(t, false)
	_ = s.runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	_ = s.pools.Create(context.Background(), poolstore.Pool{Name: "p1", RuntimeRef: "echo", IsolationProfile: "sandboxed"})

	rec, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		Pools: []admin.PoolPayload{{Name: "p1", RuntimeRef: "echo", IsolationProfile: "standard", AllowStandardIsolation: true}},
	}, "?forceUpdate=true")
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status: got %d, want 207", rec.Code)
	}
	if len(resp.Pools.Errors) != 1 || resp.Pools.Errors[0].Code != "SEED_SECURITY_CRITICAL_FIELD" {
		t.Fatalf("expected SEED_SECURITY_CRITICAL_FIELD, got %+v", resp.Pools.Errors)
	}
	row, _ := s.pools.Get(context.Background(), "p1")
	if string(row.IsolationProfile) != "sandboxed" {
		t.Errorf("isolationProfile must be unchanged, got %q", row.IsolationProfile)
	}
}

// TestBootstrapPoolSkipThenForceUpdate_spec_17_6_450 covers the upsert
// table: a differing non-critical field is skipped without force-update
// and applied with it.
func TestBootstrapPoolSkipThenForceUpdate_spec_17_6_450(t *testing.T) {
	s := newFullBootstrapRouter(t, false)
	_ = s.runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	_ = s.pools.Create(context.Background(), poolstore.Pool{Name: "p1", RuntimeRef: "echo", IsolationProfile: "sandboxed", WarmCount: 2})

	_, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		Pools: []admin.PoolPayload{{Name: "p1", RuntimeRef: "echo", IsolationProfile: "sandboxed", WarmCount: 5}},
	}, "")
	if resp.Pools.SkippedCount != 1 {
		t.Fatalf("skippedCount = %d, want 1; resp=%+v", resp.Pools.SkippedCount, resp.Pools)
	}
	row, _ := s.pools.Get(context.Background(), "p1")
	if row.WarmCount != 2 {
		t.Fatalf("warmCount must stay 2 on skip, got %d", row.WarmCount)
	}

	_, resp = postBootstrap(t, s.router, admin.BootstrapRequest{
		Pools: []admin.PoolPayload{{Name: "p1", RuntimeRef: "echo", IsolationProfile: "sandboxed", WarmCount: 5}},
	}, "?forceUpdate=true")
	if resp.Pools.UpdatedCount != 1 {
		t.Fatalf("updatedCount = %d, want 1", resp.Pools.UpdatedCount)
	}
	row, _ = s.pools.Get(context.Background(), "p1")
	if row.WarmCount != 5 {
		t.Errorf("warmCount must be 5 after force-update, got %d", row.WarmCount)
	}
}

// TestBootstrapPoolDryRunDoesNotPersist_spec_15_1_1140 covers the dry-run
// query parameter: the pool is validated but not stored.
func TestBootstrapPoolDryRunDoesNotPersist_spec_15_1_1140(t *testing.T) {
	s := newFullBootstrapRouter(t, false)
	_ = s.runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	_, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		Pools: []admin.PoolPayload{{Name: "dry-pool", RuntimeRef: "echo", IsolationProfile: "sandboxed"}},
	}, "?dryRun=true")
	if resp.Pools.CreatedCount != 1 {
		t.Fatalf("dry-run should report createdCount 1, got %+v", resp.Pools)
	}
	if _, err := s.pools.Get(context.Background(), "dry-pool"); err == nil {
		t.Error("dry-run must not persist the pool")
	}
}

// TestBootstrapSeedsDelegationPolicy_spec_17_6_410 covers the delegation
// policy seed: created keyed by (tenantId, name).
func TestBootstrapSeedsDelegationPolicy_spec_17_6_410(t *testing.T) {
	s := newFullBootstrapRouter(t, false)
	rec, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		DelegationPolicies: []admin.DelegationPolicyPayload{{TenantID: "acme", Name: "default-deny"}},
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resp.DelegationPolicies.CreatedCount != 1 {
		t.Fatalf("createdCount = %d, want 1; resp=%+v", resp.DelegationPolicies.CreatedCount, resp.DelegationPolicies)
	}
	if _, err := s.delpol.Get(context.Background(), "acme", "default-deny"); err != nil {
		t.Errorf("seeded policy not stored: %v", err)
	}
}

// TestBootstrapDelegationPolicyRequiresTenant_spec_17_6_410 covers the
// per-entry validation: a policy with no tenantId is rejected.
func TestBootstrapDelegationPolicyRequiresTenant_spec_17_6_410(t *testing.T) {
	s := newFullBootstrapRouter(t, false)
	rec, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		DelegationPolicies: []admin.DelegationPolicyPayload{{Name: "orphan"}},
	}, "")
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status: got %d, want 207", rec.Code)
	}
	if len(resp.DelegationPolicies.Errors) != 1 || resp.DelegationPolicies.Errors[0].Code != "SEED_VALIDATION" {
		t.Fatalf("expected one SEED_VALIDATION error, got %+v", resp.DelegationPolicies.Errors)
	}
}

// TestBootstrapDelegationPolicyScanRequiresInterceptor_spec_8_3 covers
// the §8.3 structural invariant: scanExportedFiles requires an
// interceptorRef.
func TestBootstrapDelegationPolicyScanRequiresInterceptor_spec_8_3(t *testing.T) {
	s := newFullBootstrapRouter(t, false)
	_, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		DelegationPolicies: []admin.DelegationPolicyPayload{
			{TenantID: "acme", Name: "scan-no-ic", ContentPolicy: admin.ContentPolicyPayload{ScanExportedFiles: true}},
		},
	}, "")
	if len(resp.DelegationPolicies.Errors) != 1 || resp.DelegationPolicies.Errors[0].Code != "SEED_VALIDATION" {
		t.Fatalf("expected SEED_VALIDATION (scan requires interceptor), got %+v", resp.DelegationPolicies.Errors)
	}
}

// TestBootstrapSeedsEnvironment_spec_17_6_438 covers the §17.6 line 438
// Option B access path: an environment with members is seeded.
func TestBootstrapSeedsEnvironment_spec_17_6_438(t *testing.T) {
	s := newFullBootstrapRouter(t, false)
	_ = s.tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	rec, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		Environments: []admin.EnvironmentPayload{
			{
				TenantID: "acme",
				Name:     "default",
				Members: []admin.MemberPayload{
					{Identity: admin.IdentityPayload{Type: "user", Value: "alice@acme.com"}, Role: "creator"},
				},
			},
		},
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; resp=%+v", rec.Code, resp.Environments)
	}
	if resp.Environments.CreatedCount != 1 {
		t.Fatalf("createdCount = %d, want 1; resp=%+v", resp.Environments.CreatedCount, resp.Environments)
	}
	if _, err := s.envs.Get(context.Background(), "acme", "default"); err != nil {
		t.Errorf("seeded environment not stored: %v", err)
	}
}

// TestBootstrapEnvironmentRejectedForRegulatedTenantWithoutSIEM_spec_11_7_449
// covers the §11.7 line 449 gate: an environment under a regulated tenant
// is rejected when no SIEM endpoint is configured.
func TestBootstrapEnvironmentRejectedForRegulatedTenantWithoutSIEM_spec_11_7_449(t *testing.T) {
	s := newFullBootstrapRouter(t, false) // SIEM not configured
	_ = s.tenants.Create(context.Background(), tenantstore.Tenant{ID: "globex", ComplianceProfile: "hipaa"})
	rec, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		Environments: []admin.EnvironmentPayload{
			{TenantID: "globex", Name: "regulated", Members: []admin.MemberPayload{
				{Identity: admin.IdentityPayload{Type: "user", Value: "bob@globex.com"}, Role: "creator"},
			}},
		},
	}, "")
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status: got %d, want 207", rec.Code)
	}
	if len(resp.Environments.Errors) != 1 || resp.Environments.Errors[0].Code != "SEED_VALIDATION" {
		t.Fatalf("expected one SEED_VALIDATION error, got %+v", resp.Environments.Errors)
	}
	if _, err := s.envs.Get(context.Background(), "globex", "regulated"); err == nil {
		t.Error("environment under a regulated tenant without SIEM must not be persisted")
	}
}

// TestBootstrapEnvironmentTierOverrideMayNotLoosen_spec_12_9 covers the
// §12.9 tier-tightening invariant: an environment override that loosens
// the tenant tier (T3 over a T4 tenant) is rejected.
func TestBootstrapEnvironmentTierOverrideMayNotLoosen_spec_12_9(t *testing.T) {
	s := newFullBootstrapRouter(t, false)
	_ = s.tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme", WorkspaceTier: tenantstore.WorkspaceTierT4})
	_, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		Environments: []admin.EnvironmentPayload{
			{TenantID: "acme", Name: "looser", WorkspaceTier: tenantstore.WorkspaceTierT3, Members: []admin.MemberPayload{
				{Identity: admin.IdentityPayload{Type: "user", Value: "alice@acme.com"}, Role: "creator"},
			}},
		},
	}, "")
	if len(resp.Environments.Errors) != 1 || resp.Environments.Errors[0].Code != "SEED_VALIDATION" {
		t.Fatalf("expected SEED_VALIDATION (tier loosening), got %+v", resp.Environments.Errors)
	}
}

// TestBootstrapSummaryCoversNewTypes_spec_15_1_863 verifies pool /
// delegationPolicy / environment seed in one run alongside the original
// three types.
func TestBootstrapSummaryCoversNewTypes_spec_15_1_863(t *testing.T) {
	s := newFullBootstrapRouter(t, false)
	_ = s.tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	rec, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
		Runtimes:           []admin.RuntimePayload{{Name: "echo", Image: "lenny/echo@sha256:abc", Type: "agent", Labels: map[string]string{"tier": "test"}}},
		Pools:              []admin.PoolPayload{{Name: "echo-pool", RuntimeRef: "echo", IsolationProfile: "sandboxed"}},
		DelegationPolicies: []admin.DelegationPolicyPayload{{TenantID: "acme", Name: "dp"}},
		Environments: []admin.EnvironmentPayload{{TenantID: "acme", Name: "env", Members: []admin.MemberPayload{
			{Identity: admin.IdentityPayload{Type: "user", Value: "alice@acme.com"}, Role: "creator"},
		}}},
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; pools=%+v dp=%+v env=%+v", rec.Code, resp.Pools, resp.DelegationPolicies, resp.Environments)
	}
	if resp.Pools.CreatedCount != 1 || resp.DelegationPolicies.CreatedCount != 1 || resp.Environments.CreatedCount != 1 {
		t.Fatalf("counts: pools=%d dp=%d env=%d", resp.Pools.CreatedCount, resp.DelegationPolicies.CreatedCount, resp.Environments.CreatedCount)
	}
}
