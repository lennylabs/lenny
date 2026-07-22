// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §8.2 delegated-child materialization steps
// that follow admission on lenny/delegate_task. Service.Delegate commits a
// delegated child in session.StateCreated with a stamped WorkspacePlan and
// no PodAssignment; the handler then drives the child through the shared
// create-and-start engine (MaterializeDelegatedChild) to claim a warm pod,
// assign the credential lease, stream the workspace through the §6.3 binder,
// launch, and transition the child to running, publishing the bind into the
// shared executor registry so the parent can interact with the running child.
//
// The flow is driven end to end through the real lenny/delegate_task MCP
// handler over a real delegation.Service and a real *sessionserver.Server
// wired as the mcptools ChildMaterializer, its pod binder, and the real
// credential-pool minting path over an envtest-backed warm pool. The real
// PodExecutor over the shared podsession.Registry confirms the materialized
// child is bound: task input the handler delivers reaches the pod rather than
// hitting the "not bound to a pod" rejection (pkg/gateway/session/executor/
// pod.go). The handler's fail-closed mapping of the engine's typed credential
// and pool sentinels to the canonical MCP tool codes is exercised against a
// real post-pod-claim assignment race (spec/08 line 470) and a real
// still-warming pool.
//
// The provAnthropic/poolAnthropic constants, the crossEnvNS namespace, the
// eagerAdapterDialer, childIDCounter, mustCreateRuntime, and
// crossEnvErrorEnvelope helpers, and the poolRecordingAssigner double live in
// cross_environment_delegation_test.go and eager_claim_lifecycle_test.go (same
// package).

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// The parent, the assignable child, the assignment-race child, and the
// still-warming child each run a distinct runtime backed by its own warm pool,
// so a claim, a rollback, or a warming rejection in one sub-test does not
// perturb the pod-claim state another sub-test asserts against on the shared
// envtest cluster.
const (
	matParentRuntime  = "planner"
	matOKRuntime      = "child-worker-ok"
	matRaceRuntime    = "child-worker-race"
	matWarmingRuntime = "child-worker-warming"

	matOKPool      = "pool-ok"
	matRacePool    = "pool-race"
	matWarmingPool = "pool-warming"

	matOKSandbox   = "sbx-ok"
	matRaceSandbox = "sbx-race"
)

// materializeFixedClock pins delegate_task response timestamps so the flow is
// deterministic across sub-tests.
func materializeFixedClock() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// materializeRespondingRuntime is an adapter.RuntimeProcess that replies to
// every written envelope with a §15.4.1 response frame, so the PodExecutor
// Attach round-trip against a materialized child returns a concrete response
// rather than blocking. It mirrors the executor package's respondingRuntime.
type materializeRespondingRuntime struct{ out chan []byte }

func (r *materializeRespondingRuntime) Start(context.Context, string) error { return nil }

func (r *materializeRespondingRuntime) WriteEnvelope(string, []byte) error {
	r.out <- []byte(`{"type":"response","text":"ack"}`)
	return nil
}

func (r *materializeRespondingRuntime) Output(context.Context, string) (<-chan []byte, error) {
	return r.out, nil
}
func (r *materializeRespondingRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *materializeRespondingRuntime) Close(context.Context, string) error           { return nil }

// materializeRaceAssigner fails every AssignProto, modelling the §8.3 line 470
// post-pod-claim credential-assignment race: the pre-claim resolution found the
// pool yet the lease mint fails in the assignment window after the warm pod is
// claimed, so the engine must release the pod and surface the assignment-race
// sentinel.
type materializeRaceAssigner struct{}

func (materializeRaceAssigner) AssignProto(_, _, _, _ string) (*adapterv1.CredentialLease, error) {
	return nil, credential.ErrPoolExhausted
}
func (materializeRaceAssigner) ReleaseSession(string) {}

// materializeCluster boots one envtest control plane and seeds the three warm
// pools the sub-tests claim from: an assignable pool with an idle pod, an
// assignment-race pool with its own idle pod, and a still-warming pool with no
// idle pod whose SandboxTemplate carries the §5.2 PoolWarmingUp condition.
func materializeCluster(t *testing.T) client.Client {
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

	seedMaterializePool(t, c, matOKPool, "tmpl-ok", matOKRuntime)
	seedMaterializeIdleSandbox(t, c, matOKSandbox, matOKPool, "10.244.3.1")

	seedMaterializePool(t, c, matRacePool, "tmpl-race", matRaceRuntime)
	seedMaterializeIdleSandbox(t, c, matRaceSandbox, matRacePool, "10.244.3.2")

	seedMaterializePool(t, c, matWarmingPool, "tmpl-warming", matWarmingRuntime)
	seedMaterializeWarming(t, c, "tmpl-warming", matWarmingPool)

	return c
}

// seedMaterializePool creates a SandboxWarmPool and its SandboxTemplate for the
// given runtime under the sandboxed §5.3 profile.
func seedMaterializePool(t *testing.T, c client.Client, pool, tmpl, runtimeRef string) {
	t.Helper()
	ctx := context.Background()
	if err := c.Create(ctx, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: pool, Namespace: crossEnvNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: tmpl, MinWarm: 1, MaxWarm: 5},
	}); err != nil {
		t.Fatalf("create warm pool %s: %v", pool, err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: tmpl, Namespace: crossEnvNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: runtimeRef, IsolationProfile: string(isolation.ProfileSandboxed)},
	}); err != nil {
		t.Fatalf("create template %s: %v", tmpl, err)
	}
}

// seedMaterializeIdleSandbox creates an idle Sandbox in the pool and stamps its
// status under the WarmPoolController field owner, matching the production
// Apply path the create/claim path reads.
func seedMaterializeIdleSandbox(t *testing.T, c client.Client, name, pool, podIP string) {
	t.Helper()
	ctx := context.Background()
	if err := c.Create(ctx, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: crossEnvNS,
			Labels: map[string]string{warmpool.LabelPool: pool},
		},
	}); err != nil {
		t.Fatalf("create sandbox %s: %v", name, err)
	}
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName(name)
	u.SetNamespace(crossEnvNS)
	_ = unstructured.SetNestedField(u.Object, map[string]interface{}{"phase": "idle", "podIP": podIP}, "status")
	if err := c.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed idle sandbox status %s: %v", name, err)
	}
}

// seedMaterializeWarming stamps the §5.2 PoolWarmingUp condition on the pool's
// SandboxTemplate and a warm-but-not-ready count on the pool, so ResolvePool
// returns the PoolWarmingError the materialization surfaces as
// RUNTIME_UNAVAILABLE. The pool has no idle Sandbox.
func seedMaterializeWarming(t *testing.T, c client.Client, tmpl, pool string) {
	t.Helper()
	ctx := context.Background()
	var template lennyv1.SandboxTemplate
	if err := c.Get(ctx, client.ObjectKey{Namespace: crossEnvNS, Name: tmpl}, &template); err != nil {
		t.Fatalf("get template %s: %v", tmpl, err)
	}
	template.Status.Conditions = []metav1.Condition{{
		Type:               "PoolWarmingUp",
		Status:             metav1.ConditionTrue,
		Reason:             "Provisioning",
		Message:            "bootstrapping",
		LastTransitionTime: metav1.Now(),
	}}
	if err := c.Status().Update(ctx, &template); err != nil {
		t.Fatalf("seed warming template status %s: %v", tmpl, err)
	}
	var warm lennyv1.SandboxWarmPool
	if err := c.Get(ctx, client.ObjectKey{Namespace: crossEnvNS, Name: pool}, &warm); err != nil {
		t.Fatalf("get warm pool %s: %v", pool, err)
	}
	warm.Status.WarmCount, warm.Status.ReadyCount = 2, 0
	if err := c.Status().Update(ctx, &warm); err != nil {
		t.Fatalf("seed warming pool status %s: %v", pool, err)
	}
}

// materializeStores builds the tenant policy, runtime registry, and credential
// pool the materialization engine resolves the child's warm pod and credential
// lease from. The acme tenant pools anthropic_direct, and every child runtime
// supports anthropic_direct so resolveCredentialPools resolves a non-empty
// provider→pool map.
func materializeStores(t *testing.T) (tenantstore.Store, runtimestore.Store, credentialpoolstore.Store) {
	t.Helper()
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
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
	runtimes := runtimestore.NewMemory()
	for _, name := range []string{matParentRuntime, matOKRuntime, matRaceRuntime, matWarmingRuntime} {
		mustCreateRuntime(t, runtimes, runtimestore.Runtime{
			Name: name, Type: runtimestore.TypeAgent,
			SupportedProviders: []string{provAnthropic},
		})
	}
	credPools := credentialpoolstore.NewMemory()
	if err := credPools.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: poolAnthropic, Provider: provAnthropic, MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{
			{ID: poolAnthropic + "-cred", SecretRef: "secret-" + poolAnthropic, Status: credentialpoolstore.CredentialActive},
		},
	}); err != nil {
		t.Fatalf("create credential pool: %v", err)
	}
	return tenants, runtimes, credPools
}

// materializeServer wires the real lenny/delegate_task MCP handler over a real
// delegation.Service and a real *sessionserver.Server as the §8.2
// ChildMaterializer, its pod binder over the shared envtest cluster, and the
// real PodExecutor over the shared registry so a materialized child is
// interactable. A running parent session (sess_parent) on matParentRuntime is
// committed. The §8.3 pre-check (CredAvailability) is left unwired so the
// handler reaches materialization for every admitted child.
func materializeServer(
	t *testing.T,
	cluster client.Client,
	tenants tenantstore.Store,
	runtimes runtimestore.Store,
	credPools credentialpoolstore.Store,
	assigner podsession.CredentialAssigner,
	rt adapter.RuntimeProcess,
) (*mcp.Server, sessionstore.Store, *podsession.Registry, executor.Executor) {
	t.Helper()
	adapterSrv := adapter.New("materialize-adapter")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.StagingDir = t.TempDir()
	adapterSrv.CredentialsDir = t.TempDir()
	adapterSrv.Runtime = rt

	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        crossEnvNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      eagerAdapterDialer(t, adapterSrv),
		Blobs:            blobstore.NewMemoryStore(nil),
		Credentials:      assigner,
	}

	store := memstore.New()
	registry := podsession.NewRegistry()
	sessionSrv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "unused" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          crossEnvNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
		Blobs:                   binder.Blobs,
	})
	podExec := executor.NewPodExecutor(registry, binder)

	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:             store,
		Executor:          podExec,
		Runtimes:          runtimes,
		ChildMaterializer: sessionSrv,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc:   childIDCounter(),
			Runtimes: runtimes,
		}),
		Clock:    materializeFixedClock,
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})

	now := materializeFixedClock()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "alice@acme.com",
		State:      session.StateRunning,
		RuntimeRef: matParentRuntime, IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create parent session: %v", err)
	}
	return srv, store, registry, podExec
}

// materializeDelegateCall invokes lenny/delegate_task against the child runtime
// target under the given credential-propagation mode with the given task input.
// It returns the decoded JSON-RPC response. The materialization-to-running case
// drives credentialPropagation: inherit, under which the child draws its lease
// from the parent's origin pool, so the §8.2 inherit resolution runs end to end
// through claim-and-start rather than the independent self-origin mint path.
func materializeDelegateCall(t *testing.T, srv *mcp.Server, target, propagation, taskInput string) map[string]any {
	t.Helper()
	args := `{"parentSessionId":"sess_parent","target":"` + target +
		`","poolRef":"pool-b","credentialPropagation":"` + propagation + `",` +
		`"task":{"input":[{"type":"text","inline":"` + taskInput + `"}]}}`
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/delegate_task","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode delegate_task response: %v; body=%s", err, rr.Body.String())
	}
	return resp
}

// materializeHandle decodes the §8.2 TaskHandle from a successful delegate_task
// result.
func materializeHandle(t *testing.T, resp map[string]any) struct {
	ChildSessionID string `json:"childSessionId"`
	State          string `json:"state"`
	RuntimeRef     string `json:"runtimeRef"`
} {
	t.Helper()
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("delegate_task returned a tool error: %+v", resp)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("delegate_task result carried no content: %+v", resp)
	}
	c0, _ := content[0].(map[string]any)
	text, _ := c0["text"].(string)
	var handle struct {
		ChildSessionID string `json:"childSessionId"`
		State          string `json:"state"`
		RuntimeRef     string `json:"runtimeRef"`
	}
	if err := json.Unmarshal([]byte(text), &handle); err != nil {
		t.Fatalf("TaskHandle is not valid JSON: %v (raw=%q)", err, text)
	}
	return handle
}

// spec: 8.2 (steps 5-9 delegated-child materialization), 8.2 (step 9 parent
// interacts with running child).
// diagnosis: a delegated child must materialize to running within delegate_task
// — claim a warm pod, be assigned a credential lease, launch, and publish its
// bind into the shared executor registry — so the parent receives a running,
// addressable child and the task input the handler delivers reaches the bound
// pod. A failure here means the child stayed in created without a pod (the T-
// 8.2.17 divergence), never claimed a pod, or launched but stayed unregistered
// so Executor.Send rejects it as unbound (pod.go: "is not bound to a pod").
func TestDelegateTaskMaterializesChildToRunning_spec_8_2(t *testing.T) {
	envtest.SkipUnlessAvailable(t)
	cluster := materializeCluster(t)
	tenants, runtimes, credPools := materializeStores(t)
	assigner := &poolRecordingAssigner{}
	srv, store, registry, podExec := materializeServer(
		t, cluster, tenants, runtimes, credPools, assigner,
		&materializeRespondingRuntime{out: make(chan []byte, 8)},
	)

	resp := materializeDelegateCall(t, srv, matOKRuntime, "inherit", "do work")
	handle := materializeHandle(t, resp)

	// The child materialized to running, and the handle is the parent-facing
	// virtual MCP child interface: an addressable child id plus its resolved
	// runtime.
	if handle.State != string(session.StateRunning) {
		t.Errorf("handle.state = %q, want running (the post-materialization state)", handle.State)
	}
	if handle.ChildSessionID == "" {
		t.Error("handle.childSessionId is empty; the parent has no virtual child interface to address")
	}
	if handle.RuntimeRef != matOKRuntime {
		t.Errorf("handle.runtimeRef = %q, want %q", handle.RuntimeRef, matOKRuntime)
	}

	// The persisted child row carries the claimed warm pod and reads running.
	row, err := store.Get(context.Background(), "acme", handle.ChildSessionID)
	if err != nil {
		t.Fatalf("get child row: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("child row state = %q, want running", row.State)
	}
	if row.PodAssignment == "" {
		t.Error("child row has no PodAssignment; the materialization claimed no warm pod")
	}
	// spec: §8.3 lines 472, 488 — the inherit hop threads the parent as the
	// child's credential origin, so the inherit resolution (not the
	// independent self-origin mint) ran through claim-and-start. An
	// independent child would carry its own id here.
	if row.CredentialOriginSessionID != "sess_parent" {
		t.Errorf("child CredentialOriginSessionID = %q, want sess_parent (the inherit hop draws from the parent's origin pool)",
			row.CredentialOriginSessionID)
	}
	// The credential lease was assigned to the child during the launch,
	// constrained to the origin∩child provider intersection (spec/08 §8.3
	// line 470): the parent runtime and the child runtime both support
	// anthropic_direct, so the inherit child draws from poolAnthropic.
	if pools := assigner.assignedPools(); len(pools) != 1 || pools[0] != poolAnthropic {
		t.Errorf("assigned pools = %v, want [%s] (the inherit child drew its lease from the origin∩child pool at materialization)", pools, poolAnthropic)
	}

	// registerBinding published the child's bind so the executor resolves it:
	// the interaction Executor.Send inside delegate_task already reached the
	// bound pod (the call did not error). Confirm the registry binding and a
	// direct Send round-trip, pinning that streamFor no longer rejects the
	// materialized session as unbound.
	if _, ok := registry.Get(handle.ChildSessionID); !ok {
		t.Fatal("registry holds no binding for the materialized child; Executor.Send would reject it as unbound")
	}
	out, err := podExec.Send(context.Background(), handle.ChildSessionID, []executor.Message{
		{Role: "user", Content: "follow-up"},
	})
	if err != nil {
		t.Fatalf("Executor.Send to the materialized child failed: %v (a bound child must be interactable)", err)
	}
	if len(out.Parts) != 1 || out.Parts[0].Text != "ack" {
		t.Errorf("Send output = %+v, want one text part \"ack\" from the bound child", out)
	}
}

// spec: 8.3 (line 470 post-claim assignment race).
// diagnosis: when a delegated child's credential assignment races and loses
// after its warm pod is claimed, the handler must fail closed: return
// CREDENTIAL_POOL_EXHAUSTED to the delegating parent and release the claimed
// pod so no warm pod leaks. A failure here means the handler surfaced the wrong
// tool code, admitted a running child on a failed assignment, or leaked the
// loser's warm pod, which spec/08 line 470 forbids.
func TestDelegateTaskMaterializationAssignmentRaceFailsClosed_spec_8_3(t *testing.T) {
	envtest.SkipUnlessAvailable(t)
	cluster := materializeCluster(t)
	tenants, runtimes, credPools := materializeStores(t)
	srv, store, registry, _ := materializeServer(
		t, cluster, tenants, runtimes, credPools, materializeRaceAssigner{},
		&materializeRespondingRuntime{out: make(chan []byte, 8)},
	)

	resp := materializeDelegateCall(t, srv, matRaceRuntime, "independent", "do work")
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("an assignment-race materialization must be a tool error: %+v", resp)
	}
	env := crossEnvErrorEnvelope(t, result)
	if env["code"] != "CREDENTIAL_POOL_EXHAUSTED" {
		t.Errorf("code = %v, want CREDENTIAL_POOL_EXHAUSTED", env["code"])
	}
	// spec: §15 — CREDENTIAL_POOL_EXHAUSTED is POLICY / 503, retryable.
	if env["category"] != "POLICY" {
		t.Errorf("category = %v, want POLICY", env["category"])
	}
	if env["retryable"] != true {
		t.Errorf("retryable = %v, want true", env["retryable"])
	}

	// The claimed warm pod was released on rollback: its per-pod SandboxClaim
	// is deleted, so no warm pod leaks past the losing assignment.
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: crossEnvNS, Name: "claim-" + matRaceSandbox}, &claim); err == nil {
		t.Error("per-pod claim still present after the assignment race; the claimed warm pod leaked")
	}
	// The registry holds no binding for a child that failed materialization,
	// and the child row did not reach running.
	if registry.Len() != 0 {
		t.Errorf("registry holds %d bindings, want 0 (the raced child must not be bound)", registry.Len())
	}
	rows, err := store.List(context.Background(), "acme", sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	for _, r := range rows {
		if r.ParentSessionID == "sess_parent" && r.State == session.StateRunning {
			t.Errorf("child %s reached running on a failed assignment; the materialization did not fail closed", r.ID)
		}
	}
}

// spec: 8.2 (steps 5-9 delegated-child materialization).
// diagnosis: a materialization failure whose engine sentinel the §8.3 pre-check
// never emits must still map to its canonical MCP tool code. A child whose warm
// pool is still bootstrapping surfaces the §5.2 PoolWarmingError, which the
// handler maps to RUNTIME_UNAVAILABLE. A failure here means the new
// sentinel-to-tool-code mapping mislabeled a pool-warming outcome (for example
// as a credential exhaustion) or swallowed it as INTERNAL_ERROR.
func TestDelegateTaskMaterializationPoolWarmingMapsRuntimeUnavailable_spec_8_2(t *testing.T) {
	envtest.SkipUnlessAvailable(t)
	cluster := materializeCluster(t)
	tenants, runtimes, credPools := materializeStores(t)
	srv, _, registry, _ := materializeServer(
		t, cluster, tenants, runtimes, credPools, &poolRecordingAssigner{},
		&materializeRespondingRuntime{out: make(chan []byte, 8)},
	)

	resp := materializeDelegateCall(t, srv, matWarmingRuntime, "independent", "do work")
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a still-warming pool must reject the materialization: %+v", resp)
	}
	env := crossEnvErrorEnvelope(t, result)
	if env["code"] != "RUNTIME_UNAVAILABLE" {
		t.Errorf("code = %v, want RUNTIME_UNAVAILABLE", env["code"])
	}
	// spec: §5.2 — RUNTIME_UNAVAILABLE is TRANSIENT / 503, retryable.
	if env["category"] != "TRANSIENT" {
		t.Errorf("category = %v, want TRANSIENT", env["category"])
	}
	if env["retryable"] != true {
		t.Errorf("retryable = %v, want true", env["retryable"])
	}
	// A warming-pool rejection fires before any pod claim, so no child is bound.
	if registry.Len() != 0 {
		t.Errorf("registry holds %d bindings, want 0 (a warming-pool rejection claims no pod)", registry.Len())
	}
}
