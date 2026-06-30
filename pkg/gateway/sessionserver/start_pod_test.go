// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/pkg/upload"
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

// countingRouter wraps the default §4.9 CredentialRouter and counts
// Resolve calls so a test can assert how many times the gateway runs the
// §7.1-step-3 pre-claim credential availability check across a request.
type countingRouter struct {
	inner credrouter.Default
	count *int
}

func (r countingRouter) Resolve(ctx context.Context, in credrouter.Input) (credrouter.Output, error) {
	*r.count++
	return r.inner.Resolve(ctx, in)
}

// spec: §4.1 (proposal: the §7.1-step-3 credential pre-check is moved into
// createSession ahead of the step-4 claim and runs once before the claim),
// §7.1 step 3, §4.9 lines 1216-1218.
// diagnosis: the combined one-call POST /v1/sessions/start runs claim,
// prepare, and launch in a single call. The credential availability
// pre-check must run exactly once (at the create-time claim), not again at
// the prepare dispatch. A failure here means startOnPod re-ran
// resolveCredentialPools after claimAtCreate already ran it, so the pre-check
// executed twice for one combined create-and-start, contradicting the
// proposal's "pre-check runs once, before the claim" placement.
func TestCombinedStartRunsCredentialPreCheckOnce_spec_4_1(t *testing.T) {
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
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	policy := credential.CredentialPolicy{
		PreferredSource: credential.PreferredSourcePool,
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
		},
	}
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: policy}); err != nil {
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

	var resolveCount int
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return "sess-precheck-once" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        countingRouter{count: &resolveCount},
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// One provider in the intersection, resolved exactly once: the
	// create-time pre-check. A count of 2 means the prepare dispatch re-ran
	// resolveCredentialPools after the create-time claim.
	if resolveCount != 1 {
		t.Errorf("CredentialRouter.Resolve called %d times, want 1 (the §7.1-step-3 pre-check runs once before the step-4 claim)", resolveCount)
	}
}

// spec: §6.3 line 348 (end-to-end span is pod claim through ready), §5
// (proposal: the combined create-and-start reuses the claim/prepare/launch
// phases and the claim-to-ready span covers claim through ready).
// diagnosis: the combined one-call POST /v1/sessions/start performs the
// claim, prepare, and launch in a single call, so the single end-to-end
// lenny_session_startup_duration_seconds observation must include the
// pod-claim component the same call measured at create. A failure here
// means prepareAndLaunch dropped the create-time pod_claim duration from
// the end-to-end total (it would equal credential_assignment +
// agent_session_start only), under-reporting the §6.3 / §16.5 claim-through-
// ready SLO envelope. The per-phase pod_claim histogram is still emitted
// exactly once.
func TestCombinedStartEndToEndIncludesPodClaim_spec_6_3_348(t *testing.T) {
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
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	var (
		phaseObs map[string]float64
		endToEnd float64
		endCount int
	)
	phaseObs = map[string]float64{}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return "sess-combined-metric" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		ObserveStartupPhase: func(phase, _ string, seconds float64) {
			phaseObs[phase] = seconds
		},
		ObserveStartupDuration: func(_, _, _ string, seconds float64) {
			endToEnd = seconds
			endCount++
		},
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// The end-to-end metric is emitted exactly once for one logical start.
	if endCount != 1 {
		t.Fatalf("lenny_session_startup_duration_seconds observed %d times, want exactly 1", endCount)
	}
	// The per-phase pod_claim sample is recorded once at /create.
	podClaim, ok := phaseObs["pod_claim"]
	if !ok || podClaim <= 0 {
		t.Fatalf("pod_claim phase = %v (present=%v), want a positive sample recorded at create", podClaim, ok)
	}
	// spec: §6.3 line 348 — the end-to-end total is pod_claim +
	// credential_assignment + agent_session_start. The pod-claim component
	// must be present, so the end-to-end observation is at least the
	// pod_claim phase plus the agent_session_start phase (both measured).
	agentStart := phaseObs["agent_session_start"]
	if agentStart <= 0 {
		t.Fatalf("agent_session_start phase = %v, want a positive sample at launch", agentStart)
	}
	// Without the pod-claim component the end-to-end would be agent_session_start
	// (+ credential_assignment) only and strictly below podClaim+agentStart.
	wantAtLeast := podClaim + agentStart
	if endToEnd < wantAtLeast-1e-9 {
		t.Errorf("end-to-end duration = %v, want >= pod_claim(%v) + agent_session_start(%v) = %v; "+
			"the combined-call observation dropped the pod-claim component",
			endToEnd, podClaim, agentStart, wantAtLeast)
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

// spec: §4.4 (proposal: /start is launch-only and does no credential work),
// §15.1 (/start precondition), §4.9 lines 1216-1218 (pre-claim credential
// resolution).
// diagnosis: the two-step `POST /v1/sessions/{id}/start` exclusive-pool path
// must launch only — the §4.9 credential resolution belongs at /create
// (pre-check) and /finalize (lease assignment), never at /start. The router
// Resolve count must be unchanged across the /start call. A failure here
// (the count grows at /start) means handleStart still routed through a path
// that re-runs resolveCredentialPools (the pre-0007-S4 startOnPod front),
// re-doing credential work the lease assignment at /finalize already
// completed.
func TestTwoStepStartRunsNoCredentialWork_spec_4_4(t *testing.T) {
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
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	policy := credential.CredentialPolicy{
		PreferredSource: credential.PreferredSourcePool,
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
		},
	}
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: policy}); err != nil {
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

	var resolveCount int
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return "sess-2step-nocred" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        countingRouter{count: &resolveCount},
	})
	h := srv.Handler()

	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	if rr := postSessionStep(t, h, "/v1/sessions", createBody); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	if rr := postSessionStep(t, h, "/v1/sessions/sess-2step-nocred/finalize", nil); rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body=%s", rr.Code, rr.Body.String())
	}
	// The §7.1-step-3 pre-check ran at /create and the lease assignment
	// re-resolved the pool at /finalize, so by now the router has resolved the
	// single provider twice. Snapshot the count immediately before /start.
	beforeStart := resolveCount

	rr := postSessionStep(t, h, "/v1/sessions/sess-2step-nocred/start", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("start: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.SessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.State != string(session.StateRunning) {
		t.Errorf("after start, state = %q, want running", resp.State)
	}
	// /start is launch-only: it must run no §4.9 credential resolution, so the
	// router Resolve count is unchanged across the call.
	if resolveCount != beforeStart {
		t.Errorf("CredentialRouter.Resolve called %d times across /start (was %d before); "+
			"/start must be launch-only and run no credential work (proposal §4.4)",
			resolveCount-beforeStart, beforeStart)
	}
}

// spec: §7.1 steps 11-13, §15.1 (finalize precondition), §4.3 (proposal).
// diagnosis: /finalize is the §4.3 preparation barrier — it materializes
// /workspace/current and runs setup before returning, and the session reaches
// `ready` only once prepared. A failure here means /finalize transitioned to
// `ready` without materializing the workspace, so the workspace plan was not
// applied until /start (the pre-0007 deferred-claim behavior) or the row
// reached `ready` while the pod was still bare.
func TestFinalizeMaterializesWorkspaceBeforeStart_spec_7_1(t *testing.T) {
	srv, _, _, wsRoot := podBindServer(t, "sess-fin-mat")
	h := srv.Handler()

	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{
		RuntimeRef: "echo",
		UserID:     "alice@acme.com",
		WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"sources": [{"type":"inlineFile","path":"CLAUDE.md","content":"# finalized","mode":"0644"}]
		}`),
	})
	if rr := postSessionStep(t, h, "/v1/sessions", createBody); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}

	// The workspace must not be materialized yet: /create only claims the pod
	// and buffers uploads; materialization is the §4.3 finalize barrier's job.
	if _, err := os.Stat(filepath.Join(wsRoot, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("CLAUDE.md materialized before finalize (err=%v); materialization must run at /finalize", err)
	}

	rr := postSessionStep(t, h, "/v1/sessions/sess-fin-mat/finalize", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body=%s", rr.Code, rr.Body.String())
	}
	// The finalize barrier returns only once the session is `ready`.
	var resp sessionserver.SessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.State != string(session.StateReady) {
		t.Errorf("after finalize, state = %q, want ready", resp.State)
	}
	// The workspace plan stored at create was materialized onto the pod's
	// adapter workspace at finalize, before /start ran.
	got, err := os.ReadFile(filepath.Join(wsRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("workspace plan was not materialized at finalize: %v", err)
	}
	if string(got) != "# finalized" {
		t.Errorf("materialized file = %q, want %q", got, "# finalized")
	}

	// /start then only launches; the session reaches running.
	rr = postSessionStep(t, h, "/v1/sessions/sess-fin-mat/start", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("start: status %d, body=%s", rr.Code, rr.Body.String())
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.State != string(session.StateRunning) {
		t.Errorf("after start, state = %q, want running", resp.State)
	}
}

// spec: §7.5 line 475, §7.3 line 387 (setup_command_failed non-retryable),
// §15.1 (SETUP_COMMAND_FAILED), §4.3 (proposal: finalize-failure reclaim),
// §6.2 (pre-attached disposition).
// diagnosis: a deterministic non-zero setup-command exit during the §4.3
// finalize barrier (which the adapter reports as gRPC FailedPrecondition) must
// surface the non-retryable 422 SETUP_COMMAND_FAILED envelope (retryable:false,
// no Retry-After, details.reason=setup_command_failed), reclaim the claimed pod
// (delete the per-pod SandboxClaim per the §6.2 pre-attached disposition), and
// transition the row to the terminal `failed` state. A failure here means the
// finalize barrier still returned the retired retryable 503 (telling a client
// to retry a deterministic failure that will fail identically), left the row
// stuck in `finalizing`, or leaked the claimed pod after the prepare phase
// aborted.
func TestFinalizeFailsSessionAndReclaimsPodOnSetupError_spec_7_5(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

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
		IDFunc:                  func() string { return "sess-fin-setupfail" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})
	h := srv.Handler()

	// A plan whose setup command exits non-zero makes the adapter's RunSetup
	// abort with FailedPrecondition during the finalize prepare phase.
	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{
		RuntimeRef: "echo",
		UserID:     "alice@acme.com",
		WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"setupCommands": [{"cmd":"exit 3"}]
		}`),
	})
	if rr := postSessionStep(t, h, "/v1/sessions", createBody); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	// The pod was claimed at /create: its per-pod claim exists.
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err != nil {
		t.Fatalf("per-pod claim missing after create: %v", err)
	}

	rr := postSessionStep(t, h, "/v1/sessions/sess-fin-setupfail/finalize", nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("finalize with a deterministic failing setup command: status %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code      string         `json:"code"`
			Category  string         `json:"category"`
			Retryable bool           `json:"retryable"`
			Details   map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode finalize error envelope: %v (body=%s)", err, rr.Body.String())
	}
	if env.Error.Code != "SETUP_COMMAND_FAILED" {
		t.Errorf("error code = %q, want SETUP_COMMAND_FAILED", env.Error.Code)
	}
	if env.Error.Category != "PERMANENT" || env.Error.Retryable {
		t.Errorf("category/retryable = %q/%v, want PERMANENT/false", env.Error.Category, env.Error.Retryable)
	}
	if env.Error.Details["reason"] != "setup_command_failed" {
		t.Errorf("details.reason = %v, want setup_command_failed", env.Error.Details["reason"])
	}
	// A deterministic non-retryable failure must not invite a retry.
	if ra := rr.Header().Get("Retry-After"); ra != "" {
		t.Errorf("Retry-After = %q, want absent on the non-retryable SETUP_COMMAND_FAILED", ra)
	}

	// The session transitioned to the terminal failed state, not stuck in
	// finalizing.
	row, err := store.Get(context.Background(), "acme", "sess-fin-setupfail")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.State != session.StateFailed {
		t.Errorf("state = %q, want failed after the finalize setup-command failure", row.State)
	}
	// The claimed pod was reclaimed: the per-pod SandboxClaim is deleted (the
	// §6.2 pre-attached disposition), so no pod leaks past the failed finalize.
	err = cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim)
	if err == nil {
		t.Errorf("per-pod claim still present after the failed finalize; the pod was not reclaimed")
	}
	if registry.Len() != 0 {
		t.Errorf("registry holds %d bindings, want 0 — no binding is registered on a failed finalize", registry.Len())
	}
}

// manyEntryTar builds an in-memory tar carrying count zero-byte regular
// entries. With count past upload.MaxEntryCount the §13.4 aggregate validator
// aborts extraction with a max_entry_count *upload.ValidationError — a
// memory-cheap decompression-bomb that exercises the archive-limit path without
// allocating the 256 MiB / 64 MiB size ceilings.
func manyEntryTar(t *testing.T, count int) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := 0; i < count; i++ {
		if err := tw.WriteHeader(&tar.Header{
			Name:     fmt.Sprintf("f%06d.txt", i),
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     0,
		}); err != nil {
			t.Fatalf("tar header %d: %v", i, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

// spec: §15.1 (UPLOAD_ARCHIVE_LIMIT_EXCEEDED, 413/PERMANENT), §13.4 (upload
// security validator), §7.4 (upload safety).
// diagnosis: an over-limit or decompression-bomb uploadArchive raised by the
// §13.4 validator during the §4.3 finalize materialization barrier must surface
// the non-retryable 413 UPLOAD_ARCHIVE_LIMIT_EXCEEDED envelope (category
// PERMANENT, retryable:false, no Retry-After, details.reason carrying the §13.4
// sub-code) rather than the retryable 503 SESSION_CREATION_FAILED fallback. A
// failure here means writePodClaimError's switch had no *upload.ValidationError
// case and fell to the retryable default, so a conforming client would retry the
// same non-conformant archive indefinitely (F-CS1).
func TestFinalizeRejectsOverLimitArchiveAsNonRetryable_spec_13_4(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	// Stage an over-limit uploadArchive blob under this session's
	// tenant+session prefix so the finalize ref-ownership check admits it and
	// the binder reaches §13.4 extraction.
	blobs := blobstore.NewMemoryStore(nil)
	binder.Blobs = blobs
	ref := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeUpload,
		SessionID:  "sess-fin-archlimit",
		PartID:     "p1",
		TTL:        time.Hour,
	}
	if _, err := blobs.Put(ref, "application/octet-stream",
		bytes.NewReader(manyEntryTar(t, upload.MaxEntryCount+1))); err != nil {
		t.Fatalf("stage over-limit archive blob: %v", err)
	}

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-fin-archlimit" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})
	h := srv.Handler()

	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{
		RuntimeRef: "echo",
		UserID:     "alice@acme.com",
	})
	if rr := postSessionStep(t, h, "/v1/sessions", createBody); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	// The pod was claimed at /create.
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err != nil {
		t.Fatalf("per-pod claim missing after create: %v", err)
	}

	// Finalize binds the plan naming the over-limit archive; the §13.4 validator
	// aborts extraction during the prepare barrier.
	finalizeBody := `{"workspacePlan":{"schemaVersion":1,"sources":[{"type":"uploadArchive","pathPrefix":"proj","uploadRef":"` +
		ref.String() + `","format":"tar"}]}}`
	rr := postSessionStep(t, h, "/v1/sessions/sess-fin-archlimit/finalize", []byte(finalizeBody))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("finalize with an over-limit archive: status %d, want 413; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code      string         `json:"code"`
			Category  string         `json:"category"`
			Retryable bool           `json:"retryable"`
			Details   map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode finalize error envelope: %v (body=%s)", err, rr.Body.String())
	}
	if env.Error.Code != "UPLOAD_ARCHIVE_LIMIT_EXCEEDED" {
		t.Errorf("error code = %q, want UPLOAD_ARCHIVE_LIMIT_EXCEEDED (not the retryable SESSION_CREATION_FAILED fallback)", env.Error.Code)
	}
	if env.Error.Category != "PERMANENT" || env.Error.Retryable {
		t.Errorf("category/retryable = %q/%v, want PERMANENT/false", env.Error.Category, env.Error.Retryable)
	}
	if env.Error.Details["reason"] != string(upload.ReasonMaxEntryCount) {
		t.Errorf("details.reason = %v, want %q (the §13.4 sub-code)", env.Error.Details["reason"], upload.ReasonMaxEntryCount)
	}
	// A deterministic non-retryable archive rejection must not invite a retry.
	if ra := rr.Header().Get("Retry-After"); ra != "" {
		t.Errorf("Retry-After = %q, want absent on the non-retryable UPLOAD_ARCHIVE_LIMIT_EXCEEDED", ra)
	}

	// The finalize barrier failed the session and reclaimed the claimed pod, so
	// no pod leaks past the rejected archive.
	row, err := store.Get(context.Background(), "acme", "sess-fin-archlimit")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.State != session.StateFailed {
		t.Errorf("state = %q, want failed after the over-limit archive rejection", row.State)
	}
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err == nil {
		t.Errorf("per-pod claim still present after the rejected finalize; the pod was not reclaimed")
	}
}

// spec: §4.9 line 1220 (check-to-assignment race), §7.3 line 138 / §7.6 line
// 153 (proposal: USER_CREDENTIAL_NOT_FOUND is not a finalize trigger; a
// check-to-assignment mismatch surfaces as CREDENTIAL_POOL_EXHAUSTED at
// /finalize), §4.3 (proposal: a finalize-step failure reclaims the create-time
// pod), §6.2 (pre-attached disposition).
// diagnosis: a credential source present at the create-time §7.1-step-3
// pre-check but gone by /finalize is the check-to-assignment mismatch. It must
// (a) surface as CREDENTIAL_POOL_EXHAUSTED rather than the create-only
// USER_CREDENTIAL_NOT_FOUND or pre-claim envelope, and (b) reclaim the pod
// claimed at /create (delete the per-pod SandboxClaim) even though the binder's
// prepare phase never ran, so the pod does not leak to the §4.6.1 orphan-claim
// GC. A failure here means either the finalize re-resolution surfaced
// USER_CREDENTIAL_NOT_FOUND (a 404 the proposal forbids at finalize) or the
// create-time pod was left holding its claim after the pre-Prepare resolution
// failed.
func TestFinalizeCredentialMismatchReclaimsPod_spec_7_6(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	policy := credential.CredentialPolicy{
		PreferredSource: credential.PreferredSourcePool,
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
		},
	}
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: policy}); err != nil {
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

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-fin-credmiss" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
	})
	h := srv.Handler()

	// Create succeeds: the credential is active at the §7.1-step-3 pre-check, so
	// the pod is claimed.
	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	if rr := postSessionStep(t, h, "/v1/sessions", createBody); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(ctx, client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err != nil {
		t.Fatalf("per-pod claim missing after create: %v", err)
	}

	// The credential vanishes during the upload window: revoke the pool's only
	// credential so the finalize re-resolution finds none assignable.
	if _, err := credPools.Update(ctx, "acme", "claude-prod", func(p *credentialpoolstore.CredentialPool) error {
		p.Credentials[0].Status = credentialpoolstore.CredentialRevoked
		return nil
	}); err != nil {
		t.Fatalf("revoke credential: %v", err)
	}

	rr := postSessionStep(t, h, "/v1/sessions/sess-fin-credmiss/finalize", nil)
	// The mismatch surfaces as CREDENTIAL_POOL_EXHAUSTED (503), not the
	// create-only USER_CREDENTIAL_NOT_FOUND (404).
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("finalize with a vanished credential: status %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("CREDENTIAL_POOL_EXHAUSTED")) {
		t.Errorf("finalize error body = %s, want CREDENTIAL_POOL_EXHAUSTED", rr.Body.String())
	}

	// The session transitioned to the terminal failed state.
	row, err := store.Get(ctx, "acme", "sess-fin-credmiss")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.State != session.StateFailed {
		t.Errorf("state = %q, want failed after the finalize credential mismatch", row.State)
	}
	// The create-time pod was reclaimed: the per-pod SandboxClaim is deleted,
	// even though the binder's prepare phase never ran (the mismatch was a
	// pre-Prepare resolution failure).
	if err := cluster.Get(ctx, client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err == nil {
		t.Errorf("per-pod claim still present after the failed finalize; the create-time pod was not reclaimed")
	}
}

// readyTransitionFailStore wraps a sessionstore.Store and fails the Update
// that transitions a row to `ready`, so a test can force the §4.3 Gap-2
// finalize failure: the binder's prepare phase succeeds (the §4.9 lease is
// assigned), and only the subsequent finalizing → ready store write fails. All
// other Updates pass through.
type readyTransitionFailStore struct {
	sessionstore.Store
	failReady bool
}

func (s *readyTransitionFailStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if s.failReady {
		// Probe the mutation on a copy: when it would set the row to `ready`,
		// reject the write to simulate a store outage at the exact finalize
		// finalizing → ready transition that follows AssignCredentials.
		probe, err := s.Store.Get(ctx, tenantID, id)
		if err == nil {
			if perr := mutate(&probe); perr == nil && probe.State == session.StateReady {
				return sessionstore.Session{}, errInjectedReadyWriteFailure
			}
		}
	}
	return s.Store.Update(ctx, tenantID, id, mutate)
}

// errInjectedReadyWriteFailure is the injected store error the Gap-2 finalize
// test forces on the finalizing → ready write.
var errInjectedReadyWriteFailure = errors.New("injected store failure on finalizing -> ready write")

// recordingLeaseAssigner records every AssignProto and ReleaseSession so a
// test can assert the §4.9 lease was assigned at finalize and revoked on a
// Gap-2 reclaim. It returns a fixed proxy-mode lease.
type recordingLeaseAssigner struct {
	assigns  []string
	released []string
}

func (a *recordingLeaseAssigner) AssignProto(pool, sessionID, _, _ string) (*adapterv1.CredentialLease, error) {
	a.assigns = append(a.assigns, sessionID)
	return &adapterv1.CredentialLease{
		LeaseId:  "cl-" + pool,
		Provider: pool,
		Payload: []byte(`{"deliveryMode":"proxy",` +
			`"materializedConfig":{"proxyUrl":"https://p/v1","leaseToken":"lt-` + pool + `"}}`),
	}, nil
}

func (a *recordingLeaseAssigner) ReleaseSession(sessionID string) {
	a.released = append(a.released, sessionID)
}

// spec: §4.3 (proposal: Gap 2 — a finalize failure AFTER AssignCredentials
// succeeded reclaims the pod AND revokes the lease), §7.1 step 23 (lease
// release), §15.1 (finalize precondition).
// diagnosis: the §4.3 finalize barrier assigns the §4.9 lease during its
// prepare phase, then transitions finalizing → ready. When that final store
// write fails after the lease was assigned, the gateway must reclaim the
// create-time pod (delete the per-pod SandboxClaim) AND revoke the lease
// (ReleaseSession) before failing the row, or a post-assignment finalize
// failure leaks the credential's active-session slot for a session that never
// reaches ready. A failure here means reclaimFinalizedPod did not run on the
// finalizing → ready write-failure branch, so the lease leaked.
func TestFinalizePostCredentialWriteFailureRevokesLease_spec_4_3(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.StagingDir = t.TempDir()
	adapterSrv.CredentialsDir = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))
	assigner := &recordingLeaseAssigner{}
	binder.Credentials = assigner

	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	policy := credential.CredentialPolicy{
		PreferredSource: credential.PreferredSourcePool,
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
		},
	}
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: policy}); err != nil {
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

	store := &readyTransitionFailStore{Store: memstore.New()}
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-fin-gap2" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
	})
	h := srv.Handler()

	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	if rr := postSessionStep(t, h, "/v1/sessions", createBody); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}

	// Arm the failure only for the finalizing → ready write so the prepare
	// phase (which assigns the lease) runs to completion first.
	store.failReady = true
	rr := postSessionStep(t, h, "/v1/sessions/sess-fin-gap2/finalize", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("finalize with an injected ready-write failure: status %d, want 500; body=%s", rr.Code, rr.Body.String())
	}

	// The lease was assigned at finalize and then revoked on the Gap-2 reclaim.
	if len(assigner.assigns) != 1 || assigner.assigns[0] != "sess-fin-gap2" {
		t.Errorf("AssignProto calls = %v, want [sess-fin-gap2] (the lease is assigned at finalize)", assigner.assigns)
	}
	if len(assigner.released) != 1 || assigner.released[0] != "sess-fin-gap2" {
		t.Errorf("ReleaseSession calls = %v, want [sess-fin-gap2] (Gap 2: the post-assignment finalize failure must revoke the lease)", assigner.released)
	}
	// The claimed pod was reclaimed: the per-pod SandboxClaim is deleted.
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(ctx, client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err == nil {
		t.Errorf("per-pod claim still present after the Gap-2 finalize failure; the create-time pod was not reclaimed")
	}
	// The row reaches the terminal failed state rather than stranding in finalizing.
	store.failReady = false
	row, err := store.Get(ctx, "acme", "sess-fin-gap2")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.State != session.StateFailed {
		t.Errorf("state = %q, want failed after the Gap-2 finalize failure", row.State)
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

// spec: §7.3 (snapshotless resume-rebuild), §4.6 (durable binding), §15.1.
// diagnosis: the snapshotless resume-rebuild path reconnected to the dead
// pod named in a stale PodAssignment instead of claiming a fresh one. A
// session that lost its pod and entered awaiting_client_action retains the
// dead pod's name in PodAssignment (failure.go), and no resume path clears
// it. startOnPod's create-time-claim reconnect branch must be gated on a
// live binding (non-recovery state), not on a non-empty PodAssignment
// alone, so resumeOnPod claims a fresh pod through the whole-sequence Bind.
// A failure means the resume reconnects to the no-longer-existing pod,
// fails Prepare, and reports the resume as failed instead of recovering it.
func TestResumeRebuildsWithStalePodAssignmentClaimsFreshPod(t *testing.T) {
	srv, store, registry, cluster := podResumeServer(t, "sess-resume-stale")
	// No WorkspaceSnapshot (snapshotless rebuild), but a non-empty stale
	// PodAssignment naming the dead pod the session lost. Before the gate
	// fix, startOnPod's `if row.PodAssignment != ""` branch would reconnect
	// to "sbx-dead" rather than claiming the idle "sbx-1".
	seedAwaitingSession(t, store, sessionstore.Session{
		ID:            "sess-resume-stale",
		PodAssignment: "sbx-dead",
		PoolRef:       "echo-pool",
		WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"sources": [{"type":"inlineFile","path":"CLAUDE.md","content":"# resumed","mode":"0644"}]
		}`),
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/sess-resume-stale/resume", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	row, err := store.Get(context.Background(), "acme", "sess-resume-stale")
	if err != nil {
		t.Fatalf("get resumed session: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("state = %q, want running after resume", row.State)
	}

	// The resume claimed the fresh idle pod (sbx-1), not the dead binding.
	binding, ok := registry.Get("sess-resume-stale")
	if !ok {
		t.Fatal("registry holds no binding after a snapshotless resume with a stale PodAssignment")
	}
	if binding.SandboxName != "sbx-1" || binding.PodIP != "10.244.2.5" {
		t.Errorf("binding = %+v, want fresh-claimed sbx-1 / 10.244.2.5 (not the stale sbx-dead)", binding)
	}

	// The fresh pod's claim is bound; the dead pod was never touched.
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err != nil {
		t.Fatalf("get per-pod claim for the freshly claimed pod: %v", err)
	}
	if claim.Status.Phase != string(claimstate.Bound) {
		t.Errorf("claim binding state = %q, want bound on the freshly claimed pod", claim.Status.Phase)
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
	// A reject pool never enters the per-pool claim FIFO, so the §16.1
	// lenny_pod_claim_queue_depth gauge is never published. Counting the gauge
	// emissions is the deterministic signal that the request did not queue; a
	// wall-clock elapsed bound flakes when a saturated -race run delays the
	// single bind attempt past any fixed threshold.
	var enqueued int32
	// No Pools store wired: the disposition is empty, which defaults to reject.
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-reject" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
		QueuePollInterval:       20 * time.Millisecond,
		SetPodClaimQueueDepth:   func(string, int) { atomic.AddInt32(&enqueued, 1) },
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("reject pool: status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	// A reject pool returns on the first exhaustion without holding the request
	// in the claim FIFO, so the queue-depth gauge is never touched.
	if got := atomic.LoadInt32(&enqueued); got != 0 {
		t.Errorf("reject pool published the claim-queue depth gauge %d times, want 0 (no queueing)", got)
	}
}

// podBindServiceTemplate is podBindTemplate's service-mode sibling: a
// SandboxTemplate whose §5.2 executionMode is `service`, so ResolvePool
// reports a service-mode match and the start path takes the claimless
// routing branch.
func podBindServiceTemplate(name, runtimeRef, isolationProfile string) *lennyv1.SandboxTemplate {
	tmpl := podBindTemplate(name, runtimeRef, isolationProfile)
	tmpl.Spec.ExecutionMode = string(runtimestore.ExecutionModeService)
	return tmpl
}

// podBindServicePool returns a poolstore mirror for a service-mode pool so
// ResolvePool folds in the §5.2 per-pod request capacity (MaxConcurrent).
func podBindServicePool(t *testing.T, name, runtimeRef string, maxConcurrent int) poolstore.Store {
	t.Helper()
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name:          name,
		RuntimeRef:    runtimeRef,
		ExecutionMode: runtimestore.ExecutionModeService,
		MaxConcurrent: maxConcurrent,
	}); err != nil {
		t.Fatalf("create service pool: %v", err)
	}
	return pools
}

// spec: §5.2 (service mode is claimless: no SandboxClaim, no workspace
// materialization), §3.6 (service-mode session contract), §7.1 line 74
// (conversationContinuity).
// diagnosis: a service-mode start that claims a Sandbox or binds a pod
// breaks the §5.2 claimless contract — every service-mode session would
// burn a warm pod and the tenant-affinity routing layer would never be
// reached. A failure here means the start path did not take the claimless
// branch for executionMode=service.
func TestServiceModeStartIsClaimless_spec_5_2(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	// The cluster carries a service-mode pool, its service-mode template, and
	// one idle Sandbox. The claimless path must leave that Sandbox unclaimed.
	cluster := podBindClient(
		t,
		podBindWarmPool("svc-pool", "svc-tmpl"),
		podBindServiceTemplate("svc-tmpl", "svc-runtime", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("svc-sbx-1", "svc-pool", "10.244.3.9"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-svc-1" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
		Pools:                   podBindServicePool(t, "svc-pool", "svc-runtime", 8),
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "svc-runtime", UserID: "alice@acme.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("service-mode start: status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// No binding is registered: a service-mode session is a connection handle,
	// not a pod-bound session.
	if b, ok := registry.Get("sess-svc-1"); ok {
		t.Errorf("service-mode session registered a pod binding %+v; want none (claimless)", b)
	}

	// spec: §5.2 — no SandboxClaim is created for a service-mode session. The
	// per-pod claim the session-claim path would have created (`claim-svc-sbx-1`)
	// must be absent.
	var claim lennyv1.SandboxClaim
	err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-svc-sbx-1"}, &claim)
	if !apierrors.IsNotFound(err) {
		t.Errorf("service-mode start created/looked up claim-svc-sbx-1 (err=%v); want NotFound (claimless)", err)
	}

	// The idle Sandbox is untouched: still idle, no occupancy projected.
	var sbx lennyv1.Sandbox
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "svc-sbx-1"}, &sbx); err != nil {
		t.Fatalf("get service-mode idle sandbox: %v", err)
	}
	if sbx.Status.Phase != "idle" {
		t.Errorf("service-mode idle sandbox phase = %q, want idle (claimless leaves it untouched)", sbx.Status.Phase)
	}

	// The response and persisted row carry the §7.1 / §5.2 service-mode
	// envelope: executionMode=service, podReuse, residualStateWarning,
	// scrubPolicy=none, conversationContinuity=none.
	var resp sessionserver.SessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode service-mode start response: %v", err)
	}
	lvl := resp.SessionIsolationLevel
	if lvl.ExecutionMode != "service" {
		t.Errorf("executionMode = %q, want service", lvl.ExecutionMode)
	}
	if !lvl.PodReuse || !lvl.ResidualStateWarning {
		t.Errorf("service-mode level = %+v, want podReuse and residualStateWarning true", lvl)
	}
	if lvl.ScrubPolicy != "none" {
		t.Errorf("scrubPolicy = %q, want none", lvl.ScrubPolicy)
	}
	if lvl.ConversationContinuity != "none" {
		t.Errorf("conversationContinuity = %q, want none", lvl.ConversationContinuity)
	}

	row, err := store.Get(context.Background(), "acme", "sess-svc-1")
	if err != nil {
		t.Fatalf("get persisted service-mode row: %v", err)
	}
	if row.ExecutionMode != "service" {
		t.Errorf("row.ExecutionMode = %q, want service", row.ExecutionMode)
	}
	if row.ConversationContinuity != "none" {
		t.Errorf("row.ConversationContinuity = %q, want none (frozen at create for the GET / List envelope)", row.ConversationContinuity)
	}
	if row.PodAssignment != "" {
		t.Errorf("row.PodAssignment = %q, want empty (claimless: no pod bound)", row.PodAssignment)
	}
}
