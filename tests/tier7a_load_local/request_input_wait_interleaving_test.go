// SPDX-License-Identifier: MIT

//go:build load_local

package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/inputwaitrecovery"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

const waitSession = "sess_waits"

// waitConn is one LNK-GWCONTROL connection's surface: a registry and the
// request_input handler over the shared store.
type waitConn struct {
	name string
	reg  *inputwait.Registry
	srv  *mcp.Server
	gen  *inputwaitrecovery.Generation
}

func newWaitConn(name string, store sessionstore.Store, ids *atomic.Int64) *waitConn {
	c := &waitConn{name: name, reg: inputwait.NewRegistry(), srv: mcp.NewServer(), gen: inputwaitrecovery.NewGeneration()}
	mcptools.Register(c.srv, mcptools.Deps{
		DevTenantFallback:   true,
		Store:               store,
		Executor:            executor.NewEchoExecutor(),
		InputWaits:          c.reg,
		RequestInputTimeout: time.Minute,
		IDFunc:              func() string { return fmt.Sprintf("%s-%d", name, ids.Add(1)) },
		TenantID:            "acme",
	})
	return c
}

// liveWait is one blocked lenny/request_input call.
type liveWait struct {
	conn      *waitConn
	requestID string
	waitID    string
	done      chan struct{}
}

func (c *waitConn) start(t *testing.T, store sessionstore.Store, requestID string) *liveWait {
	t.Helper()
	w := &liveWait{conn: c, requestID: requestID, done: make(chan struct{})}
	args := fmt.Sprintf(`{"sessionId":%q,"requestId":%q,"parts":[{"type":"text","inline":"q"}]}`, waitSession, requestID)
	ctx := inputwaitrecovery.NewContext(context.Background(), c.gen)
	go func() {
		defer close(w.done)
		_, _, _ = c.srv.DispatchTool(ctx, "lenny/request_input", json.RawMessage(args))
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range c.reg.PendingDetailsForSession(waitSession) {
			if p.RequestID == requestID {
				w.waitID = p.WaitID
			}
		}
		if w.waitID != "" {
			if row, _ := store.Get(context.Background(), "acme", waitSession); row.InputWaitID == w.waitID {
				return w
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("wait %s/%s never applied its entry write", c.name, requestID)
	return nil
}

// end ends the wait by a reply or a peer cancel and waits for its exit write.
func (w *liveWait) end(t *testing.T, reply bool) {
	t.Helper()
	if reply {
		_ = w.conn.reg.Resolve(waitSession, w.requestID, "a")
	} else {
		w.conn.reg.Cancel(waitSession, w.requestID)
	}
	select {
	case <-w.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("wait %s/%s did not return", w.conn.name, w.requestID)
	}
}

// waitModel is the reference outcome of the entry and exit writes on one row.
type waitModel struct {
	state session.State
	named string
}

// enter applies the entry write: the row names the wait.
func (m *waitModel) enter(w *liveWait) {
	m.state, m.named = session.StateInputRequired, w.waitID
}

// exit applies the exit write of w with the waits still live on w's
// connection, re-pointing to the first by request ID or restoring running.
func (m *waitModel) exit(w *liveWait, live []*liveWait) {
	if m.state != session.StateInputRequired || m.named != w.waitID {
		return
	}
	var next *liveWait
	for _, o := range live {
		if o.conn == w.conn && (next == nil || o.requestID < next.requestID) {
			next = o
		}
	}
	if next != nil {
		m.named = next.waitID
		return
	}
	m.state, m.named = session.StateRunning, ""
}

func seedWaitSession(t *testing.T) sessionstore.Store {
	t.Helper()
	store := memstore.New()
	now := time.Now().UTC()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: waitSession, TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "echo",
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

// spec: 7.2 (Session state machine)
// diagnosis: under randomized entries and exits of concurrent
// lenny/request_input waits on one session, over one connection's registry
// and over the registries of the connection the adapter left and its next
// connection, the row diverged from the entry and exit rules. Either an
// earlier wait's exit returned a later wait's row to running, an exit did not
// re-point the row to a wait still live on its connection, a reused
// requestId on the next connection let the left connection's exit apply, or
// the row named a wait no registry holds once every wait had ended.
func TestRequestInputWaitsFollowEntryAndExitRules_spec_7_2(t *testing.T) {
	for _, conns := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d registries", conns), func(t *testing.T) {
			seed := time.Now().UnixNano()
			t.Logf("seed %d", seed)
			rng := rand.New(rand.NewSource(seed))
			store := seedWaitSession(t)
			var ids atomic.Int64
			cs := []*waitConn{newWaitConn("left", store, &ids)}
			if conns == 2 {
				cs = append(cs, newWaitConn("next", store, &ids))
			}
			model := &waitModel{state: session.StateRunning}
			var live []*liveWait
			for step := 0; step < 120; step++ {
				if len(live) == 0 || (len(live) < 4 && rng.Intn(2) == 0) {
					c := cs[rng.Intn(len(cs))]
					// Request IDs are drawn from a small set, so the next
					// connection reuses the left connection's requestId.
					reqID := fmt.Sprintf("req-%d", rng.Intn(3))
					if c.reg.Pending(waitSession, reqID) {
						continue
					}
					w := c.start(t, store, reqID)
					live = append(live, w)
					model.enter(w)
				} else {
					i := rng.Intn(len(live))
					w := live[i]
					live = append(live[:i], live[i+1:]...)
					w.end(t, rng.Intn(2) == 0)
					model.exit(w, live)
				}
				row, err := store.Get(context.Background(), "acme", waitSession)
				if err != nil {
					t.Fatal(err)
				}
				if row.State != model.state || (model.state == session.StateInputRequired && row.InputWaitID != model.named) {
					t.Fatalf("step %d: row %s/%q, want %s/%q", step, row.State, row.InputWaitID, model.state, model.named)
				}
				if row.State == session.StateInputRequired && !namesLiveWait(row.InputWaitID, live) {
					t.Fatalf("step %d: row names %q, which no live wait carries", step, row.InputWaitID)
				}
			}
			for _, w := range live {
				w.end(t, true)
			}
			if row, _ := store.Get(context.Background(), "acme", waitSession); row.State != session.StateRunning {
				t.Errorf("after the last exit the row is %s, want running", row.State)
			}
		})
	}
}

func namesLiveWait(waitID string, live []*liveWait) bool {
	for _, w := range live {
		if w.waitID == waitID {
			return true
		}
	}
	return false
}

// spec: 7.2 (Session state machine)
// diagnosis: concurrent lenny/request_input waits entered and ended at the
// same time on one connection left the row input_required after every wait
// had returned, or the row named a wait that had already left the registry
// while waits were still live.
func TestConcurrentRequestInputWaitsEndRunning_spec_7_2(t *testing.T) {
	store := seedWaitSession(t)
	var ids atomic.Int64
	c := newWaitConn("conn", store, &ids)
	for round := 0; round < 20; round++ {
		var wg sync.WaitGroup
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				args := fmt.Sprintf(`{"sessionId":%q,"requestId":"r%d-%d","parts":[{"type":"text","inline":"q"}]}`, waitSession, round, i)
				ctx, cancel := context.WithTimeout(inputwaitrecovery.NewContext(context.Background(), c.gen), time.Duration(1+i)*time.Millisecond)
				defer cancel()
				_, _, _ = c.srv.DispatchTool(ctx, "lenny/request_input", json.RawMessage(args))
			}(i)
		}
		wg.Wait()
		row, err := store.Get(context.Background(), "acme", waitSession)
		if err != nil {
			t.Fatal(err)
		}
		if row.State != session.StateRunning {
			t.Fatalf("round %d: row %s/%q after every wait returned, want running", round, row.State, row.InputWaitID)
		}
	}
}
