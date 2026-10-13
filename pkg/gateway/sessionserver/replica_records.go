// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"sync"
)

// This file holds the per-replica records the resume driver and the
// terminal transition keep in memory, keyed by session ID. No other replica
// reads them and a crash drops them. Each record is bounded by the goroutine
// or claim that adds its entry, which removes the entry on every return.

// attemptRecord is the resume attempt record: at most one in-flight resume
// attempt per session on this replica, with the attempt's cancel function.
// DriveResume inserts an entry, under the record's lock, before it starts
// the attempt goroutine, and a deferred removal in that goroutine removes it
// on every return of the attempt. The zero value is ready to use.
//
// spec: §7.3 (Resume driver)
type attemptRecord struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// begin registers an attempt for sessionID with its cancel function. It
// returns false, and registers nothing, when an attempt for the session is
// already in flight on this replica.
func (r *attemptRecord) begin(sessionID string, cancel context.CancelFunc) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancels == nil {
		r.cancels = map[string]context.CancelFunc{}
	}
	if _, inFlight := r.cancels[sessionID]; inFlight {
		return false
	}
	r.cancels[sessionID] = cancel
	return true
}

// end removes the session's attempt entry. Only the attempt that begin
// admitted calls it, so the entry it removes is its own.
func (r *attemptRecord) end(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cancels, sessionID)
}

// cancel calls the registered cancel function for the session's attempt,
// and does nothing when no attempt is registered. It reads no store, is
// idempotent, and is safe to call from inside the attempt.
func (r *attemptRecord) cancel(sessionID string) {
	r.mu.Lock()
	cancel := r.cancels[sessionID]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// size reports the number of attempts in flight.
func (r *attemptRecord) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cancels)
}

// terminalClaimEntry is one session's terminal claim state: the count of
// open writer claims and whether a terminal binding release runs.
type terminalClaimEntry struct {
	claims    int
	releasing bool
}

// terminalClaimRecord is the terminal claim record. BeginTerminal adds one
// writer claim per terminal writer, from before its terminal Update until
// its post-terminal hook returns; ReleaseTerminalBinding marks the one
// binding release it runs for the session. An entry is deleted once its
// count is zero and it marks no release. The zero value is ready to use.
//
// spec: §7.2 (Interactive Session Model), §10.1.5 (Stale Replica Behavior)
type terminalClaimRecord struct {
	mu      sync.Mutex
	entries map[string]*terminalClaimEntry
}

// open adds a writer claim for sessionID and returns the idempotent function
// that ends it.
func (r *terminalClaimRecord) open(sessionID string) func() {
	r.mu.Lock()
	r.entryLocked(sessionID).claims++
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			e := r.entries[sessionID]
			if e == nil {
				return
			}
			e.claims--
			r.pruneLocked(sessionID, e)
		})
	}
}

// startRelease marks a terminal binding release for sessionID and reports
// true, or reports false and marks nothing when a writer claim is open or a
// release already runs for the session.
func (r *terminalClaimRecord) startRelease(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.entries[sessionID]; e != nil && (e.claims > 0 || e.releasing) {
		return false
	}
	r.entryLocked(sessionID).releasing = true
	return true
}

// endRelease clears the session's release mark.
func (r *terminalClaimRecord) endRelease(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[sessionID]
	if e == nil {
		return
	}
	e.releasing = false
	r.pruneLocked(sessionID, e)
}

// size reports the number of sessions with an entry.
func (r *terminalClaimRecord) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

func (r *terminalClaimRecord) entryLocked(sessionID string) *terminalClaimEntry {
	if r.entries == nil {
		r.entries = map[string]*terminalClaimEntry{}
	}
	e := r.entries[sessionID]
	if e == nil {
		e = &terminalClaimEntry{}
		r.entries[sessionID] = e
	}
	return e
}

func (r *terminalClaimRecord) pruneLocked(sessionID string, e *terminalClaimEntry) {
	if e.claims <= 0 && !e.releasing && r.entries[sessionID] == e {
		delete(r.entries, sessionID)
	}
}

// size reports the number of sessions whose lock is held or waited on.
func (l *sessionLocks) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.locks)
}
