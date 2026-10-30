// SPDX-License-Identifier: MIT

// Package statusdedup implements the §4.6.1 statusUpdateDeduplicationWindow:
// a minimum interval between consecutive status writes for the same
// resource. When a reconcile produces a status change within the window
// of the previous write for that resource, the write is deferred until the
// window expires, coalescing rapid transitions (e.g.
// Pending → Creating → Running within a few hundred milliseconds) into one
// API server write. The deduplication is trailing: the last observed
// status in the window is always written; only intermediate writes are
// suppressed. This is the primary mechanism for reducing etcd write
// pressure on managed Kubernetes where compaction and quota-backend-bytes
// are not operator-controllable.
package statusdedup

import (
	"sync"
	"time"
)

// Gate tracks the last permitted status-write time per resource key and
// enforces the deduplication window. A nil *Gate and a Gate with a
// non-positive window both disable deduplication: every write is allowed.
type Gate struct {
	window time.Duration
	now    func() time.Time

	mu   sync.Mutex
	last map[string]time.Time
}

// New returns a Gate enforcing the given minimum interval between status
// writes for the same key. A non-positive window disables deduplication.
func New(window time.Duration) *Gate {
	return &Gate{window: window, last: make(map[string]time.Time)}
}

// Allow reports whether a status write for key is permitted now. When
// permitted it records the write time and returns (true, 0). When the
// previous write for key was within the window, it returns
// (false, remaining): the caller defers the write and requeues after
// remaining so the trailing (latest) status lands once the window
// expires. A nil Gate or a non-positive window always permits the write.
func (g *Gate) Allow(key string) (bool, time.Duration) {
	if g == nil || g.window <= 0 {
		return true, 0
	}
	now := time.Now
	if g.now != nil {
		now = g.now
	}
	at := now()

	g.mu.Lock()
	defer g.mu.Unlock()
	prev, seen := g.last[key]
	if seen {
		if elapsed := at.Sub(prev); elapsed < g.window {
			return false, g.window - elapsed
		}
	}
	g.last[key] = at
	return true, 0
}

// Forget drops a key's last-write record so a deleted-then-recreated
// resource is not gated by the prior incarnation's write time.
func (g *Gate) Forget(key string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.last, key)
}
