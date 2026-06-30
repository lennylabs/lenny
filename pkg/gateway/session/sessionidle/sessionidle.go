// SPDX-License-Identifier: MIT

// Package sessionidle implements the gateway side of the §11.3 line 199 /
// §6.2 `maxClientIdleSeconds` control: the per-session idle-cap resolver and
// the activity stamper that advances `last_agent_activity_at` on qualifying
// agent events.
//
//   - Resolver resolves the effective `maxClientIdleSeconds` for a session.
//     `sessionPolicy.maxClientIdleSeconds` is the single platform idle bound
//     (it replaces the former pool-level `runtime.limits.maxIdleTimeSeconds`
//     knob). Its default is the pool's effective `maxSessionAgeSeconds`, so a
//     session with no explicit idle bound is reclaimed at the age cap rather
//     than at a fixed 600s. The §27.6 playground idle override the create
//     path lands on the row's `SessionTimeouts.MaxIdleSeconds` tightens it
//     min-wins through the existing most-restrictive resolution.
//   - Stamper coalesces `last_agent_activity_at` writes to at most once per
//     second per session (§6.2) and persists them through the session store,
//     so a streaming or long-running session is never mistaken for idle.
//
// The clock has its own pause table, distinct from the `maxSessionAge` pause
// table. It runs in `running`, `input_required`, and `awaiting_client_action`
// (an elicitation wait keeps the session in `running`, so the clock runs
// there too) and is paused in `suspended`, `resume_pending`, `resuming`,
// `finalizing`, and terminal states. The watchdog implements the pause table
// by listing only the running states for the idle sweep, so this package
// carries no pause predicate.
//
// spec: §6.2 (maxClientIdleSeconds clock); §9.2 (elicitation-wait idle
// clock); §11.3 line 199 (max client idle row). F-11.3.7 / F-9.2.15.
package sessionidle

import (
	"context"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionage"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// Resolver satisfies watchdog.IdleResolver. It resolves the effective
// `sessionPolicy.maxClientIdleSeconds` for a session. Either store may be
// nil; the resolver then consults only the non-nil surface, and a session
// with no resolvable configuration returns 0 so the watchdog applies its
// platform default.
type Resolver struct {
	runtimes runtimestore.Store
	pools    poolstore.Store
	// age resolves the effective `maxSessionAgeSeconds`, which is the
	// default `maxClientIdleSeconds` when no idle bound is declared.
	age *sessionage.Resolver
}

// NewResolver returns a Resolver backed by the runtime and pool stores. A
// nil store disables the corresponding lookup.
func NewResolver(runtimes runtimestore.Store, pools poolstore.Store) *Resolver {
	return &Resolver{
		runtimes: runtimes,
		pools:    pools,
		age:      sessionage.New(runtimes, pools),
	}
}

// EffectiveMaxIdleSeconds returns the most-restrictive `maxClientIdleSeconds`
// for sess (in seconds), or 0 when no configuration surface resolves a cap —
// in which case the watchdog applies its platform default.
//
// Resolution, most-restrictive-wins:
//
//   - The §5.2 `sessionPolicy.maxClientIdleSeconds` declared on the runtime
//     and/or pool. When neither declares one, the bound defaults to the
//     effective `maxSessionAgeSeconds` (§6.2: "The default value is the
//     pool's effective maxSessionAgeSeconds"), so an unbounded session is
//     reclaimed at the age cap rather than at a fixed default.
//   - The §27.6 playground idle override, already landed onto
//     `sess.Timeouts.MaxIdleSeconds` at create time (F-27.6.1), tightens the
//     resolved cap min-wins.
//
// spec: §11.3 line 199 (max client idle row); §6.2 (maxClientIdleSeconds
// clock default); §27.6 (playground idle override). F-11.3.7.
func (r *Resolver) EffectiveMaxIdleSeconds(ctx context.Context, sess sessionstore.Session) int {
	cap := r.policyIdleSeconds(ctx, sess)
	if cap <= 0 {
		// §6.2: the single idle bound defaults to the pool's effective
		// maxSessionAgeSeconds. The age resolver returns 0 when neither
		// the runtime nor the pool declares an age cap, leaving the
		// watchdog's platform default in force.
		cap = r.age.EffectiveMaxSessionAgeSeconds(ctx, sess)
	}
	// spec: §27.6 line 201 — the playground idle override (landed on
	// Timeouts.MaxIdleSeconds at create time) tightens the bound min-wins.
	if sess.Timeouts != nil && sess.Timeouts.MaxIdleSeconds > 0 {
		cap = minPositive(cap, int(sess.Timeouts.MaxIdleSeconds))
	}
	return cap
}

// policyIdleSeconds returns the most-restrictive
// `sessionPolicy.maxClientIdleSeconds` declared across the runtime and pool
// configuration surfaces, or 0 when neither declares one.
func (r *Resolver) policyIdleSeconds(ctx context.Context, sess sessionstore.Session) int {
	cap := 0
	if r.runtimes != nil && sess.RuntimeRef != "" {
		if rt, err := runtimestore.Resolve(ctx, r.runtimes, sess.RuntimeRef); err == nil &&
			rt.SessionPolicy != nil && rt.SessionPolicy.MaxClientIdleSeconds > 0 {
			cap = rt.SessionPolicy.MaxClientIdleSeconds
		}
	}
	if r.pools != nil && sess.PoolRef != "" {
		if p, err := r.pools.Get(ctx, sess.PoolRef); err == nil &&
			p.SessionPolicy != nil && p.SessionPolicy.MaxClientIdleSeconds > 0 {
			cap = minPositive(cap, p.SessionPolicy.MaxClientIdleSeconds)
		}
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

// DefaultStampInterval is the §6.2 coalescing window: the stamper flushes
// `last_agent_activity_at` to the store at most once per second per session
// to avoid write amplification on rapid agent-event arrivals.
const DefaultStampInterval = time.Second

// Stamper coalesces and persists `last_agent_activity_at` writes. Stamp is
// safe for concurrent use and is non-blocking: the durable write runs in a
// background goroutine so the request path (event publish, proxy chunk,
// await_children) is never held on the store. A nil *Stamper is a no-op so
// callers can wire it unconditionally.
//
// spec: §6.2 (qualifying events; ≤1/s flush). F-11.3.7.
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
