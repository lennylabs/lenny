// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/admission/direct_mode_isolation"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// spec: §4.9 — the session-start credential-delivery gate evaluates the
// effective per-provider CredentialPool deliveryMode against the bound pod's
// isolationProfile. In multi-tenant mode a direct-delivery pool paired with a
// standard-isolation pod is the cross-tenant-risky combination the gate must
// reject with the guard's DirectModeStandardIsolationMultiTenantRejected code,
// even though the pool-definition copy the registration and admission layers
// inspect admitted the pod. Pre-fix there was no such gate, so this combination
// reached the lease mint.
func TestCheckCredentialDeliveryIsolationRejectsDirectStandardMultiTenant(t *testing.T) {
	s := &Server{tenancyMode: direct_mode_isolation.TenancyMulti}
	match := podsession.PoolMatch{IsolationProfile: string(isolation.ProfileStandard)}
	err := s.checkCredentialDeliveryIsolation(match, map[string]string{"claude-prod": "direct"})
	var iso *CredentialDeliveryIsolationError
	if !errors.As(err, &iso) {
		t.Fatalf("checkCredentialDeliveryIsolation = %v, want CredentialDeliveryIsolationError", err)
	}
	if iso.Code != direct_mode_isolation.RejectDirectModeStandardIsolation {
		t.Errorf("code = %q, want %q", iso.Code, direct_mode_isolation.RejectDirectModeStandardIsolation)
	}
}

// spec: §4.9 — a proxy-delivery pool paired with a pod whose spiffeBinding is
// disabled is the second cross-tenant-risky combination; the gate rejects it in
// multi-tenant mode with ProxyModeSpiffeBindingDisabledMultiTenantRejected.
func TestCheckCredentialDeliveryIsolationRejectsProxySpiffeDisabledMultiTenant(t *testing.T) {
	s := &Server{tenancyMode: direct_mode_isolation.TenancyMulti}
	match := podsession.PoolMatch{
		IsolationProfile: string(isolation.ProfileSandboxed),
		SpiffeBinding:    "disabled",
	}
	err := s.checkCredentialDeliveryIsolation(match, map[string]string{"claude-prod": "proxy"})
	var iso *CredentialDeliveryIsolationError
	if !errors.As(err, &iso) {
		t.Fatalf("checkCredentialDeliveryIsolation = %v, want CredentialDeliveryIsolationError", err)
	}
	if iso.Code != direct_mode_isolation.RejectProxyModeSpiffeBindingDisabled {
		t.Errorf("code = %q, want %q", iso.Code, direct_mode_isolation.RejectProxyModeSpiffeBindingDisabled)
	}
}

// spec: §4.9 — outside multi-tenant mode both combinations are permitted (the
// warm-pool opt-in fields govern them there). The gate returns nil for a
// single-tenant deployment even on the direct + standard pairing, matching the
// canonical Decide's enforced() predicate.
func TestCheckCredentialDeliveryIsolationAllowsSingleTenant(t *testing.T) {
	s := &Server{tenancyMode: "single"}
	match := podsession.PoolMatch{IsolationProfile: string(isolation.ProfileStandard)}
	if err := s.checkCredentialDeliveryIsolation(match, map[string]string{"claude-prod": "direct"}); err != nil {
		t.Errorf("single-tenant gate = %v, want nil (permitted outside multi-tenant mode)", err)
	}
}

// spec: §4.9 — development mode is permissive alongside single-tenant mode; a
// multi-tenant deployment with devMode set does not reject the combination.
func TestCheckCredentialDeliveryIsolationAllowsDevMode(t *testing.T) {
	s := &Server{tenancyMode: direct_mode_isolation.TenancyMulti, devMode: true}
	match := podsession.PoolMatch{IsolationProfile: string(isolation.ProfileStandard)}
	if err := s.checkCredentialDeliveryIsolation(match, map[string]string{"claude-prod": "direct"}); err != nil {
		t.Errorf("dev-mode gate = %v, want nil (permissive in development mode)", err)
	}
}

// spec: §4.9 — an empty tenancyMode (the unset default) is treated as
// single-tenant, so the gate never rejects. This pins that omitting the
// TenancyMode wiring leaves the gate permissive rather than crashing.
func TestCheckCredentialDeliveryIsolationEmptyTenancyModeAllows(t *testing.T) {
	s := &Server{}
	match := podsession.PoolMatch{IsolationProfile: string(isolation.ProfileStandard)}
	if err := s.checkCredentialDeliveryIsolation(match, map[string]string{"claude-prod": "direct"}); err != nil {
		t.Errorf("empty tenancy gate = %v, want nil", err)
	}
}

// spec: §4.9, §15.1 — a session-start credential-delivery rejection surfaces as
// a 422 carrying the guard's rejection code and the Decision.Reason remediation
// message, not the retryable atomic-unit fallback (a retry resolves the same
// forbidden combination).
func TestWritePodClaimErrorCredentialDeliveryIsolation422(t *testing.T) {
	s := &Server{}
	rr := httptest.NewRecorder()
	s.writePodClaimError(rr, &CredentialDeliveryIsolationError{
		Code:   direct_mode_isolation.RejectDirectModeStandardIsolation,
		Reason: "DirectModeStandardIsolationMultiTenantRejected: CredentialPool ...",
	}, "SESSION_CREATION_FAILED", "claim failed")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != direct_mode_isolation.RejectDirectModeStandardIsolation {
		t.Errorf("code = %q, want %q", env.Error.Code, direct_mode_isolation.RejectDirectModeStandardIsolation)
	}
}

// spec: §4.9 — resolveCredentialPools surfaces each resolved provider pool's
// effective CredentialPool deliveryMode alongside the assignment map, so the
// session-start gate reads the delivery mode leasing actually uses rather than
// the denormalized warm-pool copy.
func TestResolveCredentialPoolsSurfacesDeliveryMode(t *testing.T) {
	policy := credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
		},
	}
	pool := poolFixture("claude-prod", "anthropic_direct", credentialpoolstore.CredentialActive)
	pool.DeliveryMode = "direct"
	s := preclaimFixture(t, policy, []string{"anthropic_direct"}, pool)
	assignments, deliveryModes, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if assignments["anthropic_direct"] != "claude-prod" {
		t.Fatalf("assignment = %v, want claude-prod", assignments)
	}
	if deliveryModes["claude-prod"] != "direct" {
		t.Errorf("deliveryModes = %v, want claude-prod→direct", deliveryModes)
	}
}

// deliveryGateResolveServer wires a Server against a fake Kubernetes client
// holding an exclusive warm pool whose SandboxTemplate carries the given
// isolation profile and spiffe binding, plus the §4.9 credential stores routing
// anthropic_direct to a pool with the given CredentialPool deliveryMode. It is
// the fixture the seam-level gate tests drive startOnPod / prepareAtFinalize
// through so the gate runs against a resolved PoolMatch and a resolved
// CredentialPool deliveryMode.
func deliveryGateResolveServer(t *testing.T, tenancyMode, isoProfile, spiffeBinding, deliveryMode string) *Server {
	t.Helper()
	ctx := context.Background()
	const ns = "lenny-agents"
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "primary"},
		},
	}}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "claude-code", SupportedProviders: []string{"anthropic_direct"}}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	credPools := credentialpoolstore.NewMemory()
	primary := poolFixture("primary", "anthropic_direct", credentialpoolstore.CredentialActive)
	primary.DeliveryMode = deliveryMode
	if err := credPools.Create(ctx, primary); err != nil {
		t.Fatalf("create credential pool: %v", err)
	}
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-pool", Namespace: ns},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "claude-tmpl", MinWarm: 1, MaxWarm: 5},
	}
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-tmpl", Namespace: ns},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-code",
			IsolationProfile: isoProfile,
			SpiffeBinding:    spiffeBinding,
		},
	}
	c := fake.NewClientBuilder().WithScheme(claimAtCreateScheme(t)).WithObjects(pool, tmpl).Build()
	return &Server{
		podBinder:      &podsession.Binder{Client: c, Namespace: ns},
		agentNamespace: ns,
		tenants:        tenants,
		runtimes:       runtimes,
		credPools:      credPools,
		credRouter:     credrouter.NewDefault(),
		tenancyMode:    tenancyMode,
	}
}

// spec: §4.9 — the gate fires on the resume / tree-recovery rebuild seam
// (startOnPod with claimed == nil). A direct-delivery CredentialPool paired with
// a standard-isolation pod in multi-tenant mode is rejected before the
// whole-sequence Bind mints a lease. Pre-fix startOnPod ran no such gate and
// reached the bind (surfacing a pod-claim error against the fake client), so
// asserting the typed rejection pins the corrected behavior.
func TestStartOnPodRejectsForbiddenDeliveryModeMultiTenant(t *testing.T) {
	s := deliveryGateResolveServer(t, direct_mode_isolation.TenancyMulti,
		string(isolation.ProfileStandard), "", "direct")
	_, err := s.startOnPod(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "claude-code",
		IsolationProfile: isolation.ProfileStandard,
	}, workspaceplan.Plan{}, nil)
	var iso *CredentialDeliveryIsolationError
	if !errors.As(err, &iso) {
		t.Fatalf("startOnPod = %v, want CredentialDeliveryIsolationError (gate fires before bind)", err)
	}
	if iso.Code != direct_mode_isolation.RejectDirectModeStandardIsolation {
		t.Errorf("code = %q, want %q", iso.Code, direct_mode_isolation.RejectDirectModeStandardIsolation)
	}
}

// spec: §4.9 — the same forbidden pairing in single-tenant mode is admitted by
// the gate, so startOnPod proceeds past it to the bind (which fails against the
// fake client with a non-delivery error). This pins that the gate is scoped to
// multi-tenant mode rather than rejecting unconditionally.
func TestStartOnPodAdmitsForbiddenDeliveryModeSingleTenant(t *testing.T) {
	s := deliveryGateResolveServer(t, "single",
		string(isolation.ProfileStandard), "", "direct")
	_, err := s.startOnPod(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "claude-code",
		IsolationProfile: isolation.ProfileStandard,
	}, workspaceplan.Plan{}, nil)
	var iso *CredentialDeliveryIsolationError
	if errors.As(err, &iso) {
		t.Fatalf("startOnPod (single-tenant) = %v, want non-delivery error (gate admits outside multi-tenant)", err)
	}
}

// spec: §4.9 — the gate fires on the two-step finalize seam (prepareAtFinalize
// before podBinder.Prepare). A direct-delivery pool paired with a
// standard-isolation pod bound at create is rejected in multi-tenant mode before
// the prepare barrier assigns the lease.
func TestPrepareAtFinalizeRejectsForbiddenDeliveryModeMultiTenant(t *testing.T) {
	s := deliveryGateResolveServer(t, direct_mode_isolation.TenancyMulti,
		string(isolation.ProfileStandard), "", "direct")
	_, err := s.prepareAtFinalize(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "claude-code",
		IsolationProfile: isolation.ProfileStandard,
		PodAssignment:    "pod-a",
		PoolRef:          "claude-pool",
		State:            "finalizing",
	}, workspaceplan.Plan{})
	var iso *CredentialDeliveryIsolationError
	if !errors.As(err, &iso) {
		t.Fatalf("prepareAtFinalize = %v, want CredentialDeliveryIsolationError (gate fires before Prepare)", err)
	}
	if iso.Code != direct_mode_isolation.RejectDirectModeStandardIsolation {
		t.Errorf("code = %q, want %q", iso.Code, direct_mode_isolation.RejectDirectModeStandardIsolation)
	}
}
