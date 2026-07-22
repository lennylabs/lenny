// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component tests for the §15 built-in OpenAI-dialect adapter
// single-shot pod-binding model. Each test drives the OpenAI Chat
// Completions handler wired to a PodExecutor-backed sessionserver.Server
// on a real kube-apiserver (envtest), exercising the bind → dispatch →
// release lifecycle, the synchronous release on dispatch failure and
// request-context timeout, the two-code pool/credential exhaustion
// fail-closed mapping, and the admission-gate denials the create-and-start
// path enforces. The claim-launch-registerBinding sequence uses SSA Apply
// per §4.6.3, which the controller-runtime fake client does not implement
// (kubernetes/kubernetes#115598), so the harness needs envtest.
//
// spec: §15 (single-shot compute model), §6.2 (release, terminal
// disposition), §4.6.3 (gateway writes no Sandbox.status), §7.1
// (create-and-start atomicity), §15.2.1 rule 1, §11.1, §10.6, §4.9.
package translators_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
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
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const ssNS = "lenny-agents"

// ssRespondingRuntime is an adapter.RuntimeProcess that replies to every
// written envelope with a §15.4.1 response frame, so the single-shot
// dispatch (exec.Send) returns a 200 rather than blocking on empty output.
type ssRespondingRuntime struct{ out chan []byte }

func (r *ssRespondingRuntime) Start(context.Context, string) error { return nil }
func (r *ssRespondingRuntime) WriteEnvelope(string, []byte) error {
	r.out <- []byte(`{"type":"response","text":"ack"}`)
	return nil
}

func (r *ssRespondingRuntime) Output(context.Context, string) (<-chan []byte, error) {
	return r.out, nil
}
func (r *ssRespondingRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *ssRespondingRuntime) Close(context.Context, string) error           { return nil }

func ssScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme lenny: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	return s
}

// ssEnvClient starts an envtest kube-apiserver, seeds the agent namespace,
// and creates each object. A seeded Sandbox.Status is split by §4.6.3 field
// ownership so the WarmPoolController-owned phase/podIP are applied under the
// WPC field manager, keeping the live claim path conflict-free.
func ssEnvClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	env := envtest.Start(t)
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: ssScheme(t)})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ssNS}}); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	for _, o := range objs {
		var seedAfter func()
		if sb, ok := o.(*lennyv1.Sandbox); ok {
			st := sb.Status
			sb.Status = lennyv1.SandboxStatus{}
			seedAfter = func() {
				wpc := map[string]interface{}{}
				if st.Phase != "" {
					wpc["phase"] = st.Phase
				}
				if st.PodIP != "" {
					wpc["podIP"] = st.PodIP
				}
				if len(wpc) == 0 {
					return
				}
				u := &unstructured.Unstructured{}
				u.SetAPIVersion(lennyv1.GroupVersion.String())
				u.SetKind("Sandbox")
				u.SetName(sb.Name)
				u.SetNamespace(sb.Namespace)
				_ = unstructured.SetNestedField(u.Object, wpc, "status")
				if err := c.Status().Patch(ctx, u, client.Apply,
					client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
					t.Fatalf("seed WPC status %s: %v", sb.Name, err)
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

func ssWarmPool(name, templateRef string) *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ssNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: templateRef, MinWarm: 1, MaxWarm: 5},
	}
}

func ssTemplate(name, runtimeRef string) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ssNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: runtimeRef, IsolationProfile: string(isolation.ProfileSandboxed)},
	}
}

func ssIdleSandbox(name, pool, podIP string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ssNS,
			Labels:    map[string]string{warmpool.LabelPool: pool},
		},
		Status: lennyv1.SandboxStatus{Phase: "idle", PodIP: podIP},
	}
}

// ssAdapterDialer serves an adapter backed by rt over bufconn and returns a
// DialAdapter func the binder uses to reach the pod's adapter.
func ssAdapterDialer(t *testing.T, rt adapter.RuntimeProcess) func(string) (*adapterclient.Client, error) {
	t.Helper()
	srv := adapter.New("singleshot-test")
	// Set the full §6.4 root layout, not just the whole-pod WorkspaceRoot, so
	// the per-slot materialization path (WorkspaceBase/slots/{slotId}) the
	// concurrent-workspace bind drives has a real tree to write into.
	base := t.TempDir()
	srv.WorkspaceRoot = filepath.Join(base, "workspace", "current")
	srv.WorkspaceBase = filepath.Join(base, "workspace")
	srv.SessionsRoot = filepath.Join(base, "sessions")
	srv.ArtifactsRoot = filepath.Join(base, "artifacts")
	srv.CredentialsDir = filepath.Join(base, "run", "lenny")
	srv.Runtime = rt
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

func ssBinder(c client.Client, dial func(string) (*adapterclient.Client, error)) *podsession.Binder {
	return &podsession.Binder{
		Client:           c,
		Namespace:        ssNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      dial,
	}
}

// ssRecyclingPool is the §5.2 poolstore record for a recycling exclusive
// (session-mode, maxConcurrentSessions:1) pool. On a clean terminal the pod
// recycles (claim patched bound → recycling); on a failed/timed-out session
// it retires (claim deleted), which is the disposition distinction the
// release path selects off the terminal outcome.
func ssRecyclingPool(name, runtimeRef string) poolstore.Pool {
	return poolstore.Pool{
		Name:          name,
		RuntimeRef:    runtimeRef,
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions: 1,
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          25,
			},
		},
	}
}

// ssConcurrentPool is the §5.2 poolstore record for a concurrent-workspace
// pool (maxConcurrentSessions > 1). A session on such a pool claims a per-slot
// reservation (ClaimSlot) rather than an exclusive whole-pod claim, its bind
// carries a non-empty SlotID, and its release routes through Binder.ReleaseSlot
// so sibling slots multiplexed on the same pod survive.
func ssConcurrentPool(name, runtimeRef string) poolstore.Pool {
	return poolstore.Pool{
		Name:          name,
		RuntimeRef:    runtimeRef,
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions: 4,
			// spec: §5.2 — maxConcurrentSessions > 1 requires the deployer
			// process-level-isolation acknowledgment; the poolstore rejects the
			// pool without it.
			AcknowledgeProcessLevelIsolation: true,
		},
	}
}

// ssSlotBinder is ssBinder wired with a miniredis-backed §5.2 slot counter,
// the intra-pod capacity gate the concurrent-session ClaimSlot / BindSlot path
// requires; a binder with no counter fails closed on the slot path.
func ssSlotBinder(t *testing.T, c client.Client, dial func(string) (*adapterclient.Client, error)) *podsession.Binder {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	b := ssBinder(c, dial)
	b.SlotCounter = slotcounter.New(rc)
	return b
}

// ssPodClaimExists reports whether the per-pod occupancy SandboxClaim exists
// for pod. The per-pod claim is the §5.2 occupancy authority: session-mode
// Release deletes it, while a per-slot ReleaseSlot keeps it while any sibling
// slot on the pod still runs.
func ssPodClaimExists(t *testing.T, c client.Client, pod string) bool {
	t.Helper()
	_, err := ssClaim(t, c, pod)
	if err == nil {
		return true
	}
	if apierrors.IsNotFound(err) {
		return false
	}
	t.Fatalf("get per-pod claim for %s: %v", pod, err)
	return false
}

// ssRecycleBoundary satisfies podsession.RecycleBoundaryArmer without a live
// coordinator so the recycle-release path can arm the missing-report timeout
// and return.
type ssRecycleBoundary struct{}

func (ssRecycleBoundary) OnRecycling(string) {}

// ssSingleShotBinder adapts *sessionserver.Server to
// translator.SingleShotBinder, mirroring the cmd/lenny-gateway wiring: it
// runs the shared create-and-start service (admission gates + claim + launch
// + registerBinding) and maps a typed ServiceError into a SingleShotError so
// the adapter re-emits the code and Retry-After in its native envelope.
type ssSingleShotBinder struct{ srv *sessionserver.Server }

func (b ssSingleShotBinder) BindSingleShot(ctx context.Context, spec translator.SingleShotSpec) (string, error) {
	resp, serr := b.srv.CreateAndStartService(ctx, spec.TenantID, sessionserver.CreateSessionRequest{
		RuntimeRef:  spec.RuntimeRef,
		UserID:      spec.UserID,
		Environment: spec.Environment,
	})
	if serr != nil {
		return "", &translator.SingleShotError{
			HTTPStatus:        serr.HTTPStatus,
			Code:              serr.Code,
			Message:           serr.Message,
			RetryAfterSeconds: serr.RetryAfterSeconds,
			Retryable:         serr.Retryable,
		}
	}
	return resp.ID, nil
}

// ssSpyExecutor wraps a real PodExecutor to observe the single-shot dispatch
// without changing the release path: Send counts its invocations and can
// substitute a dispatch error or block until the request context is
// cancelled, while Close and Release delegate to the real PodExecutor so the
// pod-drain and lease-release run against the live binder.
type ssSpyExecutor struct {
	inner    *executor.PodExecutor
	registry *podsession.Registry

	sendCalls    int
	sawSlotID    string
	sawBinding   bool
	sendErr      error
	blockUntilTO bool
	// cancelOnSend, when set with blockUntilTO, cancels the request context
	// at dispatch time (after the bind completed), modelling a client
	// disconnect or request-context timeout that lands mid-dispatch.
	cancelOnSend context.CancelFunc
}

func (e *ssSpyExecutor) Send(ctx context.Context, sessionID string, msgs []executor.Message) (executor.Response, error) {
	e.sendCalls++
	if bind, ok := e.registry.Get(sessionID); ok {
		e.sawBinding = true
		e.sawSlotID = bind.SlotID
	}
	if e.blockUntilTO {
		if e.cancelOnSend != nil {
			e.cancelOnSend()
		}
		<-ctx.Done()
		return executor.Response{}, ctx.Err()
	}
	if e.sendErr != nil {
		return executor.Response{}, e.sendErr
	}
	return e.inner.Send(ctx, sessionID, msgs)
}

func (e *ssSpyExecutor) Close(ctx context.Context, sessionID string) error {
	return e.inner.Close(ctx, sessionID)
}

func (e *ssSpyExecutor) Release(ctx context.Context, sessionID string, disp executor.Disposition) error {
	return e.inner.Release(ctx, sessionID, disp)
}

var _ executor.SessionReleaser = (*ssSpyExecutor)(nil)

// ssChatHandler builds the OpenAI Chat handler wired to the sessionserver
// binder and the spy executor over the real PodExecutor, sharing store.
func ssChatHandler(store sessionstore.Store, srv *sessionserver.Server, spy *ssSpyExecutor) *translator.OpenAIChatHandler {
	return translator.NewOpenAIChatHandler(store, spy, translator.OpenAIChatOptions{
		SingleShotBinder: ssSingleShotBinder{srv: srv},
		DefaultRuntime:   "echo",
	})
}

// ssDriveChat issues one POST /v1/chat/completions and returns the recorder.
// The optional mutate hook stamps a principal or a bounded context onto the
// request before dispatch.
func ssDriveChat(t *testing.T, h http.Handler, model string, mutate func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if mutate != nil {
		req = mutate(req)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func ssClaim(t *testing.T, c client.Client, pod string) (*lennyv1.SandboxClaim, error) {
	t.Helper()
	var claim lennyv1.SandboxClaim
	err := c.Get(context.Background(), client.ObjectKey{Namespace: ssNS, Name: podclaim.ClaimName(pod)}, &claim)
	return &claim, err
}

func ssLiveClaimCount(t *testing.T, c client.Client) int {
	t.Helper()
	var list lennyv1.SandboxClaimList
	if err := c.List(context.Background(), &list, client.InNamespace(ssNS)); err != nil {
		t.Fatalf("list SandboxClaims: %v", err)
	}
	return len(list.Items)
}

func ssErrCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, rr.Body.String())
	}
	return env.Error.Code
}

// spec: §15 (single-shot bind and release), §6.2 (completed terminal
// disposition selects the clean recycle outcome), §4.6.3 (gateway writes no
// Sandbox.status).
// diagnosis: the built-in OpenAI adapter must claim a warm pod through the
// shared create-and-start service, register the binding the PodExecutor reads
// (so dispatch succeeds rather than 500 "not bound to a pod"), and release it
// within the one HTTP call. A failure here means the bind-dispatch-release
// lifecycle regressed: either the binding was never registered (dispatch
// would 500), the release did not remove it (the pod leaks), or a completed
// turn on a recycling pool retired the pod instead of recycling it (the claim
// was deleted rather than patched bound → recycling).
func TestSingleShotBindsDispatchesAndRecyclesOnCompletion_spec_15(t *testing.T) {
	cluster := ssEnvClient(
		t,
		ssWarmPool("echo-pool", "echo-tmpl"),
		ssTemplate("echo-tmpl", "echo"),
		ssIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	binder := ssBinder(cluster, ssAdapterDialer(t, &ssRespondingRuntime{out: make(chan []byte, 8)}))
	binder.RecycleBoundary = ssRecycleBoundary{}
	registry := podsession.NewRegistry()

	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), ssRecyclingPool("echo-pool", "echo")); err != nil {
		t.Fatalf("create recycling pool: %v", err)
	}
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-ss-ok" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          ssNS,
		Pools:                   pools,
	})
	spy := &ssSpyExecutor{inner: executor.NewPodExecutor(registry, binder), registry: registry}

	rr := ssDriveChat(t, ssChatHandler(store, srv, spy).Handler(), "echo", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("dispatch status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	// A successful dispatch proves the binding was present in the registry
	// during exec.Send (streamFor resolves it, else Send would fail closed
	// with "not bound to a pod").
	if !spy.sawBinding {
		t.Error("dispatch ran without a registered pod binding")
	}
	if spy.sawSlotID != "" {
		t.Errorf("exclusive session-mode bind carried slotId %q, want empty", spy.sawSlotID)
	}
	// The deferred release removed the binding within the one HTTP call.
	if _, ok := registry.Get("sess-ss-ok"); ok {
		t.Error("binding still present after the single-shot request returned")
	}
	// The handler persisted the completed disposition to the session row
	// (§6.2). This is asserted independently of the recycle outcome below:
	// the disposition passed to Binder.Release is derived from the session's
	// completion store.Update, so a regression that recycled the pod while
	// dropping or reordering that Update (leaving the row not-completed) would
	// still pass the claim-recycling check. Reading the row back pins the
	// completed state the handler must record.
	sess, err := store.Get(context.Background(), "acme", "sess-ss-ok")
	if err != nil {
		t.Fatalf("session row missing after a completed single-shot turn: %v", err)
	}
	if sess.State != session.StateCompleted {
		t.Errorf("session row state = %q, want %q (handler records the completed disposition)", sess.State, session.StateCompleted)
	}
	// completed disposition on a recycling pool: the per-pod claim is patched
	// bound → recycling (§6.2 clean-terminal recycle), not deleted. The
	// gateway writes no Sandbox.status (§4.6.3).
	claim, err := ssClaim(t, cluster, "sbx-1")
	if err != nil {
		t.Fatalf("per-pod claim missing after a completed recycle: %v", err)
	}
	if claim.Status.Phase != string(claimstate.Recycling) {
		t.Errorf("claim binding state = %q, want recycling (completed disposition recycles)", claim.Status.Phase)
	}
}

// spec: §15 (single-shot bind and release), §5.2 (per-slot multiplexing:
// releasing one slot leaves a sibling slot's pod live), §6.2 (per-slot vs
// session-mode release), §4.6.3 (gateway writes no Sandbox.status).
// diagnosis: on a concurrent-workspace pool (maxConcurrentSessions > 1) the
// single-shot adapter must claim a per-slot reservation, dispatch with the
// resolved slotId stamped on the registry binding, and release that one slot
// through Binder.ReleaseSlot so a concurrently-held sibling slot on the same
// pod survives. A failure means the slot path regressed: the bind carried no
// slotId during exec.Send (the executor fails closed with SLOT_ID_REQUIRED or
// misroutes into another slot), the binding was not removed after the call, or
// the release drained the whole pod through session-mode Release (which
// deletes the per-pod claim and tears down the sibling) instead of decrementing
// only the request's slot.
func TestSingleShotConcurrentSlotBindDispatchReleaseKeepsSibling_spec_5_2(t *testing.T) {
	cluster := ssEnvClient(
		t,
		ssWarmPool("echo-pool", "echo-tmpl"),
		ssTemplate("echo-tmpl", "echo"),
		ssIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	binder := ssSlotBinder(t, cluster, ssAdapterDialer(t, &ssRespondingRuntime{out: make(chan []byte, 8)}))
	binder.RecycleBoundary = ssRecycleBoundary{}
	registry := podsession.NewRegistry()

	// A sibling session already holds a slot on the shared pod. It is bound
	// directly through the same binder so the pod genuinely multiplexes a
	// concurrent slot the single-shot release must leave intact.
	sibling, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: "echo-pool", SessionID: "sess-sibling", TenantID: "acme", Runtime: "echo",
		MaxConcurrentSessions: 4, Plan: &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("bind sibling slot: %v", err)
	}
	defer sibling.Adapter.Close()
	if !ssPodClaimExists(t, cluster, "sbx-1") {
		t.Fatal("sibling BindSlot did not create the per-pod occupancy claim")
	}

	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), ssConcurrentPool("echo-pool", "echo")); err != nil {
		t.Fatalf("create concurrent pool: %v", err)
	}
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-ss-slot" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          ssNS,
		Pools:                   pools,
	})
	spy := &ssSpyExecutor{inner: executor.NewPodExecutor(registry, binder), registry: registry}

	rr := ssDriveChat(t, ssChatHandler(store, srv, spy).Handler(), "echo", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("concurrent-slot dispatch status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !spy.sawBinding {
		t.Error("dispatch ran without a registered pod binding")
	}
	// A concurrent-workspace bind stamps the resolved slotId onto the registry
	// binding; the executor reads it to route the per-slot message. The slotId
	// equals the session id (§5.2), so a non-empty value matching the request's
	// session id proves the slot path ran during exec.Send rather than the
	// exclusive session-mode path (which carries an empty SlotID).
	if spy.sawSlotID != "sess-ss-slot" {
		t.Errorf("concurrent-slot bind carried slotId %q during dispatch, want sess-ss-slot", spy.sawSlotID)
	}
	// The deferred release removed the request's binding within the one call.
	if _, ok := registry.Get("sess-ss-slot"); ok {
		t.Error("binding still present after the single-shot request returned")
	}
	// The release routed through Binder.ReleaseSlot: it decremented only the
	// request's slot, so the per-pod claim survives while the sibling slot runs.
	// A session-mode Release would have drained the pod and deleted the claim.
	if !ssPodClaimExists(t, cluster, "sbx-1") {
		t.Error("the per-pod claim was deleted on release; the sibling slot's pod was torn down (session-mode drain instead of ReleaseSlot)")
	}
}

// spec: §15 (synchronous release on dispatch failure), §6.2 (failed terminal
// disposition retires the pod), §4.6.3 (gateway writes no Sandbox.status).
// diagnosis: a dispatch (exec.Send) error must still release the claimed pod
// within the one HTTP call, and the release must carry the failed
// disposition, which retires the pod (deletes the per-pod claim) even on a
// recycling pool. A regression that released with the completed disposition
// on a failure exit would recycle the claim (patch it bound → recycling)
// rather than delete it, leaving the pod bound to a phantom recycle; this
// test's NotFound assertion fails that regression.
func TestSingleShotReleasesAndRetiresOnDispatchError_spec_15(t *testing.T) {
	cluster := ssEnvClient(
		t,
		ssWarmPool("echo-pool", "echo-tmpl"),
		ssTemplate("echo-tmpl", "echo"),
		ssIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	binder := ssBinder(cluster, ssAdapterDialer(t, &ssRespondingRuntime{out: make(chan []byte, 8)}))
	binder.RecycleBoundary = ssRecycleBoundary{}
	registry := podsession.NewRegistry()
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), ssRecyclingPool("echo-pool", "echo")); err != nil {
		t.Fatalf("create recycling pool: %v", err)
	}
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-ss-senderr" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          ssNS,
		Pools:                   pools,
	})
	spy := &ssSpyExecutor{
		inner:    executor.NewPodExecutor(registry, binder),
		registry: registry,
		sendErr:  errors.New("dispatch boom"),
	}

	rr := ssDriveChat(t, ssChatHandler(store, srv, spy).Handler(), "echo", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("dispatch-error status = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
	if spy.sendCalls != 1 {
		t.Errorf("exec.Send called %d times, want 1 (the failed dispatch)", spy.sendCalls)
	}
	if _, ok := registry.Get("sess-ss-senderr"); ok {
		t.Error("binding still present after a dispatch-error release")
	}
	// failed disposition retires the pod: the per-pod claim is deleted, not
	// patched to recycling.
	if _, err := ssClaim(t, cluster, "sbx-1"); !apierrors.IsNotFound(err) {
		t.Errorf("per-pod claim get after a failed release = %v, want NotFound (failed disposition retires the pod)", err)
	}
}

// spec: §15 (synchronous release on request timeout), §6.2 (failed terminal
// disposition), §4.6.3.
// diagnosis: a request-context timeout or client disconnect mid-dispatch must
// still drain the claimed pod. The release runs on a context detached from
// the request context (context.WithoutCancel) bounded by a fresh timeout, so
// the DeleteClaim drain completes even though the request context is already
// cancelled. A regression that released on the request context would see
// context.Canceled from the binder's Get/DeleteClaim and leak the pod; this
// test asserts the claim is actually deleted after the cancelled-request
// exit, observing the drain completing rather than only that a release call
// was made.
func TestSingleShotReleaseDrainsOnDetachedContextAfterTimeout_spec_15(t *testing.T) {
	cluster := ssEnvClient(
		t,
		ssWarmPool("echo-pool", "echo-tmpl"),
		ssTemplate("echo-tmpl", "echo"),
		ssIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	binder := ssBinder(cluster, ssAdapterDialer(t, &ssRespondingRuntime{out: make(chan []byte, 8)}))
	binder.RecycleBoundary = ssRecycleBoundary{}
	registry := podsession.NewRegistry()
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), ssRecyclingPool("echo-pool", "echo")); err != nil {
		t.Fatalf("create recycling pool: %v", err)
	}
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-ss-timeout" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          ssNS,
		Pools:                   pools,
	})
	// The request context is cancelled at dispatch time (after the bind
	// completes), modelling a client disconnect or request-context timeout
	// that lands mid-dispatch. Cancelling from within Send keeps the bind's
	// apiserver calls off the cancelled deadline and isolates the failure to
	// the dispatch, where the deferred release must still drain the pod.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	spy := &ssSpyExecutor{
		inner:        executor.NewPodExecutor(registry, binder),
		registry:     registry,
		blockUntilTO: true,
		cancelOnSend: cancel,
	}
	rr := ssDriveChat(t, ssChatHandler(store, srv, spy).Handler(), "echo", func(r *http.Request) *http.Request {
		return r.WithContext(ctx)
	})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("timeout dispatch status = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := registry.Get("sess-ss-timeout"); ok {
		t.Error("binding still present after the timeout release")
	}
	// The detached release context drove the DeleteClaim drain to completion
	// even though the request context was cancelled.
	if _, err := ssClaim(t, cluster, "sbx-1"); !apierrors.IsNotFound(err) {
		t.Errorf("per-pod claim get after a cancelled-request release = %v, want NotFound (the drain completes on the detached context)", err)
	}
}

// spec: §7.1 (pod-claim exhaustion rollback), §15 (retryable claim failure).
// diagnosis: a warm-pod claim exhaustion must fail closed through the adapter
// envelope as 503 SESSION_CREATION_FAILED with a Retry-After header, without
// dispatching. This pins the ServiceError.RetryAfterSeconds propagation the
// tier-1 stub-binder test fabricates and cannot exercise: the value is read
// from the recorder's Retry-After header on a real create-and-start claim
// failure. A failure here means the adapter emitted a generic server_error,
// dropped the Retry-After, or dispatched against an unclaimed pod.
func TestSingleShotPoolExhaustionReturnsRetryableEnvelope_spec_7_1(t *testing.T) {
	// A warm pool and template exist, but no idle Sandbox: the claim finds no
	// pod and reports pool exhaustion.
	cluster := ssEnvClient(
		t,
		ssWarmPool("echo-pool", "echo-tmpl"),
		ssTemplate("echo-tmpl", "echo"),
	)
	binder := ssBinder(cluster, ssAdapterDialer(t, &ssRespondingRuntime{out: make(chan []byte, 8)}))
	registry := podsession.NewRegistry()
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-ss-exhaust" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          ssNS,
	})
	spy := &ssSpyExecutor{inner: executor.NewPodExecutor(registry, binder), registry: registry}

	rr := ssDriveChat(t, ssChatHandler(store, srv, spy).Handler(), "echo", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("pool-exhaustion status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if code := ssErrCode(t, rr); code != "SESSION_CREATION_FAILED" {
		t.Errorf("error code = %q, want SESSION_CREATION_FAILED", code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("a retryable pool-claim exhaustion must carry a Retry-After header")
	}
	if spy.sendCalls != 0 {
		t.Errorf("exec.Send called %d times on a failed claim, want 0", spy.sendCalls)
	}
	if ssLiveClaimCount(t, cluster) != 0 {
		t.Error("a pod claim leaked on an exhausted-pool create")
	}
}

// spec: §4.9 (CREDENTIAL_POOL_EXHAUSTED, no pod claimed or wasted), §7.1
// (pre-claim credential availability check), §15.
// diagnosis: a credential-availability pre-check miss must fail closed as 503
// CREDENTIAL_POOL_EXHAUSTED with NO Retry-After header, before any pod is
// claimed and without dispatching. This pins that ServiceError.RetryAfterSeconds
// is zero for this case (the recorder sets no header) while it is non-zero for
// the pool-exhaustion case, the exact two-code distinction the tier-1 stub
// binder cannot exercise. A failure here means the adapter attached a
// Retry-After to a non-retryable credential rejection, claimed a pod anyway,
// or dispatched.
func TestSingleShotCredentialExhaustionHasNoRetryAfter_spec_4_9(t *testing.T) {
	cluster := ssEnvClient(
		t,
		ssWarmPool("echo-pool", "echo-tmpl"),
		ssTemplate("echo-tmpl", "echo"),
		ssIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	binder := ssBinder(cluster, ssAdapterDialer(t, &ssRespondingRuntime{out: make(chan []byte, 8)}))
	registry := podsession.NewRegistry()

	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: credential.CredentialPolicy{
		PreferredSource: credential.PreferredSourcePool,
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
		},
	}}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "echo", SupportedProviders: []string{"anthropic_direct"}}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	// The credential pool exists but its only credential is revoked, so the
	// §7.1-step-3 pre-check finds no assignable provider.
	credPools := credentialpoolstore.NewMemory()
	if err := credPools.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "claude-prod", Provider: "anthropic_direct",
		MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{
			{ID: "claude-prod-cred-a", SecretRef: "secret-claude-prod", Status: credentialpoolstore.CredentialRevoked},
		},
	}); err != nil {
		t.Fatalf("create credential pool: %v", err)
	}

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-ss-credexhaust" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          ssNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
	})
	spy := &ssSpyExecutor{inner: executor.NewPodExecutor(registry, binder), registry: registry}

	rr := ssDriveChat(t, ssChatHandler(store, srv, spy).Handler(), "echo", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("credential-exhaustion status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if code := ssErrCode(t, rr); code != "CREDENTIAL_POOL_EXHAUSTED" {
		t.Errorf("error code = %q, want CREDENTIAL_POOL_EXHAUSTED", code)
	}
	if ra := rr.Header().Get("Retry-After"); ra != "" {
		t.Errorf("Retry-After = %q, want absent on the non-retryable CREDENTIAL_POOL_EXHAUSTED", ra)
	}
	if spy.sendCalls != 0 {
		t.Errorf("exec.Send called %d times on a credential pre-check miss, want 0", spy.sendCalls)
	}
	if ssLiveClaimCount(t, cluster) != 0 {
		t.Error("a pod was claimed despite the pre-claim credential miss; §4.9 requires no pod claimed or wasted")
	}
}

// ssRejectInterceptor is a §4.8 policy-chain interceptor that always rejects.
type ssRejectInterceptor struct{}

func (ssRejectInterceptor) Name() string                       { return "policy-block" }
func (ssRejectInterceptor) Priority() int32                    { return 150 }
func (ssRejectInterceptor) Builtin() bool                      { return false }
func (ssRejectInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (ssRejectInterceptor) Timeout() time.Duration             { return 0 }
func (ssRejectInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return interceptor.Result{Action: interceptor.ActionReject, Reason: "denied"}, nil
}

func ssRejectChain(t *testing.T) *interceptor.Chain {
	t.Helper()
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePostAuth, ssRejectInterceptor{}); err != nil {
		t.Fatalf("register interceptor: %v", err)
	}
	return chain
}

func ssRunningRow(id, tenant, user, rt string) sessionstore.Session {
	now := time.Now().UTC()
	return sessionstore.Session{
		ID: id, TenantID: tenant, UserID: user, RuntimeRef: rt,
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	}
}

// TestSingleShotAdmissionGateDenialsFailClosed drives an admission-denied
// create-and-start through the real sessionserver binder for each gate the
// create-and-start path runs, asserting the gate's native status and code
// surface in the adapter's native envelope, no warm pod is claimed, and
// exec.Send is never called. It carries a dedicated case for each of the
// three gates this build adds to the create-and-start path (concurrency,
// admission-rate, environment-admission) alongside the pre-existing gates
// (inactive-user, session-quota, policy-chain), so a regression that
// re-omitted either §11.1 gate fails its own case rather than passing on an
// alternate deny.
//
// spec: §15.2.1 rule 1 (shared-service admission), §11.1 (concurrency,
// admission-rate), §10.6 (environment-admission), §11.4 (user gate), §11.2
// (session quota), §4.8 (policy chain).
// diagnosis: the external OpenAI-dialect surface routes through the
// gate-running create-and-start service so it fails closed on admission
// rather than fail-open by dispatching an unadmitted turn. A failure here
// means a gate was skipped on the create-and-start path (the request was
// admitted and a pod claimed), or the gate's native envelope did not reach
// the adapter, or the adapter dispatched despite the denial.
func TestSingleShotAdmissionGateDenialsFailClosed_spec_15_2_1(t *testing.T) {
	// One envtest cluster and binder shared across the gate cases: every gate
	// rejects before the claim, so the idle Sandbox is never consumed and each
	// case asserts zero live claims.
	cluster := ssEnvClient(
		t,
		ssWarmPool("echo-pool", "echo-tmpl"),
		ssTemplate("echo-tmpl", "echo"),
		ssIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	binder := ssBinder(cluster, ssAdapterDialer(t, &ssRespondingRuntime{out: make(chan []byte, 8)}))
	registry := podsession.NewRegistry()

	principal := func(r *http.Request) *http.Request {
		return r.WithContext(authmw.WithPrincipal(r.Context(),
			authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"}))
	}

	// deniedTenant returns a tenant store with acme configured for the given
	// concurrent-session quota and no-environment policy.
	cases := []struct {
		name     string
		wantCode string
		wantStat int
		opts     func(store sessionstore.Store) sessionserver.Options
		mutate   func(*http.Request) *http.Request
		seed     func(store sessionstore.Store)
	}{
		{
			name: "concurrency-limit", wantCode: "QUOTA_EXCEEDED", wantStat: http.StatusTooManyRequests,
			opts: func(sessionstore.Store) sessionserver.Options {
				return sessionserver.Options{MaxConcurrentSessionsPerRuntime: 1}
			},
			seed: func(store sessionstore.Store) {
				if err := store.Create(context.Background(), ssRunningRow("run-echo-0", "acme", "alice", "echo")); err != nil {
					t.Fatalf("seed running row: %v", err)
				}
			},
		},
		{
			name: "admission-rate-limit", wantCode: "RATE_LIMITED", wantStat: http.StatusTooManyRequests,
			opts: func(sessionstore.Store) sessionserver.Options {
				fixed := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
				c := rlcounter.NewMemory()
				// Consume the per-runtime allowance in the same minute bucket the
				// request lands in (fixed clock), so the adapter request is the
				// over-limit one without a preceding successful dispatch. The key
				// mirrors the §11.1 per-runtime scope: rt:<tenant>:<runtime>.
				if _, err := c.Incr(context.Background(), "rt:acme:echo", fixed); err != nil {
					t.Fatalf("seed admission-rate counter: %v", err)
				}
				return sessionserver.Options{
					Clock:                     func() time.Time { return fixed },
					AdmissionRateLimitCounter: c,
					PerRuntimePerMinute:       1,
				}
			},
		},
		{
			name: "environment-admission", wantCode: "FORBIDDEN", wantStat: http.StatusForbidden,
			opts: func(sessionstore.Store) sessionserver.Options {
				ts := tenantstore.NewMemory()
				if err := ts.Create(context.Background(), tenantstore.Tenant{
					ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyDenyAll,
				}); err != nil {
					t.Fatalf("seed tenant: %v", err)
				}
				return sessionserver.Options{
					Tenants:                    ts,
					Environments:               environmentstore.NewMemory(),
					DefaultNoEnvironmentPolicy: tenantstore.NoEnvPolicyDenyAll,
				}
			},
			mutate: principal,
		},
		{
			name: "inactive-user", wantCode: "USER_INVALIDATED", wantStat: http.StatusForbidden,
			opts: func(sessionstore.Store) sessionserver.Options {
				us := userstore.NewMemory()
				if err := us.Create(context.Background(), userstore.User{
					Subject: "alice@acme.com", TenantID: "acme", Disabled: true,
				}); err != nil {
					t.Fatalf("seed user: %v", err)
				}
				return sessionserver.Options{Users: us}
			},
			mutate: principal,
		},
		{
			name: "session-quota", wantCode: "QUOTA_EXCEEDED", wantStat: http.StatusTooManyRequests,
			opts: func(sessionstore.Store) sessionserver.Options {
				ts := tenantstore.NewMemory()
				if err := ts.Create(context.Background(), tenantstore.Tenant{ID: "acme", MaxConcurrentSessions: 1}); err != nil {
					t.Fatalf("seed tenant: %v", err)
				}
				return sessionserver.Options{Tenants: ts}
			},
			seed: func(store sessionstore.Store) {
				if err := store.Create(context.Background(), ssRunningRow("quota-echo-0", "acme", "alice", "echo")); err != nil {
					t.Fatalf("seed running row: %v", err)
				}
			},
		},
		{
			name: "policy-chain", wantCode: "QUOTA_EXCEEDED", wantStat: http.StatusTooManyRequests,
			opts: func(sessionstore.Store) sessionserver.Options {
				return sessionserver.Options{Interceptors: ssRejectChain(t)}
			},
			mutate: principal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			if tc.seed != nil {
				tc.seed(store)
			}
			opts := tc.opts(store)
			opts.IDFunc = func() string { return "sess-ss-gate-" + tc.name }
			opts.DefaultIsolationProfile = isolation.ProfileSandboxed
			opts.PodBinder = binder
			opts.PodRegistry = registry
			opts.AgentNamespace = ssNS
			srv := sessionserver.New(store, opts)
			spy := &ssSpyExecutor{inner: executor.NewPodExecutor(registry, binder), registry: registry}

			rr := ssDriveChat(t, ssChatHandler(store, srv, spy).Handler(), "echo", tc.mutate)
			if rr.Code != tc.wantStat {
				t.Fatalf("%s: status = %d, want %d; body=%s", tc.name, rr.Code, tc.wantStat, rr.Body.String())
			}
			if code := ssErrCode(t, rr); code != tc.wantCode {
				t.Errorf("%s: error code = %q, want %q; body=%s", tc.name, code, tc.wantCode, rr.Body.String())
			}
			if spy.sendCalls != 0 {
				t.Errorf("%s: exec.Send called %d times on a denied create, want 0", tc.name, spy.sendCalls)
			}
			if ssLiveClaimCount(t, cluster) != 0 {
				t.Errorf("%s: a pod was claimed on a gate-denied create, want none", tc.name)
			}
			if registry.Len() != 0 {
				t.Errorf("%s: registry holds %d bindings on a gate-denied create, want 0", tc.name, registry.Len())
			}
		})
	}
}
