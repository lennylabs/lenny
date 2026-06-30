// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for proposal 0007 (eager pod claim at create,
// finalize-time workspace materialization and credential-lease assignment).
// It drives the §7.1 / §15.1 decomposed create → upload → finalize → start
// lifecycle through a fully wired in-process gateway Server against a real
// kube-apiserver (envtest), a real Artifact-Store blob store, a real adapter
// over an in-memory gRPC connection, and a recording §4.9 credential
// assigner. The flow asserts the proposal's three load-bearing claims that no
// single-component test exercises together:
//
//   - Uploads buffer in the Artifact Store during the created window; the
//     workspace is NOT streamed into the claimed pod until finalize.
//   - /finalize is the preparation barrier: it streams the buffered blob into
//     /workspace/current AND assigns the credential lease, returning ready.
//   - /start launches only: it assigns no lease and runs no materialization.
//
// This crosses the gateway, the datastore (the per-pod SandboxClaim on the
// apiserver), the blob store, and the pod adapter, which is the multi-service
// surface the integration tier owns.

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	"github.com/lennylabs/lenny/pkg/admission/ownership"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const eagerNS = "lenny-agents"

// eagerRuntime satisfies adapter.RuntimeProcess for the bufconn adapter the
// start path drives through StartSession. started records the launched session
// so the test can assert the runtime started only at /start.
type eagerRuntime struct {
	mu      sync.Mutex
	started string
}

func (r *eagerRuntime) Start(_ context.Context, sessionID string) error {
	r.mu.Lock()
	r.started = sessionID
	r.mu.Unlock()
	return nil
}
func (r *eagerRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (r *eagerRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *eagerRuntime) Close(context.Context, string) error           { return nil }
func (r *eagerRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *eagerRuntime) startedSession() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started
}

// recordingAssigner is a podsession.CredentialAssigner that records every
// AssignProto and ReleaseSession call so the flow can assert the §4.9 lease is
// assigned exactly once, at /finalize. It returns a fixed proxy-mode lease.
type recordingAssigner struct {
	mu       sync.Mutex
	assigns  []string // session ids passed to AssignProto
	released []string // session ids passed to ReleaseSession
}

func (a *recordingAssigner) AssignProto(pool, sessionID, _, _ string) (*adapterv1.CredentialLease, error) {
	a.mu.Lock()
	a.assigns = append(a.assigns, sessionID)
	a.mu.Unlock()
	return &adapterv1.CredentialLease{
		LeaseId:  "cl-" + pool,
		Provider: pool,
		Payload: []byte(`{"deliveryMode":"proxy",` +
			`"materializedConfig":{"proxyUrl":"https://p/v1","leaseToken":"lt-` + pool + `"}}`),
	}, nil
}

func (a *recordingAssigner) ReleaseSession(sessionID string) {
	a.mu.Lock()
	a.released = append(a.released, sessionID)
	a.mu.Unlock()
}

func (a *recordingAssigner) assignCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.assigns)
}

// eagerCluster returns an envtest-backed cluster seeded with a warm pool, its
// template, and one idle Sandbox the create handler can claim. The seeded
// Sandbox.status is split by §4.6.3 field ownership: the WarmPoolController
// seeds phase/podIP under its field manager, matching the production Apply
// path the fake client cannot exercise (no SSA).
func eagerCluster(t *testing.T) client.Client {
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
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "echo", IsolationProfile: string(isolation.ProfileSandboxed)},
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

// eagerAdapterDialer serves srv over an in-memory connection.
func eagerAdapterDialer(t *testing.T, srv *adapter.Server) func(string) (*adapterclient.Client, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return func(string) (*adapterclient.Client, error) {
		return adapterclient.Dial("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

// spec: 7.1, 7.4, 15.1 (decomposed create → upload → finalize → start; upload
// buffers in the Artifact Store; finalize materializes /workspace/current and
// assigns the lease; start launches only)
// diagnosis: a failure means the end-to-end eager-claim lifecycle diverges
// from the proposal across components. If the uploaded file appears in the pod
// workspace before finalize, the gateway streamed bytes into the pod during
// the upload window instead of buffering them in the Artifact Store. If the
// file is absent after finalize, the finalize barrier did not stream the
// buffered blob into /workspace/current. If the lease is assigned at create or
// start rather than once at finalize, the §4.9 lease-assignment step did not
// move to the finalize barrier. If the runtime starts before /start, the
// launch leaked into an earlier phase.
func TestEagerClaimLifecycleCreateUploadFinalizeStart(t *testing.T) {
	rt := &eagerRuntime{}
	wsRoot := t.TempDir()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = wsRoot
	adapterSrv.StagingDir = t.TempDir()
	adapterSrv.CredentialsDir = t.TempDir()
	adapterSrv.Runtime = rt

	cluster := eagerCluster(t)
	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        eagerNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      eagerAdapterDialer(t, adapterSrv),
	}
	// The blob store is the Artifact Store uploads buffer in; the binder
	// resolves the buffered lenny-blob:// ref at finalize so PrepareWorkspace
	// streams the content into the pod's /workspace/staging.
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
		MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{
			{ID: "claude-prod-cred-a", SecretRef: "secret-claude-prod", Status: credentialpoolstore.CredentialActive},
		},
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return "sess-eager" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          eagerNS,
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
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// 1. Create: claims the pod synchronously (§7.1 step 4). No workspace
	// content, no lease yet.
	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	rr := post("/v1/sessions", createBody, nil)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	// The pod was claimed at create: the per-pod SandboxClaim exists.
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(ctx, client.ObjectKey{Namespace: eagerNS, Name: "claim-sbx-1"}, &claim); err != nil {
		t.Fatalf("per-pod claim missing after create (the pod was not claimed at /create): %v", err)
	}
	// No lease is assigned at create: the lease assignment is the finalize step.
	if n := assigner.assignCount(); n != 0 {
		t.Fatalf("AssignProto called %d times at create, want 0 (the lease is assigned at finalize)", n)
	}

	// 2. Upload: the bytes buffer in the Artifact Store; nothing is streamed
	// into the pod. The handler returns a lenny-blob:// ref.
	rr = post("/v1/sessions/sess-eager/upload", []byte("# uploaded via artifact store"), map[string]string{
		"Content-Type": "text/markdown",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var up sessionserver.UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &up); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if up.UploadRef == "" || !bytes.HasPrefix([]byte(up.UploadRef), []byte("lenny-blob://")) {
		t.Fatalf("upload ref = %q, want a lenny-blob:// Artifact-Store reference", up.UploadRef)
	}
	// The uploaded file must NOT be in the pod workspace yet: the upload window
	// buffers in the store and performs no pod I/O (§7.4 store-mediated staging).
	if _, err := os.Stat(filepath.Join(wsRoot, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("CLAUDE.md present in the pod workspace before finalize (err=%v); uploads must buffer in the Artifact Store, not stream into the pod", err)
	}

	// 3. Finalize: the preparation barrier streams the buffered blob into
	// /workspace/current, runs setup, and assigns the credential lease; returns
	// ready.
	finalizeBody, _ := json.Marshal(map[string]any{
		"workspacePlan": map[string]any{
			"schemaVersion": 1,
			"sources": []map[string]any{
				{"type": "uploadFile", "path": "CLAUDE.md", "uploadRef": up.UploadRef},
			},
		},
	})
	rr = post("/v1/sessions/sess-eager/finalize", finalizeBody, map[string]string{"Content-Type": "application/json"})
	if rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var finResp sessionserver.SessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &finResp)
	if finResp.State != string(session.StateReady) {
		t.Fatalf("after finalize, state = %q, want ready (finalize is the barrier and returns ready)", finResp.State)
	}
	// The buffered blob was streamed into /workspace/current at finalize.
	got, err := os.ReadFile(filepath.Join(wsRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("the buffered upload was not materialized into the pod workspace at finalize: %v", err)
	}
	if string(got) != "# uploaded via artifact store" {
		t.Errorf("materialized file = %q, want the buffered upload content", got)
	}
	// The credential lease was assigned exactly once, at finalize.
	if n := assigner.assignCount(); n != 1 {
		t.Fatalf("AssignProto called %d times through finalize, want exactly 1 (the lease is assigned at finalize)", n)
	}
	// The runtime has not started yet: /finalize prepares, it does not launch.
	if got := rt.startedSession(); got != "" {
		t.Fatalf("runtime started %q at finalize, want no start (launch is the /start step)", got)
	}

	// 4. Start: launch only. The runtime starts; no new lease is assigned.
	rr = post("/v1/sessions/sess-eager/start", nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("start: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var startResp sessionserver.SessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &startResp)
	if startResp.State != string(session.StateRunning) {
		t.Errorf("after start, state = %q, want running", startResp.State)
	}
	if got := rt.startedSession(); got != "sess-eager" {
		t.Errorf("runtime started for %q at /start, want sess-eager", got)
	}
	// /start is launch-only: no additional lease assignment beyond finalize's.
	if n := assigner.assignCount(); n != 1 {
		t.Errorf("AssignProto called %d times total, want 1; /start must assign no lease", n)
	}
}
