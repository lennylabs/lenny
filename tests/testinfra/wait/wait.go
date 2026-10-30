// SPDX-License-Identifier: MIT

// Package wait provides the §17.4 condition-wait primitive.
//
// Tests must not use naked time.Sleep — every wait point is a
// condition with an explicit timeout and a descriptive message.
// This package is the single helper that satisfies that rule.
//
// Usage:
//
//	wait.For(t, 5*time.Second, "session reaches running",
//	    func() (bool, error) {
//	        return getState() == "running", nil
//	    })
package wait

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// Condition reports whether the awaited state has been reached. A
// non-nil error fails the wait immediately; returning true ends the
// wait successfully.
type Condition func() (bool, error)

// ErrPredicate is returned when the predicate itself errored mid-wait.
var ErrPredicate = errors.New("predicate error")

// For blocks until cond returns (true, nil), the predicate returns a
// non-nil error, or the timeout expires. The poll interval is 50 ms,
// which is fast enough to keep tests responsive without burning the
// scheduler. On failure For calls t.Fatalf with the reason and the
// supplied message.
func For(t testing.TB, timeout time.Duration, message string, cond Condition) {
	t.Helper()
	if err := forUntil(timeout, message, cond); err != nil {
		t.Fatalf("%v", err)
	}
}

// ForResult is For with a returned value. On success returns the
// payload the predicate captured. The predicate signals readiness by
// returning (payload, true, nil).
func ForResult[T any](t testing.TB, timeout time.Duration, message string, cond func() (T, bool, error)) T {
	t.Helper()
	var captured T
	err := forUntil(timeout, message, func() (bool, error) {
		v, ok, err := cond()
		if ok {
			captured = v
		}
		return ok, err
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	return captured
}

// forUntil is the testing.TB-free body of For. It polls cond until the
// predicate returns true, the predicate returns an error, or the
// timeout expires, and surfaces the result as a returned error rather
// than calling t.Fatalf. Splitting the loop out lets the predicate-error
// and timeout-expiry paths be exercised in unit tests without a fake
// *testing.T.
func forUntil(timeout time.Duration, message string, cond Condition) error {
	deadline := time.Now().Add(timeout)
	for {
		ok, err := cond()
		if err != nil {
			return fmt.Errorf("wait %q: predicate error: %w", message, err)
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait %q: timeout after %s", message, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// String describes the message and timeout for log output.
func String(message string, timeout time.Duration) string {
	return fmt.Sprintf("%s (deadline %s)", message, timeout)
}
