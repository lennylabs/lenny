// SPDX-License-Identifier: MIT

// Package billingcheckpoint emits the §11.2.1 token_usage.checkpoint
// billing event: a periodic per-session token-usage snapshot written to
// the per-tenant billing stream at a configurable interval rather than
// only at session end. The session.created / session.completed billing
// events are lifecycle markers that carry no token counts, so the
// periodic checkpoints are the in-flight cost-attribution signal for a
// long-running session.
//
// The Checkpointer keeps a per-session baseline of the cumulative tokens
// it has already reported and emits each interval only the delta since
// the previous checkpoint (the §11.2.1 "token count for the checkpoint
// window"), so a consumer summing token_usage.checkpoint events
// reconstructs the session's total without double counting. A session
// that has produced no new tokens since the last checkpoint emits
// nothing.
//
// spec: §11.2.1 — token_usage.checkpoint. F-11.2.1.
package billingcheckpoint

import (
	"context"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/sessionusage"
)

// Session is one active session to checkpoint, identified by the tuple
// the §11.2.1 billing envelope needs for cost attribution.
type Session struct {
	TenantID  string
	SessionID string
	UserID    string
}

// SessionLister enumerates the active (non-terminal) sessions whose
// token usage is snapshotted each interval. cmd/lenny-gateway wires a
// SessionStore-backed implementation that walks the active tenants.
type SessionLister interface {
	ListActiveSessions(ctx context.Context) ([]Session, error)
}

// UsageReader reads a session's cumulative proxy-recorded token totals.
// *sessionusage.Memory and the durable backend satisfy it via Get.
type UsageReader interface {
	Get(ctx context.Context, tenantID, sessionID string) (sessionusage.Tokens, error)
}

// Checkpointer emits the §11.2.1 token_usage.checkpoint events. It is
// nil-safe at construction: a nil emitter, lister, or usage reader yields
// a no-op Checkpointer (the no-billing / no-metering minimal gateway).
type Checkpointer struct {
	emitter  *billingfanout.Emitter
	sessions SessionLister
	usage    UsageReader

	mu       sync.Mutex
	baseline map[string]sessionusage.Tokens
}

// New returns a Checkpointer, or nil when any dependency is absent (the
// periodic checkpoint is disabled).
func New(emitter *billingfanout.Emitter, sessions SessionLister, usage UsageReader) *Checkpointer {
	if emitter == nil || sessions == nil || usage == nil {
		return nil
	}
	return &Checkpointer{
		emitter:  emitter,
		sessions: sessions,
		usage:    usage,
		baseline: map[string]sessionusage.Tokens{},
	}
}

// Checkpoint snapshots every active session once: for each, it reads the
// cumulative token totals, emits a token_usage.checkpoint carrying the
// delta since the last checkpoint when that delta is positive, and
// records the new baseline. Baselines for sessions no longer active are
// pruned so the map cannot grow without bound. A read or list error for
// one session is skipped; the pass is best-effort.
func (c *Checkpointer) Checkpoint(ctx context.Context) {
	if c == nil {
		return
	}
	sessions, err := c.sessions.ListActiveSessions(ctx)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	live := make(map[string]struct{}, len(sessions))
	for _, s := range sessions {
		if s.TenantID == "" || s.SessionID == "" {
			continue
		}
		key := s.TenantID + "\x00" + s.SessionID
		live[key] = struct{}{}
		cur, err := c.usage.Get(ctx, s.TenantID, s.SessionID)
		if err != nil {
			continue
		}
		base := c.baseline[key]
		dIn := cur.Input - base.Input
		dOut := cur.Output - base.Output
		// A negative delta cannot occur for an append-only counter; guard
		// against it (e.g. a counter reset) by treating it as no new usage.
		if dIn < 0 {
			dIn = 0
		}
		if dOut < 0 {
			dOut = 0
		}
		if dIn == 0 && dOut == 0 {
			continue
		}
		c.emitter.Emit(ctx, billingfanout.TokenUsageCheckpoint(
			s.TenantID, s.SessionID, s.UserID, uint64(dIn), uint64(dOut)))
		c.baseline[key] = cur
	}
	for key := range c.baseline {
		if _, ok := live[key]; !ok {
			delete(c.baseline, key)
		}
	}
}

// Run drives Checkpoint on the §11.2.1 configurable interval until ctx is
// cancelled. A non-positive interval disables the loop (returns
// immediately). A nil Checkpointer returns immediately.
func (c *Checkpointer) Run(ctx context.Context, interval time.Duration) {
	if c == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Checkpoint(ctx)
		}
	}
}
