// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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
	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/admission/ownership"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// spec: §15.1 — POST /v1/sessions/start places the session on a §5 warm
// pod when the gateway is wired with a §4.7 pod binder.

const podTestNS = "lenny-agents"

// podBindRuntime satisfies adapter.RuntimeProcess for the bufconn
// adapter the start path drives through StartSession.
type podBindRuntime struct{ started string }

func (r *podBindRuntime) Start(_ context.Context, sessionID string) error {
	r.started = sessionID
	return nil
}
func (r *podBindRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (r *podBindRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *podBindRuntime) Close(context.Context, string) error           { return nil }
func (r *podBindRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func podBindScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

// podBindClient returns a cluster holding a warm pool, its template, and
// one idle Sandbox the start path can claim. Backed by envtest so the
// §4.6.3 SSA Apply path the session-mode claimer + slot claimer use is
// real; the fake client does not implement SSA (kubernetes/kubernetes#115598).
// spec: §4.6.3 ownership table.
func podBindClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return podBindEnvtestClient(t, objs...)
}

// podBindEnvtestClient is podBindClient's envtest sibling, required by
// the §5.2 slot-claim path: SlotClaimer uses SSA Apply per §4.6.3, and
// the controller-runtime fake client does not implement SSA
// (kubernetes/kubernetes#115598). The seeded Sandbox.Status is split
// by §4.6.3 field ownership: WPC seeds phase/podIP, the gateway seeds
// activeSlots/tenantId. Keeping the seed under the rightful manager
// avoids cross-manager conflicts on the live reconcile.
func podBindEnvtestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	env := envtest.Start(t)
	s := podBindScheme(t)
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: s})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: podTestNS},
	}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", podTestNS, err)
	}
	for _, o := range objs {
		var (
			sbStatus  lennyv1.SandboxStatus
			seedAfter func()
		)
		if sb, ok := o.(*lennyv1.Sandbox); ok {
			sbStatus = sb.Status
			sb.Status = lennyv1.SandboxStatus{}
			seedAfter = func() {
				wpc := map[string]interface{}{}
				if sbStatus.Phase != "" {
					wpc["phase"] = sbStatus.Phase
				}
				if sbStatus.PodIP != "" {
					wpc["podIP"] = sbStatus.PodIP
				}
				if len(wpc) > 0 {
					u := &unstructured.Unstructured{}
					u.SetAPIVersion(lennyv1.GroupVersion.String())
					u.SetKind("Sandbox")
					u.SetName(sb.Name)
					u.SetNamespace(sb.Namespace)
					_ = unstructured.SetNestedField(u.Object, wpc, "status")
					if err := c.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
						t.Fatalf("seed WPC status Sandbox %s: %v", sb.Name, err)
					}
				}
			}
		}
		if err := c.Create(ctx, o); err != nil {
			t.Fatalf("create %T %s: %v", o, o.GetName(), err)
		}
		if seedAfter != nil {
			seedAfter()
		}
	}
	return c
}

func podBindWarmPool(name, templateRef string) *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: podTestNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: templateRef, MinWarm: 1, MaxWarm: 5},
	}
}

func podBindTemplate(name, runtimeRef, isolationProfile string) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: podTestNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: runtimeRef, IsolationProfile: isolationProfile},
	}
}

func podBindIdleSandbox(name, pool, podIP string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: podTestNS,
			Labels:    map[string]string{warmpool.LabelPool: pool},
		},
		Status: lennyv1.SandboxStatus{Phase: "idle", PodIP: podIP},
	}
}

// podBindAdapterDialer serves srv over an in-memory connection and
// returns a DialAdapter func wired to it.
func podBindAdapterDialer(t *testing.T, srv *adapter.Server) func(string) (*adapterclient.Client, error) {
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

func podBindBinder(c client.Client, dial func(string) (*adapterclient.Client, error)) *podsession.Binder {
	return &podsession.Binder{
		Client:           c,
		Namespace:        podTestNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      dial,
	}
}

func TestSessionStartPlacesSessionOnWarmPod(t *testing.T) {
	rt := &podBindRuntime{}
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = rt

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-pod-1" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	binding, ok := registry.Get("sess-pod-1")
	if !ok {
		t.Fatal("registry holds no binding for the started session")
	}
	if binding.SandboxName != "sbx-1" || binding.PodIP != "10.244.2.5" {
		t.Errorf("binding = %+v, want sbx-1 / 10.244.2.5", binding)
	}
	if rt.started != "sess-pod-1" {
		t.Errorf("adapter runtime started for %q, want sess-pod-1", rt.started)
	}

	// spec: §4.6.3 — a successful session-mode start records the acquisition
	// on the per-pod claim's `bound` binding state; the gateway no longer
	// writes Sandbox.status, and the WPC projects the coarse `claimed` phase.
	// The session reaching `running` is a session-model state on the Postgres
	// session row, not a CRD phase.
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err != nil {
		t.Fatalf("get per-pod claim: %v", err)
	}
	if claim.Status.Phase != string(claimstate.Bound) {
		t.Errorf("claim binding state = %q, want bound", claim.Status.Phase)
	}
}

// spec: §7.1 line 28 — the §7.1 atomicity contract demands "does NOT
// persist the session row" when the create-and-start atomic unit
// (steps 2-8) fails. A claim failure on the §15.1 `POST
// /v1/sessions/start` path returns SESSION_CREATION_FAILED with no
// session row left behind; the registry stays empty too.
func TestSessionStartLeavesNoRowOnClaimFailure_spec_7_1_4(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	// The cluster serves only the "echo" runtime; the request asks for a
	// runtime no warm pool covers.
	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-pod-nomatch" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "no-such-runtime"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if registry.Len() != 0 {
		t.Errorf("registry holds %d bindings, want 0 after a failed claim", registry.Len())
	}

	if _, err := store.Get(context.Background(), "acme", "sess-pod-nomatch"); err == nil {
		t.Fatalf("session row was persisted; §7.1 atomicity requires no row when the create-and-start atomic unit fails")
	}
	// §15.1 line 1138 — every retryable 503 carries Retry-After so a
	// client retries with a deterministic budget.
	if ra := rr.Header().Get("Retry-After"); ra == "" {
		t.Errorf("Retry-After header missing on the SESSION_CREATION_FAILED reply")
	}
}

// podBindServer builds a sessionserver wired to a warm pool, its
// template, and an idle Sandbox, returning the server, the registry,
// the cluster client, and the adapter's workspace root.
func podBindServer(t *testing.T, id string) (*sessionserver.Server, *podsession.Registry, client.Client, string) {
	t.Helper()
	wsRoot := t.TempDir()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = wsRoot
	adapterSrv.Runtime = &podBindRuntime{}

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return id },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})
	return srv, registry, cluster, wsRoot
}

// postSessionStep issues one §15.1 lifecycle POST and returns the
// recorder.
func postSessionStep(t *testing.T, h http.Handler, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(http.MethodPost, path, reader)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// spec: §15.1 — the two-step create → finalize → start lifecycle places
// the session on a warm pod at start, using the §14 WorkspacePlan
// stored on the row at create.

func TestTwoStepStartPlacesSessionOnWarmPod(t *testing.T) {
	srv, registry, cluster, wsRoot := podBindServer(t, "sess-2step-1")
	h := srv.Handler()

	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{
		RuntimeRef: "echo",
		UserID:     "alice@acme.com",
		WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"sources": [{"type":"inlineFile","path":"CLAUDE.md","content":"# stored plan","mode":"0644"}]
		}`),
	})
	if rr := postSessionStep(t, h, "/v1/sessions", createBody); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	if rr := postSessionStep(t, h, "/v1/sessions/sess-2step-1/finalize", nil); rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body=%s", rr.Code, rr.Body.String())
	}
	rr := postSessionStep(t, h, "/v1/sessions/sess-2step-1/start", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("start: status %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp sessionserver.SessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.State != string(session.StateRunning) {
		t.Errorf("state = %q, want running", resp.State)
	}
	if _, ok := registry.Get("sess-2step-1"); !ok {
		t.Error("registry holds no binding after the two-step start")
	}

	// spec: §4.6.3 — the two-step start records the acquisition on the
	// per-pod claim's `bound` binding state (the gateway no longer writes
	// Sandbox.status; the session's running state lives on the Postgres
	// session row).
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err != nil {
		t.Fatalf("get per-pod claim: %v", err)
	}
	if claim.Status.Phase != string(claimstate.Bound) {
		t.Errorf("claim binding state = %q, want bound", claim.Status.Phase)
	}

	// The plan stored at create was re-parsed at start and materialized
	// onto the pod's adapter workspace.
	got, err := os.ReadFile(filepath.Join(wsRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("stored workspace plan was not materialized: %v", err)
	}
	if string(got) != "# stored plan" {
		t.Errorf("materialized file = %q, want %q", got, "# stored plan")
	}
}

func TestTwoStepStartRejectsNonReadySession(t *testing.T) {
	srv, registry, _, _ := podBindServer(t, "sess-2step-early")
	h := srv.Handler()

	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo"})
	if rr := postSessionStep(t, h, "/v1/sessions", createBody); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}

	// /start before /finalize: the row is still `created`, not `ready`.
	rr := postSessionStep(t, h, "/v1/sessions/sess-2step-early/start", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("start before finalize: status %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if registry.Len() != 0 {
		t.Errorf("registry holds %d bindings, want 0 — no pod claimed on a rejected start", registry.Len())
	}
}

// spec: §15.1 / §7.1 — POST /v1/sessions/{id}/resume is valid only
// from `awaiting_client_action`; it restores the session onto a fresh
// §5 warm pod from its §7.1 WorkspaceSnapshot and reports the
// transition as `resume_pending` → `running`.

// resumeCheckpointSource is an adapter.CheckpointSource serving a fixed
// gzip-tar workspace archive for the resume path's restore step.
type resumeCheckpointSource struct{ archive []byte }

func (s resumeCheckpointSource) LoadCheckpoint(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.archive)), nil
}

// emptyResumeArchive returns a gzip-tar archive of an empty workspace,
// the minimal valid input the adapter's Resume RPC can restore.
func emptyResumeArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := workspace.Archive(t.TempDir(), &buf); err != nil {
		t.Fatalf("archive empty workspace: %v", err)
	}
	return buf.Bytes()
}

// podResumeServer builds a sessionserver wired to a warm pool whose
// §4.7 adapter carries a checkpoint source, so the §7.1 resume path
// can restore a session onto a fresh pod. It returns the server, the
// session store for seeding `awaiting_client_action` rows, the pod
// registry, and the cluster client.
func podResumeServer(t *testing.T, id string) (*sessionserver.Server, *memstore.Store, *podsession.Registry, client.Client) {
	t.Helper()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}
	adapterSrv.Restorer = resumeCheckpointSource{archive: emptyResumeArchive(t)}

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return id },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})
	return srv, store, registry, cluster
}

// seedAwaitingSession inserts a session row already in
// `awaiting_client_action` — the only state POST /resume accepts.
func seedAwaitingSession(t *testing.T, store *memstore.Store, row sessionstore.Session) {
	t.Helper()
	row.TenantID = "acme"
	row.State = session.StateAwaitingClientAction
	if row.RuntimeRef == "" {
		row.RuntimeRef = "echo"
	}
	if row.IsolationProfile == "" {
		row.IsolationProfile = isolation.ProfileSandboxed
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed awaiting session %s: %v", row.ID, err)
	}
}

func TestResumePlacesAwaitingSessionOnFreshPod(t *testing.T) {
	srv, store, registry, cluster := podResumeServer(t, "sess-resume-1")
	seedAwaitingSession(t, store, sessionstore.Session{
		ID: "sess-resume-1",
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref:    "ckpt-1",
			Source: sessionstore.WorkspaceSnapshotCheckpoint,
		},
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/sess-resume-1/resume", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	row, err := store.Get(context.Background(), "acme", "sess-resume-1")
	if err != nil {
		t.Fatalf("get resumed session: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("state = %q, want running after resume", row.State)
	}

	binding, ok := registry.Get("sess-resume-1")
	if !ok {
		t.Fatal("registry holds no binding for the resumed session")
	}
	if binding.SandboxName != "sbx-1" || binding.PodIP != "10.244.2.5" {
		t.Errorf("binding = %+v, want sbx-1 / 10.244.2.5", binding)
	}

	// The gateway no longer writes Sandbox.status; the resumed pod's
	// acquisition is recorded on the per-pod claim's `bound` binding state.
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err != nil {
		t.Fatalf("get per-pod claim: %v", err)
	}
	if claim.Status.Phase != string(claimstate.Bound) {
		t.Errorf("claim binding state = %q, want bound", claim.Status.Phase)
	}
}

func TestResumeRebuildsSessionWithoutSnapshotFromPlan(t *testing.T) {
	srv, store, registry, _ := podResumeServer(t, "sess-resume-nockpt")
	// No WorkspaceSnapshot: the session never checkpointed. The resume
	// path rebuilds it from the §14 WorkspacePlan recorded at create.
	seedAwaitingSession(t, store, sessionstore.Session{
		ID: "sess-resume-nockpt",
		WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"sources": [{"type":"inlineFile","path":"CLAUDE.md","content":"# resumed","mode":"0644"}]
		}`),
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/sess-resume-nockpt/resume", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	row, err := store.Get(context.Background(), "acme", "sess-resume-nockpt")
	if err != nil {
		t.Fatalf("get resumed session: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("state = %q, want running after resume", row.State)
	}
	if _, ok := registry.Get("sess-resume-nockpt"); !ok {
		t.Error("registry holds no binding after a snapshotless resume")
	}
}

func TestResumeRejectsNonResumableState(t *testing.T) {
	srv, store, registry, _ := podResumeServer(t, "sess-resume-bad")
	// A `running` session is not a valid POST /resume precondition —
	// only `awaiting_client_action` is (§15.1).
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:               "sess-resume-bad",
		TenantID:         "acme",
		State:            session.StateRunning,
		RuntimeRef:       "echo",
		IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/sess-resume-bad/resume", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("resume of a running session: status %d, want 409; body=%s", rr.Code, rr.Body.String())
	}

	row, err := store.Get(context.Background(), "acme", "sess-resume-bad")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("state = %q, want running unchanged after a rejected resume", row.State)
	}
	if registry.Len() != 0 {
		t.Errorf("registry holds %d bindings, want 0 — no pod claimed on a rejected resume", registry.Len())
	}
}

func TestResumeFailsSessionWhenNoPoolMatches(t *testing.T) {
	srv, store, registry, _ := podResumeServer(t, "sess-resume-nopool")
	// The cluster serves only the "echo" runtime; the session targets a
	// runtime no warm pool covers, so the pod claim cannot resolve.
	seedAwaitingSession(t, store, sessionstore.Session{
		ID:         "sess-resume-nopool",
		RuntimeRef: "no-such-runtime",
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref:    "ckpt-1",
			Source: sessionstore.WorkspaceSnapshotCheckpoint,
		},
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/sess-resume-nopool/resume", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("resume with no matching pool: status %d, want 503; body=%s", rr.Code, rr.Body.String())
	}

	row, err := store.Get(context.Background(), "acme", "sess-resume-nopool")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.State != session.StateFailed {
		t.Errorf("state = %q, want failed after the resume claim failure", row.State)
	}
	if registry.Len() != 0 {
		t.Errorf("registry holds %d bindings, want 0 after a failed resume", registry.Len())
	}
}

// spec: §8.10 — a child session that the gateway fails on the resume
// path is archived to the session_tree_archive.
func TestResumeArchivesFailedChild(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}
	adapterSrv.Restorer = resumeCheckpointSource{archive: emptyResumeArchive(t)}

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	store := memstore.New()
	archive := treearchive.NewMemory()
	srv := sessionserver.New(store, sessionserver.Options{
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv)),
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		TreeArchive:             archive,
	})

	// A child session targeting a runtime no warm pool covers: resume
	// cannot claim a pod, so the gateway fails the session.
	seedAwaitingSession(t, store, sessionstore.Session{
		ID:              "sess-child",
		RuntimeRef:      "no-such-runtime",
		ParentSessionID: "sess-parent",
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref:    "ckpt-1",
			Source: sessionstore.WorkspaceSnapshotCheckpoint,
		},
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/sess-child/resume", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("resume: status %d, want 503; body=%s", rr.Code, rr.Body.String())
	}

	got, err := archive.GetByNode(context.Background(), "acme", "sess-child")
	if err != nil {
		t.Fatalf("the failed child was not archived: %v", err)
	}
	if got.State != string(session.StateFailed) {
		t.Errorf("archived state = %q, want failed", got.State)
	}
	if got.ParentSessionID != "sess-parent" {
		t.Errorf("archived ParentSessionID = %q, want sess-parent", got.ParentSessionID)
	}
}

// spec: §4.2 line 160 — the §4.2 pod-to-session binding is persisted on
// the sessions row so a fresh gateway replica can recover the binding
// from Postgres after a coordinator handoff. The in-memory Registry is
// a cache; the row column is the source of truth across replicas.
func TestSessionStartPersistsPodAssignment(t *testing.T) {
	rt := &podBindRuntime{}
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = rt

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-persist", "echo-pool", "10.244.2.9"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-persist-assign" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// The pod_assignment column on the sessions row must hold the bound
	// sandbox's name; a fresh replica reading the row alone sees the
	// binding without needing access to the in-memory Registry.
	row, err := store.Get(context.Background(), "acme", "sess-persist-assign")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.PodAssignment != "sbx-persist" {
		t.Errorf("row.PodAssignment = %q, want sbx-persist (§4.2 line 160)", row.PodAssignment)
	}
	// The Registry also holds the binding — the in-memory cache is
	// authoritative for this replica's hot path.
	if _, ok := registry.Get("sess-persist-assign"); !ok {
		t.Error("registry holds no binding (cache should be populated alongside the persist)")
	}
}

// TestResumeKeepsAwaitingOnTransientPoolExhaustion covers F-7.3.23 /
// §7.3 line 423: a transient warm-pool exhaustion during the client's
// explicit `POST /resume` retry must leave the row in
// `awaiting_client_action` so the next retry can succeed once an idle
// pod is available. The §15.1 envelope still surfaces a retryable 503.
func TestResumeKeepsAwaitingOnTransientPoolExhaustion(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}
	adapterSrv.Restorer = resumeCheckpointSource{archive: emptyResumeArchive(t)}

	// A pool with no idle sandbox triggers podclaim.ErrNoIdlePod and
	// the gateway maps that to WARM_POOL_EXHAUSTED (a §5.2 line 519
	// retryable response).
	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-transient-pool" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})

	seedAwaitingSession(t, store, sessionstore.Session{
		ID:      "sess-transient-pool",
		PoolRef: "echo-pool",
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref:    "ckpt-1",
			Source: sessionstore.WorkspaceSnapshotCheckpoint,
		},
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/sess-transient-pool/resume", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("resume on empty pool: status %d, want 503; body=%s", rr.Code, rr.Body.String())
	}

	// spec: §7.3 line 423 — the row must stay in awaiting_client_action
	// so a retry can succeed once an idle pod returns to the pool.
	// F-7.3.23.
	row, err := store.Get(context.Background(), "acme", "sess-transient-pool")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.State != session.StateAwaitingClientAction {
		t.Errorf("state = %q, want awaiting_client_action (§7.3 line 423; transient pool exhaustion should not demote to failed)", row.State)
	}
}

// TestResumePassesRecoveryGenerationAndSizeHintsToAdapter covers
// F-7.3.22 + F-7.3.26: the gateway must pass the session's recovery
// generation and the last_checkpoint_workspace_bytes / template hard
// limit to the adapter so per-pod telemetry, partial-manifest tagging,
// and the symmetric pre-extraction size check have the spec-mandated
// inputs.
func TestResumePassesRecoveryGenerationAndSizeHintsToAdapter(t *testing.T) {
	rt := &podBindRuntime{}
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = rt
	captured := &capturingRestorer{archive: emptyResumeArchive(t)}
	adapterSrv.Restorer = captured

	// WorkspaceSizeLimitBytes on the template is the §4.4 / §10.1
	// per-pod hard cap; the resume path forwards it to the adapter.
	limit := int64(64 * 1024 * 1024)
	tmpl := podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed))
	tmpl.Spec.WorkspaceSizeLimitBytes = &limit

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		tmpl,
		podBindIdleSandbox("sbx-rg", "echo-pool", "10.244.4.5"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-rg-hints" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})

	// Seed with a non-zero RecoveryGeneration so the gateway re-delivers
	// the same value to the adapter, and a non-zero snapshot Bytes so
	// the adapter receives an expected size > 0. The RecoveryGeneration
	// floor in memstore clamps zero on re-write, so seed via Update
	// after Create.
	seedAwaitingSession(t, store, sessionstore.Session{
		ID:                 "sess-rg-hints",
		PoolRef:            "echo-pool",
		RecoveryGeneration: 3,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref:    "ckpt-rg",
			Source: sessionstore.WorkspaceSnapshotCheckpoint,
			Bytes:  4096,
		},
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/sess-rg-hints/resume", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// capturingRestorer is a CheckpointSource that records that LoadCheckpoint
// was called so the resume tests can assert the restore path ran.
type capturingRestorer struct {
	archive []byte
	loaded  bool
}

func (c *capturingRestorer) LoadCheckpoint(_ context.Context, _ string) (io.ReadCloser, error) {
	c.loaded = true
	return io.NopCloser(bytes.NewReader(c.archive)), nil
}

// spec: §4.2 line 156 — recovery_generation is incremented on each
// pod recovery. The resume path bumps it by one and persists the new
// pod assignment in the same Update.
func TestResumeBumpsRecoveryGeneration(t *testing.T) {
	srv, store, _, _ := podResumeServer(t, "sess-recovery-bump")
	seedAwaitingSession(t, store, sessionstore.Session{
		ID: "sess-recovery-bump",
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref:    "ckpt-1",
			Source: sessionstore.WorkspaceSnapshotCheckpoint,
		},
	})

	// Baseline: a freshly created awaiting session has
	// RecoveryGeneration=0.
	row, _ := store.Get(context.Background(), "acme", "sess-recovery-bump")
	if row.RecoveryGeneration != 0 {
		t.Fatalf("baseline RecoveryGeneration = %d, want 0", row.RecoveryGeneration)
	}

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/sess-recovery-bump/resume", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	// After one resume the counter advanced by exactly one and the
	// pod assignment reflects the new bound sandbox.
	row, _ = store.Get(context.Background(), "acme", "sess-recovery-bump")
	if row.RecoveryGeneration != 1 {
		t.Errorf("after resume, RecoveryGeneration = %d, want 1 (§4.2 line 156)",
			row.RecoveryGeneration)
	}
	if row.PodAssignment != "sbx-1" {
		t.Errorf("after resume, PodAssignment = %q, want sbx-1", row.PodAssignment)
	}
}

// poolBindQueuePool returns a poolstore pool that maps name onto runtimeRef
// with sessionPolicy.onPoolExhausted: queue and the given wait bound, so the
// §5.2 / §4.6.1 claim FIFO holds an exhausted request rather than rejecting it.
func poolBindQueuePool(t *testing.T, name, runtimeRef string, waitSeconds int) poolstore.Store {
	t.Helper()
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name:          name,
		RuntimeRef:    runtimeRef,
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			OnPoolExhausted:     runtimestore.PoolExhaustedQueue,
			MaxQueueWaitSeconds: waitSeconds,
		},
	}); err != nil {
		t.Fatalf("create queue pool: %v", err)
	}
	return pools
}

// spec: §4.6.1 (Pool exhaustion behavior — queue holds the request in the
// per-pool FIFO and re-enters acquisition as pods free); §5.2 (onPoolExhausted:
// queue); §7.1 (session_id only on success).
// diagnosis: a queue pool that does not re-enter acquisition would reject a
// request a pod freeing within the wait bound should have served, so the queue
// option would be inert. A failure here means the start path does not consult
// the per-pool claim queue or the queue does not retry the bind.
func TestSessionStartQueuesUntilPodFrees(t *testing.T) {
	rt := &podBindRuntime{}
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = rt

	// The pool starts with no idle Sandbox, so the first claim attempt
	// exhausts both the claim path and (absent Postgres) the fallback.
	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-queue-1" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
		Pools:                   poolBindQueuePool(t, "echo-pool", "echo", 30),
		QueuePollInterval:       20 * time.Millisecond,
	})

	// Free a pod shortly after the request starts queueing so a poll re-enters
	// acquisition and succeeds within the wait bound.
	go func() {
		time.Sleep(120 * time.Millisecond)
		idle := podBindIdleSandbox("sbx-late", "echo-pool", "10.244.2.7")
		idle.Status = lennyv1.SandboxStatus{}
		if err := cluster.Create(context.Background(), idle); err != nil {
			t.Errorf("create late idle sandbox: %v", err)
			return
		}
		u := &unstructured.Unstructured{}
		u.SetAPIVersion(lennyv1.GroupVersion.String())
		u.SetKind("Sandbox")
		u.SetName("sbx-late")
		u.SetNamespace(podTestNS)
		_ = unstructured.SetNestedField(u.Object, map[string]interface{}{
			"phase": "idle", "podIP": "10.244.2.7",
		}, "status")
		if err := cluster.Status().Patch(context.Background(), u, client.Apply,
			client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
			t.Errorf("seed late idle sandbox status: %v", err)
		}
	}()

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("queued start: status = %d, want 201 once a pod frees; body=%s", rr.Code, rr.Body.String())
	}
	binding, ok := registry.Get("sess-queue-1")
	if !ok {
		t.Fatal("registry holds no binding for the queued-then-served session")
	}
	if binding.SandboxName != "sbx-late" {
		t.Errorf("binding sandbox = %q, want sbx-late (the pod that freed mid-wait)", binding.SandboxName)
	}
}

// spec: §4.6.1 (Pool exhaustion behavior — queue-wait timeout returns
// WARM_POOL_EXHAUSTED); §5.2 (maxQueueWaitSeconds bound); §15.1 (WARM_POOL_EXHAUSTED
// carries a Retry-After header).
// diagnosis: a queue pool whose wait bound elapses with no pod free must still
// surface WARM_POOL_EXHAUSTED with Retry-After rather than hanging the client or
// dropping the retry hint. A failure here means the wait bound is not enforced
// or the timeout return value is not the exhaustion envelope.
func TestSessionStartQueueTimeoutReturnsExhausted(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	// No idle Sandbox is ever added, so the queue exhausts its (tiny) wait
	// bound and returns WARM_POOL_EXHAUSTED.
	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	var timeouts int32
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-queue-timeout" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
		Pools:                   poolBindQueuePool(t, "echo-pool", "echo", 1),
		QueuePollInterval:       20 * time.Millisecond,
		IncPodClaimTimeout:      func(string) { atomic.AddInt32(&timeouts, 1) },
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()

	start := time.Now()
	srv.Handler().ServeHTTP(rr, req)
	elapsed := time.Since(start)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("queue timeout: status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("queue timeout: WARM_POOL_EXHAUSTED must carry a Retry-After header (§15.1)")
	}
	// The 1s wait bound must actually be observed (not an immediate reject).
	if elapsed < 900*time.Millisecond {
		t.Errorf("queue timeout returned after %s, want >= ~1s (the maxQueueWaitSeconds bound)", elapsed)
	}
	if atomic.LoadInt32(&timeouts) != 1 {
		t.Errorf("queue timeout must increment lenny_pod_claim_timeout_total once, got %d", timeouts)
	}
	// §7.1: no session row is left behind on the atomic-create timeout.
	if _, err := store.Get(context.Background(), "acme", "sess-queue-timeout"); err == nil {
		t.Error("queue timeout must leave no session row (§7.1 atomicity)")
	}
}

// spec: §4.6.1 (Pool exhaustion behavior — reject keeps the immediate-failure
// behavior); §5.2 (onPoolExhausted default reject).
// diagnosis: a reject pool that waited would change the documented behavior and
// hide pool under-sizing behind a long wait. A failure here means the start
// path queues a reject pool.
func TestSessionStartRejectFailsImmediately(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	// No Pools store wired: the disposition is empty, which defaults to reject.
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-reject" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
		QueuePollInterval:       20 * time.Millisecond,
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()

	start := time.Now()
	srv.Handler().ServeHTTP(rr, req)
	elapsed := time.Since(start)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("reject pool: status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	// A reject pool must not wait; the response is prompt (well under any
	// queue wait bound).
	if elapsed > 500*time.Millisecond {
		t.Errorf("reject pool returned after %s, want a prompt failure (no queueing)", elapsed)
	}
}
