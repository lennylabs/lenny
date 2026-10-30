// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for a compensating reclaim lost to a gateway crash. A
// gateway bind attempt that fails after its first pod-side RPC sends the
// adapter a Shutdown naming the attempt's token, and that reclaim is the only
// thing that removes the slot registry entry the attempt created. When the
// gateway replica dies between the failure and the reclaim, nothing sends it:
// the entry stands on the pod stamped with a token no live attempt holds.
//
// The fault is injected at the gateway's side of the adapter connection: a
// unary client interceptor fails the attempt's StartSession and then keeps
// its compensating Shutdown from ever reaching the adapter, which is what the
// crash does. The binder, the slot claimer (on envtest and a miniredis slot
// counter), and both pods' adapters are real.
//
// spec: §7.1 (normal flow); §4.7.1 (role and gateway RPC contract).
package tier8_chaos_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// compLossNS is the namespace the case's pools and sandboxes live in.
const compLossNS = "lenny-agents"

// startRecordingRuntime is a runtime process that records every session it
// is started for, so the case can assert a refused attempt started nothing.
type startRecordingRuntime struct {
	mu      sync.Mutex
	started []string
}

func (r *startRecordingRuntime) Start(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, sessionID)
	return nil
}
func (*startRecordingRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (*startRecordingRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (*startRecordingRuntime) Close(context.Context, string) error           { return nil }
func (*startRecordingRuntime) Output(context.Context, string) (<-chan []byte, error) {
	return make(chan []byte), nil
}

func (r *startRecordingRuntime) startedSessions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.started...)
}

// crashingGateway fails the first StartSession it sees and then withholds
// every compensating Shutdown until healed, which is a replica that died
// between the failure and its reclaim. After heal the interceptor passes
// everything through, as a surviving replica's connection does.
type crashingGateway struct {
	mu      sync.Mutex
	crashed bool
	healed  bool
	lost    int
}

func (g *crashingGateway) intercept(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch r := req.(type) {
	case *adapterv1.StartSessionRequest:
		if !g.crashed {
			g.crashed = true
			return status.Error(codes.Unavailable, "start lost with the replica")
		}
	case *adapterv1.ShutdownRequest:
		if r.GetBindAttempt() != "" && !g.healed {
			g.lost++
			return status.Error(codes.Unavailable, "reclaim never left the crashed replica")
		}
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

func (g *crashingGateway) heal() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.healed = true
}

// compLossPod is one agent pod: its Sandbox, its adapter, and its runtime.
type compLossPod struct {
	name, pool, ip string
	srv            *adapter.Server
	rt             *startRecordingRuntime
	lis            *bufconn.Listener
}

func newCompLossPod(t *testing.T, name, pool, ip string) *compLossPod {
	t.Helper()
	base := t.TempDir()
	srv := adapter.New("tier8-" + name)
	srv.WorkspaceBase = filepath.Join(base, "workspace")
	srv.SessionsRoot = filepath.Join(base, "sessions")
	srv.ArtifactsRoot = filepath.Join(base, "artifacts")
	srv.CredentialsDir = filepath.Join(base, "run", "lenny")
	rt := &startRecordingRuntime{}
	srv.Runtime = rt
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return &compLossPod{name: name, pool: pool, ip: ip, srv: srv, rt: rt, lis: lis}
}

// compLossCluster starts envtest and seeds one idle Sandbox per pod, each in
// its own pool so a bind's placement is fixed by the pool it names.
func compLossCluster(t *testing.T, pods ...*compLossPod) client.Client {
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
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: compLossNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	for _, p := range pods {
		sb := &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{
			Name: p.name, Namespace: compLossNS,
			Labels: map[string]string{warmpool.LabelPool: p.pool},
		}}
		if err := c.Create(ctx, sb); err != nil {
			t.Fatalf("create sandbox %s: %v", p.name, err)
		}
		sb.Status = lennyv1.SandboxStatus{Phase: "idle", PodIP: p.ip}
		if err := c.Status().Update(ctx, sb); err != nil {
			t.Fatalf("seed sandbox %s status: %v", p.name, err)
		}
	}
	return c
}

// compLossBinder wires the gateway binder to the pods, routing each dial by
// the pod IP the claimed Sandbox reports, through the crashing interceptor.
func compLossBinder(t *testing.T, c client.Client, gw *crashingGateway, pods ...*compLossPod) *podsession.Binder {
	t.Helper()
	byAddr := map[string]*compLossPod{}
	for _, p := range pods {
		byAddr[net.JoinHostPort(p.ip, "50051")] = p
	}
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	return &podsession.Binder{
		Client:           c,
		Namespace:        compLossNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		SlotCounter:      slotcounter.New(rc),
		DialAdapter: func(addr string) (*adapterclient.Client, error) {
			p, ok := byAddr[addr]
			if !ok {
				return nil, errors.New("no pod at " + addr)
			}
			return adapterclient.Dial("passthrough:///bufnet",
				grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
					return p.lis.DialContext(ctx)
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithUnaryInterceptor(gw.intercept))
		},
	}
}

// compLossReq is sess-bob's bind request onto pool.
func compLossReq(pool string) podsession.SlotBindRequest {
	return podsession.SlotBindRequest{
		Pool: pool, SessionID: "sess-bob", TenantID: "acme", Runtime: "echo",
		MaxConcurrentSessions: 4, Plan: &adapterv1.WorkspacePlan{},
	}
}

// spec: §7.1 (normal flow); §4.7.1 (role and gateway RPC contract)
// diagnosis: a failure means the residue a lost compensation leaves is not
// fenced. If a later attempt at the session on the same pod was admitted or
// started, the adapter adopted an entry stamped with a dead token instead of
// refusing it, which lets a successor materialize over an abandoned attempt's
// residue. If the placement onto the other pod failed, the refusal leaked
// beyond the pod that holds the residue.
func TestCompensationLostToAGatewayCrashLeavesTheEntryRefusable_spec_7_1(t *testing.T) {
	podA := newCompLossPod(t, "sbx-a", "pool-a", "10.244.9.1")
	podB := newCompLossPod(t, "sbx-b", "pool-b", "10.244.9.2")
	c := compLossCluster(t, podA, podB)
	gw := &crashingGateway{}
	binder := compLossBinder(t, c, gw, podA, podB)
	ctx := context.Background()

	// The attempt fails at its start and its compensation never leaves the
	// crashed replica, so the reservation is released leaked.
	_, err := binder.BindSlot(ctx, compLossReq("pool-a"))
	var sbe *podsession.SlotBindError
	if !errors.As(err, &sbe) {
		t.Fatalf("first attempt error = %v, want a *SlotBindError", err)
	}
	if !sbe.Leaked || gw.lost != 1 {
		t.Fatalf("first attempt Leaked=%v with %d lost reclaims, want a leaked slot and one lost reclaim", sbe.Leaked, gw.lost)
	}
	if err := binder.ReleaseSlotReservation(ctx, sbe.Pod, sbe.SlotID, sbe.Leaked); err != nil {
		t.Fatalf("release leaked reservation: %v", err)
	}
	gw.heal()

	// Every later attempt at the session on that pod is refused rather than
	// admitted onto the dead attempt's entry.
	for i := 0; i < 3; i++ {
		_, err := binder.BindSlot(ctx, compLossReq("pool-a"))
		if !errors.Is(err, adapterclient.ErrSlotBindAttemptSuperseded) {
			t.Fatalf("attempt %d on the residue pod: error = %v, want the superseded refusal", i+2, err)
		}
		var refused *podsession.SlotBindError
		if errors.As(err, &refused) {
			if refused.Leaked {
				t.Errorf("attempt %d: a superseded compensation was booked leaked", i+2)
			}
			if err := binder.ReleaseSlotReservation(ctx, refused.Pod, refused.SlotID, refused.Leaked); err != nil {
				t.Fatalf("release refused reservation: %v", err)
			}
		}
	}
	if got := podA.rt.startedSessions(); len(got) != 0 {
		t.Errorf("the residue pod started %v, want nothing started over the dead attempt's entry", got)
	}
	if _, err := os.Stat(filepath.Join(podA.srv.WorkspaceBase, "slots", "sess-bob")); err != nil {
		t.Errorf("the dead attempt's slot tree is gone (%v); nothing should have reclaimed it", err)
	}

	// A placement onto a different pod succeeds.
	res, err := binder.BindSlot(ctx, compLossReq("pool-b"))
	if err != nil {
		t.Fatalf("placement onto the other pod: %v", err)
	}
	defer res.Adapter.Close()
	if res.SandboxName != "sbx-b" {
		t.Fatalf("placement landed on %s, want sbx-b", res.SandboxName)
	}
	if got := podB.rt.startedSessions(); len(got) != 1 || got[0] != "sess-bob" {
		t.Errorf("the other pod started %v, want [sess-bob]", got)
	}
}
