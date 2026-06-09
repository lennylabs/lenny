// SPDX-License-Identifier: MIT

// Package sessionidle implements the gateway side of the §11.3 line 199 /
// §6.2 lines 273-300 `maxIdleTime` control: the per-session idle cap
// resolver, the elicitation/request-input pause predicate the watchdog
// consults so a session blocked on a human or peer is not reaped, and the
// activity stamper that advances `last_agent_activity_at` on qualifying
// agent events.
//
//   - Resolver resolves the effective `maxIdleTimeSeconds` for a session:
//     the §5.1 RuntimeDefinition `limits.maxIdleTimeSeconds` (default 600s
//     applied by the watchdog when zero), tightened by the §27.6 playground
//     idle override the create path already lands on the row's
//     `SessionTimeouts.MaxIdleSeconds`.
//   - PauseChecker reports whether a `running` session's idle timer is
//     paused. The §6.2 timer-behavior table pauses `input_required`
//     (a distinct session state, handled by the watchdog listing only
//     `running` rows), and §9.2 line 102 additionally pauses the timer
//     while the session waits for an elicitation response
//     ("waiting_for_human, not idle"). A session that raises an elicitation
//     stays in `running`, so the watchdog needs this predicate to honor the
//     pause. F-9.2.15.
//   - Stamper coalesces `last_agent_activity_at` writes to at most once per
//     second per session (§6.2 line 277) and persists them through the
//     session store, so a streaming or long-running session is never
//     mistaken for idle.
//
// spec: §6.2 lines 273-300; §9.2 line 102; §11.3 line 199. F-11.3.7 / F-9.2.15.
package sessionidle

import (
	"context"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// Resolver satisfies watchdog.IdleResolver. The runtime store may be nil,
// in which case only the per-session §27.6 playground override applies.
type Resolver struct {
	runtimes runtimestore.Store
}

// NewResolver returns a Resolver backed by the runtime store. A nil store
// disables the per-runtime lookup.
func NewResolver(runtimes runtimestore.Store) *Resolver {
	return &Resolver{runtimes: runtimes}
}

// EffectiveMaxIdleSeconds returns the most-restrictive `maxIdleTimeSeconds`
// for sess (in seconds), or 0 when neither the runtime nor the per-session
// override declares one — in which case the watchdog applies the platform
// default (600s). The §27.6 playground idle override is already landed
// onto sess.Timeouts.MaxIdleSeconds at create time (F-27.6.1), so a
// playground session resolves to its tighter cap here.
//
// spec: §11.3 line 199; §6.2 line 296; §27.6 line 201. F-11.3.7.
func (r *Resolver) EffectiveMaxIdleSeconds(ctx context.Context, sess sessionstore.Session) int {
	cap := 0
	if r.runtimes != nil && sess.RuntimeRef != "" {
		if rt, err := runtimestore.Resolve(ctx, r.runtimes, sess.RuntimeRef); err == nil &&
			rt.Limits != nil && rt.Limits.MaxIdleTimeSeconds > 0 {
			cap = rt.Limits.MaxIdleTimeSeconds
		}
	}
	if sess.Timeouts != nil && sess.Timeouts.MaxIdleSeconds > 0 {
		cap = minPositive(cap, int(sess.Timeouts.MaxIdleSeconds))
	}
	return cap
}

// minPositive returns the smaller of two caps, ignoring a zero (unset) cap
// on either side.
func minPositive(a, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

// PauseChecker satisfies watchdog.IdlePauseChecker. It reports whether a
// session's idle timer is paused because the session is blocked waiting on
// a human (a pending §9.2 elicitation) or a peer agent (a pending §8.5
// request_input). Either store may be nil; the corresponding signal is
// then unavailable and the checker falls through to the other.
type PauseChecker struct {
	interactions interactionstore.Store
	inputWaits   *inputwait.Registry
}

// NewPauseChecker returns a PauseChecker backed by the §9.2 interaction
// store and the §8.5 request_input registry.
func NewPauseChecker(interactions interactionstore.Store, inputWaits *inputwait.Registry) *PauseChecker {
	return &PauseChecker{interactions: interactions, inputWaits: inputWaits}
}

// IdlePaused reports whether the session's §11.3 idle timer is paused. The
// timer is paused while the session waits for an elicitation response (§9.2
// line 102 — "waiting_for_human, not idle") or for a peer's reply to a
// lenny/request_input (§6.2 `input_required` row). A store error is treated
// as "not paused" so a transient blip never wrongly suppresses idle
// reclamation forever; the platform `maxSessionAge` cap remains the
// backstop. F-9.2.15.
func (p *PauseChecker) IdlePaused(ctx context.Context, sess sessionstore.Session) bool {
	if p == nil {
		return false
	}
	if p.inputWaits != nil && len(p.inputWaits.PendingForSession(sess.ID)) > 0 {
		return true
	}
	if p.interactions != nil {
		pending, err := p.interactions.ListPending(ctx, sess.TenantID, sess.ID)
		if err == nil {
			for _, in := range pending {
				if in.Kind == interactionstore.KindElicitation {
					return true
				}
			}
		}
	}
	return false
}

// DefaultStampInterval is the §6.2 line 277 coalescing window: the stamper
// flushes `last_agent_activity_at` to the store at most once per second per
// session to avoid write amplification on rapid agent-event arrivals.
const DefaultStampInterval = time.Second

// Stamper coalesces and persists `last_agent_activity_at` writes. Stamp is
// safe for concurrent use and is non-blocking: the durable write runs in a
// background goroutine so the request path (event publish, proxy chunk,
// await_children) is never held on the store. A nil *Stamper is a no-op so
// callers can wire it unconditionally.
//
// spec: §6.2 lines 273-300 (qualifying events; ≤1/s flush). F-11.3.7.
type Stamper struct {
	store sessionstore.Store
	clock func() time.Time

	interval time.Duration

	mu        sync.Mutex
	lastWrite map[string]time.Time
}

// NewStamper returns a Stamper backed by the session store. The clock is
// optional; pass nil for time.Now. A nil store yields a Stamper whose Stamp
// is a no-op.
func NewStamper(store sessionstore.Store, clock func() time.Time) *Stamper {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Stamper{
		store:     store,
		clock:     clock,
		interval:  DefaultStampInterval,
		lastWrite: map[string]time.Time{},
	}
}

// Stamp records a qualifying agent activity for (tenantID, sessionID). It
// coalesces to ≤1 store write per interval per session; a stamp within the
// window after a prior write is a cheap in-memory no-op. The durable write
// advances the row's LastAgentActivityAt (and only advances it — a stamp
// can never move the anchor backwards) and is skipped for terminal rows.
func (s *Stamper) Stamp(tenantID, sessionID string) {
	if s == nil || s.store == nil || sessionID == "" {
		return
	}
	now := s.clock()
	s.mu.Lock()
	if last, ok := s.lastWrite[sessionID]; ok && now.Sub(last) < s.interval {
		s.mu.Unlock()
		return
	}
	s.lastWrite[sessionID] = now
	s.pruneLocked(now)
	s.mu.Unlock()
	go s.persist(tenantID, sessionID, now)
}

// Forget drops a session's coalescing entry. The terminal-side-effects
// path calls it so the in-memory map does not retain entries for completed
// sessions. Optional; pruneLocked also bounds the map by age.
func (s *Stamper) Forget(sessionID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.lastWrite, sessionID)
	s.mu.Unlock()
}

// pruneLocked drops coalescing entries older than the flush window once the
// map grows large, so a long-lived gateway does not accumulate one entry
// per session ever seen. An entry older than the window would be rewritten
// on its next stamp anyway, so dropping it is harmless. Caller holds mu.
func (s *Stamper) pruneLocked(now time.Time) {
	const pruneThreshold = 4096
	if len(s.lastWrite) < pruneThreshold {
		return
	}
	cutoff := now.Add(-s.interval)
	for id, t := range s.lastWrite {
		if t.Before(cutoff) {
			delete(s.lastWrite, id)
		}
	}
}

// persist writes the new activity instant to the durable store. It runs in
// its own bounded context off the request path. A failure is dropped: the
// next qualifying event re-attempts, and the worst case is a slightly
// stale anchor that the platform maxSessionAge cap still bounds.
func (s *Stamper) persist(tenantID, sessionID string, at time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.store.Update(ctx, tenantID, sessionID, func(r *sessionstore.Session) error {
		if session.IsTerminal(r.State) {
			return nil
		}
		if r.LastAgentActivityAt.Before(at) {
			r.LastAgentActivityAt = at
		}
		return nil
	})
}
