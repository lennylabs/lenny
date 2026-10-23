// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// Checkpointer is the harness's suspension checkpointer. It records each
// checkpoint and fails every one while Fail is set. It writes no snapshot,
// so a session it suspended resumes on the rebuild branch.
type Checkpointer struct {
	fail  atomic.Bool
	taken atomic.Int64
}

// CheckpointWithTrigger records one checkpoint.
func (c *Checkpointer) CheckpointWithTrigger(context.Context, string, string, checkpoint.Trigger) error {
	if c.fail.Load() {
		return fmt.Errorf("lifecycleharness: injected checkpoint failure")
	}
	c.taken.Add(1)
	return nil
}

// Fail makes every later checkpoint fail while on is true.
func (c *Checkpointer) Fail(on bool) { c.fail.Store(on) }

// Taken reports how many checkpoints succeeded.
func (c *Checkpointer) Taken() int64 { return c.taken.Load() }

// IdleWindow is the adapter activity window the harness passes to an idle
// suspension. The adapter measures it on the wall clock, so SuspendIdle
// waits it out before asking.
const IdleWindow = 1

// SuspendIdle waits out the adapter's activity window and starts the idle
// suspension of the session on the replica, as the watchdog's idle sweep
// does when the session's idle clock fires.
func (h *Harness) SuspendIdle(r *Replica, id string) {
	h.T.Helper()
	time.Sleep(IdleWindow*time.Second + 100*time.Millisecond)
	r.Server.SuspendIdle(context.Background(), h.Row(id), IdleWindow)
	h.Settle()
}

// Recheck runs the gateway leader's backlog re-check once on the replica,
// and returns how many podless suspended rows it moved to resume_pending.
func (h *Harness) Recheck(r *Replica) int {
	n := h.backlogRechecker(r).Pass(context.Background())
	h.Settle()
	return n
}

// backlogRechecker is the gateway leader's backlog re-check on r.
func (h *Harness) backlogRechecker(r *Replica) *sessionserver.BacklogRechecker {
	return &sessionserver.BacklogRechecker{
		Server: r.Server, Lister: h.Store,
		Tenants: func(context.Context) ([]string, error) { return []string{Tenant}, nil },
	}
}

// Send posts one message to the session on the replica and returns the
// answer's status and body.
func (h *Harness) Send(r *Replica, id, text string) (int, map[string]any) {
	h.T.Helper()
	return h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/messages", map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": text}},
	})
}

// AwaitState runs checker passes until the session reaches want or the
// pass budget ends, and returns the row.
func (h *Harness) AwaitState(id string, want session.State, passes int) sessionstore.Session {
	h.T.Helper()
	row := h.Row(id)
	for i := 0; i < passes && row.State != want; i++ {
		h.Pass()
		row = h.Row(id)
	}
	return row
}

// Bound returns the replica that binds the session, or nil.
func (h *Harness) Bound(id string) *Replica {
	for _, r := range h.Surviving() {
		if _, ok := r.Registry.Get(id); ok {
			return r
		}
	}
	return nil
}

// TreeMetrics records the delegation-tree recovery outcomes every replica
// observes.
type TreeMetrics struct {
	mu       sync.Mutex
	outcomes []string
	timeouts []string
}

// ObserveTreeRecoveryDuration records one recovery outcome.
func (m *TreeMetrics) ObserveTreeRecoveryDuration(_, outcome string, _ float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outcomes = append(m.outcomes, outcome)
}

// IncTreeRecoveryTimeout records one recovery timeout.
func (m *TreeMetrics) IncTreeRecoveryTimeout(_, timeoutType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timeouts = append(m.timeouts, timeoutType)
}

// Outcomes returns the recorded recovery outcomes.
func (m *TreeMetrics) Outcomes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.outcomes...)
}

// idleResolver is the watchdog's idle resolver for the harness runtime: it
// resolves no per-pool bound, so the watchdog's platform bound applies, and
// reports every session idle-suspendable, as the gateway's resolver does for
// a session-mode pool of a runtime that is not `type: mcp`.
// spec: §6.2 (maxClientIdleSeconds clock behavior across states)
type idleResolver struct{}

func (idleResolver) EffectiveMaxIdleSeconds(context.Context, sessionstore.Session) int { return 0 }

func (idleResolver) IdleSuspendable(context.Context, sessionstore.Session) (bool, error) {
	return true, nil
}
