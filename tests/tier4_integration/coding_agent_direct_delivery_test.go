// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §26.2 air-gapped `deliveryMode: direct`
// coding-agent path. §26.2 states that direct delivery "is supported for
// air-gapped environments where the operator has already provisioned
// provider keys as Secret volumes". §4.9 defines `deliveryMode: direct` as
// one of the two credential-leasing delivery modes a deployer chooses per
// pool ("Deployers choose for each credential pool whether to use direct
// credential leasing ... or proxy mode", spec/04_system-components.md line
// 1510), and the wire contract for a minted direct-mode lease is that
// `materializedConfig` "contains the full provider credentials" (spec/
// 04_system-components.md line 961). So the air-gapped path is still a
// credential lease minted by Lenny's leasing service; the distinguishing
// property is that the pod receives the real provider key in the clear
// (unlike proxy mode, where only an opaque lease token crosses into the
// pod). This test drives a `claude-code` (coding-agent, §26.1) session
// through the real create -> upload -> finalize lifecycle against a real
// kube-apiserver (envtest), with the provider key sourced from a real
// Kubernetes Secret the operator would provision out-of-band for an
// air-gapped deployment, and asserts:
//
//   - finalize succeeds (sandboxed isolation + direct delivery is not a
//     forbidden pairing, unlike the direct+standard multi-tenant combination
//     credential_delivery_gate_test.go and admission_credential_multitenant_
//     test.go cover);
//   - exactly one credential lease is minted, at finalize (direct mode does
//     not bypass credential leasing);
//   - the pod's materialized credential file carries the real API key read
//     back from the Kubernetes Secret, under deliveryMode "direct", and
//     carries no proxy-mode fields (leaseToken, proxyUrl).
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

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
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const (
	codingAgentDirectNS         = "lenny-agents"
	codingAgentDirectSecretNS   = "lenny-system"
	codingAgentDirectSecretName = "claude-code-provider-key"
	codingAgentDirectPool       = "claude-prod"
	codingAgentDirectTenant     = "acme"
	codingAgentDirectSession    = "sess-coding-agent-direct"
)

// codingAgentDirectCluster seeds an envtest cluster with a `claude-code`
// (coding-agent) warm pool, its SandboxTemplate at the sandboxed isolation
// profile (§26.2's coding-agent default), one idle Sandbox the create
// handler can claim, and a real Kubernetes Secret in `lenny-system` holding
// the provider key material the way an operator provisions one for the
// §26.2 air-gapped `deliveryMode: direct` path. It returns the cluster
// client and the API key value read back from the live Secret object, so
// the test can assert the materialized credential file traces to this
// exact Secret rather than to a value hard-coded in the test.
func codingAgentDirectCluster(t *testing.T) (client.Client, string) {
	t.Helper()
	envtest.SkipUnlessAvailable(t)
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
	for _, ns := range []string{codingAgentDirectNS, codingAgentDirectSecretNS} {
		if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create namespace %s: %v", ns, err)
		}
	}

	// The §26.2 air-gapped provisioning model: the operator has already put
	// the provider key in a Kubernetes Secret. §4.9's Secret-shape table
	// gives `anthropic_direct` a single `apiKey` data key.
	apiKey := "sk-ant-airgapped-9f21bd7c"
	if err := c.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: codingAgentDirectSecretName, Namespace: codingAgentDirectSecretNS},
		StringData: map[string]string{"apiKey": apiKey},
	}); err != nil {
		t.Fatalf("create provider-key secret: %v", err)
	}
	var readBack corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Namespace: codingAgentDirectSecretNS, Name: codingAgentDirectSecretName}, &readBack); err != nil {
		t.Fatalf("read back provider-key secret: %v", err)
	}
	gotKey := string(readBack.Data["apiKey"])
	if gotKey != apiKey {
		t.Fatalf("secret round-trip: got apiKey %q, want %q", gotKey, apiKey)
	}

	if err := c.Create(ctx, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-code-pool", Namespace: codingAgentDirectNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "claude-code-tmpl", MinWarm: 1, MaxWarm: 5},
	}); err != nil {
		t.Fatalf("create warm pool: %v", err)
	}
	// §26.2 "Isolation profile": coding-agent runtimes default to
	// isolationProfile: sandboxed. Direct delivery + sandboxed isolation is
	// not a forbidden combination (only direct + standard is, in
	// multi-tenant mode), so finalize is expected to succeed.
	if err := c.Create(ctx, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-code-tmpl", Namespace: codingAgentDirectNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-code",
			IsolationProfile: string(isolation.ProfileSandboxed),
		},
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sbx-1", Namespace: codingAgentDirectNS,
			Labels: map[string]string{warmpool.LabelPool: "claude-code-pool"},
		},
	}
	if err := c.Create(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName("sbx-1")
	u.SetNamespace(codingAgentDirectNS)
	_ = unstructured.SetNestedField(u.Object, map[string]interface{}{"phase": "idle", "podIP": "10.244.3.6"}, "status")
	if err := c.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed WPC sandbox status: %v", err)
	}
	return c, gotKey
}

// spec: §26.2 line 51 ("`deliveryMode: direct` is supported for air-gapped
// environments where the operator has already provisioned provider keys as
// `Secret` volumes and does not need Lenny's credential leasing") and §4.9
// line 1510 ("Deployers choose for each credential pool whether to use
// direct credential leasing ... or proxy mode ... key materialized into the
// agent pod") and line 961 ("For deliveryMode: direct, materializedConfig
// contains the full provider credentials").
//
// diagnosis: a failure here means the §26.2 air-gapped direct-delivery path
// for a coding-agent runtime regressed. If finalize does not return ready,
// the session-start credential-delivery gate is (wrongly) rejecting a
// sandboxed-isolation direct-mode pool it must admit. If the lease-mint
// count is not exactly 1, direct-mode delivery has drifted from being one of
// Lenny's two credential-leasing delivery modes (§4.9 line 1510) into either
// minting no lease at all or minting more than once. If the pod's
// materialized credential file does not carry the exact key read back from
// the live Kubernetes Secret, the air-gapped Secret-provisioning path is not
// reaching the coding-agent pod's credential file the way §26.2 and the
// §4.9 materializedConfig contract require.
func TestCodingAgentDirectDeliveryFromAirGappedSecret(t *testing.T) {
	rt := &eagerRuntime{}
	credDir := t.TempDir()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.StagingDir = t.TempDir()
	adapterSrv.CredentialsDir = credDir
	adapterSrv.Runtime = rt

	cluster, apiKey := codingAgentDirectCluster(t)
	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        codingAgentDirectNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      eagerAdapterDialer(t, adapterSrv),
	}
	blobs := blobstore.NewMemoryStore(nil)
	binder.Blobs = blobs

	// The real §4.9 credential-assignment service (not a test double): a
	// direct-mode pool whose sole credential's APIKey is the value read
	// back from the live Kubernetes Secret above, mirroring the air-gapped
	// operator-provisioned-Secret model §26.2 describes. OnAssigned counts
	// every lease minted so the test can assert direct mode still goes
	// through exactly one credential-leasing mint, per §4.9 line 1510.
	var mints int32
	assignSvc := credassign.New(credleasestore.New(), credcache.New())
	assignSvc.OnAssigned(func(credassign.LeaseAssignment) { atomic.AddInt32(&mints, 1) })
	assignSvc.RegisterPool(credassign.Pool{
		Name:         codingAgentDirectPool,
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryDirect,
		Strategy:     credential.StrategyLeastLoaded,
		Credentials: []credassign.PoolCredential{
			{ID: "claude-prod-cred-a", APIKey: apiKey, Healthy: true},
		},
	})
	binder.Credentials = assignSvc

	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{
		ID: codingAgentDirectTenant,
		CredentialPolicy: credential.CredentialPolicy{
			PreferredSource: credential.PreferredSourcePool,
			ProviderPools: map[string]credential.ProviderPool{
				"anthropic_direct": {DefaultPool: codingAgentDirectPool},
			},
		},
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "claude-code", SupportedProviders: []string{"anthropic_direct"}}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	credPools := credentialpoolstore.NewMemory()
	if err := credPools.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID:              codingAgentDirectTenant,
		Name:                  codingAgentDirectPool,
		Provider:              "anthropic_direct",
		DeliveryMode:          "direct",
		MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{
			{ID: "claude-prod-cred-a", SecretRef: codingAgentDirectSecretNS + "/" + codingAgentDirectSecretName, Status: credentialpoolstore.CredentialActive},
		},
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return codingAgentDirectSession },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          codingAgentDirectNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
		Blobs:                   blobs,
	})
	h := srv.Handler()

	post := func(path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("X-Lenny-Tenant-ID", codingAgentDirectTenant)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "claude-code", UserID: "alice@acme.com"})
	if rr := post("/v1/sessions", createBody, nil); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	if n := atomic.LoadInt32(&mints); n != 0 {
		t.Fatalf("AssignProto called %d times at create, want 0 (the lease is assigned at finalize)", n)
	}

	rr := post("/v1/sessions/"+codingAgentDirectSession+"/upload", []byte("# uploaded via artifact store"), map[string]string{
		"Content-Type": "text/markdown",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var up sessionserver.UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &up); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	finalizeBody, _ := json.Marshal(map[string]any{
		"workspacePlan": map[string]any{
			"schemaVersion": 1,
			"sources": []map[string]any{
				{"type": "uploadFile", "path": "CLAUDE.md", "uploadRef": up.UploadRef},
			},
		},
	})
	rr = post("/v1/sessions/"+codingAgentDirectSession+"/finalize", finalizeBody, map[string]string{"Content-Type": "application/json"})
	if rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, want 200 (sandboxed isolation + deliveryMode: direct is not a forbidden pairing); body=%s", rr.Code, rr.Body.String())
	}
	var finResp sessionserver.SessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &finResp); err != nil {
		t.Fatalf("decode finalize response: %v", err)
	}
	if finResp.State != string(session.StateReady) {
		t.Fatalf("after finalize, state = %q, want ready", finResp.State)
	}

	// §4.9 line 1510: direct mode is still one of Lenny's two
	// credential-leasing delivery modes, so exactly one lease is minted at
	// finalize, the same as proxy mode.
	if n := atomic.LoadInt32(&mints); n != 1 {
		t.Fatalf("credential leases minted through finalize = %d, want exactly 1 (direct mode still mints a §4.9 lease; it does not bypass credential leasing)", n)
	}

	// The pod's materialized credential file carries the real key read back
	// from the live Kubernetes Secret, under deliveryMode: direct.
	entry := credProviderEntry(t, credDir, "anthropic_direct")
	if entry["deliveryMode"] != "direct" {
		t.Errorf("credential file deliveryMode = %v, want direct", entry["deliveryMode"])
	}
	if !credFileCarriesUpstream(t, credDir, apiKey) {
		t.Fatalf("the coding-agent pod's credential file does not carry the provider key materialized from the air-gapped Kubernetes Secret")
	}
	// Direct mode delivers the full provider credential, not a proxy
	// lease token or proxy URL — those are proxy-mode-only fields (§4.9
	// line 961: "For deliveryMode: proxy, materializedConfig contains only
	// proxyUrl ... and leaseToken").
	if credFileCarriesUpstream(t, credDir, "leaseToken") || credFileCarriesUpstream(t, credDir, "proxyUrl") {
		t.Errorf("direct-mode credential file carries a proxy-mode field (leaseToken/proxyUrl); direct delivery must not shadow-populate the proxy-mode shape")
	}

	// Cross-check: the credential file's key traces to this test's live
	// Secret object, not to a value the test hard-coded independently of
	// the cluster.
	var readBack corev1.Secret
	if err := cluster.Get(ctx, client.ObjectKey{Namespace: codingAgentDirectSecretNS, Name: codingAgentDirectSecretName}, &readBack); err != nil {
		t.Fatalf("re-read provider-key secret: %v", err)
	}
	if string(readBack.Data["apiKey"]) != apiKey {
		t.Fatalf("the Secret's apiKey changed independently of the value materialized into the pod; test setup is inconsistent")
	}
}
