// SPDX-License-Identifier: MIT

package wait

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// spec: 17.4 (condition wait succeeds before the deadline)
// diagnosis: A satisfied predicate did not return successfully —
//
//	the poll loop or the condition check is wrong.
func TestForSucceedsBeforeDeadline(t *testing.T) {
	t.Parallel()
	var n int32
	start := time.Now()
	For(t, 5*time.Second, "counter reaches 3", func() (bool, error) {
		atomic.AddInt32(&n, 1)
		return atomic.LoadInt32(&n) >= 3, nil
	})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("expected fast convergence; took %s", elapsed)
	}
}

// spec: 17.4 (predicate error fails the wait immediately)
// diagnosis: A predicate error was treated as "not yet" rather than
//
//	a failure. The wait must surface the error via t.Fatalf.
func TestForPropagatesPredicateError(t *testing.T) {
	t.Parallel()
	// forUntil is the testing.TB-free body of For; testing it directly
	// is the only way to exercise the predicate-error path without a
	// fake *testing.T (the testing.TB interface has unexported
	// methods, so it cannot be implemented outside the testing
	// package). For wraps forUntil and calls t.Fatalf on the returned
	// error, so a successful assertion here is also an assertion about
	// For's behaviour.
	sentinel := errors.New("database unreachable")
	calls := 0
	start := time.Now()
	err := forUntil(5*time.Second, "always errors", func() (bool, error) {
		calls++
		return false, sentinel
	})
	if err == nil {
		t.Fatal("forUntil did not surface the predicate error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("forUntil error = %v, want a wrapped %v", err, sentinel)
	}
	if !strings.Contains(err.Error(), "always errors") {
		t.Errorf("forUntil error %q does not name the wait message", err)
	}
	if calls != 1 {
		t.Errorf("predicate called %d times after returning an error, want 1 (error must fail immediately)", calls)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("predicate-error path took %s; the error must short-circuit the poll loop", elapsed)
	}
}

// spec: 17.4 (timeout expiry fails the wait)
// diagnosis: A perpetually-false predicate did not produce a fatal
//
//	at the deadline. The loop's clock check is broken.
func TestForTimesOut(t *testing.T) {
	t.Parallel()
	start := time.Now()
	err := forUntil(150*time.Millisecond, "never ready", func() (bool, error) {
		return false, nil
	})
	if err == nil {
		t.Fatal("forUntil did not time out on a perpetually-false predicate")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("forUntil error %q does not name the timeout", err)
	}
	if !strings.Contains(err.Error(), "never ready") {
		t.Errorf("forUntil error %q does not name the wait message", err)
	}
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("forUntil returned in %s, sooner than the 150ms timeout — the deadline check fired early", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("forUntil took %s, much longer than the 150ms timeout — the poll loop is not bounded by the deadline", elapsed)
	}
}

// spec: 17.4 (ForResult returns the captured payload on success)
// diagnosis: ForResult dropped the value its predicate captured, so a
//
//	caller waiting on a payload received the zero value instead.
func TestForResultReturnsCapturedValue(t *testing.T) {
	t.Parallel()
	got := ForResult(t, 5*time.Second, "captures 42", func() (int, bool, error) {
		return 42, true, nil
	})
	if got != 42 {
		t.Errorf("ForResult = %d, want 42", got)
	}
}
