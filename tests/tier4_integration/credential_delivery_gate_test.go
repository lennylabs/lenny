// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §4.9 session-start credential-delivery
// gate. The registration and admission layers inspect the
// warm-pool/SandboxTemplate deliveryMode, which is a denormalized copy that
// can diverge from the CredentialPool deliveryMode credential leasing
// actually resolves per session. This test drives the fully wired
// create → upload → finalize lifecycle through an in-process gateway against
// a real kube-apiserver (envtest) with a multi-tenant deployment whose
// resolved CredentialPool declares deliveryMode: proxy while the bound pod's
// SandboxTemplate carries spiffeBinding: disabled. The finalize barrier must
// reject the session with 422 carrying the guard's
// ProxyModeSpiffeBindingDisabled code before the credential lease is minted,
// so the recording assigner observes no AssignProto call.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/admission/direct_mode_isolation"
	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// deliveryGateCluster seeds an envtest cluster with a warm pool whose
// SandboxTemplate carries spiffeBinding: disabled (a warm-pod property absent
// from the CredentialPool record) plus one idle Sandbox the create handler
// claims. The isolation profile stays sandboxed so create and pool resolution
// succeed; the forbidden pairing is proxy delivery against the disabled SPIFFE
// binding, which does not require standard isolation.
func deliveryGateCluster(t *testing.T) client.Client {
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
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: eagerNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-pool", Namespace: eagerNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "echo-tmpl", MinWarm: 1, MaxWarm: 5},
	}); err != nil {
		t.Fatalf("create warm pool: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-tmpl", Namespace: eagerNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "echo",
			IsolationProfile: string(isolation.ProfileSandboxed),
			SpiffeBinding:    "disabled",
		},
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sbx-1", Namespace: eagerNS,
			Labels: map[string]string{warmpool.LabelPool: "echo-pool"},
		},
	}
	if err := c.Create(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName("sbx-1")
	u.SetNamespace(eagerNS)
	_ = unstructured.SetNestedField(u.Object, map[string]interface{}{"phase": "idle", "podIP": "10.244.2.5"}, "status")
	if err := c.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed WPC sandbox status: %v", err)
	}
	return c
}

// spec: §4.9 — the session-start credential-delivery gate rejects a forbidden
// pairing of the effective CredentialPool deliveryMode and the bound pod's
// spiffeBinding at the finalize lease-mint seam in multi-tenant mode.
// diagnosis: a failure means the finalize barrier minted a credential lease for
// a session whose resolved CredentialPool declares deliveryMode: proxy against a
// pod whose SandboxTemplate disables SPIFFE binding, the cross-pod lease-token
// replay exposure §4.9 forbids in multi-tenant mode. If finalize returns 200 and
// the assigner records an AssignProto call, the gate did not run at the finalize
// seam and the lease reached the pod before the delivery-mode check; if the code
// is 422 but not the guard's ProxyModeSpiffeBindingDisabled code, the gate
// rejected for the wrong reason.
func TestCredentialDeliveryGateRejectsProxySpiffeDisabledAtFinalize(t *testing.T) {
	rt := &eagerRuntime{}
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.StagingDir = t.TempDir()
	adapterSrv.CredentialsDir = t.TempDir()
	adapterSrv.Runtime = rt

	cluster := deliveryGateCluster(t)
	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        eagerNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      eagerAdapterDialer(t, adapterSrv),
	}
	blobs := blobstore.NewMemoryStore(nil)
	binder.Blobs = blobs
	assigner := &recordingAssigner{}
	binder.Credentials = assigner

	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{
		ID: "acme",
		CredentialPolicy: credential.CredentialPolicy{
			PreferredSource: credential.PreferredSourcePool,
			ProviderPools: map[string]credential.ProviderPool{
				"anthropic_direct": {DefaultPool: "claude-prod"},
			},
		},
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "echo", SupportedProviders: []string{"anthropic_direct"}}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	credPools := credentialpoolstore.NewMemory()
	if err := credPools.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID:              "acme",
		Name:                  "claude-prod",
		Provider:              "anthropic_direct",
		DeliveryMode:          "proxy",
		MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{
			{ID: "claude-prod-cred-a", SecretRef: "secret-claude-prod", Status: credentialpoolstore.CredentialActive},
		},
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return "sess-gate" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          eagerNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
		Blobs:                   blobs,
		// The multi-tenant signal the gate keys off; the same value the
		// layer-1 registration check and the layer-2 webhook read.
		TenancyMode: direct_mode_isolation.TenancyMulti,
	})
	h := srv.Handler()

	post := func(path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	if rr := post("/v1/sessions", createBody, nil); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}

	rr := post("/v1/sessions/sess-gate/upload", []byte("# uploaded"), map[string]string{"Content-Type": "text/markdown"})
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
	rr = post("/v1/sessions/sess-gate/finalize", finalizeBody, map[string]string{"Content-Type": "application/json"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("finalize: status %d, want 422 (the credential-delivery gate rejects proxy + spiffeBinding disabled in multi-tenant mode); body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode finalize error: %v", err)
	}
	if env.Error.Code != direct_mode_isolation.RejectProxyModeSpiffeBindingDisabled {
		t.Errorf("finalize error code = %q, want %q", env.Error.Code, direct_mode_isolation.RejectProxyModeSpiffeBindingDisabled)
	}
	// The lease must not have been minted: the gate runs before the prepare
	// barrier assigns credentials.
	if n := assigner.assignCount(); n != 0 {
		t.Errorf("AssignProto called %d times, want 0 (the gate rejects before the lease is minted)", n)
	}
}
