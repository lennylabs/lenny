// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// sharedAdapterServer serves one in-process adapter over bufconn and dials
// any number of clients to it, so a test can measure the goroutines its
// clients own against a baseline taken after the server started.
type sharedAdapterServer struct {
	lis *bufconn.Listener
}

func newSharedAdapterServer(t *testing.T) *sharedAdapterServer {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(adapter.New("evict-close-test-build"))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return &sharedAdapterServer{lis: lis}
}

func (s *sharedAdapterServer) dial(t *testing.T) *adapterclient.Client {
	t.Helper()
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return s.lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	return cl
}

// closeWarnings captures the warnings the eviction logs when closing an
// adapter connection fails. A second close of the same connection fails, so
// a captured warning means a connection was closed more than once.
type closeWarnings struct {
	mu   sync.Mutex
	msgs []string
}

func (c *closeWarnings) Enabled(context.Context, slog.Level) bool { return true }
func (c *closeWarnings) Handle(_ context.Context, r slog.Record) error {
	if strings.Contains(r.Message, "close evicted adapter connection") {
		c.mu.Lock()
		c.msgs = append(c.msgs, r.Message)
		c.mu.Unlock()
	}
	return nil
}
func (c *closeWarnings) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *closeWarnings) WithGroup(string) slog.Handler      { return c }

func (c *closeWarnings) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.msgs)
}

// captureCloseWarnings installs a closeWarnings handler as the default slog
// logger for the test's duration.
func captureCloseWarnings(t *testing.T) *closeWarnings {
	t.Helper()
	prev := slog.Default()
	c := &closeWarnings{}
	slog.SetDefault(slog.New(c))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

// assertClosed reports whether cl's channel is shut down: Alive reports false
// and a further Close fails because the channel is already closing.
func assertClosed(t *testing.T, cl *adapterclient.Client) {
	t.Helper()
	if cl.Alive() {
		t.Error("adapter channel still alive after the eviction; the connection was dropped without a close")
	}
	if err := cl.Close(); err == nil {
		t.Error("a further Close succeeded, so the eviction never closed the connection")
	}
}

// syncEvicter is a concurrency-safe streamEvicter that counts evictions.
type syncEvicter struct {
	recordingEvicter
	mu sync.Mutex
}

func (s *syncEvicter) EvictStream(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordingEvicter.EvictStream(sessionID)
}

// memLeases is an in-memory leasestore.LeaseStore for driving the Sweeper.
type memLeases struct {
	mu      sync.Mutex
	holders map[string]string
}

func newMemLeases() *memLeases { return &memLeases{holders: map[string]string{}} }

func (m *memLeases) Acquire(_ context.Context, tenantID, sessionID, holder string, _ time.Duration) (leasestore.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tenantID + "/" + sessionID
	if cur, ok := m.holders[k]; ok && cur != holder {
		return leasestore.Lease{}, leasestore.ErrHeld
	}
	m.holders[k] = holder
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder}, nil
}

func (m *memLeases) Renew(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (leasestore.Lease, error) {
	return m.Acquire(ctx, tenantID, sessionID, holder, ttl)
}

func (m *memLeases) Release(_ context.Context, tenantID, sessionID, holder string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tenantID + "/" + sessionID
	if m.holders[k] == holder {
		delete(m.holders, k)
	}
	return nil
}

func (m *memLeases) Get(_ context.Context, tenantID, sessionID string) (leasestore.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.holders[tenantID+"/"+sessionID]
	if !ok {
		return leasestore.Lease{}, leasestore.ErrNotFound
	}
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: h}, nil
}

func (m *memLeases) DeleteByUser(context.Context, string, string) (int, error) { return 0, nil }
func (m *memLeases) DeleteByTenant(context.Context, string) (int, error)       { return 0, nil }

// deadChannelBindings reports every held channel dead, so the Sweeper takes
// its dead-connection branch, and delegates everything else, including the
// eviction under test, to the production seam.
type deadChannelBindings struct{ coordinationBindings }

func (deadChannelBindings) ConnAlive(string) bool { return false }

// TestSweeperEvictionBranchesCloseTheAdapterConnection_spec_10_1_5 drives the
// production coordination Sweeper over the production binding seam through
// each branch that evicts a binding, and the renew branch as a control. The
// dead-connection branch (the held channel reports dead) and the peer-takeover
// branch (a peer holds the lease and the row's generation passed the binding
// stamp) each remove the binding, drop the cached stream, and close the
// adapter connection exactly once. The renew branch evicts nothing and leaves
// the connection open.
//
// Before the fix both eviction branches left the gRPC channel open with no
// remaining reference, leaking it for the life of the process.
//
// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1.5
// (Stale Replica Behavior), 10.1 (hold state on connection loss)
func TestSweeperEvictionBranchesCloseTheAdapterConnection_spec_10_1_5(t *testing.T) {
	cases := []struct {
		name      string
		dead      bool
		peerLease bool
		rowGen    int64
		wantEvict bool
	}{
		{name: "dead_connection", dead: true, rowGen: 1, wantEvict: true},
		{name: "peer_takeover", peerLease: true, rowGen: 3, wantEvict: true},
		{name: "renew_control", rowGen: 1, wantEvict: false},
		{name: "peer_lease_same_generation_control", peerLease: true, rowGen: 1, wantEvict: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warns := captureCloseWarnings(t)
			srv := newSharedAdapterServer(t)
			cl := srv.dial(t)
			reg := podsession.NewRegistry()
			ev := &syncEvicter{}
			reg.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", Adapter: cl, CoordinationGeneration: 1})
			seam := coordinationBindings{w: &gatewayWiring{gatewayWiringFields: gatewayWiringFields{podRegistry: reg, exec: ev}}}
			var bindings coordination.BindingRegistry = seam
			if tc.dead {
				bindings = deadChannelBindings{seam}
			}

			sessions := memstore.New()
			if err := sessions.Create(context.Background(), sessionstore.Session{
				ID: "s1", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1",
				CoordinationGeneration: tc.rowGen, CreatedAt: time.Unix(1, 0).UTC(),
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			leases := newMemLeases()
			holder := "rep-1"
			if tc.peerLease {
				holder = "rep-2"
			}
			if _, err := leases.Acquire(context.Background(), "acme", "s1", holder, time.Minute); err != nil {
				t.Fatalf("seed lease: %v", err)
			}
			sw := coordination.NewSweeper(staticTenantLister{tenants: []string{"acme"}}, sessions, leases,
				coordination.Options{ReplicaID: "rep-1", Bindings: bindings})
			if _, err := sw.Sweep(context.Background()); err != nil {
				t.Fatalf("Sweep: %v", err)
			}

			_, stillBound := reg.Get("s1")
			if stillBound == tc.wantEvict {
				t.Fatalf("binding present after the sweep = %v, want %v", stillBound, !tc.wantEvict)
			}
			if !tc.wantEvict {
				if !cl.Alive() {
					t.Error("a sweep that evicted nothing closed the adapter connection")
				}
				_ = cl.Close()
				return
			}
			if len(ev.evicted) != 1 {
				t.Errorf("cached stream evictions = %d, want 1", len(ev.evicted))
			}
			if n := warns.count(); n != 0 {
				t.Errorf("close warnings = %d, want 0 (the connection was closed more than once)", n)
			}
			assertClosed(t, cl)
		})
	}
}

// TestEvictBindingClosesOnlyTheRemovedBinding_spec_10_1_5 sweeps the binding
// seam's EvictBinding over every registry configuration: a bound session with
// an adapter, a bound session without one, an unbound session, a nil
// registry, and a repeated eviction. Only a removed binding's connection is
// closed, a binding of another session is never touched, and nothing is
// closed twice.
//
// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1.5
// (Stale Replica Behavior)
func TestEvictBindingClosesOnlyTheRemovedBinding_spec_10_1_5(t *testing.T) {
	warns := captureCloseWarnings(t)
	srv := newSharedAdapterServer(t)

	t.Run("bound_with_adapter", func(t *testing.T) {
		reg := podsession.NewRegistry()
		target, other := srv.dial(t), srv.dial(t)
		t.Cleanup(func() { _ = other.Close() })
		reg.Put(&podsession.BindResult{SessionID: "s1", Adapter: target})
		reg.Put(&podsession.BindResult{SessionID: "s2", Adapter: other})
		b := coordinationBindings{w: &gatewayWiring{gatewayWiringFields: gatewayWiringFields{podRegistry: reg}}}
		b.EvictBinding("s1")
		b.EvictBinding("s1") // a repeated eviction finds no binding and closes nothing
		assertClosed(t, target)
		if !other.Alive() {
			t.Error("evicting s1 closed s2's adapter connection")
		}
		if _, ok := reg.Get("s2"); !ok {
			t.Error("evicting s1 removed s2's binding")
		}
	})
	t.Run("bound_without_adapter", func(t *testing.T) {
		reg := podsession.NewRegistry()
		reg.Put(&podsession.BindResult{SessionID: "s1"})
		b := coordinationBindings{w: &gatewayWiring{gatewayWiringFields: gatewayWiringFields{podRegistry: reg}}}
		b.EvictBinding("s1")
		if _, ok := reg.Get("s1"); ok {
			t.Error("binding without an adapter not removed")
		}
	})
	t.Run("unbound_and_nil_registry", func(t *testing.T) {
		coordinationBindings{w: &gatewayWiring{gatewayWiringFields: gatewayWiringFields{podRegistry: podsession.NewRegistry()}}}.EvictBinding("s1")
		coordinationBindings{w: &gatewayWiring{}}.EvictBinding("s1")
	})
	if n := warns.count(); n != 0 {
		t.Errorf("close warnings = %d, want 0", n)
	}
}

// TestConcurrentEvictBindingClosesOnce_spec_10_1_5 races several evictions of
// the same binding, as a Sweeper pass and other eviction paths can, under
// -race. Exactly one eviction removes the binding and closes its connection;
// no close is attempted twice.
//
// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1.5
// (Stale Replica Behavior)
func TestConcurrentEvictBindingClosesOnce_spec_10_1_5(t *testing.T) {
	warns := captureCloseWarnings(t)
	srv := newSharedAdapterServer(t)
	for round := range 20 {
		reg := podsession.NewRegistry()
		ev := &syncEvicter{}
		cl := srv.dial(t)
		reg.Put(&podsession.BindResult{SessionID: "s1", Adapter: cl})
		b := coordinationBindings{w: &gatewayWiring{gatewayWiringFields: gatewayWiringFields{podRegistry: reg, exec: ev}}}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				b.EvictBinding("s1")
			}()
		}
		close(start)
		wg.Wait()
		if _, ok := reg.Get("s1"); ok {
			t.Fatalf("round %d: binding survived the evictions", round)
		}
		assertClosed(t, cl)
	}
	if n := warns.count(); n != 0 {
		t.Errorf("close warnings = %d, want 0 (a connection was closed more than once)", n)
	}
}

// TestEvictBindingLeaksNoGoroutines_spec_10_1_5 publishes and evicts many
// bindings and checks that the goroutines their gRPC channels started have
// exited. An eviction that dropped a binding without closing its connection
// would leave each channel's background goroutines running.
//
// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1.5
// (Stale Replica Behavior)
func TestEvictBindingLeaksNoGoroutines_spec_10_1_5(t *testing.T) {
	srv := newSharedAdapterServer(t)
	baseline := settledGoroutines()
	reg := podsession.NewRegistry()
	b := coordinationBindings{w: &gatewayWiring{gatewayWiringFields: gatewayWiringFields{podRegistry: reg}}}
	const n = 25
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "s" + string(rune('a'+i))
		reg.Put(&podsession.BindResult{SessionID: ids[i], Adapter: srv.dial(t)})
	}
	if grown := runtime.NumGoroutine() - baseline; grown < n {
		t.Fatalf("goroutines grew by %d for %d channels; the leak check cannot observe a leak", grown, n)
	}
	for _, id := range ids {
		b.EvictBinding(id)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		now := runtime.NumGoroutine()
		if now <= baseline+2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d after evicting %d bindings, baseline %d: the evicted channels leaked", now, n, baseline)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// settledGoroutines returns the goroutine count once it stops changing.
func settledGoroutines() int {
	prev := runtime.NumGoroutine()
	for range 50 {
		time.Sleep(10 * time.Millisecond)
		cur := runtime.NumGoroutine()
		if cur == prev {
			return cur
		}
		prev = cur
	}
	return prev
}
