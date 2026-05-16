// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
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

// podBindClient returns a fake cluster holding a warm pool, its
// template, and one idle Sandbox the start path can claim.
func podBindClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(podBindScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&lennyv1.Sandbox{}, &lennyv1.SandboxClaim{}).
		Build()
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

	cluster := podBindClient(t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess_pod_1" },
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

	binding, ok := registry.Get("sess_pod_1")
	if !ok {
		t.Fatal("registry holds no binding for the started session")
	}
	if binding.SandboxName != "sbx-1" || binding.PodIP != "10.244.2.5" {
		t.Errorf("binding = %+v, want sbx-1 / 10.244.2.5", binding)
	}
	if rt.started != "sess_pod_1" {
		t.Errorf("adapter runtime started for %q, want sess_pod_1", rt.started)
	}

	var sb lennyv1.Sandbox
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "claimed" {
		t.Errorf("sandbox phase = %q, want claimed", sb.Status.Phase)
	}
}

func TestSessionStartFailsSessionWhenNoPoolMatches(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	// The cluster serves only the "echo" runtime; the request asks for a
	// runtime no warm pool covers.
	cluster := podBindClient(t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess_pod_nomatch" },
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

	row, err := store.Get(context.Background(), "acme", "sess_pod_nomatch")
	if err != nil {
		t.Fatalf("session row not stored: %v", err)
	}
	if row.State != session.StateFailed {
		t.Errorf("session state = %q, want failed after the claim failure", row.State)
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

	cluster := podBindClient(t,
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
	srv, registry, cluster, wsRoot := podBindServer(t, "sess_2step_1")
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
	if rr := postSessionStep(t, h, "/v1/sessions/sess_2step_1/finalize", nil); rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body=%s", rr.Code, rr.Body.String())
	}
	rr := postSessionStep(t, h, "/v1/sessions/sess_2step_1/start", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("start: status %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp sessionserver.SessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.State != string(session.StateRunning) {
		t.Errorf("state = %q, want running", resp.State)
	}
	if _, ok := registry.Get("sess_2step_1"); !ok {
		t.Error("registry holds no binding after the two-step start")
	}

	var sb lennyv1.Sandbox
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "claimed" {
		t.Errorf("sandbox phase = %q, want claimed", sb.Status.Phase)
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
	srv, registry, _, _ := podBindServer(t, "sess_2step_early")
	h := srv.Handler()

	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo"})
	if rr := postSessionStep(t, h, "/v1/sessions", createBody); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}

	// /start before /finalize: the row is still `created`, not `ready`.
	rr := postSessionStep(t, h, "/v1/sessions/sess_2step_early/start", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("start before finalize: status %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if registry.Len() != 0 {
		t.Errorf("registry holds %d bindings, want 0 — no pod claimed on a rejected start", registry.Len())
	}
}
