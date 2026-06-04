// SPDX-License-Identifier: MIT

package deadlock

import (
	"sort"
	"sync"
)

// AwaitTracker records which sessions are currently blocking inside a
// live lenny/await_children call and on which children. The §8.8
// deadlock detector needs the await edges to decide whether an awaiting
// parent's children are all blocked; those edges are ephemeral (only a
// running await call holds them), so the await handler registers the
// edge for the duration of its poll loop via Begin and drops it on
// return. spec: §8.8 line 981. F-8.8.6.
type AwaitTracker struct {
	mu       sync.Mutex
	awaiting map[string][]*awaitReg
}

type awaitReg struct {
	tenantID string
	childIDs []string
}

// AwaitingSession is one session with a live await call and its tenant.
type AwaitingSession struct {
	TenantID  string
	SessionID string
}

// NewAwaitTracker returns an empty tracker.
func NewAwaitTracker() *AwaitTracker {
	return &AwaitTracker{awaiting: map[string][]*awaitReg{}}
}

// Begin registers that sessionID (in tenantID) is awaiting childIDs and
// returns a function that drops the registration. The returned func is
// safe to call once; callers defer it at the top of the await poll loop.
// A nil tracker yields a no-op so the await handler degrades gracefully
// when the detector is not wired.
func (t *AwaitTracker) Begin(tenantID, sessionID string, childIDs []string) func() {
	if t == nil {
		return func() {}
	}
	reg := &awaitReg{tenantID: tenantID, childIDs: append([]string(nil), childIDs...)}
	t.mu.Lock()
	t.awaiting[sessionID] = append(t.awaiting[sessionID], reg)
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		regs := t.awaiting[sessionID]
		for i, r := range regs {
			if r == reg {
				t.awaiting[sessionID] = append(regs[:i], regs[i+1:]...)
				break
			}
		}
		if len(t.awaiting[sessionID]) == 0 {
			delete(t.awaiting, sessionID)
		}
	}
}

// AwaitedChildren returns the deduplicated, sorted set of children
// sessionID is currently awaiting across all of its live await calls.
// Returns nil when the session is not awaiting.
func (t *AwaitTracker) AwaitedChildren(sessionID string) []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, r := range t.awaiting[sessionID] {
		for _, c := range r.childIDs {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	sort.Strings(out)
	return out
}

// AwaitingSessions returns the sorted set of sessions with at least one
// live await call, each paired with its tenant — the seeds the detector
// closes over to build a snapshot.
func (t *AwaitTracker) AwaitingSessions() []AwaitingSession {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]AwaitingSession, 0, len(t.awaiting))
	for id, regs := range t.awaiting {
		tenant := ""
		if len(regs) > 0 {
			tenant = regs[0].tenantID
		}
		out = append(out, AwaitingSession{TenantID: tenant, SessionID: id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}
