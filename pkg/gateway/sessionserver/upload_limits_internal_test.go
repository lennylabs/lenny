// SPDX-License-Identifier: MIT

package sessionserver

import (
	"sync"
	"testing"
)

// spec: §11.1 lines 10-11 — concurrent-upload + per-session
// cumulative-size admission caps. These unit tests exercise the
// uploadLimiter state machine directly (the handler integration lives
// in upload_limits_test.go). F-11.1.5, F-11.1.6.

func TestNewUploadLimiterNilWhenAllZero(t *testing.T) {
	if l := newUploadLimiter(0, 0, 0); l != nil {
		t.Fatalf("newUploadLimiter(0,0,0) should be nil (unconfigured pass-through), got %#v", l)
	}
	for _, tc := range []struct {
		name          string
		perSess, glob int
		bytesPerSess  int64
	}{
		{"per-session-concurrency", 1, 0, 0},
		{"global-concurrency", 0, 1, 0},
		{"per-session-bytes", 0, 0, 1},
	} {
		if l := newUploadLimiter(tc.perSess, tc.glob, tc.bytesPerSess); l == nil {
			t.Errorf("%s: newUploadLimiter should return a limiter when a cap is set", tc.name)
		}
	}
}

// A nil *uploadLimiter is the unconfigured posture; every method must be
// a safe pass-through. spec: §11.1 lines 10-11.
func TestUploadLimiterNilPassthrough(t *testing.T) {
	var l *uploadLimiter
	release, scope, ok := l.acquireSlot("s1")
	if !ok || scope != "" {
		t.Fatalf("nil limiter acquireSlot: ok=%v scope=%q, want true/empty", ok, scope)
	}
	release() // must not panic
	if _, _, exceeds := l.wouldExceedBytes("s1", 1<<40); exceeds {
		t.Error("nil limiter wouldExceedBytes should never exceed")
	}
	if _, _, ok := l.commitBytes("s1", 1<<40); !ok {
		t.Error("nil limiter commitBytes should always admit")
	}
	l.closeSession("s1") // must not panic
}

func TestUploadLimiterPerSessionConcurrency(t *testing.T) {
	l := newUploadLimiter(2, 0, 0)
	r1, _, ok1 := l.acquireSlot("s1")
	r2, _, ok2 := l.acquireSlot("s1")
	if !ok1 || !ok2 {
		t.Fatalf("first two acquisitions should succeed: ok1=%v ok2=%v", ok1, ok2)
	}
	if _, scope, ok := l.acquireSlot("s1"); ok || scope != scopeUploadSession {
		t.Fatalf("third acquisition should fail with per-session scope: ok=%v scope=%q", ok, scope)
	}
	// A different session is unaffected by another session's count.
	if r, _, ok := l.acquireSlot("s2"); !ok {
		t.Fatalf("a different session should still admit: ok=%v", ok)
	} else {
		r()
	}
	// Releasing one s1 slot frees capacity for s1.
	r1()
	if r, _, ok := l.acquireSlot("s1"); !ok {
		t.Fatalf("after release, s1 should admit again: ok=%v", ok)
	} else {
		r()
	}
	r2()
	if got := l.sessionInflight["s1"]; got != 0 {
		t.Errorf("s1 inflight after all releases: got %d, want 0", got)
	}
	if l.globalInflight != 0 {
		t.Errorf("globalInflight after all releases: got %d, want 0", l.globalInflight)
	}
}

func TestUploadLimiterGlobalConcurrency(t *testing.T) {
	l := newUploadLimiter(0, 2, 0)
	r1, _, _ := l.acquireSlot("s1")
	r2, _, _ := l.acquireSlot("s2")
	// Two in-flight across distinct sessions saturates the global cap.
	if _, scope, ok := l.acquireSlot("s3"); ok || scope != scopeUploadGlobal {
		t.Fatalf("third acquisition should fail with global scope: ok=%v scope=%q", ok, scope)
	}
	r1()
	if r, _, ok := l.acquireSlot("s3"); !ok {
		t.Fatalf("after a release, the global cap should admit again: ok=%v", ok)
	} else {
		r()
	}
	r2()
}

// When both scopes are configured, the per-session scope is checked
// first so a client flooding one session sees the precise reason rather
// than a global-capacity message. spec: §11.1 line 10.
func TestUploadLimiterPerSessionCheckedBeforeGlobal(t *testing.T) {
	l := newUploadLimiter(1, 10, 0)
	r1, _, ok := l.acquireSlot("s1")
	if !ok {
		t.Fatal("first acquisition should succeed")
	}
	if _, scope, ok := l.acquireSlot("s1"); ok || scope != scopeUploadSession {
		t.Fatalf("second same-session acquisition should fail per-session, not global: ok=%v scope=%q", ok, scope)
	}
	r1()
}

// release is idempotent: a double call must not drive either counter
// negative. spec: §11.1 line 10.
func TestUploadLimiterReleaseIdempotent(t *testing.T) {
	l := newUploadLimiter(0, 5, 0)
	r, _, _ := l.acquireSlot("s1")
	r()
	r() // second call is a no-op
	if l.globalInflight != 0 {
		t.Fatalf("globalInflight after double release: got %d, want 0", l.globalInflight)
	}
	// Capacity is intact: five fresh slots still acquire.
	for i := 0; i < 5; i++ {
		if _, _, ok := l.acquireSlot("s1"); !ok {
			t.Fatalf("acquisition %d after double release should succeed", i)
		}
	}
}

func TestUploadLimiterWouldExceedBytes(t *testing.T) {
	l := newUploadLimiter(0, 0, 20)
	if _, _, exceeds := l.wouldExceedBytes("s1", 20); exceeds {
		t.Error("incoming exactly at the cap should not exceed")
	}
	if _, _, exceeds := l.wouldExceedBytes("s1", 21); !exceeds {
		t.Error("incoming one past the cap should exceed")
	}
	// A non-positive incoming (no/unknown Content-Length) never exceeds;
	// the post-hoc commitBytes enforces against the streamed bytes.
	if _, _, exceeds := l.wouldExceedBytes("s1", -1); exceeds {
		t.Error("an unknown Content-Length should not early-reject")
	}
	// After committing 15, only 5 of headroom remains.
	if _, _, ok := l.commitBytes("s1", 15); !ok {
		t.Fatal("commit 15 under a 20 cap should succeed")
	}
	if _, _, exceeds := l.wouldExceedBytes("s1", 6); !exceeds {
		t.Error("6 more on top of 15 (cap 20) should exceed")
	}
	if _, _, exceeds := l.wouldExceedBytes("s1", 5); exceeds {
		t.Error("5 more on top of 15 (cap 20) should fit exactly")
	}
}

func TestUploadLimiterCommitBytesBoundaryAndAccumulation(t *testing.T) {
	l := newUploadLimiter(0, 0, 20)
	if total, limit, ok := l.commitBytes("s1", 20); !ok || total != 20 || limit != 20 {
		t.Fatalf("commit exactly at cap: total=%d limit=%d ok=%v, want 20/20/true", total, limit, ok)
	}
	if total, _, ok := l.commitBytes("s1", 1); ok || total != 20 {
		t.Fatalf("commit one past cap: total=%d ok=%v, want 20/false (no add)", total, ok)
	}
	// A zero/negative commit is a no-op that returns the running total.
	if total, _, ok := l.commitBytes("s1", 0); !ok || total != 20 {
		t.Fatalf("zero commit: total=%d ok=%v, want 20/true", total, ok)
	}
	// A distinct session accumulates independently.
	if total, _, ok := l.commitBytes("s2", 5); !ok || total != 5 {
		t.Fatalf("commit on s2: total=%d ok=%v, want 5/true", total, ok)
	}
}

func TestUploadLimiterCloseSessionClearsBytes(t *testing.T) {
	l := newUploadLimiter(0, 0, 20)
	if _, _, ok := l.commitBytes("s1", 15); !ok {
		t.Fatal("commit 15 should succeed")
	}
	l.closeSession("s1")
	// The cumulative total is reset, so a fresh 15 fits again.
	if total, _, ok := l.commitBytes("s1", 15); !ok || total != 15 {
		t.Fatalf("after closeSession the byte total must reset: total=%d ok=%v, want 15/true", total, ok)
	}
}

// closeSession must not disturb in-flight concurrency slots: a slot
// acquired before the window closes still releases cleanly afterward.
// spec: §11.1 lines 10-11.
func TestUploadLimiterCloseSessionLeavesInflight(t *testing.T) {
	l := newUploadLimiter(1, 0, 20)
	r, _, ok := l.acquireSlot("s1")
	if !ok {
		t.Fatal("acquire should succeed")
	}
	l.closeSession("s1")
	r()
	if l.globalInflight != 0 || l.sessionInflight["s1"] != 0 {
		t.Fatalf("inflight after release post-close: global=%d session=%d, want 0/0",
			l.globalInflight, l.sessionInflight["s1"])
	}
}

func TestUploadLimiterUnlimitedBytesNeverTracks(t *testing.T) {
	l := newUploadLimiter(1, 0, 0) // concurrency cap set, byte cap unlimited
	if _, _, exceeds := l.wouldExceedBytes("s1", 1<<40); exceeds {
		t.Error("unlimited byte cap should never exceed")
	}
	if _, _, ok := l.commitBytes("s1", 1<<40); !ok {
		t.Error("unlimited byte cap should always admit")
	}
	if len(l.sessionBytes) != 0 {
		t.Errorf("unlimited byte cap must not track per-session bytes: got %d entries", len(l.sessionBytes))
	}
}

// Concurrency counters stay consistent under parallel acquire/release.
// Run with -race to surface data races on the shared maps and counter.
// spec: §11.1 line 10.
func TestUploadLimiterConcurrentAcquireRelease(t *testing.T) {
	l := newUploadLimiter(0, 1000, 0)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, _, ok := l.acquireSlot("s1"); ok {
				r()
			}
		}()
	}
	wg.Wait()
	if l.globalInflight != 0 {
		t.Fatalf("globalInflight after concurrent acquire/release: got %d, want 0", l.globalInflight)
	}
	if l.sessionInflight["s1"] != 0 {
		t.Fatalf("session inflight after concurrent acquire/release: got %d, want 0", l.sessionInflight["s1"])
	}
}
