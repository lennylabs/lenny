// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// preclaimFixture builds a Server wired with a tenant credentialPolicy,
// a runtime with supportedProviders, and a set of credential pools, so
// resolveCredentialPools exercises the §4.9 intersection + pre-claim
// path. providers is the runtime's supportedProviders.
func preclaimFixture(t *testing.T, policy credential.CredentialPolicy, providers []string, pools ...credentialpoolstore.CredentialPool) *Server {
	t.Helper()
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: policy}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "claude-code", SupportedProviders: providers}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	credPools := credentialpoolstore.NewMemory()
	for _, p := range pools {
		if err := credPools.Create(ctx, p); err != nil {
			t.Fatalf("create pool %s: %v", p.Name, err)
		}
	}
	return &Server{
		tenants:    tenants,
		runtimes:   runtimes,
		credPools:  credPools,
		credRouter: credrouter.NewDefault(),
	}
}

func poolFixture(name, provider string, credStatuses ...credentialpoolstore.CredentialStatus) credentialpoolstore.CredentialPool {
	p := credentialpoolstore.CredentialPool{
		TenantID:              "acme",
		Name:                  name,
		Provider:              provider,
		MaxConcurrentSessions: 10,
	}
	for i, st := range credStatuses {
		p.Credentials = append(p.Credentials, credentialpoolstore.Credential{
			ID:        name + "-cred-" + string(rune('a'+i)),
			SecretRef: "secret-" + name,
			Status:    st,
		})
	}
	return p
}

func sessionRow() sessionstore.Session {
	return sessionstore.Session{ID: "s1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "claude-code"}
}

// spec: §4.9 lines 1326 — the resolved provider→pool map is the
// intersection of supportedProviders and the policy's providerPools,
// each routed to its defaultPool.
func TestResolveCredentialPoolsPoolSource(t *testing.T) {
	policy := credential.CredentialPolicy{
		PreferredSource: credential.PreferredSourcePool,
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
			"aws_bedrock":      {DefaultPool: "bedrock-prod"},
		},
	}
	s := preclaimFixture(
		t, policy, []string{"anthropic_direct", "aws_bedrock"},
		poolFixture("claude-prod", "anthropic_direct", credentialpoolstore.CredentialActive),
		poolFixture("bedrock-prod", "aws_bedrock", credentialpoolstore.CredentialActive),
	)
	got, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if got["anthropic_direct"] != "claude-prod" || got["aws_bedrock"] != "bedrock-prod" {
		t.Errorf("CredentialPools = %v, want both providers routed to their pools", got)
	}
}

// spec: §4.9 line 1326 — only providers in both the runtime's
// supportedProviders and the policy's providerPools are assigned.
func TestResolveCredentialPoolsIntersectionNarrows(t *testing.T) {
	policy := credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
			"vertex_ai":        {DefaultPool: "vertex-prod"}, // not supported by runtime
		},
	}
	s := preclaimFixture(
		t, policy, []string{"anthropic_direct"}, // runtime supports only one
		poolFixture("claude-prod", "anthropic_direct", credentialpoolstore.CredentialActive),
		poolFixture("vertex-prod", "vertex_ai", credentialpoolstore.CredentialActive),
	)
	got, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if len(got) != 1 || got["anthropic_direct"] != "claude-prod" {
		t.Errorf("CredentialPools = %v, want only anthropic_direct→claude-prod", got)
	}
}

// spec: §4.9 lines 1314-1319 — the fallback chain skips a pool whose
// credentials are all revoked and routes to the next pool in order.
func TestResolveCredentialPoolsFallbackOrder(t *testing.T) {
	policy := credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {Fallback: credential.ProviderFallback{Order: []string{"primary", "backup"}}},
		},
	}
	s := preclaimFixture(
		t, policy, []string{"anthropic_direct"},
		poolFixture("primary", "anthropic_direct", credentialpoolstore.CredentialRevoked), // all revoked
		poolFixture("backup", "anthropic_direct", credentialpoolstore.CredentialActive),
	)
	got, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if got["anthropic_direct"] != "backup" {
		t.Errorf("got pool %q, want backup (primary all-revoked)", got["anthropic_direct"])
	}
}

// spec: §4.9 line 1218 — every pool exhausted (all credentials revoked)
// rejects with ErrNoCredentialAvailable before a pod is claimed.
func TestResolveCredentialPoolsExhausted(t *testing.T) {
	policy := credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "primary"},
		},
	}
	s := preclaimFixture(
		t, policy, []string{"anthropic_direct"},
		poolFixture("primary", "anthropic_direct", credentialpoolstore.CredentialRevoked),
	)
	_, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if !errors.Is(err, credrouter.ErrNoCredentialAvailable) {
		t.Errorf("got %v, want ErrNoCredentialAvailable", err)
	}
}

// spec: §4.9 lines 1364, 1370 — a user-only policy with no user-credential
// checker (v1: user-source delivery deferred) rejects with
// ErrUserCredentialNotFound.
func TestResolveCredentialPoolsUserOnlyMiss(t *testing.T) {
	policy := credential.CredentialPolicy{
		PreferredSource:        credential.PreferredSourceUser,
		UserCredentialsEnabled: true,
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "primary"},
		},
	}
	s := preclaimFixture(
		t, policy, []string{"anthropic_direct"},
		poolFixture("primary", "anthropic_direct", credentialpoolstore.CredentialActive),
	)
	_, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if !errors.Is(err, credrouter.ErrUserCredentialNotFound) {
		t.Errorf("got %v, want ErrUserCredentialNotFound", err)
	}
}

// An unconfigured tenant policy assigns no upstream credentials — the
// pre-§4.9 behavior is preserved for deployments without pools.
func TestResolveCredentialPoolsUnconfiguredPolicy(t *testing.T) {
	s := preclaimFixture(t, credential.CredentialPolicy{}, []string{"anthropic_direct"})
	got, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("CredentialPools = %v, want empty for unconfigured policy", got)
	}
}

// With no registries wired the §4.9 layer is inert.
func TestResolveCredentialPoolsNoRegistries(t *testing.T) {
	s := &Server{credRouter: credrouter.NewDefault()}
	got, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, nil) with no registries", got, err)
	}
}

// claimAtCreateScheme builds the scheme the fake binder client needs to
// hold the §5 warm-pool CRDs claimAtCreate resolves a pool from.
func claimAtCreateScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

// spec: §5.2 (service mode is claimless), §7.1 step 4, line 75.
// claimAtCreate resolves a service-mode pool and returns a nil ClaimResult
// (no pod claimed) plus the service-mode §7.1 level, so the create path
// persists the row with no PodAssignment and never touches the SSA claim.
func TestClaimAtCreateServiceModeClaimless(t *testing.T) {
	const ns = "lenny-agents"
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-pool", Namespace: ns},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "svc-tmpl", MinWarm: 1, MaxWarm: 5},
	}
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-tmpl", Namespace: ns},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "svc-runtime",
			IsolationProfile: string(isolation.ProfileSandboxed),
			ExecutionMode:    string(runtimestore.ExecutionModeService),
		},
	}
	c := fake.NewClientBuilder().WithScheme(claimAtCreateScheme(t)).WithObjects(pool, tmpl).Build()
	s := &Server{podBinder: &podsession.Binder{Client: c, Namespace: ns}, agentNamespace: ns}

	out, err := s.claimAtCreate(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", RuntimeRef: "svc-runtime", IsolationProfile: isolation.ProfileSandboxed,
	}, workspaceplan.Plan{})
	if err != nil {
		t.Fatalf("claimAtCreate (service mode): %v", err)
	}
	if out.Claim != nil {
		t.Errorf("service-mode claim = %+v, want nil (claimless)", out.Claim)
	}
	if out.Level.ExecutionMode != "service" {
		t.Errorf("level.ExecutionMode = %q, want service", out.Level.ExecutionMode)
	}
}

// credPolicyStores wires the tenant, runtime, and credential-pool stores a
// §4.9 pre-check needs for a single-provider (anthropic_direct) session, so
// the claimAtCreate pre-check tests share one setup. The named credential
// pool is seeded with credStatus.
func credPolicyStores(t *testing.T, credStatus credentialpoolstore.CredentialStatus) (tenantstore.Store, runtimestore.Store, credentialpoolstore.Store) {
	t.Helper()
	ctx := context.Background()
	policy := credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "primary"},
		},
	}
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: policy}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "claude-code", SupportedProviders: []string{"anthropic_direct"}}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	credPools := credentialpoolstore.NewMemory()
	if err := credPools.Create(ctx, poolFixture("primary", "anthropic_direct", credStatus)); err != nil {
		t.Fatalf("create credential pool: %v", err)
	}
	return tenants, runtimes, credPools
}

// spec: §4.9 lines 1216-1218, §7.1 step 3 — the credential availability
// pre-check runs at create AHEAD of the step-4 claim. An exclusive pool
// whose only credential source is exhausted fails the pre-check, so
// claimAtCreate returns ErrNoCredentialAvailable before the (SSA) Claim
// runs. The fake client cannot satisfy a real claim, so reaching the claim
// would surface a different error; ErrNoCredentialAvailable proves the
// pre-check gated the claim.
func TestClaimAtCreatePreCheckGatesClaim(t *testing.T) {
	const ns = "lenny-agents"
	ctx := context.Background()
	tenants, runtimes, credPools := credPolicyStores(t, credentialpoolstore.CredentialRevoked)

	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-pool", Namespace: ns},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "claude-tmpl", MinWarm: 1, MaxWarm: 5},
	}
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-tmpl", Namespace: ns},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "claude-code", IsolationProfile: string(isolation.ProfileSandboxed)},
	}
	c := fake.NewClientBuilder().WithScheme(claimAtCreateScheme(t)).WithObjects(pool, tmpl).Build()
	s := &Server{
		podBinder:      &podsession.Binder{Client: c, Namespace: ns},
		agentNamespace: ns,
		tenants:        tenants,
		runtimes:       runtimes,
		credPools:      credPools,
		credRouter:     credrouter.NewDefault(),
	}

	_, err := s.claimAtCreate(ctx, sessionstore.Session{
		ID: "s1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "claude-code", IsolationProfile: isolation.ProfileSandboxed,
	}, workspaceplan.Plan{})
	if !errors.Is(err, credrouter.ErrNoCredentialAvailable) {
		t.Errorf("claimAtCreate = %v, want ErrNoCredentialAvailable (pre-check gates the claim)", err)
	}
}

// concurrentClaimServer builds a Server whose resolved pool is a
// concurrent-workspace pool (sessionPolicy.maxConcurrentSessions = 4), with
// the §4.9 credential stores and a miniredis-backed §5.2 slot counter wired.
// The CRD pair and the poolstore mirror share the pool name "conc-pool" so
// ResolvePool folds in MaxConcurrentSessions > 1. The pool holds no idle pod,
// so a create-time slot reservation surfaces ErrNoIdlePod (the fake client
// cannot serve the SSA Apply a successful slot reservation needs; a
// successful reservation is exercised by the podsession ClaimSlot envtest
// tests). The slot counter is wired so the binder is configured as it is in
// production rather than failing closed on a nil counter.
func concurrentClaimServer(t *testing.T, ns string, credStatus credentialpoolstore.CredentialStatus) *Server {
	t.Helper()
	ctx := context.Background()
	tenants, runtimes, credPools := credPolicyStores(t, credStatus)

	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "conc-pool", Namespace: ns},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "conc-tmpl", MinWarm: 1, MaxWarm: 5},
	}
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "conc-tmpl", Namespace: ns},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "claude-code", IsolationProfile: string(isolation.ProfileSandboxed)},
	}
	c := fake.NewClientBuilder().WithScheme(claimAtCreateScheme(t)).WithObjects(pool, tmpl).Build()

	pools := poolstore.NewMemory()
	if err := pools.Create(ctx, poolstore.Pool{
		Name:          "conc-pool",
		RuntimeRef:    "claude-code",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions:            4,
			AcknowledgeProcessLevelIsolation: true,
		},
	}); err != nil {
		t.Fatalf("create concurrent pool mirror: %v", err)
	}

	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })

	return &Server{
		podBinder:      &podsession.Binder{Client: c, Namespace: ns, SlotCounter: slotcounter.New(rc)},
		agentNamespace: ns,
		pools:          pools,
		tenants:        tenants,
		runtimes:       runtimes,
		credPools:      credPools,
		credRouter:     credrouter.NewDefault(),
	}
}

// spec: §4.9 lines 1216-1218, §7.1 step 3 — the §7.1 step-3 credential
// availability pre-check runs at create for a concurrent-workspace pool
// (maxConcurrentSessions > 1) too, AHEAD of the slot-reservation claim. An
// exhausted credential source therefore rejects the create with
// CREDENTIAL_POOL_EXHAUSTED (ErrNoCredentialAvailable) before the client
// uploads and before any slot is reserved, rather than admitting the session
// to `created` and discovering exhaustion only at /start. This pins the §2
// fail-fast contract for the concurrent pool class.
func TestClaimAtCreateConcurrentPoolPreCheckRuns(t *testing.T) {
	s := concurrentClaimServer(t, "lenny-agents", credentialpoolstore.CredentialRevoked)
	_, err := s.claimAtCreate(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "claude-code", IsolationProfile: isolation.ProfileSandboxed,
	}, workspaceplan.Plan{})
	if !errors.Is(err, credrouter.ErrNoCredentialAvailable) {
		t.Errorf("claimAtCreate (concurrent pool) = %v, want ErrNoCredentialAvailable (pre-check runs at create for concurrent pools)", err)
	}
}

// spec: §4.1 (proposal), §7.1 line 23 (atomicity), §5.2 — when the
// credential pre-check passes but the concurrent pool has no idle pod to
// reserve a slot on, claimAtCreate surfaces the exhaustion as the §7.1
// SESSION_CREATION_FAILED atomicity envelope (errCreateClaimExhausted
// wrapping ErrNoIdlePod) before the client uploads, exactly as the exclusive
// claim does. This proves the slot reservation is attempted at create rather
// than deferred to /start.
func TestClaimAtCreateConcurrentPoolExhaustionAtCreate(t *testing.T) {
	s := concurrentClaimServer(t, "lenny-agents", credentialpoolstore.CredentialActive)
	_, err := s.claimAtCreate(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "claude-code", IsolationProfile: isolation.ProfileSandboxed,
	}, workspaceplan.Plan{})
	if !errors.Is(err, errCreateClaimExhausted) {
		t.Errorf("claimAtCreate (concurrent pool, no idle pod) = %v, want errCreateClaimExhausted (slot reservation attempted at create)", err)
	}
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("claimAtCreate error = %v, want the wrapped ErrNoIdlePod inspectable", err)
	}
}

// spec: §4.4, §5.2 (proposal: /start is launch-only; a service-mode pool is
// claimless).
// launchOnPod on a service-mode pool returns (nil, nil): there is no pod to
// launch on, so the two-step /start runs no launch RPC and binds no pod. A
// service-mode session is a connection handle routed through the pool's
// Service/EndpointSlice, so /start neither claims nor binds.
func TestLaunchOnPodServiceModeClaimless(t *testing.T) {
	const ns = "lenny-agents"
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-pool", Namespace: ns},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "svc-tmpl", MinWarm: 1, MaxWarm: 5},
	}
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-tmpl", Namespace: ns},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "svc-runtime",
			IsolationProfile: string(isolation.ProfileSandboxed),
			ExecutionMode:    string(runtimestore.ExecutionModeService),
		},
	}
	c := fake.NewClientBuilder().WithScheme(claimAtCreateScheme(t)).WithObjects(pool, tmpl).Build()
	s := &Server{podBinder: &podsession.Binder{Client: c, Namespace: ns}, agentNamespace: ns}

	result, err := s.launchOnPod(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", RuntimeRef: "svc-runtime", IsolationProfile: isolation.ProfileSandboxed, State: session.StateReady,
	}, workspaceplan.Plan{})
	if err != nil {
		t.Fatalf("launchOnPod (service mode): %v", err)
	}
	if result != nil {
		t.Errorf("service-mode launch result = %+v, want nil (claimless)", result)
	}
}

// spec: §4.4, §5.2 (proposal: a concurrent-workspace pool materializes and
// launches its reserved slot together at /start), §4.6.1 (pool exhaustion).
// launchOnPod on a concurrent-workspace pool routes through bindConcurrentSlot.
// With no idle pod and a row carrying no live reservation, the fresh-slot
// reservation surfaces ErrNoIdlePod (the §5.2 exhaustion sentinel), proving
// the concurrent branch resolves credentials and dispatches the slot bind
// rather than taking the exclusive launch-only path. A successful reserved-slot
// reconnect is exercised at the binder layer (TestBindReservedSlotReconnectsAndStarts).
func TestLaunchOnPodConcurrentPoolDispatchesSlotBind(t *testing.T) {
	s := concurrentClaimServer(t, "lenny-agents", credentialpoolstore.CredentialActive)
	// A ready row with no live reservation (empty PodAssignment) drives the
	// fresh-slot reservation branch of bindConcurrentSlot. The pool holds no
	// idle pod, so the reservation surfaces ErrNoIdlePod.
	_, err := s.launchOnPod(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "claude-code",
		IsolationProfile: isolation.ProfileSandboxed, State: session.StateReady,
	}, workspaceplan.Plan{})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("launchOnPod (concurrent pool, no idle pod) = %v, want ErrNoIdlePod (slot bind dispatched at /start)", err)
	}
}

// spec: §4.9 line 1218, §15.1 line 990 — the pre-claim exhaustion
// envelope is CREDENTIAL_POOL_EXHAUSTED, HTTP 503, category POLICY,
// non-retryable bit aside; the reason distinguishes pre-claim.
func TestWriteCredentialPoolExhaustedShape(t *testing.T) {
	s := &Server{}
	rr := httptest.NewRecorder()
	s.writePodClaimError(rr, credrouter.ErrNoCredentialAvailable, "SESSION_CREATION_FAILED", "claim failed")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	var env struct {
		Error struct {
			Code     string         `json:"code"`
			Category string         `json:"category"`
			Details  map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "CREDENTIAL_POOL_EXHAUSTED" {
		t.Errorf("code = %q, want CREDENTIAL_POOL_EXHAUSTED", env.Error.Code)
	}
	if env.Error.Category != "POLICY" {
		t.Errorf("category = %q, want POLICY", env.Error.Category)
	}
	if env.Error.Details["reason"] != "pre_claim" {
		t.Errorf("reason = %v, want pre_claim", env.Error.Details["reason"])
	}
}

// spec: §4.9 lines 1364, §15.1 line 993 — the user-only miss envelope is
// USER_CREDENTIAL_NOT_FOUND, HTTP 404, category PERMANENT.
func TestWriteUserCredentialNotFoundShape(t *testing.T) {
	s := &Server{}
	rr := httptest.NewRecorder()
	s.writePodClaimError(rr, credrouter.ErrUserCredentialNotFound, "SESSION_CREATION_FAILED", "claim failed")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	var env struct {
		Error struct {
			Code     string `json:"code"`
			Category string `json:"category"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "USER_CREDENTIAL_NOT_FOUND" {
		t.Errorf("code = %q, want USER_CREDENTIAL_NOT_FOUND", env.Error.Code)
	}
	if env.Error.Category != "PERMANENT" {
		t.Errorf("category = %q, want PERMANENT", env.Error.Category)
	}
}

// spec: §4.9 line 1220 — when the pre-claim check passed but the lease
// assignment raced and failed, the gateway emits the mismatch metric
// (labeled by the failing pool and provider) and returns
// CREDENTIAL_POOL_EXHAUSTED with reason assignment_race.
func TestWritePodClaimErrorAssignmentRaceEmitsMetric(t *testing.T) {
	var gotPool, gotProvider string
	s := &Server{preclaimMismatch: func(pool, provider string) { gotPool, gotProvider = pool, provider }}
	rr := httptest.NewRecorder()
	raceErr := &podsession.CredentialAssignmentError{
		Provider: "anthropic_direct",
		Pool:     "claude-prod",
		Err:      credential.ErrPoolExhausted,
	}
	s.writePodClaimError(rr, raceErr, "SESSION_CREATION_FAILED", "claim failed")
	if gotPool != "claude-prod" || gotProvider != "anthropic_direct" {
		t.Errorf("mismatch metric labels = (%q,%q), want (claude-prod, anthropic_direct)", gotPool, gotProvider)
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != "CREDENTIAL_POOL_EXHAUSTED" || env.Error.Details["reason"] != "assignment_race" {
		t.Errorf("got code=%q reason=%v, want CREDENTIAL_POOL_EXHAUSTED/assignment_race", env.Error.Code, env.Error.Details["reason"])
	}
}

// spec: §7.1 line 28 (a create-step failure rolls back without persisting the
// row, releasing the create-time claim), §4.7 (the combined create-and-start
// path reuses the claim/prepare/launch phases), §5.2 (slot reservation
// release). The combined POST /v1/sessions/start path claims at /create then
// runs prepare/launch in the same call; on a startOnPod error
// createClaimNeedsRollback decides whether the handler must release the
// create-time claim itself or whether the binder already released it. The
// reservation-leak regression: a slot reservation's create-time active_slots
// increment is leaked when startOnPod fails in its pre-bind ResolvePool /
// PoolWarmingUp / manifest lookups (before BindReservedSlot, which owns the
// non-idempotent slot-count release), so the predicate must release for a
// non-SlotBindError slot failure and skip a SlotBindError to avoid a
// double-decrement.
func TestCreateClaimNeedsRollback_spec_7_1_28(t *testing.T) {
	exclusive := &podsession.ClaimResult{SandboxName: "pod-a"} // SlotID == "" → exclusive
	slot := &podsession.ClaimResult{SandboxName: "pod-a", SlotID: "sess-1"}
	someErr := errors.New("startOnPod failed")
	// A pre-bind failure inside startOnPod (the second ResolvePool, the
	// PoolWarmingUp gate) returns a non-SlotBindError; the create-time slot
	// reservation is then unreleased and must be rolled back here.
	preBindWarming := &podsession.PoolWarmingError{Pool: "p", PodsWarming: 1}
	// BindReservedSlot already released the reservation on its own failure,
	// surfaced as a *podsession.SlotBindError; releasing again double-decrements.
	binderFailure := slotBindErr("pod-a", "sess-1", "session_start", codes.Unavailable)

	cases := []struct {
		name  string
		claim *podsession.ClaimResult
		err   error
		want  bool
	}{
		{"nil claim (service-mode claimless)", nil, someErr, false},
		{"exclusive claim, pre-bind failure", exclusive, preBindWarming, true},
		{"exclusive claim, arbitrary failure", exclusive, someErr, true},
		{"slot reservation, pre-bind ResolvePool/warming failure", slot, preBindWarming, true},
		{"slot reservation, arbitrary non-SlotBindError", slot, someErr, true},
		{"slot reservation, BindReservedSlot SlotBindError", slot, binderFailure, false},
		// A wrapped SlotBindError still unwraps to the binder-owned release.
		{"slot reservation, wrapped SlotBindError", slot, fmt.Errorf("dispatch: %w", binderFailure), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := createClaimNeedsRollback(tc.claim, tc.err); got != tc.want {
				t.Errorf("createClaimNeedsRollback(%+v, %v) = %v, want %v", tc.claim, tc.err, got, tc.want)
			}
		})
	}
}
