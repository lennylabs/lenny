// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 cases for the credential side of the compensating slot reclaim. A
// gateway bind attempt that fails after its first pod-side RPC reclaims the
// pod-side state it created by sending the adapter a Shutdown naming its own
// bind attempt token, and releases the §4.9 leases it minted by identifier.
// The token decides whose credential material a reclaim reaches: a reclaim
// naming an attempt the slot's entry no longer belongs to must leave the
// successor's credential file, credential directory, and armed expiry timers
// alone, and a reclaim naming the entry's own attempt must leave nothing of
// that attempt behind. The gateway-side release decides whose leases the
// failure returns: only the failed attempt's own, never a successor's for the
// same session.
//
// Each case drives the gateway's real podsession.Binder against envtest, a
// miniredis slot counter, the real credassign service, and a real
// adapter.Server over gRPC, with a client interceptor standing in for the
// failure the gateway meets.
//
// spec: §4.7.1 (role and gateway RPC contract); §4.9 (credential leasing
// service); §5.2 (pool configuration and execution modes).
package tier9_security_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fenceTimer is one expiry timer the adapter armed through its test seam.
type fenceTimer struct {
	mu      sync.Mutex
	stopped bool
}

func (f *fenceTimer) Stop() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	was := !f.stopped
	f.stopped = true
	return was
}

func (f *fenceTimer) isStopped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

// fenceTimers records every §4.9 expiry timer the adapter arms. A timer is
// armed until it is stopped; none fires within a case.
type fenceTimers struct {
	mu  sync.Mutex
	all []*fenceTimer
}

func (r *fenceTimers) after(time.Duration, func()) adapter.TimerHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := &fenceTimer{}
	r.all = append(r.all, h)
	return h
}

// armed returns the timers armed and not stopped.
func (r *fenceTimers) armed() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, h := range r.all {
		if !h.isStopped() {
			n++
		}
	}
	return n
}

func (r *fenceTimers) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.all)
}

// fenceFaults is a unary client interceptor on the gateway's adapter
// connection. It drops the named RPC of the next bind attempt before it
// reaches the adapter, and records every compensating Shutdown it passes.
type fenceFaults struct {
	mu       sync.Mutex
	drop     string
	reclaims []*adapterv1.ShutdownRequest
}

func (f *fenceFaults) dropNext(rpc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drop = rpc
}

func (f *fenceFaults) intercept(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	f.mu.Lock()
	switch r := req.(type) {
	case *adapterv1.StartSessionRequest:
		if f.drop == "StartSession" {
			f.drop = ""
			f.mu.Unlock()
			return status.Error(codes.Unavailable, "start lost in transit")
		}
	case *adapterv1.AssignCredentialsRequest:
		if f.drop == "AssignCredentials" {
			f.drop = ""
			f.mu.Unlock()
			return status.Error(codes.Unavailable, "credential assignment lost in transit")
		}
	case *adapterv1.ShutdownRequest:
		if r.GetBindAttempt() != "" {
			f.reclaims = append(f.reclaims, r)
		}
	}
	f.mu.Unlock()
	return invoker(ctx, method, req, reply, cc, opts...)
}

func (f *fenceFaults) lastReclaim(t *testing.T) *adapterv1.ShutdownRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reclaims) == 0 {
		t.Fatal("no compensating Shutdown was sent")
	}
	return f.reclaims[len(f.reclaims)-1]
}

// fenceFixture is one concurrent pod the gateway binds sess-bob onto.
type fenceFixture struct {
	srv    *adapter.Server
	timers *fenceTimers
	faults *fenceFaults
	binder *podsession.Binder
	leases *credleasestore.Store
	svc    *credassign.Service
	raw    *adapterclient.Client
}

func newFenceFixture(t *testing.T, mode credential.DeliveryMode) *fenceFixture {
	t.Helper()
	c := deliveryGateCluster(t)
	srv := adapter.New("tier9-reclaim-fence")
	srv.WorkspaceBase = t.TempDir()
	srv.CredentialsDir = t.TempDir()
	srv.Runtime = noopRuntime{}
	timers := &fenceTimers{}
	srv.ExpiryAfterFunc = timers.after

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	faults := &fenceFaults{}
	dial := func(opts ...grpc.DialOption) (*adapterclient.Client, error) {
		return adapterclient.Dial("passthrough:///bufnet", append([]grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}, opts...)...)
	}
	raw, err := dial()
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	leases := credleasestore.New()
	svc := credassign.New(leases, credcache.New())
	for _, pool := range []string{"pool-a", "pool-b"} {
		svc.RegisterPool(credassign.Pool{
			Name: pool, Provider: credential.ProviderAnthropicDirect, DeliveryMode: mode,
			Strategy: credential.StrategyLeastLoaded, ProxyURL: "https://gateway-internal:8443/llm-proxy",
			ProxyDialect: "anthropic",
			Credentials: []credassign.PoolCredential{
				{ID: pool + "-key-1", APIKey: "sk-ant-" + pool + "-1", Healthy: true},
				{ID: pool + "-key-2", APIKey: "sk-ant-" + pool + "-2", Healthy: true},
			},
		})
	}

	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	binder := &podsession.Binder{
		Client:           c,
		Namespace:        deliveryGateNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		SlotCounter:      slotcounter.New(rc),
		Credentials:      svc,
		DialAdapter: func(string) (*adapterclient.Client, error) {
			return dial(grpc.WithUnaryInterceptor(faults.intercept))
		},
	}
	return &fenceFixture{srv: srv, timers: timers, faults: faults, binder: binder, leases: leases, svc: svc, raw: raw}
}

// req is sess-bob's bind request, leasing one credential per named pool.
func (f *fenceFixture) req(pools ...string) podsession.SlotBindRequest {
	creds := map[string]string{}
	for i, p := range pools {
		creds[[]string{"anthropic", "anthropic_alt"}[i]] = p
	}
	return podsession.SlotBindRequest{
		Pool: "echo-pool", SessionID: "sess-bob", TenantID: "acme", Runtime: "echo",
		MaxConcurrentSessions: 4, Plan: &adapterv1.WorkspacePlan{}, CredentialPools: creds,
	}
}

// failBind runs a bind that the dropped RPC fails and releases its
// reservation with the disposition the binder reports.
func (f *fenceFixture) failBind(t *testing.T, drop string, pools ...string) *podsession.SlotBindError {
	t.Helper()
	f.faults.dropNext(drop)
	_, err := f.binder.BindSlot(context.Background(), f.req(pools...))
	var sbe *podsession.SlotBindError
	if !errors.As(err, &sbe) {
		t.Fatalf("BindSlot error = %v, want a *SlotBindError", err)
	}
	if err := f.binder.ReleaseSlotReservation(context.Background(), sbe.Pod, sbe.SlotID, sbe.Leaked); err != nil {
		t.Fatalf("release reservation: %v", err)
	}
	return sbe
}

func (f *fenceFixture) slotPaths(t *testing.T) slotlayout.SlotPaths {
	t.Helper()
	p, err := slotlayout.Resolve(slotlayout.Roots{Workspace: f.srv.WorkspaceBase, Credentials: f.srv.CredentialsDir}, "sess-bob")
	if err != nil {
		t.Fatalf("resolve slot paths: %v", err)
	}
	return p
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
	return false
}

// spec: §4.7.1 (role and gateway RPC contract); §4.9 (credential leasing
// service); §5.2 (pool configuration and execution modes)
// diagnosis: a failure means a teardown belonging to an abandoned bind attempt
// reached a live session's credential material: the successor's
// credentials.json, its credential directory, or its armed §4.9 expiry timer
// did not survive a reclaim naming an attempt the entry no longer belongs to.
func TestSupersededReclaimLeavesTheSuccessorsCredentialsIntact_spec_4_7_1(t *testing.T) {
	f := newFenceFixture(t, credential.DeliveryDirect)
	_ = f.failBind(t, "StartSession", "pool-a")
	stale := f.faults.lastReclaim(t)

	res, err := f.binder.BindSlot(context.Background(), f.req("pool-a"))
	if err != nil {
		t.Fatalf("successor BindSlot: %v", err)
	}
	defer res.Adapter.Close()
	paths := f.slotPaths(t)
	armedBefore := f.timers.armed()
	if armedBefore != 1 || !exists(t, paths.CredentialsFile) {
		t.Fatalf("successor armed %d timers with credentials.json present=%v, want one timer and the file", armedBefore, exists(t, paths.CredentialsFile))
	}

	// The abandoned attempt's compensation arrives again after the successor
	// bound: it names a token the entry does not carry.
	outcome, _, err := f.raw.ShutdownReclaim(context.Background(), "sess-bob", stale.GetReason(),
		time.Duration(stale.GetDeadlineMs())*time.Millisecond, stale.GetBindAttempt())
	if err != nil {
		t.Fatalf("stale reclaim: %v", err)
	}
	if outcome != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED {
		t.Fatalf("stale reclaim outcome = %v, want SUPERSEDED", outcome)
	}
	if !exists(t, paths.CredentialsFile) || !exists(t, paths.CredentialsDir) {
		t.Error("the stale reclaim removed the successor's credential directory or credentials.json")
	}
	if got := f.timers.armed(); got != armedBefore {
		t.Errorf("armed expiry timers = %d after the stale reclaim, want the successor's %d", got, armedBefore)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §4.9 (credential leasing
// service); §5.2 (pool configuration and execution modes)
// diagnosis: a failure means a failed bind attempt's own reclaim left its
// credential material on the pod. For a bound-but-unstarted entry, a surviving
// credentials.json or armed §4.9 expiry timer keeps a credential live for a
// session the gateway abandoned. For a registered-but-unbound entry, a
// surviving slot or credential directory is residue nothing else collects.
func TestMatchingReclaimLeavesNothingOfTheAttemptBehind_spec_4_7_1(t *testing.T) {
	t.Run("bound_but_unstarted", func(t *testing.T) {
		f := newFenceFixture(t, credential.DeliveryDirect)
		sbe := f.failBind(t, "StartSession", "pool-a")
		if sbe.Leaked {
			t.Fatal("the reclaim was not acknowledged clean")
		}
		paths := f.slotPaths(t)
		if f.timers.count() == 0 {
			t.Fatal("the attempt armed no expiry timer; the case needs a bound, credentialed entry")
		}
		if exists(t, paths.CredentialsFile) {
			t.Error("credentials.json survived the matching reclaim")
		}
		if got := f.timers.armed(); got != 0 {
			t.Errorf("armed expiry timers = %d after the matching reclaim, want 0", got)
		}
	})
	t.Run("registered_but_unbound", func(t *testing.T) {
		f := newFenceFixture(t, credential.DeliveryDirect)
		sbe := f.failBind(t, "AssignCredentials", "pool-a")
		if sbe.Leaked {
			t.Fatal("the reclaim was not acknowledged clean")
		}
		paths := f.slotPaths(t)
		if exists(t, filepath.Dir(paths.Current)) {
			t.Error("the slot directory survived the matching reclaim")
		}
		if exists(t, paths.CredentialsDir) {
			t.Error("the credential directory survived the matching reclaim")
		}
	})
}

// spec: §4.7.1 (role and gateway RPC contract); §4.9 (credential leasing
// service); §5.2 (pool configuration and execution modes)
// diagnosis: a failure means a failed bind attempt's lease release reached
// beyond the leases that attempt minted. A successor's lease for the same
// session that no longer resolves by its token has been stripped, and the
// successor's LLM proxy traffic stops authenticating; a failed attempt's lease
// that still resolves keeps a credential's active-session slot leaked.
func TestFailedAttemptLeaseReleaseStripsNoSuccessorLease_spec_4_7_1(t *testing.T) {
	f := newFenceFixture(t, credential.DeliveryProxy)
	// A successor attempt for the same session already holds a lease, minted
	// on another replica's attempt.
	successor, err := f.svc.AssignProto("pool-a", "sess-bob", "", "acme")
	if err != nil {
		t.Fatalf("successor lease: %v", err)
	}
	stored, ok := f.leases.GetByID(successor.GetLeaseId())
	if !ok || stored.Proxy == nil || stored.Proxy.LeaseToken == "" {
		t.Fatal("the successor lease carries no proxy lease token")
	}
	token := stored.Proxy.LeaseToken

	_ = f.failBind(t, "StartSession", "pool-a", "pool-b")

	if got := f.leases.Len(); got != 1 {
		t.Errorf("lease store holds %d leases after the failed attempt, want the successor's alone", got)
	}
	got, ok := f.leases.GetByToken(token)
	if !ok || got.LeaseID != successor.GetLeaseId() {
		t.Fatal("the successor's lease token no longer authenticates after the failed attempt's release")
	}
	if _, ok := f.svc.UpstreamCredential(got); !ok {
		t.Error("the successor's upstream credential is no longer reachable through its lease")
	}
}
