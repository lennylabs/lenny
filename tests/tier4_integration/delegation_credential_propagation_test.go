// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §8.3 credentialPropagation: deny suppression
// and the inherit-from-deny origin-chain terminator. A deny hop grants the
// child no LLM credentials (spec/08 line 443), so a deny child finalized
// through the real sessionserver credential-assignment path is minted no
// credential lease. A deny session also holds no origin pool (line 490), so
// an inherit hop whose origin resolves to a deny session has nothing to
// inherit and is rejected with CREDENTIAL_POOL_EXHAUSTED at delegation time.
//
// The deny-leaf suppression is driven end to end through the real
// sessionserver create → finalize barrier, its pod binder, and the real
// credential-pool minting path over an envtest-backed warm pool, observing
// the credential assigner directly. The inherit-from-deny terminator is
// driven through the real lenny/delegate_task MCP handler over a real
// delegation.Service with the real §4.9 engine wired as the delegation-time
// credential-availability checker, observing the delegate-time rejection and
// the absent child row.
//
// The crossEnvCall, crossEnvErrorEnvelope, crossEnvChildID, childrenOf,
// childIDCounter, mustCreateRuntime, poolRecordingAssigner,
// newRecordingAdapter, and the prov*/pool* constants and crossEnvCaller*
// principals live in cross_environment_delegation_test.go and
// delegation_credential_pool_race_test.go (same package).

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/admission/ownership"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// spec: 8.3 (deny receives no LLM credentials; inherit-from-deny fails closed)
// diagnosis: the §8.3 credentialPropagation: deny suppression diverged. A
// deny child was assigned an LLM credential the spec forbids (line 443), or
// an inherit hop drew from a deny origin that holds no pool (line 490). A
// failure here means a pure file-processing tool delegated with deny received
// an upstream LLM credential, or a grandchild inheriting from a deny session
// was credentialed from the deny runtime's supportedProviders instead of
// being rejected with CREDENTIAL_POOL_EXHAUSTED.
func TestDenyChildReceivesNoCredentialAtFinalize(t *testing.T) {
	envtest.SkipUnlessAvailable(t)
	ctx := context.Background()

	cluster := denyLeafCluster(t)
	assigner := &poolRecordingAssigner{}
	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        crossEnvNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      eagerAdapterDialer(t, newRecordingAdapter(t)),
		Blobs:            blobstore.NewMemoryStore(nil),
		Credentials:      assigner,
	}

	store := memstore.New()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{
		ID: "acme",
		CredentialPolicy: credential.CredentialPolicy{
			PreferredSource: credential.PreferredSourcePool,
			ProviderPools: map[string]credential.ProviderPool{
				provAnthropic: {DefaultPool: poolAnthropic},
				provOpenAI:    {DefaultPool: poolOpenAI},
			},
		},
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	// shared-tool supports {anthropic_direct, openai_direct}; both providers
	// have an active pool, so absent the deny suppression a child on this
	// runtime draws both claude-prod and openai-prod. The deny bit is the
	// only difference between the two children finalized below.
	mustCreateRuntime(t, runtimes, runtimestore.Runtime{
		Name: "shared-tool", SupportedProviders: []string{provAnthropic, provOpenAI},
	})

	credPools := credentialpoolstore.NewMemory()
	for _, p := range []struct{ name, provider string }{
		{poolAnthropic, provAnthropic},
		{poolOpenAI, provOpenAI},
	} {
		if err := credPools.Create(ctx, credentialpoolstore.CredentialPool{
			TenantID: "acme", Name: p.name, Provider: p.provider, MaxConcurrentSessions: 10,
			Credentials: []credentialpoolstore.Credential{
				{ID: p.name + "-cred", SecretRef: "secret-" + p.name, Status: credentialpoolstore.CredentialActive},
			},
		}); err != nil {
			t.Fatalf("create pool %s: %v", p.name, err)
		}
	}

	ids := childIDCounter()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  ids,
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          crossEnvNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
		Blobs:                   binder.Blobs,
	})
	h := srv.Handler()

	post := func(path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// createChild creates a session on shared-tool and returns its id. The
	// create-time §7.1 pre-check uses the child's own eligible set (both
	// providers have active pools), so the create succeeds; the deny
	// suppression applies at the finalize-time assignment below.
	createChild := func() string {
		body, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "shared-tool", UserID: "alice@acme.com"})
		rr := post("/v1/sessions", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create child: status %d, body=%s", rr.Code, rr.Body.String())
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || out.ID == "" {
			t.Fatalf("decode create response: %v; body=%s", err, rr.Body.String())
		}
		return out.ID
	}

	// A deny child: stamp the deny marker onto the committed row, exactly as
	// the delegation Service does when a delegate_task hop sets
	// credentialPropagation: deny. Finalize must mint no credential.
	denyID := createChild()
	if _, err := store.Update(ctx, "acme", denyID, func(s *sessionstore.Session) error {
		s.CredentialDeny = true
		return nil
	}); err != nil {
		t.Fatalf("stamp deny marker onto child: %v", err)
	}
	if rr := post("/v1/sessions/"+denyID+"/finalize", []byte(`{}`)); rr.Code != http.StatusOK {
		t.Fatalf("finalize deny child: status %d, body=%s", rr.Code, rr.Body.String())
	}
	if got := assigner.assignedPools(); len(got) != 0 {
		t.Fatalf("deny child assigned pools = %v, want none (deny grants no LLM credentials)", got)
	}

	// Contrast: an independent (non-deny) child on the same fixture draws
	// both of its providers' pools. This proves the empty assignment above is
	// caused by the deny bit, not a broken fixture, and pins the regression
	// against the pre-fix code that stamped a deny child byte-identical to an
	// independent one.
	indepID := createChild()
	if rr := post("/v1/sessions/"+indepID+"/finalize", []byte(`{}`)); rr.Code != http.StatusOK {
		t.Fatalf("finalize independent child: status %d, body=%s", rr.Code, rr.Body.String())
	}
	got := assigner.assignedPools()
	want := []string{poolAnthropic, poolOpenAI}
	sort.Strings(want)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("independent child assigned pools = %v, want %v (a non-deny child draws its full eligible set)", got, want)
	}
}

// spec: 8.3 (inherit hop whose origin is deny holds no pool)
// diagnosis: the §8.3 inherit-from-deny origin-chain terminator diverged. A
// grandchild delegated with credentialPropagation: inherit from a deny parent
// was not rejected with CREDENTIAL_POOL_EXHAUSTED at delegation time, so an
// inherit hop drew eligibility from a deny origin runtime's supportedProviders
// even though a deny session holds no origin pool (spec/08 lines 443, 490).
func TestInheritFromDenyOriginRejectedAtDelegation(t *testing.T) {
	srv, store := newDenyOriginFixture(t)

	// First hop: delegate a deny child under the parent. A deny hop skips the
	// delegation-time availability pre-check (only inherit/independent/omitted
	// run it), so the child commits. The delegation Service stamps the deny
	// marker on the committed row.
	resp := crossEnvCall(t, srv, crossEnvCallerTeamA, "sess_parent", "file-tool", "deny")
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("a deny delegation must commit a child (no pre-check runs for deny): %+v", resp)
	}
	denyChild := crossEnvChildID(t, result)
	child, err := store.Get(context.Background(), "acme", denyChild)
	if err != nil {
		t.Fatalf("deny child must be committed: %v", err)
	}
	if !child.CredentialDeny {
		t.Fatalf("delegated deny child CredentialDeny = false, want true (the deny marker must persist for the origin-chain terminator)")
	}

	// The deny child must be running to delegate onward.
	if _, err := store.Update(context.Background(), "acme", denyChild, func(s *sessionstore.Session) error {
		s.State = session.StateRunning
		return nil
	}); err != nil {
		t.Fatalf("promote deny child to running: %v", err)
	}

	// Second hop: an inherit grandchild from the deny child. The deny child's
	// runtime declares anthropic_direct with an active pool, so absent the
	// origin-row deny check the pre-check would derive eligibility from it and
	// admit. Because a deny session holds no origin pool, the inherit hop is
	// rejected with CREDENTIAL_POOL_EXHAUSTED and commits no grandchild.
	resp = crossEnvCall(t, srv, crossEnvCallerTeamA, denyChild, "sub-tool", "inherit")
	result, _ = resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("an inherit hop from a deny origin must be a tool error: %+v", resp)
	}
	env := crossEnvErrorEnvelope(t, result)
	if env["code"] != "CREDENTIAL_POOL_EXHAUSTED" {
		t.Errorf("grandchild code = %v, want CREDENTIAL_POOL_EXHAUSTED (deny origin holds no pool)", env["code"])
	}
	// The rejection is pre-claim: no grandchild session row is committed under
	// the deny child.
	if childrenOf(t, store, denyChild) != 0 {
		t.Error("an inherit-from-deny rejection must commit no grandchild session (pre-claim)")
	}
}

// newDenyOriginFixture wires the real lenny/delegate_task MCP handler over a
// real delegation.Service with a real *sessionserver.Server as the §8.3
// delegation-time credential-availability checker. The parent runs planner;
// file-tool (the deny child) and sub-tool (the inherit grandchild) each
// support only anthropic_direct, which has an active pool. Every runtime and
// pool is individually assignable, so the only thing that denies the inherit
// grandchild is the deny origin-chain terminator.
func newDenyOriginFixture(t *testing.T) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	ctx := context.Background()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	tenants := tenantstore.NewMemory()
	credPools := credentialpoolstore.NewMemory()

	mustCreateRuntime(t, runtimes, runtimestore.Runtime{
		Name: "planner", Type: runtimestore.TypeAgent,
		SupportedProviders: []string{provAnthropic},
	})
	mustCreateRuntime(t, runtimes, runtimestore.Runtime{
		Name: "file-tool", Type: runtimestore.TypeAgent,
		SupportedProviders: []string{provAnthropic},
	})
	mustCreateRuntime(t, runtimes, runtimestore.Runtime{
		Name: "sub-tool", Type: runtimestore.TypeAgent,
		SupportedProviders: []string{provAnthropic},
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

// denyLeafCluster returns an envtest-backed cluster seeded with a warm pool,
// its template, and two idle Sandboxes for the shared-tool runtime, so the
// create handler can claim a pod for each of the two children the deny-leaf
// test finalizes.
func denyLeafCluster(t *testing.T) client.Client {
	t.Helper()
	env := envtest.Start(t)
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme lenny: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: s})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: crossEnvNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-pool", Namespace: crossEnvNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "shared-tmpl", MinWarm: 2, MaxWarm: 5},
	}); err != nil {
		t.Fatalf("create warm pool: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-tmpl", Namespace: crossEnvNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "shared-tool", IsolationProfile: string(isolation.ProfileSandboxed)},
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	for i, ip := range []string{"10.244.2.11", "10.244.2.12"} {
		name := "sbx-deny-" + string(rune('a'+i))
		if err := c.Create(ctx, &lennyv1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: crossEnvNS,
				Labels: map[string]string{warmpool.LabelPool: "shared-pool"},
			},
		}); err != nil {
			t.Fatalf("create sandbox %s: %v", name, err)
		}
		u := &unstructured.Unstructured{}
		u.SetAPIVersion(lennyv1.GroupVersion.String())
		u.SetKind("Sandbox")
		u.SetName(name)
		u.SetNamespace(crossEnvNS)
		_ = unstructured.SetNestedField(u.Object, map[string]interface{}{"phase": "idle", "podIP": ip}, "status")
		if err := c.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
			t.Fatalf("seed WPC sandbox status %s: %v", name, err)
		}
	}
	return c
}
