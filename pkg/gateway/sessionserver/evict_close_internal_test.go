// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// lazyAdapter returns an adapter client whose channel stays idle (no RPC is
// issued), so it reports alive until it is closed.
func lazyAdapter(t *testing.T) *adapterclient.Client {
	t.Helper()
	cl, err := adapterclient.Dial("127.0.0.1:1", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return cl
}

// adapterClosed reports whether cl's channel was closed: it no longer reports
// alive and a further Close fails.
func adapterClosed(cl *adapterclient.Client) bool {
	return !cl.Alive() && cl.Close() != nil
}

// evictCloseWarnings counts the warnings evictStaleBinding logs when closing
// an adapter connection fails, which a second close of one connection does.
type evictCloseWarnings struct {
	mu sync.Mutex
	n  int
}

func (c *evictCloseWarnings) Enabled(context.Context, slog.Level) bool { return true }
func (c *evictCloseWarnings) Handle(_ context.Context, r slog.Record) error {
	if strings.Contains(r.Message, "close evicted adapter connection") {
		c.mu.Lock()
		c.n++
		c.mu.Unlock()
	}
	return nil
}
func (c *evictCloseWarnings) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *evictCloseWarnings) WithGroup(string) slog.Handler      { return c }

func (c *evictCloseWarnings) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func captureEvictCloseWarnings(t *testing.T) *evictCloseWarnings {
	t.Helper()
	prev := slog.Default()
	c := &evictCloseWarnings{}
	slog.SetDefault(slog.New(c))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

// TestStaleStreamFailureEvictionClosesTheAdapter_spec_10_1_5 sweeps the
// stream-failure report over every state its peer-takeover check covers. A
// binding a peer superseded is evicted and its adapter connection closed; a
// binding at the row's generation is reported and keeps its connection for
// the binder release.
//
// spec: 10.1.5 (Stale Replica Behavior), 10.1.1 (Stateless Replicas and
// Per-Session Coordination), 28.5.1 (Gateway-to-pod)
// diagnosis: a failure on a stale case means the stream-failure eviction
// drops the binding without closing its gRPC channel, leaking it for the
// process lifetime; a failure on the own-binding case means the eviction
// closed a connection the release path still needs.
func TestStaleStreamFailureEvictionClosesTheAdapter_spec_10_1_5(t *testing.T) {
	for _, st := range []session.State{
		session.StateRunning, session.StateInputRequired, session.StateSuspended, session.StateResuming,
	} {
		t.Run("stale "+string(st), func(t *testing.T) {
			warns := captureEvictCloseWarnings(t)
			f := newStreamFixture(t)
			bind := f.seedAt(t, "s", st, 2, 1)
			cl := lazyAdapter(t)
			bind.Adapter = cl
			f.srv.ReportAttachStreamFailure("acme", "s", "pod-x", heartbeatEnd)
			if _, ok := f.registry.Get("s"); ok {
				t.Fatal("the stale binding stayed in the registry")
			}
			if !adapterClosed(cl) {
				t.Error("the evicted binding's adapter connection is still open")
			}
			if n := warns.count(); n != 0 {
				t.Errorf("close warnings = %d, want 0", n)
			}
		})
	}
	t.Run("own binding", func(t *testing.T) {
		f := newStreamFixture(t)
		bind := f.seedAt(t, "s", session.StateRunning, 2, 2)
		cl := lazyAdapter(t)
		t.Cleanup(func() { _ = cl.Close() })
		bind.Adapter = cl
		f.srv.ReportAttachStreamFailure("acme", "s", "pod-x", heartbeatEnd)
		if len(f.evict.evictions()) != 0 {
			t.Fatalf("evictions = %v, want none", f.evict.evictions())
		}
		if !cl.Alive() {
			t.Error("the eviction path closed the connection of a binding it did not evict")
		}
	})
}

// TestStaleTerminalBindingEvictionClosesTheAdapter_spec_10_1_5 sweeps the
// terminal binding release over every terminal state with a binding stamped
// at zero and one stamped below the row's generation. Each is evicted, as a
// peer owns the pod, and its adapter connection is closed exactly once.
//
// spec: 10.1.5 (Stale Replica Behavior), 10.1.1 (Stateless Replicas and
// Per-Session Coordination), 7.2 (Interactive Session Model)
// diagnosis: a failure means the terminal release's stale-binding eviction
// drops the binding without closing its gRPC channel.
func TestStaleTerminalBindingEvictionClosesTheAdapter_spec_10_1_5(t *testing.T) {
	for _, st := range session.TerminalStates() {
		for _, stamp := range []int64{0, 2} {
			t.Run(fmt.Sprintf("%s/stamp_%d", st, stamp), func(t *testing.T) {
				warns := captureEvictCloseWarnings(t)
				f := newFunnelFixture(t, funnelOpts{})
				row, bind := bindAt(t, f, "s", st, 3, stamp)
				cl := lazyAdapter(t)
				bind.Adapter = cl
				f.srv.ReleaseTerminalBinding(row)
				waitReleaseIdle(t, f.srv)
				if _, ok := f.registry.Get("s"); ok {
					t.Fatal("the stale terminal binding stayed in the registry")
				}
				if calls := f.exec.callList(); len(calls) != 0 {
					t.Errorf("binder releases = %v, want none", calls)
				}
				if !adapterClosed(cl) {
					t.Error("the evicted binding's adapter connection is still open")
				}
				if n := warns.count(); n != 0 {
					t.Errorf("close warnings = %d, want 0", n)
				}
			})
		}
	}
}

// TestEvictStaleBindingClosesOnlyTheBindingItRemoves_spec_10_1_5 pins the
// shared stale-binding eviction every caller (the stream-failure report, the
// terminal release, and the stale idle suspension) uses. A binding replaced
// since the caller read it is left in place with its connection open, and the
// caller's own superseded binding is not closed either, since another owner
// may hold it. When many evictors race on one binding under -race, exactly
// one removes it and closes its connection once.
//
// spec: 10.1.5 (Stale Replica Behavior), 10.1.1 (Stateless Replicas and
// Per-Session Coordination)
func TestEvictStaleBindingClosesOnlyTheBindingItRemoves_spec_10_1_5(t *testing.T) {
	t.Run("replaced binding", func(t *testing.T) {
		f := newStreamFixture(t)
		oldCl, newCl := lazyAdapter(t), lazyAdapter(t)
		t.Cleanup(func() { _ = oldCl.Close(); _ = newCl.Close() })
		old := &podsession.BindResult{SessionID: "s", Adapter: oldCl}
		cur := &podsession.BindResult{SessionID: "s", Adapter: newCl}
		f.registry.Put(cur)
		f.srv.evictStaleBinding("s", old)
		if got, ok := f.registry.Get("s"); !ok || got != cur {
			t.Fatal("the eviction removed a binding published since")
		}
		if !oldCl.Alive() || !newCl.Alive() {
			t.Error("the eviction closed a connection it did not remove")
		}
		if len(f.evict.evictions()) != 0 {
			t.Errorf("evictions = %v, want none", f.evict.evictions())
		}
	})
	t.Run("nil adapter", func(t *testing.T) {
		f := newStreamFixture(t)
		b := &podsession.BindResult{SessionID: "s"}
		f.registry.Put(b)
		f.srv.evictStaleBinding("s", b)
		if _, ok := f.registry.Get("s"); ok {
			t.Error("binding without an adapter not removed")
		}
	})
	t.Run("concurrent evictors", func(t *testing.T) {
		warns := captureEvictCloseWarnings(t)
		f := newStreamFixture(t)
		for round := range 25 {
			cl := lazyAdapter(t)
			b := &podsession.BindResult{SessionID: "s", Adapter: cl}
			f.registry.Put(b)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					f.srv.evictStaleBinding("s", b)
				}()
			}
			close(start)
			wg.Wait()
			if _, ok := f.registry.Get("s"); ok {
				t.Fatalf("round %d: binding survived", round)
			}
			if !adapterClosed(cl) {
				t.Fatalf("round %d: connection still open", round)
			}
		}
		if n := warns.count(); n != 0 {
			t.Errorf("close warnings = %d, want 0 (a connection was closed more than once)", n)
		}
		if got := len(f.evict.evictions()); got != 25 {
			t.Errorf("stream evictions = %d, want 25 (one per removed binding)", got)
		}
	})
}
