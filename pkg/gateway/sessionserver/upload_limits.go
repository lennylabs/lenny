// SPDX-License-Identifier: MIT

package sessionserver

import "sync"

// §11.1 line 10-11 upload-admission scopes the §11.1 table names that
// the §4.1 Upload Handler subsystem limiter does not cover. The
// subsystem limiter (uploadSubsystem.Limiter) is a per-replica
// back-pressure semaphore that returns 503 SUBSYSTEM_UNAVAILABLE when
// the upload work-queue saturates; it is sized to the §4.1
// extraction-threshold default and protects gateway goroutines, not
// per-caller fairness. These admission scopes are the policy caps:
//
//   - Concurrent uploads, per-session and global (§11.1 line 10) — a
//     misbehaving client must not hold unbounded parallel upload
//     sockets against one session or against the replica as a whole.
//   - Upload size, per-session cumulative (§11.1 line 11) — the
//     per-file (per-blob) cap lives at UploadMaxBodyBytes; the
//     per-session axis bounds the sum of all uploads in one session so
//     a single session cannot consume the whole tenant storage quota
//     on its own.
//
// A breach of a concurrency scope is surfaced as 429 RATE_LIMITED
// (retryable: the caller proceeds once an in-flight upload finishes); a
// breach of the per-session cumulative-size cap is surfaced as 429
// QUOTA_EXCEEDED (not retryable: re-uploading does not free the cap).
const (
	scopeUploadSession = "upload_session"
	scopeUploadGlobal  = "upload_global"
)

// uploadLimiter enforces the §11.1 line 10-11 per-session and global
// concurrent-upload caps and the per-session cumulative upload-size cap
// for one gateway replica. Concurrent-upload counting is inherently
// per-replica: an in-flight upload holds an HTTP body stream on the
// replica serving it, so the replica-local count is the meaningful
// "global concurrent uploads" figure the §11.1 table names. The
// per-session cumulative byte total is also tracked per replica; the
// §11.2 per-tenant storage quota (Redis-backed, cross-replica) remains
// the binding cross-replica backstop on total consumption.
//
// All methods are goroutine-safe and nil-receiver safe: a nil
// *uploadLimiter is the unconfigured posture (tests, minimal gateway)
// and every method is a pass-through that admits without tracking.
type uploadLimiter struct {
	// maxConcurrentPerSession caps in-flight uploads against one
	// session. Zero means the per-session concurrency scope is
	// unlimited. spec: §11.1 line 10.
	maxConcurrentPerSession int
	// maxConcurrentGlobal caps in-flight uploads across the replica.
	// Zero means the global concurrency scope is unlimited.
	// spec: §11.1 line 10.
	maxConcurrentGlobal int
	// maxBytesPerSession caps the cumulative size of all uploads in one
	// session. Zero means the per-session size scope is unlimited.
	// spec: §11.1 line 11.
	maxBytesPerSession int64

	mu              sync.Mutex
	sessionInflight map[string]int
	globalInflight  int
	sessionBytes    map[string]int64
}

// newUploadLimiter returns a limiter for the supplied §11.1 caps, or nil
// when no cap is configured — a nil limiter is the pass-through posture
// every call site already tolerates, so the unconfigured gateway pays no
// per-upload bookkeeping. spec: §11.1 lines 10-11.
func newUploadLimiter(maxPerSession, maxGlobal int, maxBytesPerSession int64) *uploadLimiter {
	if maxPerSession <= 0 && maxGlobal <= 0 && maxBytesPerSession <= 0 {
		return nil
	}
	return &uploadLimiter{
		maxConcurrentPerSession: maxPerSession,
		maxConcurrentGlobal:     maxGlobal,
		maxBytesPerSession:      maxBytesPerSession,
		sessionInflight:         map[string]int{},
		sessionBytes:            map[string]int64{},
	}
}

// acquireSlot admits one in-flight upload against the §11.1 concurrency
// caps. On success it returns a release func (idempotent; run it on the
// handler exit path via defer) and ok=true. When a scope is saturated it
// returns (nil, <violated scope>, false) so the caller can attribute the
// 429. The per-session scope is checked before the global scope so a
// client that floods one session sees the precise reason. A nil limiter
// admits unconditionally. spec: §11.1 line 10.
func (l *uploadLimiter) acquireSlot(sid string) (release func(), scope string, ok bool) {
	if l == nil {
		return func() {}, "", true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.maxConcurrentPerSession > 0 && l.sessionInflight[sid] >= l.maxConcurrentPerSession {
		return nil, scopeUploadSession, false
	}
	if l.maxConcurrentGlobal > 0 && l.globalInflight >= l.maxConcurrentGlobal {
		return nil, scopeUploadGlobal, false
	}
	l.globalInflight++
	l.sessionInflight[sid]++
	released := false
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if released {
			return
		}
		released = true
		if l.globalInflight > 0 {
			l.globalInflight--
		}
		if n := l.sessionInflight[sid] - 1; n <= 0 {
			delete(l.sessionInflight, sid)
		} else {
			l.sessionInflight[sid] = n
		}
	}, "", true
}

// wouldExceedBytes reports whether admitting `incoming` more bytes would
// push the session past its cumulative cap, using the declared
// Content-Length for an early rejection before any body is streamed. It
// does not reserve — the authoritative check-and-add is commitBytes,
// which runs against the bytes actually streamed. A nil limiter, an
// unlimited cap, or a non-positive `incoming` never exceeds.
// spec: §11.1 line 11.
func (l *uploadLimiter) wouldExceedBytes(sid string, incoming int64) (current, limit int64, exceeds bool) {
	if l == nil || l.maxBytesPerSession <= 0 || incoming <= 0 {
		return 0, 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	current = l.sessionBytes[sid]
	return current, l.maxBytesPerSession, current+incoming > l.maxBytesPerSession
}

// commitBytes is the authoritative per-session cumulative-size check: it
// atomically adds `n` to the session total only if the result stays
// within the cap, returning the resulting (or, on rejection, the
// unchanged current) total, the limit, and whether the add was admitted.
// The caller invokes it once the streamed byte count is final so the
// per-session axis is enforced against bytes actually written even when
// a client under-declares Content-Length. A nil limiter or unlimited cap
// admits without tracking. spec: §11.1 line 11.
func (l *uploadLimiter) commitBytes(sid string, n int64) (total, limit int64, ok bool) {
	if l == nil || l.maxBytesPerSession <= 0 {
		return 0, 0, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.sessionBytes[sid]
	if n <= 0 {
		return current, l.maxBytesPerSession, true
	}
	proposed := current + n
	if proposed > l.maxBytesPerSession {
		return current, l.maxBytesPerSession, false
	}
	l.sessionBytes[sid] = proposed
	return proposed, l.maxBytesPerSession, true
}

// closeSession drops the per-session cumulative byte total when the
// upload window closes (§7.4 line 463 finalize / terminal transition).
// In-flight concurrency registrations are NOT touched here: each
// in-flight upload decrements its own slot via the release closure
// acquireSlot returned, so clearing the count here would corrupt the
// global tally. A nil limiter is a no-op. spec: §11.1 line 11.
func (l *uploadLimiter) closeSession(sid string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.sessionBytes, sid)
}
