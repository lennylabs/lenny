// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 uploadRef session-scope probe. An uploadRef is scoped to the
// session for which its content was stored, and the gateway checks that
// scope on every client-submitted plan before anything reads the blob. This
// suite drives the REST create → upload → finalize flow through
// sessionserver.New against a real kube-apiserver (envtest) and a pod binder
// whose blob store counts every Get, and asserts that a plan naming another
// session's upload is rejected at creation and at finalize without the
// gateway fetching that session's content.
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
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
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

const uploadScopeNS = "lenny-agents"

// getCountingBlobStore wraps a blobstore.Store and counts Get calls per
// session segment, so the probe can assert that no client plan reached the
// binder's blob fetch with another session's content.
type getCountingBlobStore struct {
	blobstore.Store

	mu   sync.Mutex
	gets map[string]int
}

func (s *getCountingBlobStore) Get(u blobstore.URI) (blobstore.BlobInfo, io.ReadCloser, error) {
	s.mu.Lock()
	s.gets[u.SessionID]++
	s.mu.Unlock()
	return s.Store.Get(u)
}

// getsFor returns the number of Get calls that named sessionID's content.
func (s *getCountingBlobStore) getsFor(sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets[sessionID]
}

// uploadScopeCluster seeds a warm pool, its template, and n idle Sandboxes
// for the create-time pod claims.
func uploadScopeCluster(t *testing.T, n int) client.Client {
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
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: uploadScopeNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-pool", Namespace: uploadScopeNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "echo-tmpl", MinWarm: 1, MaxWarm: 5},
	}); err != nil {
		t.Fatalf("create warm pool: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-tmpl", Namespace: uploadScopeNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "echo", IsolationProfile: string(isolation.ProfileSandboxed)},
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	for i := 1; i <= n; i++ {
		seedUploadScopeSandbox(t, c, fmt.Sprintf("sbx-%d", i), fmt.Sprintf("10.244.3.%d", i))
	}
	return c
}

// seedUploadScopeSandbox creates one idle Sandbox, writing its status under
// the warm-pool controller's field manager as the controller does.
func seedUploadScopeSandbox(t *testing.T, c client.Client, name, podIP string) {
	t.Helper()
	ctx := context.Background()
	if err := c.Create(ctx, &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: uploadScopeNS, Labels: map[string]string{warmpool.LabelPool: "echo-pool"},
	}}); err != nil {
		t.Fatalf("create sandbox %s: %v", name, err)
	}
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName(name)
	u.SetNamespace(uploadScopeNS)
	_ = unstructured.SetNestedField(u.Object, map[string]interface{}{"phase": "idle", "podIP": podIP}, "status")
	if err := c.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed WPC sandbox status %s: %v", name, err)
	}
}

// uploadScopeGateway is the probe's gateway handler and its counting blob
// store.
type uploadScopeGateway struct {
	h     http.Handler
	blobs *getCountingBlobStore
}

// newUploadScopeGateway builds a session server over an envtest cluster of
// pods idle Sandboxes, with a pod binder whose blob store counts Get calls.
func newUploadScopeGateway(t *testing.T, pods int) *uploadScopeGateway {
	t.Helper()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceBase = t.TempDir()
	adapterSrv.Runtime = noopRuntime{}
	blobs := &getCountingBlobStore{Store: blobstore.NewMemoryStore(nil), gets: map[string]int{}}
	binder := &podsession.Binder{
		Client:           uploadScopeCluster(t, pods),
		Namespace:        uploadScopeNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      deliveryGateDialer(t, adapterSrv),
		Blobs:            blobs,
	}
	var next atomic.Int32
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader:         true,
		IDFunc:                  func() string { return fmt.Sprintf("sess-scope-%d", next.Add(1)) },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          uploadScopeNS,
		Blobs:                   blobs,
	})
	return &uploadScopeGateway{h: ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice), blobs: blobs}
}

// post issues a request for tenant acme and returns the recorder.
func (g *uploadScopeGateway) post(path, contentType string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rr := httptest.NewRecorder()
	g.h.ServeHTTP(rr, req)
	return rr
}

// create creates a session for alice with the optional plan and returns
// the recorder and the minted session ID, empty when the create failed.
func (g *uploadScopeGateway) create(t *testing.T, plan map[string]any) (*httptest.ResponseRecorder, string) {
	t.Helper()
	body := map[string]any{"runtimeRef": "echo", "userId": "alice@acme.com"}
	if plan != nil {
		body["workspacePlan"] = plan
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal create body: %v", err)
	}
	rr := g.post("/v1/sessions", "application/json", raw)
	var created struct {
		ID string `json:"id"`
	}
	if rr.Code == http.StatusCreated {
		if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode create response: %v (body=%s)", err, rr.Body.String())
		}
	}
	return rr, created.ID
}

// uploadFilePlan returns a plan with one uploadFile source naming ref.
func uploadFilePlan(ref string) map[string]any {
	return map[string]any{
		"schemaVersion": 1,
		"sources":       []map[string]any{{"type": "uploadFile", "path": "CLAUDE.md", "uploadRef": ref}},
	}
}

// assertForeignSessionRejection fails unless rr is the 400 VALIDATION_ERROR
// with reason upload_ref_foreign_session.
func assertForeignSessionRejection(t *testing.T, step string, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("%s: status %d, want 400; body=%s", step, rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s: decode error: %v", step, err)
	}
	if env.Error.Code != "VALIDATION_ERROR" || env.Error.Details["reason"] != "upload_ref_foreign_session" {
		t.Errorf("%s: error = %s %v, want VALIDATION_ERROR upload_ref_foreign_session", step, env.Error.Code, env.Error.Details)
	}
}

// spec: 14 (Workspace Plan Schema), 14.1 (WorkspacePlan Schema Versioning), 13.4
// diagnosis: a nonzero Get count for session A means a client plan reached
// the binder with another session's content: the uploadRef scope check ran
// after the blob fetch, or not at all, on a create or finalize bind. A
// non-400 answer means a plan naming another session's upload was bound. A
// zero count on the closing own-session finalize means the counting store
// is not in the binder's fetch path, so the zero counts above prove
// nothing.
func TestPlanCannotFetchAnotherSessionsUpload(t *testing.T) {
	g := newUploadScopeGateway(t, 3)

	rr, sessA := g.create(t, nil)
	if sessA == "" {
		t.Fatalf("create A: status %d, body=%s", rr.Code, rr.Body.String())
	}
	rr = g.post("/v1/sessions/"+sessA+"/upload", "text/markdown", []byte("# session A content"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload to A: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var up sessionserver.UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &up); err != nil || up.UploadRef == "" {
		t.Fatalf("decode upload response: %v (body=%s)", err, rr.Body.String())
	}

	// Session B names A's upload in its create-time plan.
	rr, _ = g.create(t, uploadFilePlan(up.UploadRef))
	assertForeignSessionRejection(t, "create B", rr)

	// Session C is created without a plan and names A's upload at finalize.
	rr, sessC := g.create(t, nil)
	if sessC == "" {
		t.Fatalf("create C: status %d, body=%s", rr.Code, rr.Body.String())
	}
	finalizeBody, err := json.Marshal(map[string]any{"workspacePlan": uploadFilePlan(up.UploadRef)})
	if err != nil {
		t.Fatalf("marshal finalize body: %v", err)
	}
	assertForeignSessionRejection(t, "finalize C", g.post("/v1/sessions/"+sessC+"/finalize", "application/json", finalizeBody))

	if n := g.blobs.getsFor(sessA); n != 0 {
		t.Fatalf("blob store recorded %d Get calls for session A's content, want 0", n)
	}

	// Positive control: A's own finalize fetches A's upload through the same
	// counting store, which proves the store sits in the binder's fetch path.
	rr = g.post("/v1/sessions/"+sessA+"/finalize", "application/json", finalizeBody)
	if n := g.blobs.getsFor(sessA); n == 0 {
		t.Errorf("own-session finalize recorded no Get for A's content (status %d, body=%s); the counting store is not in the fetch path",
			rr.Code, rr.Body.String())
	}
}
