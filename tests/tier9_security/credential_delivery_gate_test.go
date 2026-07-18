// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §4.9 session-start credential-delivery fail-closed probe. The two
// registration/admission layers inspect the warm-pool/SandboxTemplate
// deliveryMode, a denormalized copy that can diverge from the CredentialPool
// deliveryMode credential leasing resolves per session. When a resolved
// CredentialPool declares deliveryMode: proxy while the bound pod's
// SandboxTemplate disables SPIFFE binding, minting the lease exposes the
// gateway-side lease token to cross-pod replay, the exact cross-tenant risk
// §4.9 forbids in multi-tenant mode. This suite drives the gateway REST
// create → upload → finalize flow through sessionserver.New against a real
// kube-apiserver (envtest) with tenancy.mode: multi and asserts the finalize
// barrier rejects the session fail-closed (422, guard code
// ProxyModeSpiffeBindingDisabled) before the credential lease is minted, so no
// lease token reaches the SPIFFE-unbound pod. It exercises the security
// boundary an external client reaches, complementing the in-package seam cases
// in pkg/gateway/sessionserver.
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
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
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const deliveryGateNS = "lenny-agents"

// leaseCountingAssigner records every AssignProto so the probe can assert the
// gate rejects before any lease token is minted.
type leaseCountingAssigner struct {
	mu      sync.Mutex
	assigns int
}

func (a *leaseCountingAssigner) AssignProto(pool, _, _, _ string) (*adapterv1.CredentialLease, error) {
	a.mu.Lock()
	a.assigns++
	a.mu.Unlock()
	return &adapterv1.CredentialLease{
		LeaseId: "cl-" + pool, Provider: pool,
		Payload: []byte(`{"deliveryMode":"proxy","materializedConfig":{"proxyUrl":"https://p/v1","leaseToken":"lt"}}`),
	}, nil
}
func (a *leaseCountingAssigner) ReleaseSession(string) {}
func (a *leaseCountingAssigner) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.assigns
}

// deliveryGateDialer serves the adapter over an in-memory connection.
func deliveryGateDialer(t *testing.T, srv *adapter.Server) func(string) (*adapterclient.Client, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return func(string) (*adapterclient.Client, error) {
		return adapterclient.Dial("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

// deliveryGateCluster seeds a warm pool whose SandboxTemplate disables SPIFFE
// binding (a warm-pod property absent from the CredentialPool record) plus one
// idle Sandbox the create handler claims.
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
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: deliveryGateNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-pool", Namespace: deliveryGateNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "echo-tmpl", MinWarm: 1, MaxWarm: 5},
	}); err != nil {
		t.Fatalf("create warm pool: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-tmpl", Namespace: deliveryGateNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "echo",
			IsolationProfile: string(isolation.ProfileSandboxed),
			SpiffeBinding:    "disabled",
		},
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{
		Name: "sbx-1", Namespace: deliveryGateNS, Labels: map[string]string{warmpool.LabelPool: "echo-pool"},
	}}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName("sbx-1")
	u.SetNamespace(deliveryGateNS)
	_ = unstructured.SetNestedField(u.Object, map[string]interface{}{"phase": "idle", "podIP": "10.244.2.5"}, "status")
	if err := c.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed WPC sandbox status: %v", err)
	}
	return c
}

// spec: §4.9 — the session-start credential-delivery gate fails a multi-tenant
// session closed at the finalize lease-mint seam when the resolved
// CredentialPool deliveryMode + the bound pod's spiffeBinding is the forbidden
// cross-tenant combination, before the lease token reaches the pod.
// diagnosis: a 200 finalize (or a nonzero AssignProto count) means the gateway
// minted a proxy-mode lease token into a SPIFFE-unbound pod in multi-tenant
// mode, the cross-pod replay exposure the gate exists to prevent. A 422 with a
// code other than ProxyModeSpiffeBindingDisabled means the rejection came from a
// different control than the credential-delivery gate.
func TestCredentialDeliveryGateFailsClosedAtFinalize(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.StagingDir = t.TempDir()
	adapterSrv.CredentialsDir = t.TempDir()
	adapterSrv.Runtime = noopRuntime{}

	binder := &podsession.Binder{
		Client:           deliveryGateCluster(t),
		Namespace:        deliveryGateNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      deliveryGateDialer(t, adapterSrv),
	}
	blobs := blobstore.NewMemoryStore(nil)
	binder.Blobs = blobs
	assigner := &leaseCountingAssigner{}
	binder.Credentials = assigner

	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{
		ID: "acme",
		CredentialPolicy: credential.CredentialPolicy{
			PreferredSource: credential.PreferredSourcePool,
			ProviderPools:   map[string]credential.ProviderPool{"anthropic_direct": {DefaultPool: "claude-prod"}},
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
		TenantID: "acme", Name: "claude-prod", Provider: "anthropic_direct",
		DeliveryMode: "proxy", MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{{ID: "c-a", SecretRef: "s", Status: credentialpoolstore.CredentialActive}},
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return "sess-gate" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          deliveryGateNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
		Blobs:                   blobs,
		TenancyMode:             direct_mode_isolation.TenancyMulti,
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
		t.Fatalf("decode upload: %v", err)
	}
	finalizeBody, _ := json.Marshal(map[string]any{
		"workspacePlan": map[string]any{
			"schemaVersion": 1,
			"sources":       []map[string]any{{"type": "uploadFile", "path": "CLAUDE.md", "uploadRef": up.UploadRef}},
		},
	})
	rr = post("/v1/sessions/sess-gate/finalize", finalizeBody, map[string]string{"Content-Type": "application/json"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("finalize: status %d, want 422 fail-closed; body=%s", rr.Code, rr.Body.String())
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
	if n := assigner.count(); n != 0 {
		t.Errorf("AssignProto called %d times, want 0 (no lease token reaches the SPIFFE-unbound pod)", n)
	}
}
