// SPDX-License-Identifier: MIT

package podsession_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	"github.com/lennylabs/lenny/pkg/agentpodstate"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/vcscred"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// stubRestorer is an adapter.CheckpointSource serving a fixed archive.
type stubRestorer struct{ archive []byte }

func (s stubRestorer) LoadCheckpoint(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.archive)), nil
}

// emptyArchive returns a valid gzip-tar of an empty workspace.
func emptyArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := workspace.Archive(t.TempDir(), &buf); err != nil {
		t.Fatalf("build archive: %v", err)
	}
	return buf.Bytes()
}

const (
	testNS   = "lenny-agents"
	testPool = "claude-worker"
)

// fakeRuntime satisfies adapter.RuntimeProcess for the adapter server
// the binder's StartSession call drives.
type fakeRuntime struct {
	started string
}

func (f *fakeRuntime) Start(_ context.Context, sessionID string) error {
	f.started = sessionID
	return nil
}
func (f *fakeRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (f *fakeRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (f *fakeRuntime) Close(context.Context, string) error           { return nil }

func (f *fakeRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func idleSandbox(name, podIP string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: testPool},
		},
		Status: lennyv1.SandboxStatus{Phase: "idle", PodIP: podIP},
	}
}

// unlabeledSandbox is an idle Sandbox without the pool label. The
// label-selecting List in podclaim.Claimer.Claim does not see it, so
// the normal claim returns ErrNoIdlePod, but the Sandbox still exists
// by name for the §4.6.1 fallback claim path to Get and to flip
// idle → claimed. This models the §4.6.1 scenario where the gateway's
// Kubernetes-API view is degraded while the Postgres mirror still has
// the pod.
func unlabeledSandbox(name, podIP string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
		},
		Status: lennyv1.SandboxStatus{Phase: "idle", PodIP: podIP},
	}
}

// fakeMirror is an in-memory agentpodstate.Store for exercising the
// §4.6.1 Binder fallback claim without a Postgres container. ClaimIdle
// hands out idle pods from the pool's queue; lag is a fixed knob.
type fakeMirror struct {
	// idle maps poolID to the pod IDs the mirror reports as idle.
	idle map[string][]string
	// lag is the value MirrorLagSeconds returns for every pool.
	lag float64
	// claims records each (pool, pod, session, tenant) ClaimIdle served.
	claims []agentpodstate.PodState
}

func (m *fakeMirror) Sync(context.Context, string, []agentpodstate.PodState) error {
	return nil
}

func (m *fakeMirror) ReconcileAll(context.Context, []agentpodstate.PodState) error {
	return nil
}

func (m *fakeMirror) MirrorLagSeconds(context.Context, string) (float64, error) {
	return m.lag, nil
}

func (m *fakeMirror) ClaimIdle(_ context.Context, poolID, sessionID, tenantID string) (agentpodstate.PodState, bool, error) {
	pods := m.idle[poolID]
	if len(pods) == 0 {
		return agentpodstate.PodState{}, false, nil
	}
	podID := pods[0]
	m.idle[poolID] = pods[1:]
	claimed := agentpodstate.PodState{
		PodID: podID, PoolID: poolID, State: "claimed",
		SessionID: sessionID, TenantID: tenantID,
	}
	m.claims = append(m.claims, claimed)
	return claimed, true, nil
}

func k8sClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	// envtest backs the client with a real kube-apiserver so the
	// §4.6.3 SSA Apply path the gateway slot-claimer uses works.
	// The fake client does not yet implement SSA
	// (kubernetes/kubernetes#115598).
	env := envtest.Start(t)
	s := newScheme(t)
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: s})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: testNS},
	}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", testNS, err)
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
				if sbStatus.Phase == "" && sbStatus.PodName == "" &&
					sbStatus.NodeName == "" && sbStatus.PodIP == "" &&
					sbStatus.TenantID == "" && sbStatus.ActiveSlots == 0 {
					return
				}
				// Split the seed by §4.6.3 field ownership. WPC owns
				// Phase / PodName / NodeName / PodIP / ObservedGeneration;
				// the gateway owns ActiveSlots / TenantID. Seeding each
				// subset under its rightful manager keeps the production
				// Apply paths conflict-free.
				wpc := map[string]interface{}{}
				if sbStatus.Phase != "" {
					wpc["phase"] = sbStatus.Phase
				}
				if sbStatus.PodName != "" {
					wpc["podName"] = sbStatus.PodName
				}
				if sbStatus.NodeName != "" {
					wpc["nodeName"] = sbStatus.NodeName
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
				if sbStatus.ActiveSlots > 0 || sbStatus.TenantID != "" {
					gw := map[string]interface{}{
						"activeSlots": int64(sbStatus.ActiveSlots),
					}
					if sbStatus.TenantID != "" {
						gw["tenantId"] = sbStatus.TenantID
					}
					u := &unstructured.Unstructured{}
					u.SetAPIVersion(lennyv1.GroupVersion.String())
					u.SetKind("Sandbox")
					u.SetName(sb.Name)
					u.SetNamespace(sb.Namespace)
					_ = unstructured.SetNestedField(u.Object, gw, "status")
					if err := c.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.Gateway))); err != nil {
						t.Fatalf("seed gateway status Sandbox %s: %v", sb.Name, err)
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

// adapterDialer serves srv over an in-memory connection and returns a
// DialAdapter func wired to it.
func adapterDialer(t *testing.T, srv *adapter.Server) func(string) (*adapterclient.Client, error) {
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

func newBinder(c client.Client, dial func(string) (*adapterclient.Client, error)) *podsession.Binder {
	return &podsession.Binder{
		Client:           c,
		Namespace:        testNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      dial,
	}
}

// fakeAssigner is a podsession.CredentialAssigner for the binder's §4.9
// credential-assignment path. It returns a fixed proxy-mode lease per
// pool and records every Assign call, or fails when err is set.
type fakeAssigner struct {
	// err, when non-nil, is returned by every AssignProto call.
	err error
	// calls records each (pool, session, spiffeURI) AssignProto served.
	calls []assignerCall
	// released records each sessionID passed to ReleaseSession.
	released []string
}

type assignerCall struct {
	pool, session, spiffe, tenant string
}

func (a *fakeAssigner) AssignProto(pool, session, spiffe, tenant string) (*adapterv1.CredentialLease, error) {
	a.calls = append(a.calls, assignerCall{pool: pool, session: session, spiffe: spiffe, tenant: tenant})
	if a.err != nil {
		return nil, a.err
	}
	return &adapterv1.CredentialLease{
		LeaseId:  "cl-" + pool,
		Provider: pool,
		Payload: []byte(`{"deliveryMode":"proxy",` +
			`"materializedConfig":{"proxyUrl":"https://p/v1","leaseToken":"lt-` + pool + `"}}`),
	}, nil
}

func (a *fakeAssigner) ReleaseSession(sessionID string) {
	a.released = append(a.released, sessionID)
}

func TestBindClaimsAndStartsTheSession(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	if res.SandboxName != "sbx-1" || res.PodIP != "10.244.1.7" {
		t.Errorf("result = %+v, want sbx-1 / 10.244.1.7", res)
	}
	if res.Adapter == nil {
		t.Fatal("Bind returned no adapter connection")
	}
	if rt.started != "sess-1" {
		t.Errorf("runtime started for %q, want sess-1", rt.started)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	// spec: §6.2 lines 83-94 — a successful Bind advances the Sandbox through
	// the setup chain to attached.
	if sb.Status.Phase != "attached" {
		t.Errorf("sandbox phase = %q, want attached", sb.Status.Phase)
	}
}

// spec: §6.3 lines 358, 372 — Bind records the per-phase wall-clock
// durations on its result so the start path can attribute the §6.3
// latency budget. Each phase duration is non-negative and the recorded
// phases cannot together exceed the overall Bind wall-clock.
func TestBindRecordsPhaseTimings_spec_6_3(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	wallStart := time.Now()
	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
	})
	wall := time.Since(wallStart)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	tm := res.Timings
	phases := map[string]time.Duration{
		"pod_claim":                 tm.PodClaim,
		"workspace_materialization": tm.WorkspaceMaterialization,
		"setup_commands":            tm.SetupCommands,
		"credential_assignment":     tm.CredentialAssignment,
		"agent_session_start":       tm.AgentSessionStart,
	}
	var sum time.Duration
	for name, d := range phases {
		if d < 0 {
			t.Errorf("phase %q duration is negative: %v", name, d)
		}
		sum += d
	}
	if sum > wall {
		t.Errorf("recorded phase durations sum to %v, exceeding the %v Bind wall clock", sum, wall)
	}
	// The agent-start phase is the last RPC before the session is ready;
	// it must have been entered (the connect+start path runs real work).
	if tm.AgentSessionStart == 0 && tm.PodClaim == 0 {
		t.Error("Bind recorded no claim or agent-start time; phase instrumentation is not wired")
	}
}

func TestResumeClaimsAndRestoresTheSession(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt
	srv.Restorer = stubRestorer{archive: emptyArchive(t)}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Resume(context.Background(), podsession.ResumeRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
		Runtime: "claude-code", CheckpointID: "ckpt-1",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.Result == nil {
		t.Fatalf("Resume: nil BindResult")
	}
	defer res.Result.Adapter.Close()

	if res.Result.SandboxName != "sbx-1" || res.Result.PodIP != "10.244.1.7" {
		t.Errorf("result = %+v, want sbx-1 / 10.244.1.7", res.Result)
	}
	if rt.started != "sess-1" {
		t.Errorf("runtime started for %q, want sess-1 — Resume must start the runtime", rt.started)
	}
	// spec: §4.4 / §7.2 — the adapter signals mode=full on a healthy
	// full-checkpoint restore so the gateway can carry it onto the
	// session.resumed event. F-7.3.22.
	if res.Mode != "full" {
		t.Errorf("Mode = %q, want %q", res.Mode, "full")
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "claimed" {
		t.Errorf("sandbox phase = %q, want claimed", sb.Status.Phase)
	}
}

func TestResumeReturnsErrNoIdlePodWhenPoolEmpty(t *testing.T) {
	srv := adapter.New("adapter-test")
	binder := newBinder(k8sClient(t), adapterDialer(t, srv))

	_, err := binder.Resume(context.Background(), podsession.ResumeRequest{
		Pool: testPool, SessionID: "sess-1", CheckpointID: "ckpt-1",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod", err)
	}
}

// spec: §6.3 line 352, §16.1 line 122 — a successful Bind records one
// idle→claimed transition on `lenny_warmpool_claims_total{pool,
// runtime_class}`. The runtime_class label is mapped from the pod's
// §5.3 isolation profile so the §6.3 demotion-rate ratio per runtime
// class is observable from telemetry.
func TestBindRecordsWarmpoolClaim_spec_6_3_F_6_3_6(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt

	sb := idleSandbox("sbx-1", "10.244.1.7")
	sb.Spec.IsolationProfile = "standard" // → runc
	c := k8sClient(t, sb)
	binder := newBinder(c, adapterDialer(t, srv))
	var claims []struct {
		Pool         string
		RuntimeClass string
	}
	binder.ClaimAccepted = func(pool, runtimeClass string) {
		claims = append(claims, struct {
			Pool         string
			RuntimeClass string
		}{pool, runtimeClass})
	}

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	if len(claims) != 1 {
		t.Fatalf("want 1 ClaimAccepted call, got %d (%+v)", len(claims), claims)
	}
	if claims[0].Pool != testPool {
		t.Errorf("pool label: got %q, want %q", claims[0].Pool, testPool)
	}
	if claims[0].RuntimeClass != "runc" {
		t.Errorf("runtime_class label: got %q, want %q (standard maps to runc)", claims[0].RuntimeClass, "runc")
	}
}

// spec: §6.3 line 352 — Bind that fails after the claim does not record
// the claim. This matches the §6.3 line 348 SLO semantics: only
// successful claim transitions are counted.
func TestBindSkipsWarmpoolClaimOnAdapterDialFailure_spec_6_3_F_6_3_6(t *testing.T) {
	sb := idleSandbox("sbx-1", "") // no PodIP → connect fails on dial
	c := k8sClient(t, sb)
	binder := newBinder(c, adapterDialer(t, adapter.New("adapter-test")))
	var claims int
	binder.ClaimAccepted = func(_, _ string) { claims++ }

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1",
	})
	if err == nil {
		t.Fatal("Bind succeeded for a pod with no PodIP; want a failure")
	}
	if claims != 0 {
		t.Errorf("ClaimAccepted called %d times on failed Bind; want 0", claims)
	}
}

// spec: §6.3 line 352 — an unrecognized isolation profile is skipped
// rather than emitting an empty `runtime_class` series. The series
// must stay low-cardinality and meaningful.
func TestBindSkipsWarmpoolClaimOnUnknownIsolation_spec_6_3_F_6_3_6(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt

	sb := idleSandbox("sbx-1", "10.244.1.7")
	// IsolationProfile left empty: RuntimeClassName(empty) returns ok=false.
	c := k8sClient(t, sb)
	binder := newBinder(c, adapterDialer(t, srv))
	var claims int
	binder.ClaimAccepted = func(_, _ string) { claims++ }

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	if claims != 0 {
		t.Errorf("ClaimAccepted called %d times for unresolved runtime_class; want 0", claims)
	}
}

func TestBindReturnsErrNoIdlePodWhenPoolEmpty(t *testing.T) {
	srv := adapter.New("adapter-test")
	binder := newBinder(k8sClient(t), adapterDialer(t, srv))

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod", err)
	}
}

func TestBindFailsWhenSandboxHasNoPodIP(t *testing.T) {
	srv := adapter.New("adapter-test")
	c := k8sClient(t, idleSandbox("sbx-1", "")) // claimed pod has no IP recorded
	binder := newBinder(c, adapterDialer(t, srv))

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1",
	})
	if err == nil {
		t.Error("Bind succeeded for a pod with no IP, want a failure")
	}
}

func TestBindFailsOnIncompatibleProtocolVersion(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.ProtocolVersions = []string{"9.9.9"} // no version the gateway accepts
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1",
	})
	if err == nil {
		t.Error("Bind succeeded against an incompatible adapter, want a failure")
	}
}

func TestReleaseDrainsTheSandbox(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := binder.Release(context.Background(), res, state.Completed); err != nil {
		t.Fatalf("Release: %v", err)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "draining" {
		t.Errorf("sandbox phase = %q, want draining after Release", sb.Status.Phase)
	}
}

// TestReleaseReturnsCredentialLeasesToPool_spec_7_1 asserts the §7.1
// step 23 teardown: Release returns the session's §4.9 credential leases
// to the pool. Without this the credential's active-session counter
// leaks on every completed session.
func TestReleaseReturnsCredentialLeasesToPool_spec_7_1(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	srv.CredentialsDir = t.TempDir()

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	assigner := &fakeAssigner{}
	binder.Credentials = assigner

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-rel", Runtime: "claude-code",
		CredentialPools: map[string]string{"anthropic": "claude-prod"},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := binder.Release(context.Background(), res, state.Completed); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if len(assigner.released) != 1 || assigner.released[0] != "sess-rel" {
		t.Errorf("ReleaseSession calls = %v, want [sess-rel]", assigner.released)
	}
}

func TestReleaseFailsWhenSandboxGone(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if err := c.Delete(context.Background(), &sb); err != nil {
		t.Fatalf("delete sandbox: %v", err)
	}
	if err := binder.Release(context.Background(), res, state.Completed); err == nil {
		t.Error("Release succeeded though the Sandbox was deleted, want an error")
	}
}

func TestBindFailsWhenAStagingRPCFails(t *testing.T) {
	// No WorkspaceRoot: the FinalizeWorkspace RPC in the §4.7 sequence
	// fails, and Bind propagates that failure.
	srv := adapter.New("adapter-test")

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1",
	})
	if err == nil {
		t.Error("Bind succeeded though a staging RPC could not run, want a failure")
	}

	// spec: §6.2 lines 99-102 — a pre-attached setup-RPC failure best-effort
	// moves the Sandbox to the `failed` phase (here finalizing_workspace →
	// failed, after receiving_uploads → finalizing_workspace had advanced it).
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != string(state.Failed) {
		t.Errorf("sandbox phase = %q, want failed after a pre-attached RPC failure", sb.Status.Phase)
	}
}

// recordingPhaseClient wraps c so a test can assert the §6.2 transition
// sequence the gateway wrote, including phases that are transient in the
// final stored object (like the terminal disposition recorded just before
// draining). It records the Sandbox phase of every status-subresource Update.
// A wrapper struct is used rather than controller-runtime's interceptor.Funcs
// because the envtest client is a plain client.Client, not a client.WithWatch.
func recordingPhaseClient(c client.Client, phases *[]string) client.Client {
	return &phaseRecorder{Client: c, phases: phases}
}

type phaseRecorder struct {
	client.Client
	phases *[]string
}

func (r *phaseRecorder) Status() client.SubResourceWriter {
	return &phaseStatusWriter{SubResourceWriter: r.Client.Status(), phases: r.phases}
}

type phaseStatusWriter struct {
	client.SubResourceWriter
	phases *[]string
}

func (w *phaseStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if sb, ok := obj.(*lennyv1.Sandbox); ok {
		*w.phases = append(*w.phases, sb.Status.Phase)
	}
	return nil
}

// Patch records the §4.6.3 SSA Apply path the binder uses for the
// gateway-claimed phases (claimed, receiving_uploads, finalizing_workspace,
// running_setup, starting_session, attached, completed/failed/cancelled).
// The drain step still uses Update (yields to WPC); both paths are
// observed so the test asserts the full §6.2 transition sequence.
func (w *phaseStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	if err := w.SubResourceWriter.Patch(ctx, obj, patch, opts...); err != nil {
		return err
	}
	if u, ok := obj.(*unstructured.Unstructured); ok && u.GetKind() == "Sandbox" {
		if phase, ok, _ := unstructured.NestedString(u.Object, "status", "phase"); ok {
			*w.phases = append(*w.phases, phase)
		}
	}
	return nil
}

// TestBindWritesSetupChainPhases_spec_6_2 asserts Bind advances the Sandbox
// through the full §6.2 lines 83-94 setup chain (claimed →
// receiving_uploads → finalizing_workspace → running_setup →
// starting_session → attached) so the authoritative state machine
// (line 305) reflects each phase, not just claimed.
func TestBindWritesSetupChainPhases_spec_6_2(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	var phases []string
	c := recordingPhaseClient(k8sClient(t, idleSandbox("sbx-1", "10.244.1.7")), &phases)
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	want := []string{"claimed", "receiving_uploads", "finalizing_workspace", "running_setup", "starting_session", "attached"}
	if !equalStrings(phases, want) {
		t.Errorf("setup-chain phase sequence = %v, want %v", phases, want)
	}
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: res.SandboxName}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != string(state.Attached) {
		t.Errorf("final sandbox phase = %q, want attached", sb.Status.Phase)
	}
}

// TestReleaseRecordsTerminalPhaseThenDrains_spec_6_2 asserts Release records
// the session's terminal disposition on the Sandbox (§6.2 lines 105-117,
// attached → completed) before draining the exclusive pod (→ draining), so
// the state machine reflects how the session ended.
func TestReleaseRecordsTerminalPhaseThenDrains_spec_6_2(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	var phases []string
	c := recordingPhaseClient(k8sClient(t, idleSandbox("sbx-1", "10.244.1.7")), &phases)
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	phases = phases[:0] // drop the setup-chain phases; assert the release sequence
	if err := binder.Release(context.Background(), res, state.Completed); err != nil {
		t.Fatalf("Release: %v", err)
	}

	want := []string{"completed", "draining"}
	if !equalStrings(phases, want) {
		t.Errorf("release phase sequence = %v, want %v", phases, want)
	}
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: res.SandboxName}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != string(state.Draining) {
		t.Errorf("final sandbox phase = %q, want draining", sb.Status.Phase)
	}
}

// TestReleaseExpiredSkipsTerminalPhase_spec_6_2 asserts a disposition with no
// §6.2 edge from the current phase (attached → expired, which the state
// machine does not model) is skipped gracefully: Release drains the pod
// without recording the terminal phase, so reclamation is never blocked.
func TestReleaseExpiredSkipsTerminalPhase_spec_6_2(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	var phases []string
	c := recordingPhaseClient(k8sClient(t, idleSandbox("sbx-1", "10.244.1.7")), &phases)
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	phases = phases[:0]
	if err := binder.Release(context.Background(), res, state.Expired); err != nil {
		t.Fatalf("Release: %v", err)
	}

	want := []string{"draining"}
	if !equalStrings(phases, want) {
		t.Errorf("release phase sequence = %v, want %v (expired has no edge from attached)", phases, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBindStagesUploadFile(t *testing.T) {
	// A plan with an uploadFile source: Bind fetches the blob, streams
	// it via PrepareWorkspace, and FinalizeWorkspace materializes it.
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	root := t.TempDir()
	srv.WorkspaceRoot = root
	srv.StagingDir = t.TempDir()
	srv.Runtime = rt

	blobs := blobstore.NewMemoryStore(nil)
	uri := blobstore.URI{
		TenantID: "acme", SessionID: "sess-1", PartID: "part-1",
		TTL: time.Hour, Encoding: blobstore.Encoding,
	}
	if _, err := blobs.Put(uri, "application/octet-stream",
		bytes.NewReader([]byte("uploaded payload"))); err != nil {
		t.Fatalf("put blob: %v", err)
	}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	binder.Blobs = blobs

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{Type: "uploadFile", Path: "data/payload.bin", UploadRef: uri.String()},
			},
		},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	got, err := os.ReadFile(filepath.Join(root, "data", "payload.bin"))
	if err != nil {
		t.Fatalf("read materialized upload: %v", err)
	}
	if string(got) != "uploaded payload" {
		t.Errorf("materialized upload = %q, want %q", got, "uploaded payload")
	}
}

// tempGitRepo creates a one-commit local git repository and returns
// its path and the commit SHA.
func tempGitRepo(t *testing.T) (dir, sha string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=alice", "GIT_AUTHOR_EMAIL=alice@acme.com",
			"GIT_COMMITTER_NAME=alice", "GIT_COMMITTER_EMAIL=alice@acme.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "service.go"), []byte("package service"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "service.go")
	run("commit", "-m", "initial")
	return dir, run("rev-parse", "HEAD")
}

func TestBindClonesGitSource(t *testing.T) {
	// A gitClone source: Bind clones the repository on the gateway's
	// network path, streams the tree via PrepareWorkspace, and
	// FinalizeWorkspace materializes it.
	repo, sha := tempGitRepo(t)

	srv := adapter.New("adapter-test")
	root := t.TempDir()
	srv.WorkspaceRoot = root
	srv.StagingDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{Type: "gitClone", Path: "checkout", Url: repo, ResolvedCommitSha: sha},
			},
		},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	got, err := os.ReadFile(filepath.Join(root, "checkout", "service.go"))
	if err != nil {
		t.Fatalf("read cloned file: %v", err)
	}
	if string(got) != "package service" {
		t.Errorf("cloned file = %q, want %q", got, "package service")
	}
}

func TestBindRejectsAuthenticatedGitCloneWithNoResolver(t *testing.T) {
	// spec: §14 line 95 — an authenticated gitClone needs a wired VCS
	// credential resolver. With none, Bind fails with a clear error rather
	// than cloning unauthenticated.
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.StagingDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{
					Type: "gitClone", Path: ".",
					Url:               "https://example.com/acme/private.git",
					ResolvedCommitSha: "0123456789abcdef0123456789abcdef01234567",
					Auth:              &adapterv1.GitAuth{Mode: "credential-lease", LeaseScope: "vcs.github.read"},
				},
			},
		},
	})
	if err == nil {
		t.Error("Bind succeeded for an authenticated gitClone with no resolver, want a failure")
	}
}

// stubVCSResolver records the Resolve call and returns a fixed credential
// or error.
type stubVCSResolver struct {
	cred    vcscred.Credential
	err     error
	gotURL  string
	gotTen  string
	gotScpe string
	calls   int
}

func (s *stubVCSResolver) Resolve(_ context.Context, tenantID, gitURL, leaseScope string) (vcscred.Credential, error) {
	s.calls++
	s.gotTen, s.gotURL, s.gotScpe = tenantID, gitURL, leaseScope
	return s.cred, s.err
}

func TestBindClonesAuthenticatedGitClone(t *testing.T) {
	// spec: §14 line 95 — an authenticated gitClone resolves its VCS
	// credential on the gateway and clones on the gateway's network path.
	// The local file:// remote ignores the injected HTTP header, so this
	// exercises the binder's resolver-call-and-thread wiring end to end.
	repo, sha := tempGitRepo(t)

	srv := adapter.New("adapter-test")
	root := t.TempDir()
	srv.WorkspaceRoot = root
	srv.StagingDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	resolver := &stubVCSResolver{cred: vcscred.Credential{Username: "x-access-token", Token: "ghs_secret"}}
	binder.VCSCreds = resolver

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{
					Type: "gitClone", Path: "checkout", Url: repo, ResolvedCommitSha: sha,
					Auth: &adapterv1.GitAuth{Mode: "credential-lease", LeaseScope: "vcs.github.read"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	if resolver.calls != 1 {
		t.Errorf("VCS resolver called %d times, want 1", resolver.calls)
	}
	if resolver.gotTen != "acme" || resolver.gotURL != repo || resolver.gotScpe != "vcs.github.read" {
		t.Errorf("resolver got (tenant=%q url=%q scope=%q), want (acme, %q, vcs.github.read)",
			resolver.gotTen, resolver.gotURL, resolver.gotScpe, repo)
	}
	got, err := os.ReadFile(filepath.Join(root, "checkout", "service.go"))
	if err != nil {
		t.Fatalf("read cloned file: %v", err)
	}
	if string(got) != "package service" {
		t.Errorf("cloned file = %q, want %q", got, "package service")
	}
}

func TestBindFailsWhenVCSCredentialResolveFails(t *testing.T) {
	// spec: §14 — a credential-resolution failure aborts the bind.
	repo, sha := tempGitRepo(t)

	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.StagingDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	binder.VCSCreds = &stubVCSResolver{err: errors.New("pool exhausted")}

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{
					Type: "gitClone", Path: "checkout", Url: repo, ResolvedCommitSha: sha,
					Auth: &adapterv1.GitAuth{Mode: "credential-lease", LeaseScope: "vcs.github.read"},
				},
			},
		},
	})
	if err == nil {
		t.Error("Bind succeeded despite a VCS credential-resolution failure")
	}
}

func TestBindFailsWhenUploadPlanHasNoBlobStore(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.StagingDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv)) // no Blobs configured

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{Type: "uploadFile", Path: "f.bin", UploadRef: "lenny-blob://acme/sess-1/part-1?ttl=600"},
			},
		},
	})
	if err == nil {
		t.Error("Bind succeeded for an upload plan with no blob store, want a failure")
	}
}

// spec: 4.6.1
// diagnosis: the §4.6.1 Postgres-backed fallback claim in
// podsession.Binder.connect did not behave as specified. When the
// Kubernetes-API claim returns ErrNoIdlePod and a Fallback mirror is
// configured, connect must claim an idle pod from the mirror, create
// the binding SandboxClaim CRD (so the lenny-sandboxclaim-guard webhook
// still guards the single-claim invariant), and best-effort flip the
// Sandbox phase idle → claimed; when the mirror lag exceeds the
// freshness threshold the fallback must be skipped and the original
// ErrNoIdlePod returned; and when the mirror is also exhausted the
// fallback must return ErrNoIdlePod for the caller to surface as
// WARM_POOL_EXHAUSTED.
func TestBindFallsBackToPostgresWhenKubeClaimFindsNoIdlePod(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt

	// The Sandbox exists by name but carries no pool label, so the
	// normal label-selecting claim returns ErrNoIdlePod. The mirror
	// still reports the pod idle, so the fallback claims it.
	c := k8sClient(t, unlabeledSandbox("sbx-fb", "10.244.2.9"))
	binder := newBinder(c, adapterDialer(t, srv))
	mirror := &fakeMirror{idle: map[string][]string{testPool: {"sbx-fb"}}, lag: 2}
	binder.Fallback = mirror

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Bind via fallback: %v", err)
	}
	defer res.Adapter.Close()

	if res.SandboxName != "sbx-fb" || res.PodIP != "10.244.2.9" {
		t.Errorf("result = %+v, want sbx-fb / 10.244.2.9 claimed via the fallback", res)
	}
	if rt.started != "sess-1" {
		t.Errorf("runtime started for %q, want sess-1", rt.started)
	}

	// The fallback served exactly one claim, stamped with the session.
	if len(mirror.claims) != 1 || mirror.claims[0].PodID != "sbx-fb" ||
		mirror.claims[0].SessionID != "sess-1" || mirror.claims[0].TenantID != "acme" {
		t.Errorf("mirror claims = %+v, want one claim of sbx-fb for sess-1/acme", mirror.claims)
	}

	// The fallback created the binding SandboxClaim, so the
	// lenny-sandboxclaim-guard webhook's CREATE-time check still guards
	// the single-claim invariant.
	var claim lennyv1.SandboxClaim
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: testNS, Name: "claim-sess-1"}, &claim); err != nil {
		t.Fatalf("the fallback did not create a SandboxClaim: %v", err)
	}
	if claim.Spec.SandboxRef != "sbx-fb" || claim.Spec.SessionID != "sess-1" ||
		claim.Spec.TenantID != "acme" {
		t.Errorf("SandboxClaim spec = %+v, want a binding of sess-1/acme to sbx-fb", claim.Spec)
	}

	// The fallback flipped the Sandbox idle → claimed, then the full Bind
	// advanced it through the §6.2 setup chain to attached (the fallback
	// claim feeds the same Bind path as a normal claim).
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: testNS, Name: "sbx-fb"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "attached" {
		t.Errorf("sandbox phase = %q, want attached after the fallback claim + Bind", sb.Status.Phase)
	}
}

func TestResumeFallsBackToPostgresWhenKubeClaimFindsNoIdlePod(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt
	srv.Restorer = stubRestorer{archive: emptyArchive(t)}

	c := k8sClient(t, unlabeledSandbox("sbx-fb", "10.244.2.9"))
	binder := newBinder(c, adapterDialer(t, srv))
	binder.Fallback = &fakeMirror{idle: map[string][]string{testPool: {"sbx-fb"}}, lag: 1}

	res, err := binder.Resume(context.Background(), podsession.ResumeRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
		Runtime: "claude-code", CheckpointID: "ckpt-1",
	})
	if err != nil {
		t.Fatalf("Resume via fallback: %v", err)
	}
	if res.Result == nil {
		t.Fatalf("Resume via fallback: nil BindResult")
	}
	defer res.Result.Adapter.Close()

	if res.Result.SandboxName != "sbx-fb" {
		t.Errorf("resumed onto %q, want sbx-fb claimed via the fallback", res.Result.SandboxName)
	}
	// Resume and Bind share connect, so the fallback benefits both.
	var claim lennyv1.SandboxClaim
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: testNS, Name: "claim-sess-1"}, &claim); err != nil {
		t.Errorf("the fallback did not create a SandboxClaim for Resume: %v", err)
	}
}

func TestBindFallbackSkippedWhenMirrorIsStale(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, unlabeledSandbox("sbx-fb", "10.244.2.9"))
	binder := newBinder(c, adapterDialer(t, srv))
	// The mirror has an idle pod, but its lag exceeds the default 10s
	// freshness threshold, so the fallback must be skipped.
	mirror := &fakeMirror{idle: map[string][]string{testPool: {"sbx-fb"}}, lag: 25}
	binder.Fallback = mirror

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod when the mirror is too stale to trust", err)
	}
	if len(mirror.claims) != 0 {
		t.Errorf("the fallback claimed %d pod(s) despite a stale mirror, want 0", len(mirror.claims))
	}
}

func TestBindFallbackHonorsCustomMirrorLagThreshold(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, unlabeledSandbox("sbx-fb", "10.244.2.9"))
	binder := newBinder(c, adapterDialer(t, srv))
	// A 4s lag would pass the default 10s threshold, but a configured
	// 3s threshold rejects it.
	mirror := &fakeMirror{idle: map[string][]string{testPool: {"sbx-fb"}}, lag: 4}
	binder.Fallback = mirror
	binder.FallbackMaxMirrorLagSeconds = 3

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod when lag exceeds the configured threshold", err)
	}
	if len(mirror.claims) != 0 {
		t.Errorf("the fallback claimed despite lag above the configured threshold")
	}
}

func TestBindFallbackReturnsErrNoIdlePodWhenMirrorAlsoExhausted(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	binder := newBinder(k8sClient(t), adapterDialer(t, srv))
	// Fresh mirror, but no idle pod: the warm pool is genuinely
	// exhausted, so the fallback returns ErrNoIdlePod for the caller to
	// surface as WARM_POOL_EXHAUSTED.
	binder.Fallback = &fakeMirror{idle: map[string][]string{}, lag: 1}

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod when the mirror is also exhausted", err)
	}
}

func TestBindWithoutFallbackReturnsErrNoIdlePod(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	// No Fallback configured: the no-idle-pod result surfaces directly.
	binder := newBinder(k8sClient(t), adapterDialer(t, srv))

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod with no fallback configured", err)
	}
}

// spec: §4.6.1 "Fallback preconditions" precondition 1 — a stale mirror
// skips the fallback and records lenny_pod_claim_fallback_skipped_total
// with reason mirror_stale.
func TestBindFallbackSkipRecordsMirrorStale(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, unlabeledSandbox("sbx-fb", "10.244.2.9"))
	binder := newBinder(c, adapterDialer(t, srv))
	mirror := &fakeMirror{idle: map[string][]string{testPool: {"sbx-fb"}}, lag: 99}
	binder.Fallback = mirror
	var skips []string
	binder.FallbackSkipped = func(reason string) { skips = append(skips, reason) }

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod when the mirror is stale", err)
	}
	if len(skips) != 1 || skips[0] != podsession.FallbackSkipReasonMirrorStale {
		t.Errorf("skips = %v, want [%q]", skips, podsession.FallbackSkipReasonMirrorStale)
	}
	if len(mirror.claims) != 0 {
		t.Errorf("the fallback claimed despite a stale mirror")
	}
}

// spec: §4.6.1 "Fallback preconditions" precondition 2 — a failed API
// server reachability probe skips the fallback before locking a mirror
// row and records reason apiserver_unreachable.
func TestBindFallbackSkipRecordsAPIServerUnreachable(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, unlabeledSandbox("sbx-fb", "10.244.2.9"))
	binder := newBinder(c, adapterDialer(t, srv))
	mirror := &fakeMirror{idle: map[string][]string{testPool: {"sbx-fb"}}, lag: 1}
	binder.Fallback = mirror
	binder.APIServerReachable = func(context.Context) error { return errors.New("apiserver down") }
	var skips []string
	binder.FallbackSkipped = func(reason string) { skips = append(skips, reason) }

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod when the API server probe fails", err)
	}
	if len(skips) != 1 || skips[0] != podsession.FallbackSkipReasonAPIServerUnreachable {
		t.Errorf("skips = %v, want [%q]", skips, podsession.FallbackSkipReasonAPIServerUnreachable)
	}
	if len(mirror.claims) != 0 {
		t.Errorf("the fallback locked a mirror row despite an unreachable API server")
	}
}

// spec: §4.7 / §4.9 — the binder's session-assignment sequence runs
// AssignCredentials before StartSession: when a BindRequest names
// credential pools the binder mints a lease per pool and pushes the set
// to the pod's adapter, which materializes the runtime credential file.

// credEntries reads the runtime credential file the adapter materialized
// into dir and indexes its entries by provider.
func credEntries(t *testing.T, dir string) map[string]map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	var doc struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode credential file: %v", err)
	}
	byProvider := map[string]map[string]any{}
	for _, entry := range doc.Providers {
		name, _ := entry["provider"].(string)
		byProvider[name] = entry
	}
	return byProvider
}

func TestBindAssignsCredentialsBeforeStartSession(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	credDir := t.TempDir()
	srv.CredentialsDir = credDir
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	assigner := &fakeAssigner{}
	binder.Credentials = assigner

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		CredentialPools: map[string]string{"anthropic_direct": "claude-direct-prod"},
		PodSpiffeURI:    "spiffe://lenny.test/agent/claude-direct-prod/sbx-1",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	// The binder leased from the pool the request resolved for the
	// anthropic_direct provider, stamping the session and the issuing
	// pod's SPIFFE identity onto the request.
	if len(assigner.calls) != 1 {
		t.Fatalf("assigner served %d calls, want 1", len(assigner.calls))
	}
	got := assigner.calls[0]
	if got.pool != "claude-direct-prod" || got.session != "sess-1" ||
		got.spiffe != "spiffe://lenny.test/agent/claude-direct-prod/sbx-1" {
		t.Errorf("assigner call = %+v, want claude-direct-prod / sess-1 / the pod SPIFFE URI", got)
	}

	// The adapter materialized the minted lease into the credential file,
	// keyed by the provider the binder filed the lease under.
	entry, ok := credEntries(t, credDir)["anthropic_direct"]
	if !ok {
		t.Fatalf("the credential file has no anthropic_direct entry after Bind: %v", credEntries(t, credDir))
	}
	if entry["leaseId"] != "cl-claude-direct-prod" {
		t.Errorf("credential file leaseId = %v, want the minted cl-claude-direct-prod", entry["leaseId"])
	}

	// The runtime still started — credential assignment precedes it.
	if rt.started != "sess-1" {
		t.Errorf("runtime started for %q, want sess-1", rt.started)
	}
}

func TestBindWithoutCredentialPoolsAssignsNothing(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.CredentialsDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	assigner := &fakeAssigner{}
	binder.Credentials = assigner

	// A BindRequest that names no credential pools: the binder assigns
	// nothing even though a credential service is wired.
	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	if len(assigner.calls) != 0 {
		t.Errorf("assigner served %d calls for a request with no pools, want 0", len(assigner.calls))
	}
}

func TestBindWithoutCredentialServiceSkipsAssignment(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.CredentialsDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv)) // no Credentials wired

	// Even with credential pools named, a binder with no credential
	// service assigns nothing rather than failing.
	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		CredentialPools: map[string]string{"anthropic_direct": "claude-prod"},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	res.Adapter.Close()
}

func TestBindFailsWhenCredentialAssignmentFails(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.CredentialsDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	binder.Credentials = &fakeAssigner{err: errors.New("pool exhausted")}

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		CredentialPools: map[string]string{"anthropic_direct": "claude-prod"},
	})
	if err == nil {
		t.Error("Bind succeeded though credential assignment failed, want a failure")
	}
}
